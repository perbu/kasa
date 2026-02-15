package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// ImportResourceTool provides the import_resource tool for the agent.
type ImportResourceTool struct {
	dynamicClient dynamic.Interface
	manifest      *manifest.Manager
}

// NewImportResourceTool creates a new ImportResourceTool.
func NewImportResourceTool(dynamicClient dynamic.Interface, manifest *manifest.Manager) *ImportResourceTool {
	return &ImportResourceTool{
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

// Category returns the tool category.
func (t *ImportResourceTool) Category() ToolCategory {
	return CategoryMutating
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
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	namespace, ok := argsMap["namespace"].(string)
	if !ok || namespace == "" {
		return errorResult("namespace is required")
	}

	name, ok := argsMap["name"].(string)
	if !ok || name == "" {
		return errorResult("name is required")
	}

	kind, ok := argsMap["kind"].(string)
	if !ok || kind == "" {
		return errorResult("kind is required")
	}

	apiVersion := ""
	if av, ok := argsMap["api_version"].(string); ok {
		apiVersion = av
	}

	overwrite := false
	if ow, ok := argsMap["overwrite"].(bool); ok {
		overwrite = ow
	}

	// Normalize kind and validate
	resourceType := NormalizeKindName(kind)
	if _, found := LookupGVR(resourceType); !found && apiVersion == "" {
		return errorResult(fmt.Sprintf("unsupported resource kind: %s. Provide api_version for custom resources.", kind))
	}

	// Check if manifest already exists
	if !overwrite && t.manifest.ManifestExists(namespace, name, resourceType) {
		return map[string]any{
			"exists":  true,
			"message": "Manifest already exists. Call with overwrite=true to replace.",
			"hint":    "Use read_manifest to view existing content before overwriting",
		}, nil
	}

	// Fetch resource from cluster using dynamic client
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	gvr, found := BuildGVRFromKindAndAPIVersion(resourceType, apiVersion)
	if !found && apiVersion == "" {
		return errorResult(fmt.Sprintf("unknown resource kind '%s'. Provide api_version for custom resources", kind))
	}

	namespaced := IsNamespaced(resourceType)
	resourceClient := namespacedClient(t.dynamicClient, gvr, namespace, namespaced)

	obj, getErr := resourceClient.Get(timeoutCtx, name, metav1.GetOptions{})
	if getErr != nil {
		return errorResult(fmt.Sprintf("failed to fetch %s/%s: %v", resourceType, name, getErr))
	}

	resourceMap := obj.Object

	// Clean runtime fields
	cleanForImport(resourceMap)

	// Marshal to YAML
	yamlBytes, marshalErr := yaml.Marshal(resourceMap)
	if marshalErr != nil {
		return errorResult(fmt.Sprintf("failed to marshal resource: %v", marshalErr))
	}

	// Save manifest
	manifestPath, saveErr := t.manifest.SaveManifest(namespace, name, resourceType, yamlBytes)
	if saveErr != nil {
		return errorResult(fmt.Sprintf("failed to save manifest: %v", saveErr))
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
