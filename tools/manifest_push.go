package tools

import (
	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// PushManifestsTool provides the push_manifests tool for pushing commits to remote.
type PushManifestsTool struct {
	manifest *manifest.Manager
}

// NewPushManifestsTool creates a new PushManifestsTool.
func NewPushManifestsTool(manifest *manifest.Manager) *PushManifestsTool {
	return &PushManifestsTool{
		manifest: manifest,
	}
}

// Name returns the tool name.
func (t *PushManifestsTool) Name() string {
	return "push_manifests"
}

// Description returns the tool description.
func (t *PushManifestsTool) Description() string {
	return "Push committed manifest changes to the git remote. Use when auto-push after commit failed."
}

// IsLongRunning returns false.
func (t *PushManifestsTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *PushManifestsTool) Category() ToolCategory {
	return CategoryMutating
}

// ProcessRequest adds this tool to the LLM request.
func (t *PushManifestsTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *PushManifestsTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type:       "object",
			Properties: map[string]*genai.Schema{},
		},
	}
}

// Run executes the tool.
func (t *PushManifestsTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	if !t.manifest.HasRemote() {
		return map[string]any{
			"success": false,
			"error":   "no git remote configured",
		}, nil
	}

	if err := t.manifest.Push(); err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, nil
	}

	return map[string]any{
		"success": true,
		"message": "Pushed commits to remote",
	}, nil
}
