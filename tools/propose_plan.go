package tools

import (
	"fmt"
	"sort"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// toolSchema describes the parameters a tool declares, used to validate
// planned actions before the plan is shown to the user.
type toolSchema struct {
	params   map[string]bool
	required []string
}

// ProposePlanTool captures planned mutating actions for user approval.
type ProposePlanTool struct {
	// schemas maps tool name → declared parameters. Populated via
	// SetToolSchemas after the full tool registry is built. When nil,
	// parameter validation is skipped.
	schemas map[string]toolSchema
}

// NewProposePlanTool creates a new ProposePlanTool.
func NewProposePlanTool() *ProposePlanTool {
	return &ProposePlanTool{}
}

// SetToolSchemas registers the declared parameters of all available tools so
// that planned actions can be validated against the real tool schemas.
func (t *ProposePlanTool) SetToolSchemas(schemas map[string]toolSchema) {
	t.schemas = schemas
}

// Name returns the tool name.
func (t *ProposePlanTool) Name() string {
	return "propose_plan"
}

// Description returns the tool description.
func (t *ProposePlanTool) Description() string {
	return "Propose a plan of mutating actions for user approval. Must be called before executing any mutating operations. The plan will be displayed to the user who must approve it before execution can proceed. Each action's parameters must use exactly the parameter names the target tool declares, with the same values you will pass when executing — approved plans are enforced parameter-by-parameter at execution time."
}

// IsLongRunning returns false as this is a quick operation.
func (t *ProposePlanTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *ProposePlanTool) Category() ToolCategory {
	return CategoryPlanning
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
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	description, _ := argsMap["description"].(string)
	if description == "" {
		return errorResult("description is required")
	}

	actions, ok := argsMap["actions"].([]any)
	if !ok || len(actions) == 0 {
		return errorResult("at least one action is required")
	}

	// Validate actions have required fields
	for i, action := range actions {
		actionMap, ok := action.(map[string]any)
		if !ok {
			return errorResult("invalid action format")
		}
		var missing []string
		if v, _ := actionMap["tool"].(string); v == "" {
			missing = append(missing, "tool")
		}
		if v, _ := actionMap["reason"].(string); v == "" {
			missing = append(missing, "reason")
		}
		if len(missing) > 0 {
			return map[string]any{
				"error": "action at index " + fmt.Sprintf("%d", i) + " is missing required fields: " + strings.Join(missing, ", ") + ". Each action must have: tool (the tool name to call, e.g. \"apply_resource\"), parameters (map of args), reason (why this action is needed).",
				"index": i,
			}, nil
		}

		if err := t.validateActionParams(i, actionMap); err != nil {
			return map[string]any{
				"error": err.Error(),
				"index": i,
			}, nil
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

// validateActionParams checks a planned action's parameters against the target
// tool's declared schema, so schema mistakes are caught at proposal time
// instead of failing the mutation guard after the user has approved the plan.
func (t *ProposePlanTool) validateActionParams(index int, action map[string]any) error {
	if t.schemas == nil {
		return nil
	}

	toolName, _ := action["tool"].(string)
	schema, known := t.schemas[toolName]
	if !known {
		return fmt.Errorf("action at index %d: unknown tool %q. Use one of the available tools.", index, toolName)
	}

	params, _ := action["parameters"].(map[string]any)

	var invalid []string
	for k := range params {
		if !schema.params[k] {
			invalid = append(invalid, k)
		}
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		valid := make([]string, 0, len(schema.params))
		for k := range schema.params {
			valid = append(valid, k)
		}
		sort.Strings(valid)
		return fmt.Errorf("action at index %d: tool %q does not accept parameter(s): %s. Valid parameters are: %s. Re-propose the plan using only parameters this tool declares.",
			index, toolName, strings.Join(invalid, ", "), strings.Join(valid, ", "))
	}

	var missingRequired []string
	for _, r := range schema.required {
		if _, ok := params[r]; !ok {
			missingRequired = append(missingRequired, r)
		}
	}
	if len(missingRequired) > 0 {
		return fmt.Errorf("action at index %d: tool %q is missing required parameter(s): %s. Include them in the action's parameters so the plan shows exactly what will be executed.",
			index, toolName, strings.Join(missingRequired, ", "))
	}

	return nil
}
