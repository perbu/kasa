package tools

import (
	"encoding/json"
	"path/filepath"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// ReadManifestTool provides the read_manifest tool for the agent.
type ReadManifestTool struct {
	manifest *manifest.Manager
}

// NewReadManifestTool creates a new ReadManifestTool.
func NewReadManifestTool(manifest *manifest.Manager) *ReadManifestTool {
	return &ReadManifestTool{
		manifest: manifest,
	}
}

// Name returns the tool name.
func (t *ReadManifestTool) Name() string {
	return "read_manifest"
}

// Description returns the tool description.
func (t *ReadManifestTool) Description() string {
	return "Read the content of a specific manifest file from the deployments directory."
}

// IsLongRunning returns false as this is a quick operation.
func (t *ReadManifestTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *ReadManifestTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *ReadManifestTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ReadManifestTool) Declaration() *genai.FunctionDeclaration {
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
func (t *ReadManifestTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	// Read manifest
	content, err := t.manifest.ReadManifest(namespace, app, resourceType)
	if err != nil {
		return map[string]any{
			"error": err.Error(),
		}, nil
	}

	relPath := filepath.Join(namespace, app, resourceType+".yaml")

	return map[string]any{
		"content": string(content),
		"path":    relPath,
	}, nil
}
