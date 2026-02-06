package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	"k8s.io/client-go/dynamic"
)

// DiffResourceTool provides the diff_resource tool for the agent.
type DiffResourceTool struct {
	dynamicClient dynamic.Interface
	manifest      *manifest.Manager
}

// NewDiffResourceTool creates a new DiffResourceTool.
func NewDiffResourceTool(dynamicClient dynamic.Interface, manifest *manifest.Manager) *DiffResourceTool {
	return &DiffResourceTool{
		dynamicClient: dynamicClient,
		manifest:      manifest,
	}
}

// Name returns the tool name.
func (t *DiffResourceTool) Name() string {
	return "diff_resource"
}

// Description returns the tool description.
func (t *DiffResourceTool) Description() string {
	return "Compare a stored manifest against the live cluster resource and return field-by-field differences."
}

// IsLongRunning returns false as this is a quick operation.
func (t *DiffResourceTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *DiffResourceTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *DiffResourceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *DiffResourceTool) Declaration() *genai.FunctionDeclaration {
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
					Description: "The resource type (e.g., deployment, service)",
				},
			},
			Required: []string{"namespace", "app", "type"},
		},
	}
}

// Run executes the tool.
func (t *DiffResourceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	app, ok := argsMap["app"].(string)
	if !ok || app == "" {
		return map[string]any{"error": "app is required"}, nil
	}

	resourceType, ok := argsMap["type"].(string)
	if !ok || resourceType == "" {
		return map[string]any{"error": "type is required"}, nil
	}

	// Read stored manifest
	content, err := t.manifest.ReadManifest(namespace, app, resourceType)
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}

	// Compare against live cluster
	result := CompareManifest(context.Background(), t.dynamicClient, namespace, app, resourceType, content)

	response := map[string]any{
		"namespace":  result.Namespace,
		"name":       result.Name,
		"kind":       result.Kind,
		"status":     result.Status,
		"diff_count": len(result.Diffs),
	}

	if result.Error != "" {
		response["error"] = result.Error
	}

	if len(result.Diffs) > 0 {
		diffs := make([]map[string]any, len(result.Diffs))
		for i, d := range result.Diffs {
			entry := map[string]any{
				"path":        d.Path,
				"change_type": d.ChangeType,
			}
			if d.Stored != nil {
				entry["stored"] = d.Stored
			}
			if d.Live != nil {
				entry["live"] = d.Live
			}
			diffs[i] = entry
		}
		response["diffs"] = diffs
	}

	// Build human-readable summary
	switch result.Status {
	case "in_sync":
		response["summary"] = fmt.Sprintf("%s/%s/%s is in sync with the cluster", namespace, app, resourceType)
	case "drifted":
		response["summary"] = fmt.Sprintf("%s/%s/%s has %d field(s) that differ from the cluster", namespace, app, resourceType, len(result.Diffs))
	case "missing":
		response["summary"] = fmt.Sprintf("%s/%s/%s does not exist in the cluster", namespace, app, resourceType)
	case "error":
		response["summary"] = fmt.Sprintf("Error comparing %s/%s/%s: %s", namespace, app, resourceType, result.Error)
	}

	return response, nil
}
