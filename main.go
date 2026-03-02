package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/perbu/kasa/manifest"
	"github.com/perbu/kasa/openaimodel"
	"github.com/perbu/kasa/repl"
	"github.com/perbu/kasa/tools"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	sigsyaml "sigs.k8s.io/yaml"
)

//go:embed .version
var version string

func main() {
	prompt := flag.String("prompt", "", "Run a single prompt and exit (non-interactive mode)")
	debug := flag.Bool("debug", false, "Enable debug output")
	noTools := flag.Bool("no-tools", false, "Run without tools (for testing)")
	flag.Parse()

	// Handle "init" subcommand
	if flag.Arg(0) == "init" {
		if err := runWizard(true); err != nil {
			log.Fatalf("Setup wizard failed: %v", err)
		}
		return
	}

	cfg, configExists, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Auto-trigger wizard on first run when no config exists
	if !configExists {
		if err := runWizard(false); err != nil {
			log.Fatalf("Setup wizard failed: %v", err)
		}
		// Reload config after wizard writes it
		cfg, _, err = loadConfig()
		if err != nil {
			log.Fatalf("Failed to load config after setup: %v", err)
		}
	}

	// Initialize Kubernetes client
	clientset, dynamicClient, kubeContext, err := initKubeClient(cfg.Kubernetes.Kubeconfig, cfg.Kubernetes.Context)
	if err != nil {
		log.Fatalf("Failed to initialize Kubernetes client: %v", err)
	}

	// Initialize manifest manager (scoped to active cluster context)
	manifestMgr, err := manifest.NewManager(cfg.Deployments.Directory, kubeContext)
	if err != nil {
		log.Fatalf("Failed to initialize manifest manager: %v", err)
	}

	// Ensure git is initialized in the manifest directory
	if err := manifestMgr.EnsureGitInit(); err != nil {
		log.Fatalf("Failed to initialize git in manifest directory: %v", err)
	}

	// Set up git remote if configured (no auto-pull; use /pull to sync)
	if cfg.Deployments.Remote != "" {
		if err := manifestMgr.SetupRemote(cfg.Deployments.Remote); err != nil {
			log.Fatalf("Failed to set up git remote: %v", err)
		}
	}

	// Get API key for web tools (optional)
	jinaAPIKey := cfg.JinaAPIKey()

	// Initialize DirectIO for secret tool side-channel communication
	directIO := tools.NewDirectIO()

	// Initialize tools
	kubeTools := tools.NewKubeTools(clientset, dynamicClient, manifestMgr, jinaAPIKey, cfg.Agent.ToolWarnThreshold, directIO)

	// Get API key
	apiKey := cfg.APIKey()
	if apiKey == "" {
		log.Fatalf("API key not configured. Set OPENROUTER_API_KEY env var or api_key in ~/.kasa/config.yaml")
	}
	if cfg.Agent.Model == "" {
		log.Fatalf("Model not configured. Set agent.model in ~/.kasa/config.yaml (e.g. anthropic/claude-sonnet-4, google/gemini-2.5-flash)")
	}

	ctx := context.Background()

	// Create LLM model for ADK via OpenAI-compatible API (OpenRouter by default)
	llmModel := openaimodel.New(cfg.Agent.Model, apiKey, cfg.BaseURL(), &retryTransport{
		base:       http.DefaultTransport,
		maxRetries: 3,
		debug:      *debug,
	})

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

	// In interactive mode, run drift scan and inject results into system prompt
	isInteractive := *prompt == ""
	var scanResults *tools.DriftScanResults
	if isInteractive {
		progress := func(current, total int, namespace, name, kind string) {
			fmt.Fprintf(os.Stderr, "\r\033[KDrift scan: checking %s/%s/%s (%d/%d)...", namespace, name, kind, current+1, total)
		}
		scanResults, err = tools.RunDriftScan(ctx, dynamicClient, manifestMgr, progress)
		fmt.Fprintf(os.Stderr, "\r\033[K")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: drift scan failed: %v\n", err)
		} else if scanResults != nil {
			systemPrompt += tools.FormatDriftContext(scanResults)
		}
	}

	agentConfig := llmagent.Config{
		Name:        cfg.Agent.Name,
		Description: "Kubernetes deployment assistant",
		Model:       llmModel,
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

	// Resolve kubeconfig path for context listing/switching.
	kubeconfigPath := cfg.Kubernetes.Kubeconfig
	if kubeconfigPath == "" {
		if home := homedir.HomeDir(); home != "" {
			kubeconfigPath = filepath.Join(home, ".kube", "config")
		}
	}
	deployDir := cfg.Deployments.Directory

	// Mutable: tracks the active context so /contexts shows the right marker.
	activeKubeContext := kubeContext

	listContextsFn := func() ([]repl.ContextInfo, error) {
		ctxs, _, err := listKubeContexts(kubeconfigPath)
		if err != nil {
			return nil, err
		}
		// Override the active flag to match the running context (which may
		// differ from the kubeconfig's current-context after a switch).
		for i := range ctxs {
			ctxs[i].Active = ctxs[i].Name == activeKubeContext
		}
		return ctxs, nil
	}

	// makeResourceFetcher builds a ResourceFetcher closure for a given dynamic client.
	// It parses the proposed YAML, fetches the live cluster resource, and returns
	// clean YAML suitable for diffing. Returns ("", nil) for new resources.
	makeResourceFetcher := func(dynClient dynamic.Interface) repl.ResourceFetcher {
		return func(yamlContent string) (string, error) {
			var m map[string]any
			if err := sigsyaml.Unmarshal([]byte(yamlContent), &m); err != nil {
				return "", err
			}
			kind, _ := m["kind"].(string)
			apiVersion, _ := m["apiVersion"].(string)
			namespace, name := "", ""
			if meta, ok := m["metadata"].(map[string]any); ok {
				namespace, _ = meta["namespace"].(string)
				name, _ = meta["name"].(string)
			}
			if kind == "" || name == "" {
				return "", nil
			}
			fetchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			liveMap, err := tools.FetchAndCleanLiveResource(fetchCtx, dynClient, namespace, name, kind, apiVersion)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					return "", nil // new resource — no diff
				}
				return "", err
			}
			yamlBytes, err := sigsyaml.Marshal(liveMap)
			if err != nil {
				return "", err
			}
			return string(yamlBytes), nil
		}
	}

	makeDriftScanFn := func(dynClient dynamic.Interface) repl.DriftScanFunc {
		return func(ctx context.Context, mgr *manifest.Manager) (*tools.DriftScanResults, error) {
			return tools.RunDriftScan(ctx, dynClient, mgr, nil)
		}
	}

	switchContextFn := func(contextName string) (*repl.ContextSwitchResult, error) {
		// 1. Build new Kubernetes clients.
		newClientset, newDynamic, resolvedCtx, err := initKubeClient(kubeconfigPath, contextName)
		if err != nil {
			return nil, fmt.Errorf("initializing kube client: %w", err)
		}

		// 2. New manifest manager.
		newManifest, err := manifest.NewManager(deployDir, resolvedCtx)
		if err != nil {
			return nil, fmt.Errorf("initializing manifest manager: %w", err)
		}
		if err := newManifest.EnsureGitInit(); err != nil {
			return nil, fmt.Errorf("initializing git: %w", err)
		}
		if cfg.Deployments.Remote != "" {
			if err := newManifest.SetupRemote(cfg.Deployments.Remote); err != nil {
				return nil, fmt.Errorf("setting up remote: %w", err)
			}
		}

		// 3. New tools (with fresh DirectIO).
		newDirectIO := tools.NewDirectIO()
		newJinaKey := cfg.JinaAPIKey()
		newKubeTools := tools.NewKubeTools(newClientset, newDynamic, newManifest, newJinaKey, cfg.Agent.ToolWarnThreshold, newDirectIO)
		newToolDocs := newKubeTools.GenerateToolDocs()
		newSysPrompt := strings.Replace(cfg.Prompts.System, "{{TOOL_DOCS}}", newToolDocs, 1)

		// 4. New agent (reuses the existing llmModel).
		var newAgentTools []tool.Tool
		if !*noTools {
			newAgentTools = newKubeTools.All()
		}
		newAgent, err := llmagent.New(llmagent.Config{
			Name:        cfg.Agent.Name,
			Description: "Kubernetes deployment assistant",
			Model:       llmModel,
			Instruction: newSysPrompt,
			Tools:       newAgentTools,
		})
		if err != nil {
			return nil, fmt.Errorf("creating agent: %w", err)
		}

		// 5. New session + runner.
		newSS := session.InMemoryService()
		newRunner, err := runner.New(runner.Config{
			AppName:        "kasa",
			Agent:          newAgent,
			SessionService: newSS,
		})
		if err != nil {
			return nil, fmt.Errorf("creating runner: %w", err)
		}
		_, err = newSS.Create(ctx, &session.CreateRequest{
			AppName:   "kasa",
			UserID:    "user1",
			SessionID: "session1",
		})
		if err != nil {
			return nil, fmt.Errorf("creating session: %w", err)
		}

		// 6. Update the mutable active context.
		activeKubeContext = resolvedCtx

		return &repl.ContextSwitchResult{
			Runner:          newRunner,
			SessionService:  newSS,
			Manifest:        newManifest,
			ContextName:     resolvedCtx,
			ResourceFetcher: makeResourceFetcher(newDynamic),
			DirectIO:        newDirectIO,
			DriftScanFunc:   makeDriftScanFn(newDynamic),
			MutationGuard:   newKubeTools.Guard(),
		}, nil
	}

	// Create REPL instance
	replInstance := repl.New(r, sessionService, *debug, manifestMgr, apiKey, cfg.BaseURL(), cfg.Agent.Model, cfg.Agent.MaxToolCalls, kubeTools.Counter(), kubeTools.Guard(), listContextsFn, switchContextFn, makeResourceFetcher(dynamicClient), directIO, kubeContext, makeDriftScanFn(dynamicClient))

	// Non-interactive mode (no approval workflow - runs directly)
	if !isInteractive {
		// Disable mutation guard: non-interactive mode has no plan/approval workflow.
		kubeTools.Guard().Allow()
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
	replInstance.PrintWelcome(strings.TrimSpace(version), cfg.Agent.Model, len(kubeTools.All()), manifestMgr.BaseDir(), cfg.Deployments.Remote)

	// Display drift scan results to the user
	if scanResults != nil {
		printDriftScanResults(scanResults)
	}

	// Run the REPL
	if err := replInstance.Run(ctx); err != nil {
		log.Fatalf("REPL error: %v", err)
	}
}

// initKubeClient initializes a Kubernetes clientset, dynamic client, and resolves
// the active context name.
func initKubeClient(kubeconfig, kubecontext string) (*kubernetes.Clientset, dynamic.Interface, string, error) {
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

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, configOverrides)

	config, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, nil, "", fmt.Errorf("building kubeconfig: %w", err)
	}

	// Resolve the active context name
	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		return nil, nil, "", fmt.Errorf("reading raw kubeconfig: %w", err)
	}
	resolvedContext := rawConfig.CurrentContext
	if kubecontext != "" {
		resolvedContext = kubecontext
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, "", fmt.Errorf("creating kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, "", fmt.Errorf("creating dynamic client: %w", err)
	}

	return clientset, dynamicClient, resolvedContext, nil
}

// listKubeContexts reads a kubeconfig file and returns all contexts with their
// cluster names and which one is the current-context.
func listKubeContexts(kubeconfig string) ([]repl.ContextInfo, string, error) {
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})

	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		return nil, "", fmt.Errorf("reading kubeconfig: %w", err)
	}

	var ctxs []repl.ContextInfo
	for name, ctx := range rawConfig.Contexts {
		ctxs = append(ctxs, repl.ContextInfo{
			Name:    name,
			Cluster: ctx.Cluster,
			Active:  name == rawConfig.CurrentContext,
		})
	}

	// Sort for stable display order.
	sortContextInfos(ctxs)

	return ctxs, rawConfig.CurrentContext, nil
}

// sortContextInfos sorts contexts alphabetically by name, with the active
// context always first.
func sortContextInfos(ctxs []repl.ContextInfo) {
	// Simple insertion sort — context lists are small.
	for i := 1; i < len(ctxs); i++ {
		for j := i; j > 0; j-- {
			if ctxs[j].Active && !ctxs[j-1].Active {
				ctxs[j], ctxs[j-1] = ctxs[j-1], ctxs[j]
			} else if !ctxs[j-1].Active && !ctxs[j].Active && ctxs[j].Name < ctxs[j-1].Name {
				ctxs[j], ctxs[j-1] = ctxs[j-1], ctxs[j]
			} else if ctxs[j-1].Active || ctxs[j].Name >= ctxs[j-1].Name {
				break
			}
		}
	}
}

// printDriftScanResults renders the drift scan results as a markdown table via glamour.
func printDriftScanResults(results *tools.DriftScanResults) {
	md := tools.FormatDriftScanResults(results)
	if md == "" {
		return
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		// Fallback to plain text
		fmt.Print(md)
		return
	}

	out, err := renderer.Render(md)
	if err != nil {
		fmt.Print(md)
		return
	}

	fmt.Print(out)
}
