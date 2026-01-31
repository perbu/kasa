package tools

import (
	"fmt"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	"k8s.io/client-go/kubernetes"
)

// KubeTools holds the Kubernetes clientset and provides tool definitions.
type KubeTools struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewKubeTools creates a new KubeTools instance with the given clientset and manifest manager.
func NewKubeTools(clientset *kubernetes.Clientset, manifest *manifest.Manager) *KubeTools {
	return &KubeTools{
		clientset: clientset,
		manifest:  manifest,
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
		NewGetResourceTool(k.clientset),
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
		NewDeleteResourceTool(k.clientset, k.manifest),
		NewImportResourceTool(k.clientset, k.manifest),
		NewApplyManifestTool(k.clientset, k.manifest),
		NewDryRunApplyTool(k.clientset, k.manifest),
	}
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
