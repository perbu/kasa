package tools

import (
	"fmt"

	"github.com/perbu/kasa/manifest"
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
)

// mutatingToolNames is a set of tool names that are classified as mutating.
var mutatingToolNames = map[string]bool{
	"create_namespace":    true,
	"delete_namespace":    true,
	"create_deployment":   true,
	"create_service":      true,
	"create_configmap":    true,
	"create_secret":       true,
	"create_ingress":      true,
	"delete_resource":     true,
	"delete_manifest":     true,
	"apply_manifest":      true,
	"apply_resource":      true,
	"import_resource":     true,
	"commit_manifests":    true,
}

// IsMutating returns true if the given tool name is classified as mutating.
func IsMutating(toolName string) bool {
	return mutatingToolNames[toolName]
}

// KubeTools holds the Kubernetes clientset and provides tool definitions.
type KubeTools struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	manifest      *manifest.Manager
	jinaAPIKey    string
}

// NewKubeTools creates a new KubeTools instance with the given clientset, dynamic client, manifest manager, and Jina API key.
func NewKubeTools(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, manifest *manifest.Manager, jinaAPIKey string) *KubeTools {
	return &KubeTools{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		manifest:      manifest,
		jinaAPIKey:    jinaAPIKey,
	}
}

// All returns all available Kubernetes tools implementing tool.Tool interface.
func (k *KubeTools) All() []tool.Tool {
	return []tool.Tool{
		NewListNamespacesTool(k.clientset),
		NewCreateNamespaceTool(k.clientset),
		NewDeleteNamespaceTool(k.clientset, k.manifest),
		NewListPodsTool(k.clientset),
		NewGetLogsTool(k.clientset),
		NewGetEventsTool(k.clientset),
		NewGetResourceTool(k.clientset, k.dynamicClient),
		NewGetReferenceTool(),
		NewCreateDeploymentTool(k.clientset, k.manifest),
		NewCreateServiceTool(k.clientset, k.manifest),
		NewCreateConfigMapTool(k.clientset, k.manifest),
		NewCreateSecretTool(k.clientset, k.manifest),
		NewCreateIngressTool(k.clientset, k.manifest),
		NewCheckDeploymentHealthTool(k.clientset),
		NewCommitManifestsTool(k.manifest),
		NewListManifestsTool(k.manifest),
		NewReadManifestTool(k.manifest),
		NewDeleteManifestTool(k.clientset, k.manifest),
		NewDeleteResourceTool(k.clientset, k.dynamicClient, k.manifest),
		NewImportResourceTool(k.clientset, k.dynamicClient, k.manifest),
		NewApplyManifestTool(k.clientset, k.manifest),
		NewDryRunApplyTool(k.clientset, k.manifest),
		NewProposePlanTool(),
		// Generic resource tools using dynamic client
		NewApplyResourceTool(k.dynamicClient, k.manifest),
		NewListResourcesTool(k.dynamicClient),
		// Utility tools
		NewSleepTool(),
		NewWaitForConditionTool(k.clientset, k.dynamicClient),
		// Web tools
		NewFetchUrlTool(k.jinaAPIKey),
	}
}

// ReadOnlyTools returns tools that only read data and have no side effects.
func (k *KubeTools) ReadOnlyTools() []tool.Tool {
	all := k.All()
	result := make([]tool.Tool, 0, len(all))
	for _, t := range all {
		if !IsMutating(t.Name()) {
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
		if IsMutating(t.Name()) {
			result = append(result, t)
		}
	}
	return result
}

// functionTool is an interface for tools that provide function declarations
type functionTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
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
