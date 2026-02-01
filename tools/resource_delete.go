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
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// DeleteResourceTool provides the delete_resource tool for the agent.
type DeleteResourceTool struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	manifest      *manifest.Manager
}

// NewDeleteResourceTool creates a new DeleteResourceTool.
func NewDeleteResourceTool(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, manifest *manifest.Manager) *DeleteResourceTool {
	return &DeleteResourceTool{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		manifest:      manifest,
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
					Description: "The resource type. Core: pod, deployment, service, configmap, secret, ingress (aliases: po, deploy, svc, cm, ing). Also supports CRDs: httproute, gateway, certificate, etc.",
				},
				"name": {
					Type:        "string",
					Description: "The name of the resource to delete",
				},
				"namespace": {
					Type:        "string",
					Description: "The Kubernetes namespace",
				},
				"api_version": {
					Type:        "string",
					Description: "API version for CRDs (e.g., 'gateway.networking.k8s.io/v1'). Only needed for unknown resource types.",
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

	apiVersion := ""
	if av, ok := argsMap["api_version"].(string); ok {
		apiVersion = av
	}

	deleteManifest := true
	if dm, ok := argsMap["delete_manifest"].(bool); ok {
		deleteManifest = dm
	}

	// Normalize resource type - first check if it's a known core type
	normalizedType := normalizeResourceType(resourceType)
	useDynamic := false

	if normalizedType == "" {
		// Check if it's a known CRD kind from our GVR table
		normalized := NormalizeKindName(resourceType)
		if _, found := LookupGVR(normalized); found || apiVersion != "" {
			normalizedType = normalized
			useDynamic = true
		} else {
			return map[string]any{
				"error": fmt.Sprintf("unsupported resource type: %s. Provide api_version for custom resources.", resourceType),
			}, nil
		}
	}

	// Delete from cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var err error
	if useDynamic {
		err = t.deleteDynamicResource(timeoutCtx, namespace, name, normalizedType, apiVersion)
	} else {
		err = t.deleteFromCluster(timeoutCtx, namespace, name, normalizedType)
	}
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

// deleteDynamicResource deletes any resource using the dynamic client.
func (t *DeleteResourceTool) deleteDynamicResource(ctx context.Context, namespace, name, kind, apiVersion string) error {
	if t.dynamicClient == nil {
		return fmt.Errorf("dynamic client not available")
	}

	// Build GVR from kind and api_version
	gvr, found := BuildGVRFromKindAndAPIVersion(kind, apiVersion)
	if !found && apiVersion == "" {
		return fmt.Errorf("unknown resource kind '%s'. Provide api_version for custom resources", kind)
	}

	// Check if resource is namespaced
	namespaced := IsNamespaced(kind)

	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	// Get the resource interface and delete
	if namespaced {
		return t.dynamicClient.Resource(gvr).Namespace(namespace).Delete(ctx, name, deleteOptions)
	}
	return t.dynamicClient.Resource(gvr).Delete(ctx, name, deleteOptions)
}
