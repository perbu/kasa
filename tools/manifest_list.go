package tools

import (
	"encoding/json"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// ListManifestsTool provides the list_manifests tool for the agent.
type ListManifestsTool struct {
	manifest *manifest.Manager
}

// NewListManifestsTool creates a new ListManifestsTool.
func NewListManifestsTool(manifest *manifest.Manager) *ListManifestsTool {
	return &ListManifestsTool{
		manifest: manifest,
	}
}

// Name returns the tool name.
func (t *ListManifestsTool) Name() string {
	return "list_manifests"
}

// Description returns the tool description.
func (t *ListManifestsTool) Description() string {
	return "List manifest files stored in the deployments directory. Can filter by namespace and/or app name."
}

// IsLongRunning returns false as this is a quick operation.
func (t *ListManifestsTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *ListManifestsTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *ListManifestsTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ListManifestsTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "Filter by Kubernetes namespace (optional)",
				},
				"app": {
					Type:        "string",
					Description: "Filter by application name (optional)",
				},
			},
		},
	}
}

// Run executes the tool.
func (t *ListManifestsTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	// Parse arguments
	argsMap, ok := args.(map[string]any)
	if !ok {
		if argsStr, ok := args.(string); ok {
			if err := json.Unmarshal([]byte(argsStr), &argsMap); err != nil {
				argsMap = make(map[string]any)
			}
		} else {
			argsMap = make(map[string]any)
		}
	}

	// Extract optional filters
	namespace, _ := argsMap["namespace"].(string)
	app, _ := argsMap["app"].(string)

	// List manifests
	manifests, err := t.manifest.ListManifests(namespace, app)
	if err != nil {
		return map[string]any{
			"error": err.Error(),
		}, nil
	}

	return map[string]any{
		"manifests": manifests,
		"count":     len(manifests),
	}, nil
}
