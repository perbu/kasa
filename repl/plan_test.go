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

func TestComputePlanDiffsIgnoresKeyOrder(t *testing.T) {
	clusterYAML := "apiVersion: v1\ndata:\n  user.vcl: |\n    sub vcl_recv {\n    }\nkind: ConfigMap\nmetadata:\n  name: test\n  namespace: default\n"
	proposedYAML := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n  namespace: default\ndata:\n  user.vcl: |\n    sub vcl_recv {\n        return (synth(403));\n    }\n"

	// Unit tests treat the dry-run apply as identity (projected == proposed)
	// so they can assert pure dyff behavior without standing up a fake cluster.
	fetcher := func(yamlContent string) (string, string, error) { return clusterYAML, yamlContent, nil }

	plan := &Plan{
		Description: "test",
		Actions: []PlannedAction{{
			Tool:       "apply_resource",
			Reason:     "update config",
			Parameters: map[string]any{"yaml": proposedYAML},
		}},
	}

	diff, ok := computePlanDiffs(plan, fetcher)[0]
	if !ok {
		t.Fatal("expected a diff for action 0")
	}
	if !strings.Contains(diff, "return (synth(403))") {
		t.Errorf("diff should mention the added content, got:\n%s", diff)
	}
	if strings.Contains(diff, "apiVersion") || strings.Contains(diff, "kind: ConfigMap") {
		t.Errorf("unchanged keys (apiVersion / kind) should not appear in diff, got:\n%s", diff)
	}
}

func TestComputePlanDiffsApplyManifest(t *testing.T) {
	// Stored manifest has image 0.4.0
	storedYAML := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: activity\n  namespace: activity\nspec:\n  template:\n    spec:\n      containers:\n      - name: activity\n        image: activity:0.4.0\n"

	// Cluster has image 0.3.9
	clusterYAML := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: activity\n  namespace: activity\nspec:\n  template:\n    spec:\n      containers:\n      - name: activity\n        image: activity:0.3.9\n"

	fetcher := func(yamlContent string) (string, string, error) {
		return clusterYAML, yamlContent, nil
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
	fetcher := func(yamlContent string) (string, string, error) {
		return "apiVersion: v1\nkind: ConfigMap\n", yamlContent, nil
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
	if !strings.Contains(md, "#### Diff from current cluster state") {
		t.Error("expected h4 diff header for apply_manifest action")
	}
	if !strings.Contains(md, "```") {
		t.Error("expected code fence around diff")
	}
}

// When an apply_resource action has a diff, the proposed YAML is redundant
// with the diff and should not appear in the rendered plan.
func TestBuildPlanMarkdownApplyResourceDiffSuppressesYAML(t *testing.T) {
	yamlBody := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: nginx\nspec:\n  replicas: 3"
	plan := &Plan{
		Description: "Scale nginx",
		Actions: []PlannedAction{{
			Tool:       "apply_resource",
			Reason:     "scale up",
			Parameters: map[string]any{"yaml": yamlBody},
		}},
	}

	diffs := map[int]string{0: "replicas changed from 1 to 3\n"}
	md := buildPlanMarkdown(plan, diffs)

	if strings.Contains(md, yamlBody) {
		t.Errorf("yaml body should be suppressed when a diff is present, got:\n%s", md)
	}
	if strings.Contains(md, "**yaml:**") {
		t.Error("yaml header should be suppressed when a diff is present")
	}
	if !strings.Contains(md, "#### Diff from current cluster state") {
		t.Error("expected h4 diff header")
	}
	if !strings.Contains(md, "replicas changed from 1 to 3") {
		t.Error("expected diff body to appear")
	}
}

// When apply_resource has no diff (e.g. brand-new resource), the proposed
// YAML must still be shown so the user can see what's being created.
func TestBuildPlanMarkdownApplyResourceNoDiffShowsYAML(t *testing.T) {
	yamlBody := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: nginx"
	plan := &Plan{
		Description: "Create nginx",
		Actions: []PlannedAction{{
			Tool:       "apply_resource",
			Reason:     "new deployment",
			Parameters: map[string]any{"yaml": yamlBody},
		}},
	}

	md := buildPlanMarkdown(plan, nil)
	if !strings.Contains(md, yamlBody) {
		t.Error("yaml body should be shown when no diff is available")
	}
	if strings.Contains(md, "Diff from current cluster state") {
		t.Error("no diff header expected when there is no diff")
	}
}

func TestRenderPlanNil(t *testing.T) {
	out := RenderPlan(nil, nil)
	if !strings.Contains(out, "No plan") {
		t.Errorf("expected 'No plan' message, got %q", out)
	}
}

// TestComputePlanDiffsInsertsEnvVarCleanly covers the case where the
// proposed YAML inserts a new env var in the middle of the list. The dyff
// HumanReport should describe it as a single list entry added, not a
// cascade of "changes" across every later entry.
func TestComputePlanDiffsInsertsEnvVarCleanly(t *testing.T) {
	clusterYAML := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: nexus:1.0
        env:
        - name: PHOENIX_API_KEY
          value: secret-key
        - name: SALESFORCE_DOMAIN
          value: varnish.my.salesforce.com
        - name: ZEN_URL
          value: https://zen.varnish-software.com/
        - name: AUTH_MODE
          value: headers
`

	proposedYAML := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: nexus:1.0
        env:
        - name: PHOENIX_API_KEY
          value: secret-key
        - name: PHOENIX_URL
          value: http://phoenix.portal.svc.cluster.local:7080/
        - name: SALESFORCE_DOMAIN
          value: varnish.my.salesforce.com
        - name: ZEN_URL
          value: https://zen.varnish-software.com/
        - name: AUTH_MODE
          value: headers
`

	fetcher := func(yamlContent string) (string, string, error) { return clusterYAML, yamlContent, nil }

	plan := &Plan{
		Description: "add PHOENIX_URL env var",
		Actions: []PlannedAction{{
			Tool:       "apply_resource",
			Reason:     "add env var",
			Parameters: map[string]any{"yaml": proposedYAML},
		}},
	}

	diffs := computePlanDiffs(plan, fetcher)
	diff, ok := diffs[0]
	if !ok {
		t.Fatal("expected a diff for action 0")
	}

	if !strings.Contains(diff, "PHOENIX_URL") {
		t.Errorf("expected PHOENIX_URL to be reported as added, got:\n%s", diff)
	}
	// Unchanged env vars must not appear in the diff body.
	for _, unchanged := range []string{"SALESFORCE_DOMAIN", "ZEN_URL", "AUTH_MODE", "PHOENIX_API_KEY"} {
		if strings.Contains(diff, unchanged) {
			t.Errorf("unchanged env var %s should not appear, got:\n%s", unchanged, diff)
		}
	}
}

// TestComputePlanDiffsShowsEnvRename covers a rename: SALESFORCE_DOMAIN in
// the cluster is replaced by SALESFORCE_BASE_URL in the proposed manifest.
// The diff must show *both* sides — the old key being removed and the new
// key being added — otherwise the user can't tell the old value is being
// dropped.
func TestComputePlanDiffsShowsEnvRename(t *testing.T) {
	clusterYAML := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: nexus:1.0
        env:
        - name: PHOENIX_API_KEY
          value: secret-key
        - name: SALESFORCE_DOMAIN
          value: varnish.my.salesforce.com
        - name: ZEN_URL
          value: https://zen.varnish-software.com/
`

	proposedYAML := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: nexus:1.0
        env:
        - name: PHOENIX_API_KEY
          value: secret-key
        - name: SALESFORCE_BASE_URL
          value: varnish.my.salesforce.com
        - name: ZEN_URL
          value: https://zen.varnish-software.com/
`

	fetcher := func(yamlContent string) (string, string, error) { return clusterYAML, yamlContent, nil }

	plan := &Plan{
		Description: "rename SALESFORCE_DOMAIN to SALESFORCE_BASE_URL",
		Actions: []PlannedAction{{
			Tool:       "apply_resource",
			Reason:     "rename env var",
			Parameters: map[string]any{"yaml": proposedYAML},
		}},
	}

	diff := computePlanDiffs(plan, fetcher)[0]
	if !strings.Contains(diff, "SALESFORCE_DOMAIN") {
		t.Errorf("rename should mention removed key SALESFORCE_DOMAIN, got:\n%s", diff)
	}
	if !strings.Contains(diff, "SALESFORCE_BASE_URL") {
		t.Errorf("rename should mention added key SALESFORCE_BASE_URL, got:\n%s", diff)
	}
	if !strings.Contains(diff, "removed") {
		t.Errorf("rename should describe a removal, got:\n%s", diff)
	}
	if !strings.Contains(diff, "added") {
		t.Errorf("rename should describe an addition, got:\n%s", diff)
	}
}

// TestComputePlanDiffsWashesOutServerDefaults proves the core point of the
// dry-run-against-live model: fields that the cluster fills in by default
// (and that a real server-side dry-run apply would re-fill in the projected
// state) must NOT appear as "removed" in the diff. Only the user's actual
// change should show up.
func TestComputePlanDiffsWashesOutServerDefaults(t *testing.T) {
	// Live state has server-defaulted fields the user never authored.
	liveYAML := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  progressDeadlineSeconds: 600
  revisionHistoryLimit: 10
  template:
    spec:
      dnsPolicy: ClusterFirst
      restartPolicy: Always
      containers:
      - name: nexus
        image: ghcr.io/varnish/nexus:sha-old
        terminationMessagePath: /dev/termination-log
`

	// Proposed YAML only sets what the user cares about.
	proposedYAML := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  template:
    spec:
      containers:
      - name: nexus
        image: ghcr.io/varnish/nexus:sha-new
`

	// A real server-side dry-run apply would replay defaulting onto the
	// projected state, restoring all the fields above. Simulate that by
	// returning a projected YAML that matches live everywhere except the
	// user's actual change.
	projectedYAML := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexus
  namespace: portal
spec:
  progressDeadlineSeconds: 600
  revisionHistoryLimit: 10
  template:
    spec:
      dnsPolicy: ClusterFirst
      restartPolicy: Always
      containers:
      - name: nexus
        image: ghcr.io/varnish/nexus:sha-new
        terminationMessagePath: /dev/termination-log
`

	fetcher := func(string) (string, string, error) { return liveYAML, projectedYAML, nil }

	plan := &Plan{
		Description: "bump image",
		Actions: []PlannedAction{{
			Tool:       "apply_resource",
			Reason:     "image update",
			Parameters: map[string]any{"yaml": proposedYAML},
		}},
	}

	diff, ok := computePlanDiffs(plan, fetcher)[0]
	if !ok {
		t.Fatal("expected a diff for action 0")
	}
	if !strings.Contains(diff, "sha-new") || !strings.Contains(diff, "sha-old") {
		t.Errorf("diff should show the image change, got:\n%s", diff)
	}
	for _, defaulted := range []string{
		"progressDeadlineSeconds",
		"revisionHistoryLimit",
		"dnsPolicy",
		"restartPolicy",
		"terminationMessagePath",
	} {
		if strings.Contains(diff, defaulted) {
			t.Errorf("server-defaulted field %q should not appear in the diff, got:\n%s", defaulted, diff)
		}
	}
}
