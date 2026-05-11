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

		// For apply_resource/apply_manifest actions, show diff from current cluster state if available
		if action.Tool == "apply_resource" || action.Tool == "apply_manifest" {
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
	md.WriteString("**Commands:** `/approve` approve · `/abort` reject · `/plan` show again · `/copy` copy YAML\n")
	return md.String()
}

// computePlanDiffs computes unified diffs between the current cluster state and
// the proposed YAML for each apply_resource or apply_manifest action in the plan.
// Returns nil when fetcher is nil (no-op for non-interactive mode).
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
// proposed. For nested maps it recurses; for slices of maps it matches
// elements semantically (by merge key) when possible, falling back to index.
//
// Proposed-only keys are *omitted* from the pruned-live result so they
// surface as additions in the unified diff. Echoing them back (the previous
// behaviour) silently hid real changes like `value` → `valueFrom`.
func pruneToProposed(live, proposed map[string]any) map[string]any {
	result := make(map[string]any, len(proposed))
	for key, proposedVal := range proposed {
		liveVal, ok := live[key]
		if !ok {
			// Proposed-only — leave out so it appears as an addition in the diff.
			continue
		}

		// Both sides are maps — recurse.
		pMap, pIsMap := proposedVal.(map[string]any)
		lMap, lIsMap := liveVal.(map[string]any)
		if pIsMap && lIsMap {
			result[key] = pruneToProposed(lMap, pMap)
			continue
		}

		// Both sides are slices — align semantically and recurse into pairs.
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

// mergeKeyCandidates lists the field names commonly used to identify
// elements in Kubernetes-style lists of objects, in priority order. The
// first candidate that uniquely identifies every element in a slice is used
// to align it with its counterpart on the other side of the diff.
var mergeKeyCandidates = []string{
	"name",
	"containerPort",
	"port",
	"mountPath",
	"devicePath",
	"topologyKey",
	"key",
}

// findMergeKey returns the first candidate that every element in slice
// carries as a scalar with values unique across the slice. Returns "" if no
// candidate qualifies — the slice isn't safely mergeable by key.
func findMergeKey(slice []any) string {
	if len(slice) == 0 {
		return ""
	}
	for _, cand := range mergeKeyCandidates {
		if sliceHasUniqueScalarKey(slice, cand) {
			return cand
		}
	}
	return ""
}

// sliceHasUniqueScalarKey reports whether every element of slice is a map
// containing key with a non-nil scalar value, and those values are unique
// across the slice.
func sliceHasUniqueScalarKey(slice []any, key string) bool {
	seen := make(map[any]struct{}, len(slice))
	for _, elem := range slice {
		m, ok := elem.(map[string]any)
		if !ok {
			return false
		}
		v, has := m[key]
		if !has || v == nil {
			return false
		}
		switch v.(type) {
		case map[string]any, []any:
			return false
		}
		if _, dup := seen[v]; dup {
			return false
		}
		seen[v] = struct{}{}
	}
	return true
}

// pruneSlice aligns live and proposed slices for diffing. When both slices
// share a viable merge key it pairs elements by that key (so inserting a
// new env var doesn't make every subsequent entry look "changed"); otherwise
// it falls back to positional alignment.
func pruneSlice(live, proposed []any) []any {
	if key := findMergeKey(proposed); key != "" && findMergeKey(live) == key {
		return pruneSliceByKey(live, proposed, key)
	}

	result := make([]any, 0, len(proposed))
	for i, pElem := range proposed {
		if i >= len(live) {
			// Proposed-only — omit so it appears as an addition in the diff.
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

// pruneSliceByKey aligns live to proposed using mergeKey, recursively prunes
// each matched pair, and returns the result in proposed order. Proposed-only
// elements are omitted (they appear as additions in the diff); live-only
// elements are omitted (consistent with how live-only fields are treated as
// server-side defaults elsewhere).
func pruneSliceByKey(live, proposed []any, mergeKey string) []any {
	liveByKey := make(map[any]map[string]any, len(live))
	for _, elem := range live {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		if k, has := m[mergeKey]; has {
			liveByKey[k] = m
		}
	}

	result := make([]any, 0, len(proposed))
	for _, pElem := range proposed {
		pMap, ok := pElem.(map[string]any)
		if !ok {
			continue
		}
		key, hasKey := pMap[mergeKey]
		if !hasKey {
			continue
		}
		lMap, found := liveByKey[key]
		if !found {
			continue
		}
		result = append(result, pruneToProposed(lMap, pMap))
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

	sb.WriteString("\nExecute these actions in order. Proceed directly with the mutating tools.")
	sb.WriteString("\n\nIMPORTANT: The execution guard enforces this plan strictly:")
	sb.WriteString("\n- Each action can only be executed ONCE (call count is tracked)")
	sb.WriteString("\n- Targeting parameters (namespace, name, app, kind, type) must match exactly")
	sb.WriteString("\n- Tools not listed above will be blocked")
	sb.WriteString("\nIf you need to deviate from the plan, call propose_plan with a revised plan.")
	return sb.String()
}
