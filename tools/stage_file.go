package tools

import (
	"fmt"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/perbu/kasa/manifest"
)

// StageFileTool provides the stage_file tool for the agent.
type StageFileTool struct {
	manifest *manifest.Manager
}

// NewStageFileTool creates a new StageFileTool.
func NewStageFileTool(manifest *manifest.Manager) *StageFileTool {
	return &StageFileTool{
		manifest: manifest,
	}
}

// Name returns the tool name.
func (t *StageFileTool) Name() string {
	return "stage_file"
}

// Description returns the tool description.
func (t *StageFileTool) Description() string {
	return "Stage an untracked file in the manifest repository for the next commit. " +
		"Use this to add files that exist on disk but are not tracked by git. " +
		"The path must be relative to the manifest repository root (e.g., 'my-context/default/nginx/deployment.yaml')."
}

// IsLongRunning returns false.
func (t *StageFileTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *StageFileTool) Category() ToolCategory {
	return CategoryMutating
}

// ProcessRequest adds this tool to the LLM request.
func (t *StageFileTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *StageFileTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"path": {
					Type:        "string",
					Description: "Path to the file, relative to the manifest repository root (e.g., 'my-context/default/nginx/deployment.yaml')",
				},
			},
			Required: []string{"path"},
		},
	}
}

// Run executes the tool.
func (t *StageFileTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	path, ok := argsMap["path"].(string)
	if !ok || path == "" {
		return errorResult("path is required")
	}

	if err := t.manifest.StageFile(path); err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, nil
	}

	return map[string]any{
		"success": true,
		"message": fmt.Sprintf("Staged %s for commit", path),
	}, nil
}
