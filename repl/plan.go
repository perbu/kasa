package repl

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
)

// DisplayPlan formats and prints a proposed plan for user review.
func DisplayPlan(plan *Plan) {
	if plan == nil {
		fmt.Println("No plan to display.")
		return
	}

	// Build markdown content
	var md strings.Builder
	md.WriteString("# Proposed Plan\n\n")
	md.WriteString(plan.Description)
	md.WriteString("\n\n## Actions\n\n")

	for i, action := range plan.Actions {
		md.WriteString(fmt.Sprintf("### %d. `%s`\n\n", i+1, action.Tool))
		md.WriteString(fmt.Sprintf("**Reason:** %s\n\n", action.Reason))

		if len(action.Parameters) > 0 {
			md.WriteString("| Parameter | Value |\n")
			md.WriteString("|-----------|-------|\n")
			for k, v := range action.Parameters {
				valueStr := fmt.Sprintf("%v", v)
				if len(valueStr) > 50 {
					valueStr = valueStr[:47] + "..."
				}
				// Escape pipe characters in values
				valueStr = strings.ReplaceAll(valueStr, "|", "\\|")
				md.WriteString(fmt.Sprintf("| `%s` | `%s` |\n", k, valueStr))
			}
			md.WriteString("\n")
		}
	}

	md.WriteString("---\n\n")
	md.WriteString("**Commands:** `yes` approve · `no` reject · `/plan` show again\n")

	// Render with glamour
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		// Fallback to plain output
		fmt.Fprintln(os.Stderr, "Warning: could not create renderer:", err)
		fmt.Println(md.String())
		return
	}

	out, err := renderer.Render(md.String())
	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: could not render markdown:", err)
		fmt.Println(md.String())
		return
	}

	fmt.Print(out)
}

// formatParameters formats parameter map for display.
func formatParameters(params map[string]any) string {
	if len(params) == 0 {
		return "(none)"
	}

	parts := make([]string, 0, len(params))
	for k, v := range params {
		// Truncate long values
		valueStr := fmt.Sprintf("%v", v)
		if len(valueStr) > 50 {
			valueStr = valueStr[:47] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, valueStr))
	}
	return strings.Join(parts, ", ")
}

// ParsePlanFromResponse extracts a Plan from the propose_plan tool response.
func ParsePlanFromResponse(args map[string]any) *Plan {
	description, _ := args["description"].(string)

	actionsRaw, ok := args["actions"].([]any)
	if !ok {
		return nil
	}

	actions := make([]PlannedAction, 0, len(actionsRaw))
	for _, actionRaw := range actionsRaw {
		actionMap, ok := actionRaw.(map[string]any)
		if !ok {
			continue
		}

		action := PlannedAction{
			Tool:   getString(actionMap, "tool"),
			Reason: getString(actionMap, "reason"),
		}

		if params, ok := actionMap["parameters"].(map[string]any); ok {
			action.Parameters = params
		} else {
			action.Parameters = make(map[string]any)
		}

		actions = append(actions, action)
	}

	return &Plan{
		Description: description,
		Actions:     actions,
	}
}

// getString safely extracts a string from a map.
func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// FormatExecutionPrompt creates a prompt instructing the agent to execute the approved plan.
func FormatExecutionPrompt(plan *Plan) string {
	var sb strings.Builder
	sb.WriteString("The user has APPROVED your plan. Execute the following actions now:\n\n")
	sb.WriteString("Plan: ")
	sb.WriteString(plan.Description)
	sb.WriteString("\n\nActions to execute:\n")

	for i, action := range plan.Actions {
		sb.WriteString(fmt.Sprintf("%d. Call %s with parameters: ", i+1, action.Tool))
		for k, v := range action.Parameters {
			sb.WriteString(fmt.Sprintf("%s=%v ", k, v))
		}
		sb.WriteString(fmt.Sprintf("(Reason: %s)\n", action.Reason))
	}

	sb.WriteString("\nExecute these actions in order. Do not call propose_plan again - proceed directly with the mutating tools.")
	return sb.String()
}
