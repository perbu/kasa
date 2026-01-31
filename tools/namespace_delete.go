package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DeleteNamespaceTool provides the delete_namespace tool for the agent.
type DeleteNamespaceTool struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewDeleteNamespaceTool creates a new DeleteNamespaceTool.
func NewDeleteNamespaceTool(clientset *kubernetes.Clientset, manifest *manifest.Manager) *DeleteNamespaceTool {
	return &DeleteNamespaceTool{
		clientset: clientset,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *DeleteNamespaceTool) Name() string {
	return "delete_namespace"
}

// Description returns the tool description.
func (t *DeleteNamespaceTool) Description() string {
	return "Delete a Kubernetes namespace and optionally remove any stored manifests for that namespace. Warning: This deletes all resources in the namespace."
}

// IsLongRunning returns false as this is a quick operation.
func (t *DeleteNamespaceTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *DeleteNamespaceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *DeleteNamespaceTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"name": {
					Type:        "string",
					Description: "The name of the namespace to delete",
				},
				"force": {
					Type:        "boolean",
					Description: "Force deletion even if namespace contains resources (default: false)",
				},
				"delete_manifests": {
					Type:        "boolean",
					Description: "Also delete stored manifests for this namespace (default: true)",
				},
			},
			Required: []string{"name"},
		},
	}
}

// Run executes the tool.
func (t *DeleteNamespaceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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
	name, ok := argsMap["name"].(string)
	if !ok || name == "" {
		return map[string]any{"error": "name is required"}, nil
	}

	// Protect system namespaces
	protectedNamespaces := map[string]bool{
		"default":         true,
		"kube-system":     true,
		"kube-public":     true,
		"kube-node-lease": true,
	}
	if protectedNamespaces[name] {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Cannot delete protected namespace: %s", name),
		}, nil
	}

	force := false
	if f, ok := argsMap["force"].(bool); ok {
		force = f
	}

	deleteManifests := true
	if dm, ok := argsMap["delete_manifests"].(bool); ok {
		deleteManifests = dm
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Check if namespace exists
	ns, err := t.clientset.CoreV1().Namespaces().Get(timeoutCtx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Namespace doesn't exist in cluster, but we might still want to clean up manifests
			if deleteManifests {
				deletedManifests, _ := t.manifest.DeleteNamespace(name)
				if len(deletedManifests) > 0 {
					return map[string]any{
						"success":           true,
						"namespace_deleted": false,
						"manifests_deleted": deletedManifests,
						"message":           fmt.Sprintf("Namespace %s not found in cluster, but deleted %d manifest(s)", name, len(deletedManifests)),
					}, nil
				}
			}
			return map[string]any{
				"success": false,
				"error":   fmt.Sprintf("Namespace %s not found", name),
			}, nil
		}
		return map[string]any{"error": fmt.Sprintf("failed to get namespace: %v", err)}, nil
	}

	// Check if namespace is empty (unless force is set)
	if !force {
		// Check for pods in the namespace
		pods, err := t.clientset.CoreV1().Pods(name).List(timeoutCtx, metav1.ListOptions{Limit: 1})
		if err == nil && len(pods.Items) > 0 {
			return map[string]any{
				"success": false,
				"error":   fmt.Sprintf("Namespace %s is not empty (contains pods). Use force=true to delete anyway.", name),
				"hint":    "Warning: force deletion will delete all resources in the namespace",
			}, nil
		}

		// Check for other resources
		deployments, err := t.clientset.AppsV1().Deployments(name).List(timeoutCtx, metav1.ListOptions{Limit: 1})
		if err == nil && len(deployments.Items) > 0 {
			return map[string]any{
				"success": false,
				"error":   fmt.Sprintf("Namespace %s is not empty (contains deployments). Use force=true to delete anyway.", name),
				"hint":    "Warning: force deletion will delete all resources in the namespace",
			}, nil
		}
	}

	// Check if namespace is already terminating
	if ns.Status.Phase == "Terminating" {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Namespace %s is already being deleted", name),
		}, nil
	}

	// Delete the namespace
	deletePolicy := metav1.DeletePropagationForeground
	err = t.clientset.CoreV1().Namespaces().Delete(timeoutCtx, name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to delete namespace: %v", err)}, nil
	}

	result := map[string]any{
		"success":           true,
		"name":              name,
		"namespace_deleted": true,
		"message":           fmt.Sprintf("Namespace %s deletion initiated", name),
	}

	// Delete stored manifests if requested
	if deleteManifests {
		deletedManifests, err := t.manifest.DeleteNamespace(name)
		if err != nil {
			result["manifest_error"] = err.Error()
		} else if len(deletedManifests) > 0 {
			result["manifests_deleted"] = deletedManifests
		}
	}

	return result, nil
}
