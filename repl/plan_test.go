package repl

import (
	"strings"
	"testing"
)

func TestParsePlanFromResponse(t *testing.T) {
	args := map[string]any{
		"description": "Deploy nginx to default namespace",
		"actions": []any{
			map[string]any{
				"tool":   "apply_resource",
				"reason": "create the nginx deployment",
				"parameters": map[string]any{
					"yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: nginx\n  namespace: default",
				},
			},
			map[string]any{
				"tool":   "apply_resource",
				"reason": "expose nginx",
				"parameters": map[string]any{
					"yaml": "apiVersion: v1\nkind: Service\nmetadata:\n  name: nginx\n  namespace: default",
				},
			},
		},
	}

	plan := ParsePlanFromResponse(args)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
	if plan.Description != "Deploy nginx to default namespace" {
		t.Errorf("unexpected description: %s", plan.Description)
	}
	if len(plan.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(plan.Actions))
	}
	if plan.Actions[0].Tool != "apply_resource" {
		t.Errorf("expected apply_resource, got %s", plan.Actions[0].Tool)
	}
	if plan.Actions[0].Parameters["yaml"] == nil {
		t.Error("expected yaml parameter in first action")
	}
	if plan.Actions[1].Tool != "apply_resource" {
		t.Errorf("expected apply_resource, got %s", plan.Actions[1].Tool)
	}
}

func TestParsePlanFromResponseNoActions(t *testing.T) {
	args := map[string]any{
		"description": "empty plan",
	}
	plan := ParsePlanFromResponse(args)
	if plan != nil {
		t.Error("expected nil plan when no actions key")
	}
}

func TestParsePlanFromResponseInvalidAction(t *testing.T) {
	args := map[string]any{
		"description": "test",
		"actions": []any{
			"not a map", // invalid
			map[string]any{
				"tool":   "list_pods",
				"reason": "check pods",
			},
		},
	}
	plan := ParsePlanFromResponse(args)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
	if len(plan.Actions) != 1 {
		t.Errorf("expected 1 valid action, got %d", len(plan.Actions))
	}
}

func TestParsePlanFromResponseNoParameters(t *testing.T) {
	args := map[string]any{
		"description": "test",
		"actions": []any{
			map[string]any{
				"tool":   "list_pods",
				"reason": "check pods",
			},
		},
	}
	plan := ParsePlanFromResponse(args)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
	if plan.Actions[0].Parameters == nil {
		t.Error("expected non-nil parameters map (empty default)")
	}
}

func TestBuildPlanMarkdown(t *testing.T) {
	plan := &Plan{
		Description: "Test deployment plan",
		Actions: []PlannedAction{
			{
				Tool:       "apply_resource",
				Reason:     "create nginx",
				Parameters: map[string]any{"yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: nginx"},
			},
		},
	}

	md := buildPlanMarkdown(plan, nil)

	if !strings.Contains(md, "# Proposed Plan") {
		t.Error("expected markdown heading")
	}
	if !strings.Contains(md, "Test deployment plan") {
		t.Error("expected description in markdown")
	}
	if !strings.Contains(md, "`apply_resource`") {
		t.Error("expected tool name in markdown")
	}
	if !strings.Contains(md, "create nginx") {
		t.Error("expected reason in markdown")
	}
	if !strings.Contains(md, "/approve") {
		t.Error("expected /approve command in markdown")
	}
}

func TestBuildPlanMarkdownMultilineParams(t *testing.T) {
	plan := &Plan{
		Description: "Apply manifest",
		Actions: []PlannedAction{
			{
				Tool:   "apply_manifest",
				Reason: "apply yaml",
				Parameters: map[string]any{
					"yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test",
				},
			},
		},
	}

	md := buildPlanMarkdown(plan, nil)
	if !strings.Contains(md, "```yaml") {
		t.Error("expected yaml code block for multiline parameter")
	}
}

func TestFormatExecutionPrompt(t *testing.T) {
	plan := &Plan{
		Description: "Scale nginx",
		Actions: []PlannedAction{
			{
				Tool:       "apply_resource",
				Reason:     "scale up",
				Parameters: map[string]any{"yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: nginx\nspec:\n  replicas: 3"},
			},
		},
	}

	prompt := FormatExecutionPrompt(plan)

	if !strings.Contains(prompt, "APPROVED") {
		t.Error("expected APPROVED in execution prompt")
	}
	if !strings.Contains(prompt, "Scale nginx") {
		t.Error("expected plan description in prompt")
	}
	if !strings.Contains(prompt, "apply_resource") {
		t.Error("expected tool name in prompt")
	}
	if !strings.Contains(prompt, "scale up") {
		t.Error("expected reason in prompt")
	}
}

func TestFormatParameters(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"empty", map[string]any{}, "(none)"},
		{"nil", nil, "(none)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatParameters(tt.params)
			if got != tt.want {
				t.Errorf("formatParameters() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatParametersMultiline(t *testing.T) {
	params := map[string]any{
		"yaml": "line1\nline2",
	}
	got := formatParameters(params)
	if !strings.Contains(got, "multi-line") {
		t.Errorf("expected multi-line indicator, got %q", got)
	}
}

func TestGetString(t *testing.T) {
	m := map[string]any{
		"name": "nginx",
		"port": float64(80),
	}
	if got := getString(m, "name"); got != "nginx" {
		t.Errorf("expected 'nginx', got %q", got)
	}
	if got := getString(m, "port"); got != "" {
		t.Errorf("expected empty string for non-string value, got %q", got)
	}
	if got := getString(m, "missing"); got != "" {
		t.Errorf("expected empty string for missing key, got %q", got)
	}
}

func TestNormalizeYAML(t *testing.T) {
	// Same content, different key ordering
	a := "apiVersion: v1\ndata:\n  key: value\nkind: ConfigMap\nmetadata:\n  name: test\n"
	b := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\ndata:\n  key: value\n"

	na := normalizeYAML(a)
	nb := normalizeYAML(b)
	if na != nb {
		t.Errorf("normalized YAML should match:\n--- a ---\n%s\n--- b ---\n%s", na, nb)
	}
}

func TestComputePlanDiffsNormalizesKeyOrder(t *testing.T) {
	// Cluster returns keys in alphabetical order (data before kind)
	clusterYAML := "apiVersion: v1\ndata:\n  user.vcl: |\n    sub vcl_recv {\n    }\nkind: ConfigMap\nmetadata:\n  name: test\n  namespace: default\n"

	// Proposed has conventional order (kind before metadata before data) + one new line
	proposedYAML := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n  namespace: default\ndata:\n  user.vcl: |\n    sub vcl_recv {\n        return (synth(403));\n    }\n"

	fetcher := func(yaml string) (string, error) {
		return clusterYAML, nil
	}

	plan := &Plan{
		Description: "test",
		Actions: []PlannedAction{
			{
				Tool:       "apply_resource",
				Reason:     "update config",
				Parameters: map[string]any{"yaml": proposedYAML},
			},
		},
	}

	diffs := computePlanDiffs(plan, fetcher)
	diff, ok := diffs[0]
	if !ok {
		t.Fatal("expected a diff for action 0")
	}

	// The diff should only show the actual content change, not key reordering.
	// Count changed lines (starting with + or - but not --- or +++)
	lines := strings.Split(diff, "\n")
	changed := 0
	for _, line := range lines {
		if (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")) &&
			!strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "+++") {
			changed++
		}
	}
	// Should be a small diff (the one added line + one removed line), not 30+ lines
	if changed > 6 {
		t.Errorf("diff has too many changed lines (%d), key reordering not normalized:\n%s", changed, diff)
	}
}

func TestComputePlanDiffsApplyManifest(t *testing.T) {
	// Stored manifest has image 0.4.0
	storedYAML := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: activity\n  namespace: activity\nspec:\n  template:\n    spec:\n      containers:\n      - name: activity\n        image: activity:0.4.0\n"

	// Cluster has image 0.3.9
	clusterYAML := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: activity\n  namespace: activity\nspec:\n  template:\n    spec:\n      containers:\n      - name: activity\n        image: activity:0.3.9\n"

	fetcher := func(yaml string) (string, error) {
		return clusterYAML, nil
	}
	manifestReader := func(namespace, app, resourceType string) (string, error) {
		if namespace == "activity" && app == "activity" && resourceType == "deployment" {
			return storedYAML, nil
		}
		return "", nil
	}

	plan := &Plan{
		Description: "Bump image to 0.4.0",
		Actions: []PlannedAction{
			{
				Tool:   "apply_manifest",
				Reason: "Update the activity deployment image from 0.3.9 to 0.4.0",
				Parameters: map[string]any{
					"namespace": "activity",
					"app":       "activity",
					"type":      "deployment",
				},
			},
		},
	}

	diffs := computePlanDiffs(plan, fetcher, manifestReader)
	diff, ok := diffs[0]
	if !ok {
		t.Fatal("expected a diff for apply_manifest action 0")
	}
	if !strings.Contains(diff, "0.3.9") || !strings.Contains(diff, "0.4.0") {
		t.Errorf("diff should show image version change, got:\n%s", diff)
	}
}

func TestComputePlanDiffsApplyManifestNoReader(t *testing.T) {
	fetcher := func(yaml string) (string, error) {
		return "apiVersion: v1\nkind: ConfigMap\n", nil
	}

	plan := &Plan{
		Description: "test",
		Actions: []PlannedAction{
			{
				Tool:   "apply_manifest",
				Reason: "apply",
				Parameters: map[string]any{
					"namespace": "default",
					"app":       "test",
					"type":      "configmap",
				},
			},
		},
	}

	// No manifest reader — should produce no diffs (not panic)
	diffs := computePlanDiffs(plan, fetcher)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs without manifest reader, got %d", len(diffs))
	}
}

func TestBuildPlanMarkdownApplyManifestDiff(t *testing.T) {
	plan := &Plan{
		Description: "Bump image",
		Actions: []PlannedAction{
			{
				Tool:   "apply_manifest",
				Reason: "update deployment",
				Parameters: map[string]any{
					"namespace": "default",
					"app":       "nginx",
					"type":      "deployment",
				},
			},
		},
	}

	diffs := map[int]string{
		0: "--- cluster\n+++ proposed\n@@ -1,2 +1,2 @@\n-image: nginx:1.0\n+image: nginx:2.0\n",
	}

	md := buildPlanMarkdown(plan, diffs)
	if !strings.Contains(md, "Diff from current cluster state") {
		t.Error("expected diff header for apply_manifest action")
	}
	if !strings.Contains(md, "```diff") {
		t.Error("expected diff code block")
	}
}

func TestRenderPlanNil(t *testing.T) {
	out := RenderPlan(nil, nil)
	if !strings.Contains(out, "No plan") {
		t.Errorf("expected 'No plan' message, got %q", out)
	}
}
