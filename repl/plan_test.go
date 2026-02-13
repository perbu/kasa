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

	md := buildPlanMarkdown(plan)

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

	md := buildPlanMarkdown(plan)
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

func TestRenderPlanNil(t *testing.T) {
	out := RenderPlan(nil)
	if !strings.Contains(out, "No plan") {
		t.Errorf("expected 'No plan' message, got %q", out)
	}
}
