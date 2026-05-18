package tools

import (
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/perbu/kasa/workspace"
)

// ReadWorkspaceFileTool lets the agent read a file from the local workspace.
type ReadWorkspaceFileTool struct {
	ws *workspace.Workspace
}

// NewReadWorkspaceFileTool creates a new ReadWorkspaceFileTool.
func NewReadWorkspaceFileTool(ws *workspace.Workspace) *ReadWorkspaceFileTool {
	return &ReadWorkspaceFileTool{ws: ws}
}

// Name returns the tool name.
func (t *ReadWorkspaceFileTool) Name() string {
	return "read_workspace_file"
}

// Description returns the tool description.
func (t *ReadWorkspaceFileTool) Description() string {
	return "Read a file from the local workspace (the directory kasa was launched from). " +
		"Use this to consult task documentation, notes, or draft manifests the user has prepared. " +
		"Large files are truncated; the response includes a truncated flag when that happens."
}

// IsLongRunning returns false.
func (t *ReadWorkspaceFileTool) IsLongRunning() bool { return false }

// Category returns the tool category.
func (t *ReadWorkspaceFileTool) Category() ToolCategory { return CategoryReadOnly }

// ProcessRequest adds this tool to the LLM request.
func (t *ReadWorkspaceFileTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ReadWorkspaceFileTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"path": {
					Type:        "string",
					Description: "Path to the file, relative to the workspace root.",
				},
			},
			Required: []string{"path"},
		},
	}
}

// Run executes the tool.
func (t *ReadWorkspaceFileTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	if t.ws == nil {
		return errorResult("workspace not configured")
	}
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	path, ok := argsMap["path"].(string)
	if !ok || path == "" {
		return errorResult("path is required")
	}

	content, truncated, err := t.ws.Read(path)
	if err != nil {
		return errorResult(err.Error())
	}

	result := map[string]any{
		"path":    path,
		"content": string(content),
		"bytes":   len(content),
	}
	if truncated {
		result["truncated"] = true
	}
	return result, nil
}
