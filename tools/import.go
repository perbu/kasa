package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// ImportResourceTool provides the import_resource tool for the agent.
type ImportResourceTool struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	manifest      *manifest.Manager
}

// NewImportResourceTool creates a new ImportResourceTool.
func NewImportResourceTool(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, manifest *manifest.Manager) *ImportResourceTool {
	return &ImportResourceTool{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		manifest:      manifest,
	}
}

// Name returns the tool name.
func (t *ImportResourceTool) Name() string {
	return "import_resource"
}

// Description returns the tool description.
func (t *ImportResourceTool) Description() string {
	return "Import an existing Kubernetes resource from the cluster into managed manifests. Fetches the resource, removes runtime fields, and saves it to the manifest directory."
}

// IsLongRunning returns false as this is a quick operation.
func (t *ImportResourceTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *ImportResourceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ImportResourceTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "The Kubernetes namespace of the resource",
				},
				"name": {
					Type:        "string",
					Description: "The name of the resource to import",
				},
				"kind": {
					Type:        "string",
					Description: "The resource type. Core: deployment, service, configmap, secret, ingress (aliases: deploy, svc, cm). Also supports CRDs: httproute, gateway, certificate, etc.",
				},
				"api_version": {
					Type:        "string",
					Description: "API version for CRDs (e.g., 'gateway.networking.k8s.io/v1'). Only needed for unknown resource types.",
				},
				"overwrite": {
					Type:        "boolean",
					Description: "If true, overwrite an existing manifest. Default is false.",
				},
			},
			Required: []string{"namespace", "name", "kind"},
		},
	}
}

// Run executes the tool.
func (t *ImportResourceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	namespace, ok := argsMap["namespace"].(string)
	if !ok || namespace == "" {
		return map[string]any{"error": "namespace is required"}, nil
	}

	name, ok := argsMap["name"].(string)
	if !ok || name == "" {
		return map[string]any{"error": "name is required"}, nil
	}

	kind, ok := argsMap["kind"].(string)
	if !ok || kind == "" {
		return map[string]any{"error": "kind is required"}, nil
	}

	apiVersion := ""
	if av, ok := argsMap["api_version"].(string); ok {
		apiVersion = av
	}

	overwrite := false
	if ow, ok := argsMap["overwrite"].(bool); ok {
		overwrite = ow
	}

	// Normalize kind - first check if it's a known core type
	resourceType := normalizeKind(kind)
	useDynamic := false

	if resourceType == "" {
		// Check if it's a known CRD kind from our GVR table
		normalized := NormalizeKindName(kind)
		if _, found := LookupGVR(normalized); found || apiVersion != "" {
			resourceType = normalized
			useDynamic = true
		} else {
			return map[string]any{
				"error": fmt.Sprintf("unsupported resource kind: %s. Provide api_version for custom resources.", kind),
			}, nil
		}
	}

	// Check if manifest already exists
	if !overwrite && t.manifest.ManifestExists(namespace, name, resourceType) {
		return map[string]any{
			"exists":  true,
			"message": "Manifest already exists. Call with overwrite=true to replace.",
			"hint":    "Use read_manifest to view existing content before overwriting",
		}, nil
	}

	// Fetch resource from cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var resourceMap map[string]any
	var err error

	if useDynamic {
		resourceMap, err = t.fetchDynamicResource(timeoutCtx, namespace, name, resourceType, apiVersion)
	} else {
		resourceMap, err = t.fetchResource(timeoutCtx, namespace, name, resourceType)
	}
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}

	// Clean runtime fields
	cleanForImport(resourceMap)

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(resourceMap)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to marshal resource: %v", err)}, nil
	}

	// Save manifest
	manifestPath, err := t.manifest.SaveManifest(namespace, name, resourceType, yamlBytes)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to save manifest: %v", err)}, nil
	}

	result := map[string]any{
		"success":       true,
		"name":          name,
		"namespace":     namespace,
		"kind":          resourceType,
		"manifest_path": manifestPath,
		"message":       fmt.Sprintf("Imported %s/%s from cluster to %s", resourceType, name, manifestPath),
	}

	// Add warning for secrets
	if resourceType == "secret" {
		result["warning"] = "Secret data imported. Ensure manifest directory is secured."
	}

	return result, nil
}

// normalizeKind converts kind aliases to canonical names.
func normalizeKind(kind string) string {
	switch strings.ToLower(kind) {
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

// fetchResource fetches a resource from the cluster and converts it to a map.
func (t *ImportResourceTool) fetchResource(ctx context.Context, namespace, name, resourceType string) (map[string]any, error) {
	var obj any
	var err error

	switch resourceType {
	case "deployment":
		obj, err = t.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	case "service":
		obj, err = t.clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	case "configmap":
		obj, err = t.clientset.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	case "secret":
		obj, err = t.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	case "ingress":
		obj, err = t.clientset.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
	default:
		return nil, fmt.Errorf("unsupported resource type: %s", resourceType)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s/%s: %v", resourceType, name, err)
	}

	// Convert to map via JSON
	return toMap(obj)
}

// fetchDynamicResource fetches any resource using the dynamic client.
func (t *ImportResourceTool) fetchDynamicResource(ctx context.Context, namespace, name, kind, apiVersion string) (map[string]any, error) {
	if t.dynamicClient == nil {
		return nil, fmt.Errorf("dynamic client not available")
	}

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
		return nil, fmt.Errorf("failed to fetch %s/%s: %v", kind, name, err)
	}

	return obj.Object, nil
}
