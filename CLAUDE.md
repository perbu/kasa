# Kasa - Kubernetes Deployment Agent

## Project Overview

Kasa is a conversational Kubernetes deployment assistant built with Go, using Google's ADK (Agent Development Kit) for LLM agent capabilities and client-go for Kubernetes interaction.

## Build & Run

```bash
go build -o kasa .
./kasa                           # Interactive REPL mode
./kasa -prompt "list namespaces" # Single prompt mode
./kasa -debug -prompt "..."      # With debug output
```

## Configuration

- `.env` - Contains `GOOGLE_API_KEY` (not committed)
- `config.yaml` - Kubernetes settings, model selection, and system prompt

## Project Structure

```
kasa/
├── main.go              # Entry point, agent setup, runner/session
├── config.yaml          # Configuration and prompts
├── tools/
│   ├── tools.go         # KubeTools struct, tool registration, addFunctionTool helper
│   ├── namespaces.go    # list_namespaces tool
│   ├── pods.go          # list_pods tool
│   ├── logs.go          # get_logs tool
│   ├── events.go        # get_events tool
│   ├── resource.go      # get_resource tool
│   ├── references.go    # get_reference tool (K8s resource docs)
│   ├── deploy.go        # create_deployment tool
│   ├── service_create.go # create_service tool
│   ├── health.go        # check_deployment_health tool
│   ├── commit.go        # commit_manifests tool
│   ├── manifest_list.go # list_manifests tool
│   ├── manifest_read.go # read_manifest tool
│   └── manifest_delete.go # delete_manifest tool
├── manifest/
│   └── manifest.go      # Manifest file storage and git operations
├── references/
│   ├── references.go    # Embedded documentation lookup
│   └── data/*.md        # Reference docs for K8s resources
├── deployments/         # Git-tracked manifest storage (created at runtime)
├── .env                 # API keys (gitignored)
└── spec.md              # Full project specification
```

## Key Patterns

### Tool Implementation

Tools implement the `tool.Tool` interface from `google.golang.org/adk/tool`. Each tool is a struct with these methods:

```go
type MyTool struct {
    // dependencies (e.g., clientset, config)
}

func NewMyTool(...) *MyTool {
    return &MyTool{...}
}

func (t *MyTool) Name() string {
    return "my_tool"
}

func (t *MyTool) Description() string {
    return "What this tool does"
}

func (t *MyTool) IsLongRunning() bool {
    return false
}

func (t *MyTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
    return addFunctionTool(req, t)
}

func (t *MyTool) Declaration() *genai.FunctionDeclaration {
    return &genai.FunctionDeclaration{
        Name:        t.Name(),
        Description: t.Description(),
        Parameters: &genai.Schema{
            Type: "object",  // NOTE: string, not genai.TypeObject
            Properties: map[string]*genai.Schema{
                "param_name": {
                    Type:        "string",
                    Description: "Parameter description",
                },
            },
            Required: []string{"param_name"},
        },
    }
}

func (t *MyTool) Run(ctx tool.Context, args any) (map[string]any, error) {
    argsMap, ok := args.(map[string]any)
    if !ok {
        return map[string]any{"error": "invalid arguments"}, nil
    }

    // Execute tool logic
    result := doSomething(argsMap["param_name"].(string))

    return map[string]any{
        "result": result,
    }, nil
}
```

### Adding a New Tool

1. Create `tools/mytool.go` with the struct and all interface methods
2. Register in `tools/tools.go` by adding to `All()`:
   ```go
   func (k *KubeTools) All() []tool.Tool {
       return []tool.Tool{
           // ... existing tools ...
           NewMyTool(k.clientset),           // K8s-only tool
           NewMyTool(k.manifest),            // Manifest-only tool
           NewMyTool(k.clientset, k.manifest), // Both
       }
   }
   ```
3. Build and test

### Agent Architecture

The agent uses ADK's runner/session pattern:

```go
// Create Gemini model
geminiModel, _ := gemini.NewModel(ctx, modelName, &genai.ClientConfig{
    APIKey:  apiKey,
    Backend: genai.BackendGeminiAPI,
})

// Create agent with tools
agent, _ := llmagent.New(llmagent.Config{
    Name:        "kasa",
    Model:       geminiModel,
    Instruction: systemPrompt,
    Tools:       kubeTools.All(),
})

// Run with session
sessionService := session.InMemoryService()
r, _ := runner.New(runner.Config{
    AppName:        "kasa",
    Agent:          agent,
    SessionService: sessionService,
})

// Execute and stream response
for event, err := range r.Run(ctx, userID, sessionID, userMessage, agent.RunConfig{}) {
    // Handle events
}
```

### References Package

Embedded Kubernetes resource documentation in `references/data/*.md`. Access via:

```go
references.List()                    // []string of available topics
references.ListWithDescriptions()    // map[string]string with descriptions
references.Lookup("deployment")      // Returns markdown content
```

### Manifest Package

Handles manifest file storage with git integration. Files are stored as `<baseDir>/<namespace>/<app>/<type>.yaml`.

```go
manager, _ := manifest.NewManager("~/deployments")
manager.EnsureGitInit()

// Save and stage a manifest
path, _ := manager.SaveManifest("default", "nginx", "deployment", yamlBytes)

// List manifests (filter by namespace/app, empty = all)
manifests, _ := manager.ListManifests("default", "")  // []ManifestInfo

// Read manifest content
content, _ := manager.ReadManifest("default", "nginx", "deployment")

// Delete manifest(s) and stage deletion (empty type = delete all for app)
deleted, _ := manager.DeleteManifest("default", "nginx", "deployment")

// Commit staged changes
manager.Commit("Deploy nginx to default namespace")
```

## Dependencies

- `google.golang.org/adk` - Agent Development Kit (runner, session, tool interfaces)
- `google.golang.org/genai` - Gemini API client and types
- `k8s.io/client-go` - Kubernetes client
- `github.com/joho/godotenv` - .env loading
- `gopkg.in/yaml.v3` - Config parsing

## Testing

Requires:
- Valid kubeconfig at `~/.kube/config`
- `GOOGLE_API_KEY` set in `.env` or environment
