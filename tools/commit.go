package tools

import (
	"encoding/json"
	"fmt"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// CommitManifestsTool provides the commit_manifests tool for the agent.
type CommitManifestsTool struct {
	manifest *manifest.Manager
}

// NewCommitManifestsTool creates a new CommitManifestsTool.
func NewCommitManifestsTool(manifest *manifest.Manager) *CommitManifestsTool {
	return &CommitManifestsTool{
		manifest: manifest,
	}
}

// Name returns the tool name.
func (t *CommitManifestsTool) Name() string {
	return "commit_manifests"
}

// Description returns the tool description.
func (t *CommitManifestsTool) Description() string {
	return "Commit staged manifest changes to git. Use after creating/updating deployments and verifying health."
}

// IsLongRunning returns false as this is a quick operation.
func (t *CommitManifestsTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *CommitManifestsTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *CommitManifestsTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"message": {
					Type:        "string",
					Description: "The commit message describing the changes",
				},
			},
			Required: []string{"message"},
		},
	}
}

// Run executes the tool.
func (t *CommitManifestsTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	// Extract message
	message, ok := argsMap["message"].(string)
	if !ok || message == "" {
		return map[string]any{"error": "message is required"}, nil
	}

	// Get current status for the result
	status, _ := t.manifest.GetStatus()

	// Commit changes
	if err := t.manifest.Commit(message); err != nil {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("failed to commit: %v", err),
			"status":  status,
		}, nil
	}

	return map[string]any{
		"success":   true,
		"message":   fmt.Sprintf("Committed changes: %s", message),
		"directory": t.manifest.BaseDir(),
	}, nil
}
