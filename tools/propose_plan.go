package tools

import (
	"encoding/json"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// ProposePlanTool captures planned mutating actions for user approval.
type ProposePlanTool struct{}

// NewProposePlanTool creates a new ProposePlanTool.
func NewProposePlanTool() *ProposePlanTool {
	return &ProposePlanTool{}
}

// Name returns the tool name.
func (t *ProposePlanTool) Name() string {
	return "propose_plan"
}

// Description returns the tool description.
func (t *ProposePlanTool) Description() string {
	return "Propose a plan of mutating actions for user approval. Must be called before executing any mutating operations. The plan will be displayed to the user who must approve it before execution can proceed."
}

// IsLongRunning returns false as this is a quick operation.
func (t *ProposePlanTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *ProposePlanTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ProposePlanTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"description": {
					Type:        "string",
					Description: "A clear description of what this plan will accomplish",
				},
				"actions": {
					Type:        "array",
					Description: "List of actions to execute, in order",
					Items: &genai.Schema{
						Type: "object",
						Properties: map[string]*genai.Schema{
							"tool": {
								Type:        "string",
								Description: "The name of the tool to call",
							},
							"parameters": {
								Type:        "object",
								Description: "The parameters to pass to the tool",
							},
							"reason": {
								Type:        "string",
								Description: "Brief explanation of why this action is needed",
							},
						},
						Required: []string{"tool", "parameters", "reason"},
					},
				},
			},
			Required: []string{"description", "actions"},
		},
	}
}

// Run executes the tool. This tool does NOT execute any actions - it only
// captures the plan for display and returns a status indicating approval is needed.
func (t *ProposePlanTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	description, _ := argsMap["description"].(string)
	if description == "" {
		return map[string]any{"error": "description is required"}, nil
	}

	actions, ok := argsMap["actions"].([]any)
	if !ok || len(actions) == 0 {
		return map[string]any{"error": "at least one action is required"}, nil
	}

	// Validate actions have required fields
	for i, action := range actions {
		actionMap, ok := action.(map[string]any)
		if !ok {
			return map[string]any{"error": "invalid action format"}, nil
		}
		if _, ok := actionMap["tool"].(string); !ok {
			return map[string]any{"error": "action missing tool name", "index": i}, nil
		}
		if _, ok := actionMap["reason"].(string); !ok {
			return map[string]any{"error": "action missing reason", "index": i}, nil
		}
	}

	// Return the plan details for the REPL to capture and display
	return map[string]any{
		"status":      "awaiting_approval",
		"message":     "Plan proposed. Waiting for user approval. Type 'yes' to approve or 'no' to reject.",
		"description": description,
		"actions":     actions,
	}, nil
}
