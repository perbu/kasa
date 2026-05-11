package repl

import (
	"fmt"
	"strings"
)

// RenderPlan renders a plan to a string using glamour markdown rendering.
// Returns the rendered string, or plain markdown if rendering fails.
// diffs is a map from action index to unified diff string; nil means no diffs.
func RenderPlan(plan *Plan, diffs map[int]string) string {
	if plan == nil {
		return "No plan to display.\n"
	}

	return renderMarkdownSimple(buildPlanMarkdown(plan, diffs))
}

// DisplayPlan formats and prints a proposed plan for user review.
func DisplayPlan(plan *Plan, diffs map[int]string) {
	fmt.Print(RenderPlan(plan, diffs))
}

// buildPlanMarkdown builds the markdown string for a plan.
func buildPlanMarkdown(plan *Plan, diffs map[int]string) string {
	var md strings.Builder
	md.WriteString("# Proposed Plan\n\n")
	md.WriteString(plan.Description)
	md.WriteString("\n\n## Actions\n\n")

	for i, action := range plan.Actions {
		md.WriteString(fmt.Sprintf("### %d. `%s`\n\n", i+1, action.Tool))
		md.WriteString(fmt.Sprintf("**Reason:** %s\n\n", action.Reason))

		if len(action.Parameters) > 0 {
			// Separate simple values from multi-line values
			var simpleParams []struct{ key, value string }
			var multiLineParams []struct{ key, value string }

			for k, v := range action.Parameters {
				valueStr := fmt.Sprintf("%v", v)
				if strings.Contains(valueStr, "\n") || len(valueStr) > 80 {
					multiLineParams = append(multiLineParams, struct{ key, value string }{k, valueStr})
				} else {
					simpleParams = append(simpleParams, struct{ key, value string }{k, valueStr})
				}
			}

			// Show simple params in a table
			if len(simpleParams) > 0 {
				md.WriteString("| Parameter | Value |\n")
				md.WriteString("|-----------|-------|\n")
				for _, p := range simpleParams {
					// Escape pipe characters in values
					valueStr := strings.ReplaceAll(p.value, "|", "\\|")
					md.WriteString(fmt.Sprintf("| `%s` | `%s` |\n", p.key, valueStr))
				}
				md.WriteString("\n")
			}

			// Show multi-line params in code blocks
			for _, p := range multiLineParams {
				md.WriteString(fmt.Sprintf("**%s:**\n", p.key))
				md.WriteString("```yaml\n")
				md.WriteString(p.value)
				if !strings.HasSuffix(p.value, "\n") {
					md.WriteString("\n")
				}
				md.WriteString("```\n\n")
			}
		}

		if action.Tool == "apply_resource" || action.Tool == "apply_manifest" {
			if diff, ok := diffs[i]; ok && diff != "" {
				md.WriteString("**Diff from current cluster state:**\n")
				md.WriteString("```\n")
				md.WriteString(diff)
				if !strings.HasSuffix(diff, "\n") {
					md.WriteString("\n")
				}
				md.WriteString("```\n\n")
			}
		}
	}

	md.WriteString("---\n\n")
	md.WriteString("**Commands:** `/approve` approve · `/abort` reject · `/plan` show again · `/copy` copy YAML\n")
	return md.String()
}

// computePlanDiffs renders a dyff HumanReport between the current cluster
// state and the proposed YAML for each apply_resource or apply_manifest
// action in the plan. Returns nil when fetcher is nil (non-interactive mode).
func computePlanDiffs(plan *Plan, fetcher ResourceFetcher, manifestReader ...ManifestReader) map[int]string {
	if fetcher == nil || plan == nil {
		return nil
	}

	var mReader ManifestReader
	if len(manifestReader) > 0 {
		mReader = manifestReader[0]
	}

	diffs := make(map[int]string)
	for i, action := range plan.Actions {
		var yamlContent string

		switch action.Tool {
		case "apply_resource":
			y, ok := action.Parameters["yaml"].(string)
			if !ok || y == "" {
				continue
			}
			yamlContent = y

		case "apply_manifest":
			if mReader == nil {
				continue
			}
			ns, _ := action.Parameters["namespace"].(string)
			app, _ := action.Parameters["app"].(string)
			rt, _ := action.Parameters["type"].(string)
			if ns == "" || app == "" || rt == "" {
				continue
			}
			content, err := mReader(ns, app, rt)
			if err != nil || content == "" {
				continue
			}
			yamlContent = content

		default:
			continue
		}

		existing, err := fetcher(yamlContent)
		if err != nil || existing == "" {
			continue
		}
		diff, err := dyffDiff(existing, yamlContent)
		if err != nil || diff == "" {
			continue
		}
		diffs[i] = diff
	}
	return diffs
}

// formatParameters formats parameter map for display.
func formatParameters(params map[string]any) string {
	if len(params) == 0 {
		return "(none)"
	}

	parts := make([]string, 0, len(params))
	for k, v := range params {
		valueStr := fmt.Sprintf("%v", v)
		// For multi-line values, show first line with indicator
		if strings.Contains(valueStr, "\n") {
			firstLine := strings.SplitN(valueStr, "\n", 2)[0]
			parts = append(parts, fmt.Sprintf("%s=%s... (multi-line)", k, firstLine))
		} else {
			parts = append(parts, fmt.Sprintf("%s=%s", k, valueStr))
		}
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

	sb.WriteString("\nExecute these actions in order. Proceed directly with the mutating tools.")
	sb.WriteString("\n\nIMPORTANT: The execution guard enforces this plan strictly:")
	sb.WriteString("\n- Each action can only be executed ONCE (call count is tracked)")
	sb.WriteString("\n- Targeting parameters (namespace, name, app, kind, type) must match exactly")
	sb.WriteString("\n- Tools not listed above will be blocked")
	sb.WriteString("\nIf you need to deviate from the plan, call propose_plan with a revised plan.")
	return sb.String()
}
