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
├── main.go              # Entry point, agent setup, runner/session, REPL with approval flow
├── config.yaml          # Configuration and prompts
├── session_state.go     # SessionState, Plan, PlannedAction types for approval workflow
├── plan_display.go      # Plan display and parsing utilities
├── status.go            # Terminal status line display
├── tools/
│   ├── tools.go         # KubeTools struct, tool categories, registration
│   ├── gvr.go           # GVR helpers, CommonGVRs map, kind aliases for dynamic client
│   ├── propose_plan.go  # propose_plan tool for safe mode approval workflow
│   ├── namespaces.go    # list_namespaces tool
│   ├── namespace_create.go # create_namespace tool
│   ├── namespace_delete.go # delete_namespace tool
│   ├── pods.go          # list_pods tool
│   ├── logs.go          # get_logs tool
│   ├── events.go        # get_events tool
│   ├── resource.go      # get_resource tool (with dynamic client fallback)
│   ├── resource_delete.go # delete_resource tool (with dynamic client support)
│   ├── references.go    # get_reference tool (K8s resource docs)
│   ├── deploy.go        # create_deployment tool
│   ├── service_create.go # create_service tool
│   ├── configmap_create.go # create_configmap tool
│   ├── secret_create.go # create_secret tool
│   ├── ingress_create.go # create_ingress tool
│   ├── health.go        # check_deployment_health tool
│   ├── commit.go        # commit_manifests tool
│   ├── manifest_list.go # list_manifests tool
│   ├── manifest_read.go # read_manifest tool
│   ├── manifest_delete.go # delete_manifest tool
│   ├── apply.go         # apply_manifest tool
│   ├── apply_resource.go # apply_resource tool (generic, any YAML)
│   ├── list_resources.go # list_resources tool (generic, any kind)
│   ├── import.go        # import_resource tool (with dynamic client support)
│   └── dryrun.go        # dry_run_apply tool
├── manifest/
│   └── manifest.go      # Manifest file storage and git operations
├── references/
│   ├── references.go    # Embedded documentation lookup
│   └── data/*.md        # Reference docs for K8s resources
├── deployments/         # Git-tracked manifest storage (created at runtime)
├── .env                 # API keys (gitignored)
└── spec.md              # Full project specification
```

## Safe Mode (Plan/Approval Workflow)

Kasa operates in **Safe Mode** by default in interactive REPL. Mutating operations require user approval before execution.

### How It Works

1. User requests a change (e.g., "deploy nginx")
2. Agent gathers information using read-only tools
3. Agent calls `propose_plan` with description and actions
4. REPL detects the plan and displays it for review
5. User types `yes` to approve or `no` to reject
6. If approved, agent executes the planned actions

### Tool Categories

Tools are classified in `tools/tools.go`:

**Read-Only (use freely):**
- list_namespaces, list_pods, get_logs, get_events, get_resource
- get_reference, check_deployment_health
- list_manifests, read_manifest, dry_run_apply
- list_resources (generic, supports CRDs)

**Mutating (require plan approval):**
- create_namespace, delete_namespace
- create_deployment, create_service, create_configmap, create_secret, create_ingress
- delete_resource, delete_manifest
- apply_manifest, apply_resource, import_resource, commit_manifests

### REPL Commands

- `yes` / `y` / `/approve` - Approve pending plan
- `no` / `n` / `/reject` - Reject pending plan
- `/plan` - Display pending plan again

### Key Files

- `session_state.go` - `SessionState`, `Plan`, `PlannedAction` types
- `plan_display.go` - `DisplayPlan()`, `ParsePlanFromResponse()`, `FormatExecutionPrompt()`
- `tools/propose_plan.go` - The `propose_plan` tool
- `tools/tools.go` - `IsMutating()`, `ReadOnlyTools()`, `MutatingTools()`

### Non-Interactive Mode

When using `-prompt`, safe mode is disabled (no approval workflow). The agent executes directly.

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
           NewMyTool(k.clientset),                    // K8s typed client only
           NewMyTool(k.dynamicClient),                // Dynamic client only (for CRDs)
           NewMyTool(k.clientset, k.dynamicClient),   // Both clients
           NewMyTool(k.manifest),                     // Manifest-only tool
           NewMyTool(k.clientset, k.dynamicClient, k.manifest), // All three
       }
   }
   ```
3. If the tool modifies state, add it to `mutatingToolNames` in `tools/tools.go`:
   ```go
   var mutatingToolNames = map[string]bool{
       // ... existing tools ...
       "my_tool": true,
   }
   ```
4. Build and test

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

### Dynamic Client & CRD Support

Kasa uses both typed clients (for core resources) and a dynamic client (for CRDs and unknown resources). The `tools/gvr.go` file contains:

- `CommonGVRs` - Map of 30+ known resource kinds to their GroupVersionResource
- `KindAliases` - User-friendly aliases (e.g., `deploy` → `deployment`, `gw` → `gateway`)
- Helper functions: `NormalizeKindName()`, `LookupGVR()`, `IsNamespaced()`, `ParseYAMLToUnstructured()`, `GVKToGVR()`

**Supported CRDs out of the box:**
- **Gateway API**: Gateway, HTTPRoute, GRPCRoute, TCPRoute, UDPRoute, TLSRoute, ReferenceGrant, GatewayClass
- **cert-manager**: Certificate, Issuer, ClusterIssuer, CertificateRequest
- **Autoscaling**: HorizontalPodAutoscaler

**Generic tools for any resource:**
- `apply_resource` - Apply any YAML manifest (creates or updates)
- `list_resources` - List any resource type by kind
- `get_resource` - Get any resource (falls back to dynamic client for unknown kinds)
- `import_resource` - Import any resource from cluster to manifests
- `delete_resource` - Delete any resource type

For unknown CRDs, provide the `api_version` parameter (e.g., `gateway.networking.k8s.io/v1`).

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
- `k8s.io/client-go` - Kubernetes typed client and dynamic client
- `k8s.io/apimachinery` - Kubernetes API types and unstructured objects
- `github.com/joho/godotenv` - .env loading
- `gopkg.in/yaml.v3` - Config parsing
- `sigs.k8s.io/yaml` - YAML/JSON conversion for Kubernetes objects

## Testing

Requires:
- Valid kubeconfig at `~/.kube/config`
- `GOOGLE_API_KEY` set in `.env` or environment
