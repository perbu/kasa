package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/glamour"
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
	clientset, err := initKubeClient(cfg.Kubernetes.Kubeconfig, cfg.Kubernetes.Context)
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

	// Initialize tools
	kubeTools := tools.NewKubeTools(clientset, manifestMgr)

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

	agentConfig := llmagent.Config{
		Name:        cfg.Agent.Name,
		Description: "Kubernetes deployment assistant",
		Model:       geminiModel,
		Instruction: cfg.Prompts.System,
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

	// Non-interactive mode
	if *prompt != "" {
		if *debug {
			fmt.Printf("Model: %s | Tools: %d | Deployments: %s\n", cfg.Agent.Model, len(kubeTools.All()), manifestMgr.BaseDir())
			fmt.Printf("Prompt: %s\n\n", *prompt)
		}
		if err := runAgent(ctx, r, *prompt, *debug); err != nil {
			log.Fatalf("Error: %v", err)
		}
		return
	}

	// Interactive REPL mode
	fmt.Printf("Kasa - Kubernetes Deployment Assistant\n")
	fmt.Printf("Model: %s | Tools: %d | Deployments: %s\n", cfg.Agent.Model, len(kubeTools.All()), manifestMgr.BaseDir())
	fmt.Printf("Type 'exit' or 'quit' to exit.\n\n")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			break
		}

		// Send message and handle response
		if err := runAgent(ctx, r, input, *debug); err != nil {
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

// initKubeClient initializes a Kubernetes clientset.
func initKubeClient(kubeconfig, kubecontext string) (*kubernetes.Clientset, error) {
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
		return nil, fmt.Errorf("building kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	return clientset, nil
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
func runAgent(ctx context.Context, r *runner.Runner, prompt string, debug bool) error {
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

	return nil
}
