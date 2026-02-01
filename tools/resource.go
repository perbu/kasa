package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// GetResourceTool provides the get_resource tool for the agent.
type GetResourceTool struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
}

// NewGetResourceTool creates a new GetResourceTool.
func NewGetResourceTool(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface) *GetResourceTool {
	return &GetResourceTool{
		clientset:     clientset,
		dynamicClient: dynamicClient,
	}
}

// Name returns the tool name.
func (t *GetResourceTool) Name() string {
	return "get_resource"
}

// Description returns the tool description.
func (t *GetResourceTool) Description() string {
	return "Get detailed information about a Kubernetes resource (similar to kubectl describe)"
}

// IsLongRunning returns false as this is a quick operation.
func (t *GetResourceTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *GetResourceTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *GetResourceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *GetResourceTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"kind": {
					Type:        "string",
					Description: "The resource kind. Core types: deployment, service, pod, configmap, secret, ingress. Also supports CRDs: httproute, gateway, certificate, etc.",
				},
				"name": {
					Type:        "string",
					Description: "The name of the resource",
				},
				"namespace": {
					Type:        "string",
					Description: "The namespace (defaults to 'default')",
				},
				"api_version": {
					Type:        "string",
					Description: "API version for CRDs (e.g., 'gateway.networking.k8s.io/v1'). Only needed for unknown resource types.",
				},
			},
			Required: []string{"kind", "name"},
		},
	}
}

// Run executes the tool.
func (t *GetResourceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	kind := ""
	if k, ok := argsMap["kind"].(string); ok {
		kind = strings.ToLower(k)
	}
	if kind == "" {
		return map[string]any{"error": "kind is required"}, nil
	}

	name := ""
	if n, ok := argsMap["name"].(string); ok {
		name = n
	}
	if name == "" {
		return map[string]any{"error": "name is required"}, nil
	}

	namespace := "default"
	if ns, ok := argsMap["namespace"].(string); ok && ns != "" {
		namespace = ns
	}

	apiVersion := ""
	if av, ok := argsMap["api_version"].(string); ok {
		apiVersion = av
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var resource any
	var err error

	// Try typed clients first for known core resources
	switch kind {
	case "deployment", "deployments", "deploy":
		resource, err = t.getDeployment(timeoutCtx, namespace, name)
	case "service", "services", "svc":
		resource, err = t.getService(timeoutCtx, namespace, name)
	case "pod", "pods", "po":
		resource, err = t.getPod(timeoutCtx, namespace, name)
	case "configmap", "configmaps", "cm":
		resource, err = t.getConfigMap(timeoutCtx, namespace, name)
	case "secret", "secrets":
		resource, err = t.getSecret(timeoutCtx, namespace, name)
	case "ingress", "ingresses", "ing":
		resource, err = t.getIngress(timeoutCtx, namespace, name)
	default:
		// Use dynamic client fallback for unknown kinds
		if t.dynamicClient == nil {
			return map[string]any{
				"error": fmt.Sprintf("unsupported resource kind: %s. Supported core kinds: deployment, service, pod, configmap, secret, ingress", kind),
			}, nil
		}
		resource, err = t.getDynamicResource(timeoutCtx, namespace, name, kind, apiVersion)
	}

	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}

	return map[string]any{"resource": resource}, nil
}

// toMap converts a struct to a map via JSON marshal/unmarshal.
func toMap(obj any) (map[string]any, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (t *GetResourceTool) getDeployment(ctx context.Context, namespace, name string) (map[string]any, error) {
	deployment, err := t.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	result, err := toMap(deployment)
	if err != nil {
		return nil, err
	}

	// Clean up managed fields and other verbose metadata
	cleanMetadata(result)

	return result, nil
}

func (t *GetResourceTool) getService(ctx context.Context, namespace, name string) (map[string]any, error) {
	service, err := t.clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	result, err := toMap(service)
	if err != nil {
		return nil, err
	}

	cleanMetadata(result)

	return result, nil
}

func (t *GetResourceTool) getPod(ctx context.Context, namespace, name string) (map[string]any, error) {
	pod, err := t.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	result, err := toMap(pod)
	if err != nil {
		return nil, err
	}

	cleanMetadata(result)

	return result, nil
}

func (t *GetResourceTool) getConfigMap(ctx context.Context, namespace, name string) (map[string]any, error) {
	configMap, err := t.clientset.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	result, err := toMap(configMap)
	if err != nil {
		return nil, err
	}

	cleanMetadata(result)

	return result, nil
}

func (t *GetResourceTool) getSecret(ctx context.Context, namespace, name string) (map[string]any, error) {
	secret, err := t.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	result, err := toMap(secret)
	if err != nil {
		return nil, err
	}

	cleanMetadata(result)

	// Mask secret data values
	if data, ok := result["data"].(map[string]any); ok {
		for key := range data {
			data[key] = "[REDACTED]"
		}
	}
	if stringData, ok := result["stringData"].(map[string]any); ok {
		for key := range stringData {
			stringData[key] = "[REDACTED]"
		}
	}

	return result, nil
}

func (t *GetResourceTool) getIngress(ctx context.Context, namespace, name string) (map[string]any, error) {
	ingress, err := t.clientset.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	result, err := toMap(ingress)
	if err != nil {
		return nil, err
	}

	cleanMetadata(result)

	return result, nil
}

// cleanMetadata removes verbose metadata fields to reduce output size.
func cleanMetadata(result map[string]any) {
	if metadata, ok := result["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
	}
}

// getDynamicResource fetches any resource type using the dynamic client.
func (t *GetResourceTool) getDynamicResource(ctx context.Context, namespace, name, kind, apiVersion string) (map[string]any, error) {
	// Build GVR from kind and api_version
	gvr, found := BuildGVRFromKindAndAPIVersion(kind, apiVersion)
	if !found && apiVersion == "" {
		return nil, fmt.Errorf("unknown resource kind '%s'. Provide api_version for custom resources", kind)
	}

	// Check if resource is namespaced
	namespaced := IsNamespaced(kind)

	// Get the resource interface
	var resourceClient dynamic.ResourceInterface
	if namespaced {
		resourceClient = t.dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceClient = t.dynamicClient.Resource(gvr)
	}

	obj, err := resourceClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get %s/%s: %v", kind, name, err)
	}

	result := obj.Object
	cleanMetadata(result)

	return result, nil
}
