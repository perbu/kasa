package tools

import (
	"strings"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// ManifestStatusTool provides the manifest_status tool for inspecting uncommitted manifest changes.
type ManifestStatusTool struct {
	manifest *manifest.Manager
}

// NewManifestStatusTool creates a new ManifestStatusTool.
func NewManifestStatusTool(manifest *manifest.Manager) *ManifestStatusTool {
	return &ManifestStatusTool{
		manifest: manifest,
	}
}

// Name returns the tool name.
func (t *ManifestStatusTool) Name() string {
	return "manifest_status"
}

// Description returns the tool description.
func (t *ManifestStatusTool) Description() string {
	return "Show uncommitted manifest changes for the current context. Returns git status and staged diff."
}

// IsLongRunning returns false.
func (t *ManifestStatusTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *ManifestStatusTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *ManifestStatusTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ManifestStatusTool) Declaration() *genai.FunctionDeclaration {
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
func (t *ManifestStatusTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	result := map[string]any{
		"context": t.manifest.Context(),
	}

	status, err := t.manifest.GetStatus()
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}

	status = strings.TrimSpace(status)
	if status == "" {
		result["status"] = "clean"
		result["message"] = "No uncommitted changes in this context"
		return result, nil
	}

	result["status"] = "dirty"
	result["files"] = status

	diff, err := t.manifest.StagedDiff()
	if err != nil {
		result["diff_error"] = err.Error()
		return result, nil
	}

	diff = strings.TrimSpace(diff)
	if diff != "" {
		result["diff"] = diff
	}

	return result, nil
}
