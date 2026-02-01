package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DeleteManifestTool provides the delete_manifest tool for the agent.
type DeleteManifestTool struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewDeleteManifestTool creates a new DeleteManifestTool.
func NewDeleteManifestTool(clientset *kubernetes.Clientset, manifest *manifest.Manager) *DeleteManifestTool {
	return &DeleteManifestTool{
		clientset: clientset,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *DeleteManifestTool) Name() string {
	return "delete_manifest"
}

// Description returns the tool description.
func (t *DeleteManifestTool) Description() string {
	return "Delete manifest files from the deployments directory. Can optionally delete the resources from the Kubernetes cluster as well."
}

// IsLongRunning returns false as this is a quick operation.
func (t *DeleteManifestTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *DeleteManifestTool) Category() ToolCategory {
	return CategoryMutating
}

// ProcessRequest adds this tool to the LLM request.
func (t *DeleteManifestTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *DeleteManifestTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "The Kubernetes namespace",
				},
				"app": {
					Type:        "string",
					Description: "The application name",
				},
				"type": {
					Type:        "string",
					Description: "The resource type (e.g., deployment, service). If empty, deletes all manifests for the app.",
				},
				"delete_from_cluster": {
					Type:        "boolean",
					Description: "Also delete the resources from the Kubernetes cluster (default: true)",
				},
			},
			Required: []string{"namespace", "app"},
		},
	}
}

// Run executes the tool.
func (t *DeleteManifestTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	// Parse arguments
	argsMap, ok := args.(map[string]any)
	if !ok {
		if argsStr, ok := args.(string); ok {
			if err := json.Unmarshal([]byte(argsStr), &argsMap); err != nil {
				return map[string]any{"error": "invalid arguments format"}, nil
			}
		} else {
			return map[string]any{"error": "invalid arguments type"}, nil
		}
	}

	// Extract required parameters
	namespace, ok := argsMap["namespace"].(string)
	if !ok || namespace == "" {
		return map[string]any{"error": "namespace is required"}, nil
	}

	app, ok := argsMap["app"].(string)
	if !ok || app == "" {
		return map[string]any{"error": "app is required"}, nil
	}

	// Extract optional parameters
	resourceType, _ := argsMap["type"].(string)

	deleteFromCluster := true
	if val, ok := argsMap["delete_from_cluster"].(bool); ok {
		deleteFromCluster = val
	}

	// Get list of manifests to delete (for cluster deletion)
	var manifestsToDelete []manifest.ManifestInfo
	if deleteFromCluster {
		manifests, err := t.manifest.ListManifests(namespace, app)
		if err == nil {
			if resourceType != "" {
				// Filter to specific type
				for _, m := range manifests {
					if m.Type == resourceType {
						manifestsToDelete = append(manifestsToDelete, m)
					}
				}
			} else {
				manifestsToDelete = manifests
			}
		}
	}

	// Delete manifest files
	deleted, err := t.manifest.DeleteManifest(namespace, app, resourceType)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, nil
	}

	// Delete from cluster if requested
	var clusterDeleteErrors []string
	if deleteFromCluster && len(manifestsToDelete) > 0 {
		for _, m := range manifestsToDelete {
			if err := t.deleteFromCluster(ctx, m.Namespace, app, m.Type); err != nil {
				clusterDeleteErrors = append(clusterDeleteErrors, fmt.Sprintf("%s: %v", m.Type, err))
			}
		}
	}

	result := map[string]any{
		"success": true,
		"deleted": deleted,
		"message": fmt.Sprintf("Deleted %d manifest(s)", len(deleted)),
	}

	if len(clusterDeleteErrors) > 0 {
		result["cluster_errors"] = clusterDeleteErrors
	}

	return result, nil
}

// deleteFromCluster deletes a resource from the Kubernetes cluster.
func (t *DeleteManifestTool) deleteFromCluster(_ tool.Context, namespace, app, resourceType string) error {
	goCtx := context.Background()
	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	switch resourceType {
	case "deployment":
		return t.clientset.AppsV1().Deployments(namespace).Delete(goCtx, app, deleteOptions)
	case "service":
		return t.clientset.CoreV1().Services(namespace).Delete(goCtx, app, deleteOptions)
	case "configmap":
		return t.clientset.CoreV1().ConfigMaps(namespace).Delete(goCtx, app, deleteOptions)
	case "secret":
		return t.clientset.CoreV1().Secrets(namespace).Delete(goCtx, app, deleteOptions)
	case "ingress":
		return t.clientset.NetworkingV1().Ingresses(namespace).Delete(goCtx, app, deleteOptions)
	default:
		return fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}
