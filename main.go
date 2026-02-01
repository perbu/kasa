package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/chzyer/readline"
	"github.com/joho/godotenv"
	"github.com/perbu/kasa/manifest"
	"github.com/perbu/kasa/tools"
	"golang.org/x/term"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Config represents the application configuration.
type Config struct {
	Kubernetes struct {
		Kubeconfig string `yaml:"kubeconfig"`
		Context    string `yaml:"context"`
	} `yaml:"kubernetes"`
	Agent struct {
		Model string `yaml:"model"`
		Name  string `yaml:"name"`
	} `yaml:"agent"`
	Deployments struct {
		Directory string `yaml:"directory"`
	} `yaml:"deployments"`
	Prompts struct {
		System string `yaml:"system"`
	} `yaml:"prompts"`
}

func main() {
	prompt := flag.String("prompt", "", "Run a single prompt and exit (non-interactive mode)")
	debug := flag.Bool("debug", false, "Enable debug output")
	noTools := flag.Bool("no-tools", false, "Run without tools (for testing)")
	flag.Parse()

	// Load .env file (optional, won't error if missing)
	if err := godotenv.Load(); err != nil {
		if *debug {
			log.Printf("No .env file found, using environment variables")
		}
	}

	cfg, err := loadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize Kubernetes client
	clientset, dynamicClient, err := initKubeClient(cfg.Kubernetes.Kubeconfig, cfg.Kubernetes.Context)
	if err != nil {
		log.Fatalf("Failed to initialize Kubernetes client: %v", err)
	}

	// Initialize manifest manager
	manifestDir := cfg.Deployments.Directory
	if manifestDir == "" {
		manifestDir = "~/.kasa/deployments"
	}
	manifestMgr, err := manifest.NewManager(manifestDir)
	if err != nil {
		log.Fatalf("Failed to initialize manifest manager: %v", err)
	}

	// Ensure git is initialized in the manifest directory
	if err := manifestMgr.EnsureGitInit(); err != nil {
		log.Fatalf("Failed to initialize git in manifest directory: %v", err)
	}

	// Get API keys for web tools (optional)
	jinaAPIKey := os.Getenv("JINA_READER_API_KEY")
	tavilyAPIKey := os.Getenv("TAVILY_API_KEY")

	// Initialize tools
	kubeTools := tools.NewKubeTools(clientset, dynamicClient, manifestMgr, jinaAPIKey, tavilyAPIKey)

	// Get API key from environment
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		log.Fatalf("GOOGLE_API_KEY environment variable not set")
	}

	ctx := context.Background()

	// Create Gemini model for ADK
	geminiModel, err := gemini.NewModel(ctx, cfg.Agent.Model, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalf("Failed to create Gemini model: %v", err)
	}

	// Create agent
	var agentTools []tool.Tool
	if !*noTools {
		agentTools = kubeTools.All()
	} else if *debug {
		fmt.Println("[DEBUG] Running without tools")
	}

	// Generate dynamic tool documentation and inject into system prompt
	toolDocs := kubeTools.GenerateToolDocs()
	systemPrompt := strings.Replace(cfg.Prompts.System, "{{TOOL_DOCS}}", toolDocs, 1)

	agentConfig := llmagent.Config{
		Name:        cfg.Agent.Name,
		Description: "Kubernetes deployment assistant",
		Model:       geminiModel,
		Instruction: systemPrompt,
		Tools:       agentTools,
	}

	agt, err := llmagent.New(agentConfig)
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	// Create session service and runner once (shared across all messages)
	sessionService := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:        "kasa",
		Agent:          agt,
		SessionService: sessionService,
	})
	if err != nil {
		log.Fatalf("Failed to create runner: %v", err)
	}

	// Create the session
	_, err = sessionService.Create(ctx, &session.CreateRequest{
		AppName:   "kasa",
		UserID:    "user1",
		SessionID: "session1",
	})
	if err != nil {
		log.Fatalf("Failed to create session: %v", err)
	}

	// Non-interactive mode (no approval workflow - runs directly)
	if *prompt != "" {
		if *debug {
			fmt.Printf("Model: %s | Tools: %d | Deployments folder: %s\n", cfg.Agent.Model, len(kubeTools.All()), manifestMgr.BaseDir())
			fmt.Printf("Prompt: %s\n\n", *prompt)
		}
		// Non-interactive mode doesn't support plan approval, pass nil state
		if err := runAgent(ctx, r, nil, *prompt, *debug); err != nil {
			log.Fatalf("Error: %v", err)
		}
		return
	}

	// Interactive REPL mode - print fancy welcome
	printWelcome(cfg.Agent.Model, len(kubeTools.All()), manifestMgr.BaseDir())

	// Initialize session state for plan/approval workflow
	state := NewSessionState()

	// Set up readline with history
	historyFile := ""
	if home := homedir.HomeDir(); home != "" {
		kasaDir := filepath.Join(home, ".kasa")
		if err := os.MkdirAll(kasaDir, 0755); err == nil {
			historyFile = filepath.Join(kasaDir, "history")
		}
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:            "> ",
		HistoryFile:       historyFile,
		HistorySearchFold: true,
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
	})
	if err != nil {
		log.Fatalf("Failed to initialize readline: %v", err)
	}
	defer rl.Close()

	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				continue // Ctrl+C, just show new prompt
			}
			if err == io.EOF {
				fmt.Println("Goodbye!")
				break
			}
			fmt.Printf("Error reading input: %v\n", err)
			break
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			break
		}

		// Handle special commands for plan approval
		switch strings.ToLower(input) {
		case "yes", "y", "/approve":
			if state.HasPendingPlan() {
				plan := state.ApprovePlan()
				fmt.Println("Plan approved. Executing...")
				execPrompt := FormatExecutionPrompt(plan)
				if err := runAgent(ctx, r, state, execPrompt, *debug); err != nil {
					fmt.Printf("Error: %v\n", err)
				}
				state.Reset()
			} else {
				fmt.Println("No pending plan to approve.")
			}
			continue
		case "no", "n", "/reject":
			if state.HasPendingPlan() {
				state.RejectPlan()
				fmt.Println("Plan rejected.")
			} else {
				fmt.Println("No pending plan to reject.")
			}
			continue
		case "/plan":
			if state.HasPendingPlan() {
				DisplayPlan(state.PendingPlan)
			} else {
				fmt.Println("No pending plan.")
			}
			continue
		}

		// If there's a pending plan, warn the user
		if state.HasPendingPlan() {
			fmt.Println("You have a pending plan. Type 'yes' to approve, 'no' to reject, or '/plan' to review.")
			continue
		}

		// Send message and handle response
		if err := runAgent(ctx, r, state, input, *debug); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
}

// loadConfig loads the configuration from a YAML file.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return &cfg, nil
}

// initKubeClient initializes a Kubernetes clientset and dynamic client.
func initKubeClient(kubeconfig, kubecontext string) (*kubernetes.Clientset, dynamic.Interface, error) {
	// Use default kubeconfig path if not specified
	if kubeconfig == "" {
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	// Build config with optional context override
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	configOverrides := &clientcmd.ConfigOverrides{}
	if kubecontext != "" {
		configOverrides.CurrentContext = kubecontext
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, configOverrides).ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("building kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	return clientset, dynamicClient, nil
}

// setupMarkdownRenderer creates a glamour renderer configured for the terminal.
func setupMarkdownRenderer() (*glamour.TermRenderer, error) {
	// Detect terminal width
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}

	// Create renderer with auto style detection
	return glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
}

// runAgent runs the agent with the given prompt.
// If state is provided, it will detect propose_plan calls and update the state.
func runAgent(ctx context.Context, r *runner.Runner, state *SessionState, prompt string, debug bool) error {
	if debug {
		fmt.Printf("[DEBUG] Sending message: %s\n", prompt)
	}

	// Setup markdown renderer
	mdRenderer, mdErr := setupMarkdownRenderer()
	if mdErr != nil && debug {
		fmt.Printf("[DEBUG] Markdown renderer setup failed: %v\n", mdErr)
	}

	// Create user message content
	userMessage := genai.NewContentFromText(prompt, genai.RoleUser)

	// Initialize and start status line
	status := NewStatusLine()
	status.Start()

	// Execute agent with the user message
	for event, err := range r.Run(ctx, "user1", "session1", userMessage, agent.RunConfig{}) {
		if err != nil {
			status.Stop()
			return fmt.Errorf("agent execution failed: %w", err)
		}

		// Update status line with event info
		status.Update(event)

		if event != nil && event.Content != nil {
			// Extract text from all parts in the content
			for _, part := range event.Content.Parts {
				// Detect propose_plan function call
				if part.FunctionCall != nil && part.FunctionCall.Name == "propose_plan" {
					if state != nil && part.FunctionCall.Args != nil {
						plan := ParsePlanFromResponse(part.FunctionCall.Args)
						if plan != nil {
							state.SetPendingPlan(plan)
							if debug {
								fmt.Printf("[DEBUG] Detected propose_plan call\n")
							}
						}
					}
				}

				if part.Text != "" {
					// Clear status line before printing content
					status.ClearForOutput()

					// Try to render as markdown
					if mdRenderer != nil {
						rendered, renderErr := mdRenderer.Render(part.Text)
						if renderErr == nil {
							fmt.Print(rendered)
							continue
						}
					}
					// Fallback to plain text
					fmt.Print(part.Text)
				}
			}
		}
	}

	// Stop and clear status line
	status.Stop()
	fmt.Println()

	// Display pending plan if one was proposed
	if state != nil && state.HasPendingPlan() {
		DisplayPlan(state.PendingPlan)
	}

	return nil
}

// printWelcome displays a fancy markdown-rendered welcome message.
func printWelcome(model string, toolCount int, deploymentsDir string) {
	welcome := fmt.Sprintf(`# Kasa

**Kubernetes Deployment Assistant** _(Safe Mode)_

| Setting | Value |
|---------|-------|
| Model | %s |
| Tools | %d |
| Deployments folder | %s |

Commands: **yes**/**no** to approve/reject plans, **exit** to quit.
`, model, toolCount, deploymentsDir)

	renderer, err := setupMarkdownRenderer()
	if err != nil {
		// Fallback to plain text
		fmt.Printf("Kasa - Kubernetes Deployment Assistant (Safe Mode)\n")
		fmt.Printf("Model: %s | Tools: %d | Deployments: %s\n", model, toolCount, deploymentsDir)
		fmt.Printf("Type 'exit' or 'quit' to exit.\n\n")
		return
	}

	rendered, err := renderer.Render(welcome)
	if err != nil {
		// Fallback to plain text
		fmt.Printf("Kasa - Kubernetes Deployment Assistant (Safe Mode)\n")
		fmt.Printf("Model: %s | Tools: %d | Deployments: %s\n", model, toolCount, deploymentsDir)
		fmt.Printf("Type 'exit' or 'quit' to exit.\n\n")
		return
	}

	fmt.Print(rendered)
}
