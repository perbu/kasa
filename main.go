package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/perbu/kasa/manifest"
	"github.com/perbu/kasa/repl"
	"github.com/perbu/kasa/tools"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

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

	// Create REPL instance
	replInstance := repl.New(r, *debug)

	// Non-interactive mode (no approval workflow - runs directly)
	if *prompt != "" {
		if *debug {
			fmt.Printf("Model: %s | Tools: %d | Deployments folder: %s\n", cfg.Agent.Model, len(kubeTools.All()), manifestMgr.BaseDir())
			fmt.Printf("Prompt: %s\n\n", *prompt)
		}
		if err := replInstance.RunSinglePrompt(ctx, *prompt); err != nil {
			log.Fatalf("Error: %v", err)
		}
		return
	}

	// Interactive REPL mode - print fancy welcome
	replInstance.PrintWelcome(cfg.Agent.Model, len(kubeTools.All()), manifestMgr.BaseDir())

	// Run the REPL
	if err := replInstance.Run(ctx); err != nil {
		log.Fatalf("REPL error: %v", err)
	}
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
