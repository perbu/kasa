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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// ApplyResourceTool provides the apply_resource tool for applying any Kubernetes resource.
type ApplyResourceTool struct {
	dynamicClient dynamic.Interface
	manifest      *manifest.Manager
}

// NewApplyResourceTool creates a new ApplyResourceTool.
func NewApplyResourceTool(dynamicClient dynamic.Interface, manifest *manifest.Manager) *ApplyResourceTool {
	return &ApplyResourceTool{
		dynamicClient: dynamicClient,
		manifest:      manifest,
	}
}

// Name returns the tool name.
func (t *ApplyResourceTool) Name() string {
	return "apply_resource"
}

// Description returns the tool description.
func (t *ApplyResourceTool) Description() string {
	return "Apply any Kubernetes resource from YAML. Supports core resources (Deployment, Service, ConfigMap, etc.) and CRDs (HTTPRoute, Gateway, Certificate, etc.). Creates or updates the resource."
}

// IsLongRunning returns false as this is a quick operation.
func (t *ApplyResourceTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *ApplyResourceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ApplyResourceTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"yaml": {
					Type:        "string",
					Description: "The complete YAML manifest to apply",
				},
				"namespace": {
					Type:        "string",
					Description: "Override the namespace in the manifest (optional)",
				},
				"app": {
					Type:        "string",
					Description: "Application name for manifest storage. If not provided, uses the resource name.",
				},
				"dry_run": {
					Type:        "boolean",
					Description: "If true, validate without applying (server-side dry run)",
				},
			},
			Required: []string{"yaml"},
		},
	}
}

// Run executes the tool.
func (t *ApplyResourceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	yamlContent, ok := argsMap["yaml"].(string)
	if !ok || yamlContent == "" {
		return map[string]any{"error": "yaml is required"}, nil
	}

	namespaceOverride := ""
	if ns, ok := argsMap["namespace"].(string); ok {
		namespaceOverride = ns
	}

	appName := ""
	if app, ok := argsMap["app"].(string); ok {
		appName = app
	}

	dryRun := false
	if dr, ok := argsMap["dry_run"].(bool); ok {
		dryRun = dr
	}

	// Parse YAML to unstructured
	obj, err := ParseYAMLToUnstructured([]byte(yamlContent))
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to parse YAML: %v", err)}, nil
	}

	// Extract GVK
	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" {
		return map[string]any{"error": "YAML must contain a 'kind' field"}, nil
	}

	// Convert GVK to GVR
	gvr := GVKToGVR(gvk)

	// Determine namespace
	namespace := obj.GetNamespace()
	if namespaceOverride != "" {
		namespace = namespaceOverride
		obj.SetNamespace(namespaceOverride)
	}

	// Check if resource is namespaced
	namespaced := IsNamespaced(gvk.Kind)
	if namespaced && namespace == "" {
		namespace = "default"
		obj.SetNamespace(namespace)
	}

	name := obj.GetName()
	if name == "" {
		return map[string]any{"error": "YAML must contain metadata.name"}, nil
	}

	// Use resource name as app name if not provided
	if appName == "" {
		appName = name
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Determine resource type for manifest storage (lowercase kind)
	resourceType := strings.ToLower(gvk.Kind)

	// Get the resource interface
	var resourceClient dynamic.ResourceInterface
	if namespaced {
		resourceClient = t.dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceClient = t.dynamicClient.Resource(gvr)
	}

	// Build create/update options
	createOptions := metav1.CreateOptions{}
	updateOptions := metav1.UpdateOptions{}
	if dryRun {
		createOptions.DryRun = []string{metav1.DryRunAll}
		updateOptions.DryRun = []string{metav1.DryRunAll}
	}

	// Try to get existing resource to determine create vs update
	existing, err := resourceClient.Get(timeoutCtx, name, metav1.GetOptions{})
	var resultObj *unstructured.Unstructured
	var action string

	if err != nil {
		// Resource doesn't exist, create it
		resultObj, err = resourceClient.Create(timeoutCtx, obj, createOptions)
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to create %s: %v", gvk.Kind, err)}, nil
		}
		action = "created"
	} else {
		// Resource exists, update it
		// Preserve the resourceVersion for optimistic concurrency
		obj.SetResourceVersion(existing.GetResourceVersion())
		resultObj, err = resourceClient.Update(timeoutCtx, obj, updateOptions)
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to update %s: %v", gvk.Kind, err)}, nil
		}
		action = "updated"
	}

	result := map[string]any{
		"success":    true,
		"action":     action,
		"kind":       gvk.Kind,
		"name":       name,
		"apiVersion": gvk.GroupVersion().String(),
	}

	if namespaced {
		result["namespace"] = namespace
	}

	if dryRun {
		result["dry_run"] = true
		result["message"] = fmt.Sprintf("Dry run: would have %s %s/%s", action, gvk.Kind, name)
	} else {
		// Capitalize first letter of action
		actionTitle := action
		if len(action) > 0 {
			actionTitle = strings.ToUpper(action[:1]) + action[1:]
		}
		result["message"] = fmt.Sprintf("%s %s/%s", actionTitle, gvk.Kind, name)

		// Save manifest to git storage (only on actual apply, not dry run)
		if t.manifest != nil && namespaced {
			manifestPath, err := t.manifest.SaveManifest(namespace, appName, resourceType, []byte(yamlContent))
			if err != nil {
				result["manifest_warning"] = fmt.Sprintf("Applied to cluster but failed to save manifest: %v", err)
			} else {
				result["manifest_path"] = manifestPath
			}
		}
	}

	// Add resource UID for reference
	if resultObj != nil {
		result["uid"] = string(resultObj.GetUID())
	}

	return result, nil
}
