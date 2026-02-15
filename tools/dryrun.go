package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

// DryRunApplyTool provides the dry_run_apply tool for the agent.
type DryRunApplyTool struct {
	dynamicClient dynamic.Interface
	manifest      *manifest.Manager
}

// NewDryRunApplyTool creates a new DryRunApplyTool.
func NewDryRunApplyTool(dynamicClient dynamic.Interface, manifest *manifest.Manager) *DryRunApplyTool {
	return &DryRunApplyTool{
		dynamicClient: dynamicClient,
		manifest:      manifest,
	}
}

// Name returns the tool name.
func (t *DryRunApplyTool) Name() string {
	return "dry_run_apply"
}

// Description returns the tool description.
func (t *DryRunApplyTool) Description() string {
	return "Validate a manifest against the cluster without applying it. Accepts either inline YAML or a reference to a stored manifest. Uses Kubernetes server-side dry-run to check for errors."
}

// IsLongRunning returns false as this is a quick operation.
func (t *DryRunApplyTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *DryRunApplyTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *DryRunApplyTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *DryRunApplyTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"yaml": {
					Type:        "string",
					Description: "Inline YAML manifest to validate. When provided, namespace/app/type are ignored.",
				},
				"namespace": {
					Type:        "string",
					Description: "The namespace of the stored manifest (used when yaml is not provided)",
				},
				"app": {
					Type:        "string",
					Description: "The app name / manifest directory name (used when yaml is not provided)",
				},
				"type": {
					Type:        "string",
					Description: "The resource type (e.g. deployment, service, configmap, secret, ingress, httproute, etc.) (used when yaml is not provided)",
				},
			},
		},
	}
}

// Run executes the tool.
func (t *DryRunApplyTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	// Check if inline YAML is provided
	if yamlContent, ok := argsMap["yaml"].(string); ok && yamlContent != "" {
		return t.dryRunUnstructured([]byte(yamlContent), "", "", "")
	}

	// Fall back to stored manifest path
	return t.runStoredManifest(argsMap)
}

// runStoredManifest reads a manifest from storage and validates it via dry-run.
func (t *DryRunApplyTool) runStoredManifest(argsMap map[string]any) (map[string]any, error) {
	namespace, ok := argsMap["namespace"].(string)
	if !ok || namespace == "" {
		return errorResult("namespace is required")
	}

	app, ok := argsMap["app"].(string)
	if !ok || app == "" {
		return errorResult("app is required")
	}

	resourceType, ok := argsMap["type"].(string)
	if !ok || resourceType == "" {
		return errorResult("type is required")
	}

	resourceType = NormalizeKindName(resourceType)

	content, err := t.manifest.ReadManifest(namespace, app, resourceType)
	if err != nil {
		return errorResult(err.Error())
	}

	return t.dryRunUnstructured(content, namespace, app, resourceType)
}

// dryRunUnstructured validates YAML content using the dynamic client with server-side dry-run.
// If namespace/app/resourceType are provided (stored manifest path), they are included in the result.
func (t *DryRunApplyTool) dryRunUnstructured(content []byte, namespace, app, resourceType string) (map[string]any, error) {
	obj, err := ParseYAMLToUnstructured(content)
	if err != nil {
		return map[string]any{
			"valid":   false,
			"error":   fmt.Sprintf("failed to parse YAML: %v", err),
			"message": fmt.Sprintf("YAML parsing failed: %v", err),
		}, nil
	}

	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" {
		return map[string]any{
			"valid":   false,
			"error":   "YAML must contain a 'kind' field",
			"message": "YAML must contain a 'kind' field",
		}, nil
	}

	name := obj.GetName()
	if name == "" {
		return map[string]any{
			"valid":   false,
			"error":   "YAML must contain metadata.name",
			"message": "YAML must contain metadata.name",
		}, nil
	}

	gvr := GVKToGVR(gvk)
	objNamespace := obj.GetNamespace()
	namespaced := IsNamespaced(gvk.Kind)

	// For stored manifest path, override namespace from args
	if namespace != "" {
		objNamespace = namespace
		obj.SetNamespace(namespace)
	}
	if namespaced && objNamespace == "" {
		objNamespace = "default"
		obj.SetNamespace(objNamespace)
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var resourceClient dynamic.ResourceInterface
	if namespaced {
		resourceClient = t.dynamicClient.Resource(gvr).Namespace(objNamespace)
	} else {
		resourceClient = t.dynamicClient.Resource(gvr)
	}

	dryRunCreate := metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}
	dryRunUpdate := metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}}

	// Try create first; if already exists, try update
	_, err = resourceClient.Create(timeoutCtx, obj, dryRunCreate)
	if errors.IsAlreadyExists(err) {
		existing, getErr := resourceClient.Get(timeoutCtx, name, metav1.GetOptions{})
		if getErr != nil {
			return map[string]any{
				"valid":   false,
				"error":   getErr.Error(),
				"message": fmt.Sprintf("Failed to get existing resource for update validation: %v", getErr),
			}, nil
		}
		obj.SetResourceVersion(existing.GetResourceVersion())
		if strings.EqualFold(gvk.Kind, "Service") {
			preserveServiceFields(existing, obj)
		}
		_, err = resourceClient.Update(timeoutCtx, obj, dryRunUpdate)
	}

	if err != nil {
		return map[string]any{
			"valid":   false,
			"error":   err.Error(),
			"message": fmt.Sprintf("Manifest validation failed: %v", err),
		}, nil
	}

	// Build result — stored manifest path includes app/type, inline path includes kind/name
	if app != "" {
		return map[string]any{
			"valid":     true,
			"namespace": objNamespace,
			"app":       app,
			"type":      resourceType,
			"message":   fmt.Sprintf("Manifest %s/%s/%s is valid", objNamespace, app, resourceType),
		}, nil
	}

	result := map[string]any{
		"valid":   true,
		"kind":    gvk.Kind,
		"name":    name,
		"message": fmt.Sprintf("%s/%s is valid", gvk.Kind, name),
	}
	if namespaced {
		result["namespace"] = objNamespace
	}
	return result, nil
}

