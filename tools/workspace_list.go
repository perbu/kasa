package tools

import (
	"fmt"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/perbu/kasa/workspace"
)

// ListWorkspaceTool lets the agent enumerate files under the workspace root.
type ListWorkspaceTool struct {
	ws *workspace.Workspace
}

// NewListWorkspaceTool creates a new ListWorkspaceTool.
func NewListWorkspaceTool(ws *workspace.Workspace) *ListWorkspaceTool {
	return &ListWorkspaceTool{ws: ws}
}

// Name returns the tool name.
func (t *ListWorkspaceTool) Name() string {
	return "list_workspace"
}

// Description returns the tool description.
func (t *ListWorkspaceTool) Description() string {
	return "List files and directories in the local workspace (the directory kasa was launched from). " +
		"Use this to discover task documentation, notes, or draft manifests the user has prepared. " +
		"Skips VCS dirs, node_modules, binaries, and other noise."
}

// IsLongRunning returns false.
func (t *ListWorkspaceTool) IsLongRunning() bool { return false }

// Category returns the tool category.
func (t *ListWorkspaceTool) Category() ToolCategory { return CategoryReadOnly }

// ProcessRequest adds this tool to the LLM request.
func (t *ListWorkspaceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ListWorkspaceTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"prefix": {
					Type:        "string",
					Description: "Subdirectory under the workspace to list, relative to the workspace root. Empty or '.' for the whole tree.",
				},
				"max_depth": {
					Type:        "integer",
					Description: "Maximum recursion depth (0 = unlimited). Defaults to 0.",
				},
			},
		},
	}
}

// Run executes the tool.
func (t *ListWorkspaceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	if t.ws == nil {
		return errorResult("workspace not configured")
	}
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	prefix, _ := argsMap["prefix"].(string)
	maxDepth := 0
	if v, ok := toFloat64(argsMap["max_depth"]); ok {
		maxDepth = int(v)
	}

	entries, truncated, err := t.ws.List(prefix, maxDepth)
	if err != nil {
		return errorResult(err.Error())
	}

	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		item := map[string]any{
			"path":   e.Path,
			"is_dir": e.IsDir,
		}
		if !e.IsDir {
			item["size"] = e.Size
		}
		out = append(out, item)
	}

	result := map[string]any{
		"root":    t.ws.Root(),
		"entries": out,
		"count":   len(out),
	}
	if truncated {
		result["truncated"] = true
		result["hint"] = fmt.Sprintf("listing capped at %d entries; narrow the prefix or set max_depth to see less", len(out))
	}
	return result, nil
}
