package repl

import (
	"fmt"
	"strings"

	udiff "github.com/aymanbagabas/go-udiff"
	sigsyaml "sigs.k8s.io/yaml"
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

		// For apply_resource actions, show diff from current cluster state if available
		if action.Tool == "apply_resource" {
			if diff, ok := diffs[i]; ok && diff != "" {
				md.WriteString("**Diff from current cluster state:**\n")
				md.WriteString("```diff\n")
				md.WriteString(diff)
				if !strings.HasSuffix(diff, "\n") {
					md.WriteString("\n")
				}
				md.WriteString("```\n\n")
			}
		}
	}

	md.WriteString("---\n\n")
	md.WriteString("**Commands:** `/approve` approve · `/abort` reject · `/plan` show again\n")
	return md.String()
}

// computePlanDiffs computes unified diffs between the current cluster state and
// the proposed YAML for each apply_resource action in the plan.
// Returns nil when fetcher is nil (no-op for non-interactive mode).
func computePlanDiffs(plan *Plan, fetcher ResourceFetcher) map[int]string {
	if fetcher == nil || plan == nil {
		return nil
	}

	diffs := make(map[int]string)
	for i, action := range plan.Actions {
		if action.Tool != "apply_resource" {
			continue
		}
		yamlContent, ok := action.Parameters["yaml"].(string)
		if !ok || yamlContent == "" {
			continue
		}
		existing, err := fetcher(yamlContent)
		if err != nil || existing == "" {
			continue
		}
		// Prune server-side defaults from the live YAML so the diff only
		// shows fields the user actually manages in their manifest.
		prunedExisting := pruneToProposedYAML(existing, yamlContent)
		diff := udiff.Unified("cluster", "proposed", normalizeYAML(prunedExisting), normalizeYAML(yamlContent))
		if diff != "" {
			diffs[i] = diff
		}
	}
	return diffs
}

// pruneToProposedYAML parses both YAML strings into maps, prunes keys from
// the live map that don't exist in the proposed map, and re-marshals the result.
// This removes server-side defaults from the diff. Falls back to the original
// live YAML on any parse error.
func pruneToProposedYAML(liveYAML, proposedYAML string) string {
	var liveMap, proposedMap map[string]any
	if err := sigsyaml.Unmarshal([]byte(liveYAML), &liveMap); err != nil {
		return liveYAML
	}
	if err := sigsyaml.Unmarshal([]byte(proposedYAML), &proposedMap); err != nil {
		return liveYAML
	}
	pruned := pruneToProposed(liveMap, proposedMap)
	out, err := sigsyaml.Marshal(pruned)
	if err != nil {
		return liveYAML
	}
	return string(out)
}

// pruneToProposed recursively removes keys from live that don't exist in
// proposed. For nested maps it recurses; for slices of maps it matches by
// index and recurses each pair.
func pruneToProposed(live, proposed map[string]any) map[string]any {
	result := make(map[string]any, len(proposed))
	for key, proposedVal := range proposed {
		liveVal, ok := live[key]
		if !ok {
			// Key only in proposed — keep it so it shows as an addition in the diff.
			result[key] = proposedVal
			continue
		}

		// Both sides are maps — recurse.
		pMap, pIsMap := proposedVal.(map[string]any)
		lMap, lIsMap := liveVal.(map[string]any)
		if pIsMap && lIsMap {
			result[key] = pruneToProposed(lMap, pMap)
			continue
		}

		// Both sides are slices — recurse into map elements by index.
		pSlice, pIsSlice := proposedVal.([]any)
		lSlice, lIsSlice := liveVal.([]any)
		if pIsSlice && lIsSlice {
			result[key] = pruneSlice(lSlice, pSlice)
			continue
		}

		// Scalar or mismatched types — keep the live value as-is.
		result[key] = liveVal
	}
	return result
}

// pruneSlice matches slice elements by index. For map elements it recurses
// via pruneToProposed; other element types are kept from live as-is.
func pruneSlice(live, proposed []any) []any {
	result := make([]any, 0, len(proposed))
	for i, pElem := range proposed {
		if i >= len(live) {
			// Extra proposed element — keep it so it shows as an addition.
			result = append(result, pElem)
			continue
		}
		pMap, pIsMap := pElem.(map[string]any)
		lMap, lIsMap := live[i].(map[string]any)
		if pIsMap && lIsMap {
			result = append(result, pruneToProposed(lMap, pMap))
		} else {
			result = append(result, live[i])
		}
	}
	return result
}

// normalizeYAML parses and re-marshals YAML so that key ordering is
// consistent (alphabetical via sigs.k8s.io/yaml). This prevents
// cosmetic key-reordering noise in diffs.
func normalizeYAML(yamlStr string) string {
	var m map[string]any
	if err := sigsyaml.Unmarshal([]byte(yamlStr), &m); err != nil {
		return yamlStr
	}
	out, err := sigsyaml.Marshal(m)
	if err != nil {
		return yamlStr
	}
	return string(out)
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

	sb.WriteString("\nExecute these actions in order. Proceed directly with the mutating tools. If any action fails and you need to retry with different parameters, call propose_plan again with a revised plan.")
	return sb.String()
}
