package tools

import (
	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// SaveNotesTool provides the save_notes tool for the agent.
type SaveNotesTool struct {
	manifest *manifest.Manager
}

// NewSaveNotesTool creates a new SaveNotesTool.
func NewSaveNotesTool(manifest *manifest.Manager) *SaveNotesTool {
	return &SaveNotesTool{
		manifest: manifest,
	}
}

// Name returns the tool name.
func (t *SaveNotesTool) Name() string {
	return "save_notes"
}

// Description returns the tool description.
func (t *SaveNotesTool) Description() string {
	return "Save deployment notes (KASA.md) at the cluster-level, namespace-level, or app-level. Notes are automatically shown when reading or listing manifests."
}

// IsLongRunning returns false as this is a quick operation.
func (t *SaveNotesTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *SaveNotesTool) Category() ToolCategory {
	return CategoryMutating
}

// ProcessRequest adds this tool to the LLM request.
func (t *SaveNotesTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *SaveNotesTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "Kubernetes namespace (optional — omit for cluster-level notes)",
				},
				"app": {
					Type:        "string",
					Description: "Application name (optional — omit for namespace-level notes)",
				},
				"content": {
					Type:        "string",
					Description: "Markdown content for the KASA.md notes file",
				},
			},
			Required: []string{"content"},
		},
	}
}

// Run executes the tool.
func (t *SaveNotesTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	content, ok := argsMap["content"].(string)
	if !ok || content == "" {
		return errorResult("content is required")
	}

	namespace, _ := argsMap["namespace"].(string)
	app, _ := argsMap["app"].(string)

	// Don't allow app without namespace
	if app != "" && namespace == "" {
		return errorResult("namespace is required when app is specified")
	}

	path, err := t.manifest.SaveNotes(namespace, app, content)
	if err != nil {
		return errorResult(err.Error())
	}

	return map[string]any{
		"path":   path,
		"status": "saved",
	}, nil
}
