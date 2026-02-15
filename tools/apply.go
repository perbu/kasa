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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

// ApplyManifestTool provides the apply_manifest tool for the agent.
type ApplyManifestTool struct {
	dynamicClient dynamic.Interface
	manifest      *manifest.Manager
}

// NewApplyManifestTool creates a new ApplyManifestTool.
func NewApplyManifestTool(dynamicClient dynamic.Interface, manifest *manifest.Manager) *ApplyManifestTool {
	return &ApplyManifestTool{
		dynamicClient: dynamicClient,
		manifest:      manifest,
	}
}

// Name returns the tool name.
func (t *ApplyManifestTool) Name() string {
	return "apply_manifest"
}

// Description returns the tool description.
func (t *ApplyManifestTool) Description() string {
	return "Apply a stored manifest to the Kubernetes cluster. Reads the manifest from storage and creates or updates the resource in the cluster."
}

// IsLongRunning returns false as this is a quick operation.
func (t *ApplyManifestTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *ApplyManifestTool) Category() ToolCategory {
	return CategoryMutating
}

// ProcessRequest adds this tool to the LLM request.
func (t *ApplyManifestTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ApplyManifestTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "The namespace of the manifest",
				},
				"app": {
					Type:        "string",
					Description: "The app name (manifest directory name)",
				},
				"type": {
					Type:        "string",
					Description: "The resource type (e.g. deployment, service, configmap, secret, ingress, httproute, etc.)",
				},
				"dry_run": {
					Type:        "boolean",
					Description: "If true, validate without applying (default: false)",
				},
			},
			Required: []string{"namespace", "app", "type"},
		},
	}
}

// Run executes the tool.
func (t *ApplyManifestTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	// Parse arguments
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

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

	dryRun := false
	if dr, ok := argsMap["dry_run"].(bool); ok {
		dryRun = dr
	}

	// Normalize resource type
	resourceType = NormalizeKindName(resourceType)

	// Read manifest from storage
	content, readErr := t.manifest.ReadManifest(namespace, app, resourceType)
	if readErr != nil {
		return errorResult(readErr.Error())
	}

	// Parse YAML to unstructured
	obj, parseErr := ParseYAMLToUnstructured(content)
	if parseErr != nil {
		return errorResult(fmt.Sprintf("invalid YAML: %v", parseErr))
	}

	// Set namespace
	obj.SetNamespace(namespace)

	// Determine GVR from the parsed object
	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" {
		return errorResult("manifest YAML must contain a 'kind' field")
	}
	gvr := GVKToGVR(gvk)

	name := obj.GetName()
	if name == "" {
		return errorResult("manifest YAML must contain metadata.name")
	}

	// Apply to cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	namespaced := IsNamespaced(gvk.Kind)
	resourceClient := namespacedClient(t.dynamicClient, gvr, namespace, namespaced)

	createOptions := metav1.CreateOptions{}
	updateOptions := metav1.UpdateOptions{}
	if dryRun {
		createOptions.DryRun = []string{metav1.DryRunAll}
		updateOptions.DryRun = []string{metav1.DryRunAll}
	}

	// Try to get existing resource to determine create vs update
	existing, getErr := resourceClient.Get(timeoutCtx, name, metav1.GetOptions{})
	var action string

	if getErr != nil {
		// Resource doesn't exist, create it
		_, createErr := resourceClient.Create(timeoutCtx, obj, createOptions)
		if createErr != nil {
			return map[string]any{
				"success": false,
				"error":   fmt.Sprintf("failed to create %s: %v", gvk.Kind, createErr),
			}, nil
		}
		action = "created"
	} else {
		// Resource exists, update it
		obj.SetResourceVersion(existing.GetResourceVersion())
		if strings.EqualFold(gvk.Kind, "Service") {
			preserveServiceFields(existing, obj)
		}
		_, updateErr := resourceClient.Update(timeoutCtx, obj, updateOptions)
		if updateErr != nil {
			return map[string]any{
				"success": false,
				"error":   fmt.Sprintf("failed to update %s: %v", gvk.Kind, updateErr),
			}, nil
		}
		action = "updated"
	}

	result := map[string]any{
		"success":   true,
		"action":    action,
		"namespace": namespace,
		"app":       app,
		"type":      resourceType,
	}

	if dryRun {
		result["dry_run"] = true
		result["message"] = fmt.Sprintf("Dry run: %s/%s/%s would be %s", namespace, app, resourceType, action)
	} else {
		result["message"] = fmt.Sprintf("Successfully %s %s/%s in namespace %s", action, resourceType, app, namespace)
	}

	return result, nil
}
