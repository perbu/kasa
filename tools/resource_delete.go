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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DeleteResourceTool provides the delete_resource tool for the agent.
type DeleteResourceTool struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewDeleteResourceTool creates a new DeleteResourceTool.
func NewDeleteResourceTool(clientset *kubernetes.Clientset, manifest *manifest.Manager) *DeleteResourceTool {
	return &DeleteResourceTool{
		clientset: clientset,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *DeleteResourceTool) Name() string {
	return "delete_resource"
}

// Description returns the tool description.
func (t *DeleteResourceTool) Description() string {
	return "Delete a Kubernetes resource from the cluster. Optionally removes the stored manifest if one exists."
}

// IsLongRunning returns false as this is a quick operation.
func (t *DeleteResourceTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *DeleteResourceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *DeleteResourceTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"type": {
					Type:        "string",
					Description: "The resource type: pod, deployment, service, configmap, secret, ingress (aliases: po, deploy, svc, cm, ing)",
				},
				"name": {
					Type:        "string",
					Description: "The name of the resource to delete",
				},
				"namespace": {
					Type:        "string",
					Description: "The Kubernetes namespace",
				},
				"delete_manifest": {
					Type:        "boolean",
					Description: "Also delete the stored manifest if one exists (default: true)",
				},
			},
			Required: []string{"type", "name", "namespace"},
		},
	}
}

// Run executes the tool.
func (t *DeleteResourceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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
	resourceType, ok := argsMap["type"].(string)
	if !ok || resourceType == "" {
		return map[string]any{"error": "type is required"}, nil
	}

	name, ok := argsMap["name"].(string)
	if !ok || name == "" {
		return map[string]any{"error": "name is required"}, nil
	}

	namespace, ok := argsMap["namespace"].(string)
	if !ok || namespace == "" {
		return map[string]any{"error": "namespace is required"}, nil
	}

	deleteManifest := true
	if dm, ok := argsMap["delete_manifest"].(bool); ok {
		deleteManifest = dm
	}

	// Normalize resource type
	normalizedType := normalizeResourceType(resourceType)
	if normalizedType == "" {
		return map[string]any{
			"error": fmt.Sprintf("unsupported resource type: %s. Supported: pod, deployment, service, configmap, secret, ingress", resourceType),
		}, nil
	}

	// Delete from cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := t.deleteFromCluster(timeoutCtx, namespace, name, normalizedType)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, nil
	}

	result := map[string]any{
		"success":   true,
		"type":      normalizedType,
		"name":      name,
		"namespace": namespace,
		"message":   fmt.Sprintf("Deleted %s/%s from namespace %s", normalizedType, name, namespace),
	}

	// Delete manifest if requested and it exists
	if deleteManifest && normalizedType != "pod" {
		if t.manifest.ManifestExists(namespace, name, normalizedType) {
			deleted, err := t.manifest.DeleteManifest(namespace, name, normalizedType)
			if err != nil {
				result["manifest_error"] = err.Error()
			} else {
				result["manifest_deleted"] = deleted
			}
		}
	}

	return result, nil
}

// normalizeResourceType converts type aliases to canonical names.
func normalizeResourceType(resourceType string) string {
	switch resourceType {
	case "pod", "pods", "po":
		return "pod"
	case "deployment", "deployments", "deploy":
		return "deployment"
	case "service", "services", "svc":
		return "service"
	case "configmap", "configmaps", "cm":
		return "configmap"
	case "secret", "secrets":
		return "secret"
	case "ingress", "ingresses", "ing":
		return "ingress"
	default:
		return ""
	}
}

// deleteFromCluster deletes a resource from the Kubernetes cluster.
func (t *DeleteResourceTool) deleteFromCluster(ctx context.Context, namespace, name, resourceType string) error {
	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	switch resourceType {
	case "pod":
		return t.clientset.CoreV1().Pods(namespace).Delete(ctx, name, deleteOptions)
	case "deployment":
		return t.clientset.AppsV1().Deployments(namespace).Delete(ctx, name, deleteOptions)
	case "service":
		return t.clientset.CoreV1().Services(namespace).Delete(ctx, name, deleteOptions)
	case "configmap":
		return t.clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, name, deleteOptions)
	case "secret":
		return t.clientset.CoreV1().Secrets(namespace).Delete(ctx, name, deleteOptions)
	case "ingress":
		return t.clientset.NetworkingV1().Ingresses(namespace).Delete(ctx, name, deleteOptions)
	default:
		return fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}
