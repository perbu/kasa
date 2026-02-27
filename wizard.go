package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/huh"
	openai "github.com/sashabaranov/go-openai"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

const (
	providerOpenRouter = "openrouter"
	providerGoogle     = "google"
	providerCustom     = "custom"
)

// runWizard runs the interactive setup wizard.
// If forceInit is true, it prompts before overwriting an existing config.
// Returns nil on success, or an error.
func runWizard(forceInit bool) error {
	dir, err := configDir()
	if err != nil {
		return fmt.Errorf("determining config directory: %w", err)
	}
	configPath := filepath.Join(dir, "config.yaml")

	// If config exists and this is an explicit "init", ask to overwrite.
	if forceInit {
		if _, err := os.Stat(configPath); err == nil {
			var overwrite bool
			err := huh.NewConfirm().
				Title("Config file already exists at " + configPath).
				Description("Overwrite it?").
				Value(&overwrite).
				Affirmative("Yes").
				Negative("No").
				Run()
			if err != nil {
				return handleAbort(err)
			}
			if !overwrite {
				fmt.Println("Cancelled.")
				return nil
			}
		}
	}

	fmt.Println("Welcome to Kasa! Let's set up your configuration.")
	fmt.Println()

	// --- Collect values ---
	var (
		provider     string
		customURL    string
		apiKey       string
		model        string
		customModel  string
		kubeContext  string
		jinaAPIKey   string
		deployRemote string
	)

	// Step 1: Provider selection
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("LLM Provider").
				Description("Which LLM provider do you want to use?").
				Options(
					huh.NewOption("OpenRouter (recommended)", providerOpenRouter),
					huh.NewOption("Google AI (direct)", providerGoogle),
					huh.NewOption("Custom OpenAI-compatible", providerCustom),
				).
				Value(&provider),
		),
	).Run()
	if err != nil {
		return handleAbort(err)
	}

	// Step 2: Base URL (only for custom provider)
	if provider == providerCustom {
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Base URL").
					Description("OpenAI-compatible API base URL (e.g. http://localhost:11434/v1 for Ollama)").
					Placeholder("https://api.example.com/v1").
					Value(&customURL).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("base URL is required for custom providers")
						}
						return nil
					}),
			),
		).Run()
		if err != nil {
			return handleAbort(err)
		}
	}

	// Step 3: API Key
	apiKeyDesc := "Enter your OpenRouter API key (from https://openrouter.ai/keys)"
	if provider == providerGoogle {
		apiKeyDesc = "Enter your Google AI API key (from https://aistudio.google.com/apikey)"
	} else if provider == providerCustom {
		apiKeyDesc = "Enter the API key for your provider (leave empty if not needed)"
	}

	err = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("API Key").
				Description(apiKeyDesc).
				EchoMode(huh.EchoModePassword).
				Value(&apiKey).
				Validate(func(s string) error {
					if s == "" && provider != providerCustom {
						return fmt.Errorf("API key is required")
					}
					return nil
				}),
		),
	).Run()
	if err != nil {
		return handleAbort(err)
	}

	// Step 4: Model selection
	modelOptions := modelOptionsForProvider(provider)

	if len(modelOptions) > 0 {
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Model").
					Description("Which model should Kasa use?").
					Options(modelOptions...).
					Value(&model),
			),
		).Run()
		if err != nil {
			return handleAbort(err)
		}
	} else {
		model = "other"
	}

	// Step 5: Custom model name
	if model == "other" {
		placeholder := "org/model-name"
		if provider == providerGoogle {
			placeholder = "gemini-3.1-flash-preview"
		}
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Model Name").
					Description("Enter the full model identifier").
					Placeholder(placeholder).
					Value(&customModel).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("model name is required")
						}
						return nil
					}),
			),
		).Run()
		if err != nil {
			return handleAbort(err)
		}
		model = customModel
	}

	// Step 6: Kubernetes context
	contexts := detectKubeContexts()
	if len(contexts) > 0 {
		contextOptions := []huh.Option[string]{
			huh.NewOption("Default (current context)", ""),
		}
		for _, c := range contexts {
			label := c
			contextOptions = append(contextOptions, huh.NewOption(label, c))
		}

		err = huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Kubernetes Context").
					Description("Which cluster should Kasa connect to?").
					Options(contextOptions...).
					Value(&kubeContext),
			),
		).Run()
		if err != nil {
			return handleAbort(err)
		}
	}

	// Step 7: Deployment remote (optional, for team sync)
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Deployment Git Remote (optional)").
				Description("Git remote for syncing manifests with your team. Leave empty to skip.").
				Placeholder("git@github.com:org/manifests.git").
				Value(&deployRemote),
		),
	).Run()
	if err != nil {
		return handleAbort(err)
	}

	// Step 8: Jina API key (optional, for web search)
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Jina API Key (optional)").
				Description("For web search and URL fetching. Leave empty to skip. (https://jina.ai/api-key)").
				EchoMode(huh.EchoModePassword).
				Value(&jinaAPIKey),
		),
	).Run()
	if err != nil {
		return handleAbort(err)
	}

	// --- Resolve final values ---
	finalModel := model
	var baseURL string
	switch provider {
	case providerOpenRouter:
		// Default base URL, omit from config
	case providerGoogle:
		baseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"
	case providerCustom:
		baseURL = customURL
	}

	// --- Validate ---
	fmt.Println()
	if apiKey != "" {
		testURL := baseURL
		if testURL == "" {
			testURL = "https://openrouter.ai/api/v1"
		}
		fmt.Print("Testing API connection... ")
		if err := testAPIKey(testURL, apiKey, finalModel); err != nil {
			fmt.Printf("warning: %v\n", err)
			var saveAnyway bool
			confirmErr := huh.NewConfirm().
				Title("API test failed. Save config anyway?").
				Value(&saveAnyway).
				Affirmative("Yes").
				Negative("No").
				Run()
			if confirmErr != nil {
				return handleAbort(confirmErr)
			}
			if !saveAnyway {
				fmt.Println("Cancelled.")
				return nil
			}
		} else {
			fmt.Println("ok")
		}
	}

	if kubeContext != "" {
		if err := testKubeContext(kubeContext); err != nil {
			fmt.Printf("Warning: kube context %q: %v\n", kubeContext, err)
		}
	}

	// --- Write config ---
	configContent, err := buildConfigYAML(baseURL, apiKey, finalModel, kubeContext, jinaAPIKey, deployRemote)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	fmt.Printf("\nConfig written to %s\n", configPath)
	return nil
}

// modelOptionsForProvider returns the curated model list for a given provider.
func modelOptionsForProvider(provider string) []huh.Option[string] {
	switch provider {
	case providerOpenRouter:
		return []huh.Option[string]{
			huh.NewOption("Claude Sonnet 4.6", "anthropic/claude-sonnet-4.6"),
			huh.NewOption("GPT-5.3 Codex)", "openai/gpt-5.3-codex"),
			huh.NewOption("MiniMax M2.5", "minimax/minimax-m2.5"),
			huh.NewOption("Other (enter manually)", "other"),
		}
	case providerGoogle:
		return []huh.Option[string]{
			huh.NewOption("Gemini 3.1 Flash [Preview]", "gemini-3.1-flash-preview"),
			huh.NewOption("Gemini 3.1 Pro [Preview]", "gemini-3.1-pro-preview"),
			huh.NewOption("Other (enter manually)", "other"),
		}
	default:
		// Custom provider: go straight to manual input
		return nil
	}
}

// detectKubeContexts returns the names of all kubeconfig contexts, or nil if
// no kubeconfig is found.
func detectKubeContexts() []string {
	kubeconfigPath := ""
	if home := homedir.HomeDir(); home != "" {
		kubeconfigPath = filepath.Join(home, ".kube", "config")
	}
	if kubeconfigPath == "" {
		return nil
	}

	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		return nil
	}

	var names []string
	for name := range rawConfig.Contexts {
		names = append(names, name)
	}
	return names
}

// testAPIKey sends a minimal chat completion request to verify the API key works.
func testAPIKey(baseURL, apiKey, model string) error {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	client := openai.NewClientWithConfig(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:     model,
		MaxTokens: 5,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "Say ok"},
		},
	})
	if err != nil {
		return fmt.Errorf("API request failed: %w", err)
	}
	return nil
}

// testKubeContext verifies that the given kube context can be loaded (no API call).
func testKubeContext(contextName string) error {
	kubeconfigPath := ""
	if home := homedir.HomeDir(); home != "" {
		kubeconfigPath = filepath.Join(home, ".kube", "config")
	}

	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	_, err := clientConfig.ClientConfig()
	return err
}

// buildConfigYAML generates the config file content by marshalling a Config struct.
func buildConfigYAML(baseURL, apiKey, model, kubeContext, jinaAPIKey, deployRemote string) (string, error) {
	var cfg Config
	cfg.Kubernetes.Context = kubeContext
	cfg.Agent.Model = model
	cfg.Agent.Name = "kasa"
	cfg.Agent.MaxToolCalls = 25
	cfg.Agent.ToolWarnThreshold = 3
	cfg.Deployments.Remote = deployRemote
	cfg.Credentials.APIKey = apiKey
	cfg.Credentials.BaseURL = baseURL
	cfg.Credentials.JinaAPIKey = jinaAPIKey

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return "", fmt.Errorf("marshalling config: %w", err)
	}

	header := "# Kasa configuration\n# Location: ~/.kasa/config.yaml\n# Run 'kasa init' to re-run the setup wizard.\n\n"
	return header + string(data), nil
}

// handleAbort returns nil for user abort (clean exit) or the original error.
func handleAbort(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Println("Cancelled.")
		os.Exit(0)
	}
	return err
}
