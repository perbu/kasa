package tools

import (
	"fmt"
	"strings"

	"github.com/perbu/kasa/manifest"
	"github.com/perbu/kasa/workspace"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// ToolCategory classifies tools by their side effects.
type ToolCategory string

const (
	// CategoryReadOnly indicates tools that only read data and have no side effects.
	CategoryReadOnly ToolCategory = "read-only"
	// CategoryMutating indicates tools that modify cluster or manifest state.
	CategoryMutating ToolCategory = "mutating"
	// CategoryPlanning indicates tools used for planning workflows.
	CategoryPlanning ToolCategory = "planning"
)

// IsMutating returns true if the given tool is classified as mutating.
func IsMutating(t tool.Tool) bool {
	if ft, ok := t.(functionTool); ok {
		return ft.Category() == CategoryMutating
	}
	return false
}

// KubeTools holds the Kubernetes clientset and provides tool definitions.
type KubeTools struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	manifest      *manifest.Manager
	workspace     *workspace.Workspace
	jinaAPIKey    string
	counter       *ToolCallCounter
	directIO      *DirectIO
	guard         *MutationGuard
	driftCache    *DriftCache
}

// NewKubeTools creates a new KubeTools instance with the given clientset, dynamic client, manifest manager, and Jina API key.
// The warnThreshold controls how many calls to the same tool before a warning is
// injected into the response. Pass 0 to disable per-tool warnings.
// The driftCache is optional (nil disables caching); it persists drift scan results
// across restarts and invalidates after mutating operations.
func NewKubeTools(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, manifest *manifest.Manager, ws *workspace.Workspace, jinaAPIKey string, warnThreshold int, directIO *DirectIO, driftCache *DriftCache) *KubeTools {
	// Install discovery-backed resolver for dynamic CRD resolution.
	SetResolver(NewResourceResolver(clientset.Discovery()))

	return &KubeTools{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		manifest:      manifest,
		workspace:     ws,
		jinaAPIKey:    jinaAPIKey,
		counter:       NewToolCallCounter(warnThreshold),
		directIO:      directIO,
		guard:         NewMutationGuard(),
		driftCache:    driftCache,
	}
}

// DirectIO returns the DirectIO instance for side-channel communication.
func (k *KubeTools) DirectIO() *DirectIO {
	return k.directIO
}

// Counter returns the shared tool call counter so the REPL can reset it between turns.
func (k *KubeTools) Counter() *ToolCallCounter {
	return k.counter
}

// Guard returns the mutation guard so the REPL can toggle it based on
// the plan/approval workflow.
func (k *KubeTools) Guard() *MutationGuard {
	return k.guard
}

// DriftCache returns the drift cache for background scanning and invalidation.
func (k *KubeTools) DriftCache() *DriftCache {
	return k.driftCache
}

// All returns all available Kubernetes tools implementing tool.Tool interface.
// Each tool is wrapped with an invocation counter that injects a warning into
// the response when a single tool is called too many times in one turn.
func (k *KubeTools) All() []tool.Tool {
	raw := []tool.Tool{
		NewListNamespacesTool(k.clientset),
		NewDeleteNamespaceTool(k.clientset, k.manifest),
		NewListPodsTool(k.clientset),
		NewGetLogsTool(k.clientset),
		NewGetEventsTool(k.clientset),
		NewGetResourceTool(k.clientset, k.dynamicClient),
		NewGetReferenceTool(),
		NewCheckDeploymentHealthTool(k.clientset),
		NewSyncManifestsTool(k.manifest),
		NewManifestStatusTool(k.manifest),
		NewListManifestsTool(k.manifest),
		NewReadManifestTool(k.manifest),
		NewSaveNotesTool(k.manifest),
		NewDeleteManifestTool(k.clientset, k.manifest),
		NewDeleteResourceTool(k.dynamicClient, k.manifest),
		NewImportResourceTool(k.dynamicClient, k.manifest),
		NewApplyManifestTool(k.dynamicClient, k.manifest),
		NewDryRunApplyTool(k.dynamicClient, k.manifest),
		NewProposePlanTool(),
		NewAskClarificationTool(),
		// Generic resource tools using dynamic client
		NewApplyResourceTool(k.dynamicClient, k.manifest),
		NewListResourcesTool(k.dynamicClient),
		NewDiffResourceTool(k.dynamicClient, k.manifest),
		// Git staging
		NewStageFileTool(k.manifest),
		// Drift scanning
		NewShowDriftTool(k.driftCache, k.dynamicClient, k.manifest),
		// Utility tools
		NewSleepTool(),
		NewWaitForConditionTool(k.clientset, k.dynamicClient),
		// Web tools
		NewFetchUrlTool(k.jinaAPIKey),
		NewSearchWebTool(k.jinaAPIKey),
		// HTTP verification tool
		NewHTTPRequestTool(),
		// Helm inspection tools
		NewListHelmReleasesTool(k.clientset),
		NewGetHelmReleaseTool(k.clientset),
		NewGetHelmValuesTool(k.clientset),
		// Secret tools (side-channel: values bypass the LLM)
		NewCreateSecretTool(k.clientset, k.directIO),
		NewShowSecretTool(k.clientset, k.directIO),
	}

	// Local workspace tools — only registered when a workspace root is configured.
	if k.workspace != nil {
		raw = append(raw,
			NewListWorkspaceTool(k.workspace),
			NewReadWorkspaceFileTool(k.workspace),
		)
	}

	wrapped := make([]tool.Tool, len(raw))
	for i, t := range raw {
		if rt, ok := t.(runnableTool); ok {
			wrapped[i] = &countingTool{inner: rt, counter: k.counter, guard: k.guard, driftCache: k.driftCache}
		} else {
			wrapped[i] = t // shouldn't happen, but don't break
		}
	}
	return wrapped
}

// ReadOnlyTools returns tools that only read data and have no side effects.
func (k *KubeTools) ReadOnlyTools() []tool.Tool {
	all := k.All()
	result := make([]tool.Tool, 0, len(all))
	for _, t := range all {
		if ft, ok := t.(functionTool); ok && ft.Category() == CategoryReadOnly {
			result = append(result, t)
		}
	}
	return result
}

// MutatingTools returns tools that modify cluster or manifest state.
func (k *KubeTools) MutatingTools() []tool.Tool {
	all := k.All()
	result := make([]tool.Tool, 0, len(all))
	for _, t := range all {
		if ft, ok := t.(functionTool); ok && ft.Category() == CategoryMutating {
			result = append(result, t)
		}
	}
	return result
}

// GenerateToolDocs generates markdown documentation for all tools organized by category.
func (k *KubeTools) GenerateToolDocs() string {
	var readOnly, mutating, planning []string

	for _, t := range k.All() {
		ft, ok := t.(functionTool)
		if !ok {
			continue
		}
		line := fmt.Sprintf("- %s - %s", ft.Name(), ft.Description())

		switch ft.Category() {
		case CategoryReadOnly:
			readOnly = append(readOnly, line)
		case CategoryMutating:
			mutating = append(mutating, line)
		case CategoryPlanning:
			planning = append(planning, line)
		}
	}

	return fmt.Sprintf(`### Read-Only Tools (use freely for gathering information)
%s

### Mutating Tools (require plan approval)
%s

### Planning Tools
%s`,
		strings.Join(readOnly, "\n"),
		strings.Join(mutating, "\n"),
		strings.Join(planning, "\n"))
}

// functionTool is an interface for tools that provide function declarations and categories.
type functionTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
	Category() ToolCategory
}

// addFunctionTool adds a function tool to the LLM request
func addFunctionTool(req *model.LLMRequest, t functionTool) error {
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}

	decl := t.Declaration()
	if decl == nil {
		return fmt.Errorf("tool %q has no declaration", t.Name())
	}

	// Inject "reason" parameter into every tool declaration
	// This allows us to display why the tool is being called
	if decl.Parameters != nil && decl.Parameters.Properties != nil {
		decl.Parameters.Properties["reason"] = &genai.Schema{
			Type:        "string",
			Description: "Brief explanation of why you are calling this tool (shown to user)",
		}
	}

	// Add to tools map for execution lookup
	if req.Tools == nil {
		req.Tools = make(map[string]any)
	}
	req.Tools[t.Name()] = t

	// Add function declaration to config
	req.Config.Tools = append(req.Config.Tools, &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{decl},
	})

	return nil
}
