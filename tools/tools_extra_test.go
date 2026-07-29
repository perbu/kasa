package tools

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestIsMutating tests the IsMutating function.
func TestIsMutating(t *testing.T) {
	mgr := newTestManifestManager(t)

	t.Run("mutating tools return true", func(t *testing.T) {
		tool := NewApplyResourceTool(dynamicClient, mgr)
		if !IsMutating(tool) {
			t.Error("expected apply_resource to be mutating")
		}
	})

	t.Run("read-only tools return false", func(t *testing.T) {
		tool := NewListNamespacesTool(clientset)
		if IsMutating(tool) {
			t.Error("expected list_namespaces to not be mutating")
		}
	})

	t.Run("planning tools return false", func(t *testing.T) {
		tool := NewProposePlanTool()
		if IsMutating(tool) {
			t.Error("expected propose_plan to not be mutating")
		}
	})
}

// TestReadOnlyTools tests that ReadOnlyTools returns only read-only tools.
func TestReadOnlyTools(t *testing.T) {
	mgr := newTestManifestManager(t)
	kt := NewKubeTools(clientset, dynamicClient, nil, mgr, nil, "", 3, NewDirectIO(), nil)

	tools := kt.ReadOnlyTools()
	if len(tools) == 0 {
		t.Fatal("expected at least one read-only tool")
	}

	for _, tool := range tools {
		ft, ok := tool.(functionTool)
		if !ok {
			t.Errorf("tool %s does not implement functionTool", tool.Name())
			continue
		}
		if ft.Category() != CategoryReadOnly {
			t.Errorf("tool %s has category %s, expected read-only", tool.Name(), ft.Category())
		}
	}
}

// TestMutatingTools tests that MutatingTools returns only mutating tools.
func TestMutatingTools(t *testing.T) {
	mgr := newTestManifestManager(t)
	kt := NewKubeTools(clientset, dynamicClient, nil, mgr, nil, "", 3, NewDirectIO(), nil)

	tools := kt.MutatingTools()
	if len(tools) == 0 {
		t.Fatal("expected at least one mutating tool")
	}

	for _, tool := range tools {
		ft, ok := tool.(functionTool)
		if !ok {
			t.Errorf("tool %s does not implement functionTool", tool.Name())
			continue
		}
		if ft.Category() != CategoryMutating {
			t.Errorf("tool %s has category %s, expected mutating", tool.Name(), ft.Category())
		}
	}
}

// TestGenerateToolDocs tests that GenerateToolDocs returns non-empty documentation.
func TestGenerateToolDocs(t *testing.T) {
	mgr := newTestManifestManager(t)
	kt := NewKubeTools(clientset, dynamicClient, nil, mgr, nil, "", 3, NewDirectIO(), nil)

	docs := kt.GenerateToolDocs()
	if docs == "" {
		t.Fatal("expected non-empty documentation")
	}

	// Should contain all three sections
	if !containsSubstring(docs, "Read-Only Tools") {
		t.Error("expected 'Read-Only Tools' section")
	}
	if !containsSubstring(docs, "Mutating Tools") {
		t.Error("expected 'Mutating Tools' section")
	}
	if !containsSubstring(docs, "Planning Tools") {
		t.Error("expected 'Planning Tools' section")
	}

	// Should contain specific tool names
	if !containsSubstring(docs, "list_namespaces") {
		t.Error("expected list_namespaces in docs")
	}
	if !containsSubstring(docs, "apply_resource") {
		t.Error("expected apply_resource in docs")
	}
	if !containsSubstring(docs, "propose_plan") {
		t.Error("expected propose_plan in docs")
	}
}

// TestProposePlanTool tests the propose_plan tool.
func TestProposePlanTool(t *testing.T) {
	tool := NewProposePlanTool()

	t.Run("valid plan", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"description": "Deploy nginx to default namespace",
			"actions": []any{
				map[string]any{
					"tool":       "apply_resource",
					"parameters": map[string]any{"yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: nginx"},
					"reason":     "Create the nginx deployment",
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["status"] != "awaiting_approval" {
			t.Errorf("expected status 'awaiting_approval', got %v", result["status"])
		}
		if result["description"] != "Deploy nginx to default namespace" {
			t.Errorf("unexpected description: %v", result["description"])
		}
	})

	t.Run("missing description", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"actions": []any{
				map[string]any{"tool": "test", "parameters": map[string]any{}, "reason": "test"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["error"]; !ok {
			t.Error("expected error for missing description")
		}
	})

	t.Run("empty actions", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"description": "test",
			"actions":     []any{},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		errMsg := result["error"].(string)
		if errMsg != "at least one action is required" {
			t.Errorf("unexpected error: %s", errMsg)
		}
	})

	t.Run("action missing tool", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"description": "test",
			"actions": []any{
				map[string]any{"parameters": map[string]any{}, "reason": "test"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		errStr, _ := result["error"].(string)
		if !strings.Contains(errStr, "missing required fields") || !strings.Contains(errStr, "tool") {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})

	t.Run("action missing reason", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"description": "test",
			"actions": []any{
				map[string]any{"tool": "test", "parameters": map[string]any{}},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		errStr, _ := result["error"].(string)
		if !strings.Contains(errStr, "missing required fields") || !strings.Contains(errStr, "reason") {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})

	t.Run("invalid args type", func(t *testing.T) {
		result, err := tool.Run(nil, 12345)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["error"] != "invalid arguments type" {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})

	t.Run("args as JSON string", func(t *testing.T) {
		result, err := tool.Run(nil, `{"description":"test plan","actions":[{"tool":"t","parameters":{},"reason":"r"}]}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["status"] != "awaiting_approval" {
			t.Errorf("expected status 'awaiting_approval', got %v", result["status"])
		}
	})

	t.Run("invalid JSON string", func(t *testing.T) {
		result, err := tool.Run(nil, "not json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["error"] != "invalid arguments format" {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})

	t.Run("invalid action format", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"description": "test",
			"actions":     []any{"not a map"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["error"] != "invalid action format" {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})
}

// TestProposePlanSchemaValidation tests that planned action parameters are
// validated against the target tool's declared schema at proposal time.
func TestProposePlanSchemaValidation(t *testing.T) {
	tool := NewProposePlanTool()
	tool.SetToolSchemas(map[string]toolSchema{
		"apply_resource": {
			params:   map[string]bool{"yaml": true, "namespace": true, "app": true, "dry_run": true},
			required: []string{"yaml"},
		},
	})

	validYAML := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: nginx"

	t.Run("valid parameters pass", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"description": "Deploy nginx",
			"actions": []any{
				map[string]any{
					"tool":       "apply_resource",
					"parameters": map[string]any{"yaml": validYAML, "namespace": "default"},
					"reason":     "Create the deployment",
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["status"] != "awaiting_approval" {
			t.Errorf("expected status 'awaiting_approval', got %v", result)
		}
	})

	t.Run("undeclared parameter rejected", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"description": "Bump image tag",
			"actions": []any{
				map[string]any{
					"tool":       "apply_resource",
					"parameters": map[string]any{"type": "deployment", "app": "internal-agents", "namespace": "internal-agents"},
					"reason":     "Apply updated deployment",
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		errStr, _ := result["error"].(string)
		if !strings.Contains(errStr, `does not accept parameter(s): type`) {
			t.Errorf("expected undeclared-parameter error, got %v", result["error"])
		}
		if !strings.Contains(errStr, "app, dry_run, namespace, yaml") {
			t.Errorf("expected valid parameter list in error, got %v", result["error"])
		}
	})

	t.Run("missing required parameter rejected", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"description": "Bump image tag",
			"actions": []any{
				map[string]any{
					"tool":       "apply_resource",
					"parameters": map[string]any{"app": "internal-agents", "namespace": "internal-agents"},
					"reason":     "Apply updated deployment",
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		errStr, _ := result["error"].(string)
		if !strings.Contains(errStr, "missing required parameter(s): yaml") {
			t.Errorf("expected missing-required error, got %v", result["error"])
		}
	})

	t.Run("unknown tool rejected", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"description": "test",
			"actions": []any{
				map[string]any{
					"tool":       "no_such_tool",
					"parameters": map[string]any{},
					"reason":     "test",
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		errStr, _ := result["error"].(string)
		if !strings.Contains(errStr, `unknown tool "no_such_tool"`) {
			t.Errorf("expected unknown-tool error, got %v", result["error"])
		}
	})

	t.Run("no schemas set skips validation", func(t *testing.T) {
		unwired := NewProposePlanTool()
		result, err := unwired.Run(nil, map[string]any{
			"description": "test",
			"actions": []any{
				map[string]any{
					"tool":       "apply_resource",
					"parameters": map[string]any{"type": "deployment"},
					"reason":     "test",
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["status"] != "awaiting_approval" {
			t.Errorf("expected validation to be skipped, got %v", result)
		}
	})
}

// TestAskClarificationTool tests the ask_clarification tool.
func TestAskClarificationTool(t *testing.T) {
	tool := NewAskClarificationTool()

	t.Run("valid clarification", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"context": "Need to deploy nginx but unsure about namespace",
			"questions": []any{
				map[string]any{
					"question": "Which namespace should I deploy to?",
					"options":  []any{"default", "production", "staging"},
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["status"] != "awaiting_answers" {
			t.Errorf("expected status 'awaiting_answers', got %v", result["status"])
		}
		if result["context"] != "Need to deploy nginx but unsure about namespace" {
			t.Errorf("unexpected context: %v", result["context"])
		}
	})

	t.Run("missing context", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"questions": []any{
				map[string]any{"question": "test?"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["error"] != "context is required" {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})

	t.Run("empty questions", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"context":   "test",
			"questions": []any{},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["error"] != "at least one question is required" {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})

	t.Run("question missing text", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"context": "test",
			"questions": []any{
				map[string]any{"options": []any{"a", "b"}},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["error"] != "question missing text at index 0" {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})

	t.Run("invalid args type", func(t *testing.T) {
		result, err := tool.Run(nil, 42)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["error"] != "invalid arguments type" {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})

	t.Run("args as JSON string", func(t *testing.T) {
		result, err := tool.Run(nil, `{"context":"ctx","questions":[{"question":"q?"}]}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["status"] != "awaiting_answers" {
			t.Errorf("expected 'awaiting_answers', got %v", result["status"])
		}
	})

	t.Run("invalid question format", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"context":   "test",
			"questions": []any{"not a map"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["error"] != "invalid question format at index 0" {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})
}

// TestDeleteNamespaceTool tests the delete_namespace tool.
func TestDeleteNamespaceTool(t *testing.T) {
	mgr := newTestManifestManager(t)
	tool := NewDeleteNamespaceTool(clientset, mgr)

	t.Run("protects system namespaces", func(t *testing.T) {
		for _, ns := range []string{"default", "kube-system", "kube-public", "kube-node-lease"} {
			result, err := tool.Run(nil, map[string]any{"name": ns})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result["success"] != false {
				t.Errorf("expected failure for protected namespace %s", ns)
			}
		}
	})

	t.Run("returns error for non-existent namespace", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name": "non-existent-ns-xyz",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != false {
			t.Errorf("expected failure for non-existent namespace")
		}
	})

	t.Run("validates required name", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{})
		if result["error"] != "name is required" {
			t.Errorf("expected 'name is required', got: %v", result["error"])
		}
	})

	t.Run("refuses non-empty namespace without force", func(t *testing.T) {
		nsName := "test-delete-nonempty"
		createTestNamespace(t, clientset, nsName)
		createTestPod(t, clientset, nsName, "blocking-pod", nil)

		result, err := tool.Run(nil, map[string]any{
			"name": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != false {
			t.Errorf("expected failure for non-empty namespace without force")
		}
	})

	t.Run("deletes empty namespace", func(t *testing.T) {
		nsName := "test-delete-empty"
		createTestNamespace(t, clientset, nsName)

		result, err := tool.Run(nil, map[string]any{
			"name": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})
}

// TestSleepTool tests the sleep tool.
func TestSleepTool(t *testing.T) {
	tool := NewSleepTool()

	t.Run("sleeps for short duration", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"seconds": float64(0.01),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["message"] != "Sleep completed" {
			t.Errorf("expected 'Sleep completed', got %v", result["message"])
		}
		slept := result["slept_seconds"].(float64)
		if slept < 0.001 {
			t.Errorf("expected positive sleep duration, got %v", slept)
		}
	})

	t.Run("validates missing seconds", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{})
		if result["error"] != "seconds parameter is required" {
			t.Errorf("expected 'seconds parameter is required', got: %v", result["error"])
		}
	})

	t.Run("rejects negative seconds", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"seconds": float64(-1),
		})
		if result["error"] != "seconds cannot be negative" {
			t.Errorf("expected 'seconds cannot be negative', got: %v", result["error"])
		}
	})

	t.Run("rejects non-number seconds", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"seconds": "not a number",
		})
		if result["error"] != "seconds must be a number" {
			t.Errorf("expected 'seconds must be a number', got: %v", result["error"])
		}
	})

	t.Run("accepts integer seconds", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"seconds": int(0),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["message"] != "Sleep completed" {
			t.Errorf("unexpected result: %v", result)
		}
	})

	t.Run("invalid args type", func(t *testing.T) {
		result, _ := tool.Run(nil, "bad")
		if result["error"] != "invalid arguments format" {
			t.Errorf("expected 'invalid arguments format', got: %v", result["error"])
		}
	})
}

// TestApplyManifestTool tests the apply_manifest tool.
func TestApplyManifestTool(t *testing.T) {
	nsName := "test-apply-manifest"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewApplyManifestTool(dynamicClient, mgr)

	t.Run("applies deployment manifest", func(t *testing.T) {
		manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: apply-test
spec:
  replicas: 1
  selector:
    matchLabels:
      app: apply-test
  template:
    metadata:
      labels:
        app: apply-test
    spec:
      containers:
      - name: nginx
        image: nginx:1.25
`
		writeTestManifest(t, mgr, nsName, "apply-test", "deployment", manifest)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "apply-test",
			"type":      "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
		if result["action"] != "created" {
			t.Errorf("expected action 'created', got %v", result["action"])
		}
	})

	t.Run("applies configmap manifest", func(t *testing.T) {
		manifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: apply-cm
data:
  key: value
`
		writeTestManifest(t, mgr, nsName, "apply-cm", "configmap", manifest)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "apply-cm",
			"type":      "configmap",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})

	t.Run("dry run does not create", func(t *testing.T) {
		manifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: dryrun-cm
data:
  key: value
`
		writeTestManifest(t, mgr, nsName, "dryrun-cm", "configmap", manifest)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "dryrun-cm",
			"type":      "configmap",
			"dry_run":   true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["dry_run"] != true {
			t.Error("expected dry_run=true in result")
		}
	})

	t.Run("validates required parameters", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"app": "test", "type": "deployment",
		})
		if result["error"] != "namespace is required" {
			t.Errorf("expected namespace required, got: %v", result["error"])
		}

		result, _ = tool.Run(nil, map[string]any{
			"namespace": nsName, "type": "deployment",
		})
		if result["error"] != "app is required" {
			t.Errorf("expected app required, got: %v", result["error"])
		}

		result, _ = tool.Run(nil, map[string]any{
			"namespace": nsName, "app": "test",
		})
		if result["error"] != "type is required" {
			t.Errorf("expected type required, got: %v", result["error"])
		}
	})

	t.Run("rejects unsupported type", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"namespace": nsName, "app": "test", "type": "customthing",
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error for unsupported type")
		}
	})

	t.Run("returns error for missing manifest", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "non-existent",
			"type":      "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["error"]; !ok {
			t.Error("expected error for missing manifest")
		}
	})
}

// TestListResourcesTool tests the list_resources tool.
func TestListResourcesTool(t *testing.T) {
	tool := NewListResourcesTool(dynamicClient)

	nsName := "test-list-resources"
	createTestNamespace(t, clientset, nsName)
	createTestDeployment(t, clientset, nsName, "lr-deploy")

	t.Run("lists deployments", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"kind":      "deployment",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["error"]; ok {
			t.Fatalf("unexpected error: %v", result["error"])
		}

		count := result["count"].(int)
		if count < 1 {
			t.Errorf("expected at least 1 deployment, got %d", count)
		}
	})

	t.Run("lists using alias", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"kind":      "deploy",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["kind"] != "deployment" {
			t.Errorf("expected normalized kind 'deployment', got %v", result["kind"])
		}
	})

	t.Run("validates kind required", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{})
		if result["error"] != "kind is required" {
			t.Errorf("expected 'kind is required', got: %v", result["error"])
		}
	})

	t.Run("returns error for unknown kind without api_version", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"kind": "unknownthing",
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error for unknown kind")
		}
	})

	t.Run("lists all namespaces when namespace empty", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"kind": "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["scope"] != "all namespaces" {
			t.Errorf("expected 'all namespaces' scope, got %v", result["scope"])
		}
	})
}

// TestExtractStatusSummary tests the extractStatusSummary helper.
func TestExtractStatusSummary(t *testing.T) {
	t.Run("deployment status", func(t *testing.T) {
		status := map[string]any{
			"replicas":          float64(3),
			"readyReplicas":     float64(3),
			"availableReplicas": float64(3),
		}
		summary := extractStatusSummary(status, "deployment")
		if summary["replicas"] != float64(3) {
			t.Errorf("expected replicas=3, got %v", summary["replicas"])
		}
		if summary["ready"] != float64(3) {
			t.Errorf("expected ready=3, got %v", summary["ready"])
		}
	})

	t.Run("pod status", func(t *testing.T) {
		status := map[string]any{
			"phase": "Running",
		}
		summary := extractStatusSummary(status, "pod")
		if summary["phase"] != "Running" {
			t.Errorf("expected phase=Running, got %v", summary["phase"])
		}
	})

	t.Run("service with load balancer", func(t *testing.T) {
		status := map[string]any{
			"loadBalancer": map[string]any{
				"ingress": []any{
					map[string]any{"ip": "1.2.3.4"},
				},
			},
		}
		summary := extractStatusSummary(status, "service")
		if summary["loadBalancerIP"] == nil {
			t.Error("expected loadBalancerIP in summary")
		}
	})

	t.Run("gateway API with conditions", func(t *testing.T) {
		status := map[string]any{
			"conditions": []any{
				map[string]any{"type": "Accepted", "status": "True"},
				map[string]any{"type": "Programmed", "status": "True"},
			},
		}
		summary := extractStatusSummary(status, "httproute")
		conditions := summary["conditions"].([]string)
		if len(conditions) != 2 {
			t.Errorf("expected 2 conditions, got %d", len(conditions))
		}
	})

	t.Run("certificate status", func(t *testing.T) {
		status := map[string]any{
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "True", "reason": "Ready"},
			},
			"notAfter": "2026-12-01T00:00:00Z",
		}
		summary := extractStatusSummary(status, "certificate")
		if summary["ready"] != "True" {
			t.Errorf("expected ready=True, got %v", summary["ready"])
		}
		if summary["notAfter"] != "2026-12-01T00:00:00Z" {
			t.Errorf("unexpected notAfter: %v", summary["notAfter"])
		}
	})

	t.Run("unknown kind with conditions", func(t *testing.T) {
		status := map[string]any{
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "True"},
			},
			"phase": "Active",
		}
		summary := extractStatusSummary(status, "somethingunknown")
		if summary["conditionCount"] != 1 {
			t.Errorf("expected conditionCount=1, got %v", summary["conditionCount"])
		}
		if summary["phase"] != "Active" {
			t.Errorf("expected phase=Active, got %v", summary["phase"])
		}
	})

	t.Run("non-map status returns nil", func(t *testing.T) {
		summary := extractStatusSummary("not a map", "deployment")
		if summary != nil {
			t.Errorf("expected nil for non-map status, got %v", summary)
		}
	})

	t.Run("empty status returns nil", func(t *testing.T) {
		summary := extractStatusSummary(map[string]any{}, "deployment")
		if summary != nil {
			t.Errorf("expected nil for empty status, got %v", summary)
		}
	})
}

// TestUnstructuredNestedField tests the unstructuredNestedField helper.
func TestUnstructuredNestedField(t *testing.T) {
	obj := map[string]any{
		"metadata": map[string]any{
			"name":      "test",
			"namespace": "default",
		},
		"spec": map[string]any{
			"replicas": float64(3),
		},
	}

	t.Run("finds nested field", func(t *testing.T) {
		val, found, err := unstructuredNestedField(obj, "metadata", "name")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !found {
			t.Fatal("expected field to be found")
		}
		if val != "test" {
			t.Errorf("expected 'test', got %v", val)
		}
	})

	t.Run("returns false for missing field", func(t *testing.T) {
		_, found, err := unstructuredNestedField(obj, "metadata", "missing")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Error("expected field to not be found")
		}
	})

	t.Run("returns false for non-map intermediate", func(t *testing.T) {
		_, found, err := unstructuredNestedField(obj, "spec", "replicas", "deep")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Error("expected field to not be found through non-map")
		}
	})

	t.Run("single field lookup", func(t *testing.T) {
		val, found, _ := unstructuredNestedField(obj, "spec")
		if !found {
			t.Fatal("expected spec to be found")
		}
		specMap, ok := val.(map[string]any)
		if !ok {
			t.Fatal("expected map")
		}
		if specMap["replicas"] != float64(3) {
			t.Errorf("expected replicas=3, got %v", specMap["replicas"])
		}
	})
}

// TestCleanForImport tests the cleanForImport function.
func TestCleanForImport(t *testing.T) {
	t.Run("removes runtime metadata", func(t *testing.T) {
		resource := map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":              "test",
				"namespace":         "default",
				"uid":               "abc-123",
				"resourceVersion":   "12345",
				"generation":        float64(1),
				"creationTimestamp": "2024-01-01T00:00:00Z",
				"managedFields":     []any{},
				"selfLink":          "/apis/apps/v1/...",
			},
			"status": map[string]any{
				"replicas": float64(1),
			},
		}
		cleanForImport(resource)

		metadata := resource["metadata"].(map[string]any)
		if _, ok := metadata["uid"]; ok {
			t.Error("expected uid to be removed")
		}
		if _, ok := metadata["resourceVersion"]; ok {
			t.Error("expected resourceVersion to be removed")
		}
		if _, ok := metadata["generation"]; ok {
			t.Error("expected generation to be removed")
		}
		if _, ok := metadata["creationTimestamp"]; ok {
			t.Error("expected creationTimestamp to be removed")
		}
		if _, ok := metadata["managedFields"]; ok {
			t.Error("expected managedFields to be removed")
		}
		if _, ok := resource["status"]; ok {
			t.Error("expected status to be removed")
		}
		// Name should be preserved
		if metadata["name"] != "test" {
			t.Error("name should be preserved")
		}
	})

	t.Run("removes kubectl annotations", func(t *testing.T) {
		resource := map[string]any{
			"metadata": map[string]any{
				"name": "test",
				"annotations": map[string]any{
					"kubectl.kubernetes.io/last-applied-configuration": "{}",
					"deployment.kubernetes.io/revision":                "1",
					"kubernetes.io/change-cause":                       "test",
					"custom-annotation":                                "keep",
				},
			},
		}
		cleanForImport(resource)

		metadata := resource["metadata"].(map[string]any)
		annotations := metadata["annotations"].(map[string]any)
		if _, ok := annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
			t.Error("expected kubectl annotation to be removed")
		}
		if _, ok := annotations["deployment.kubernetes.io/revision"]; ok {
			t.Error("expected deployment revision annotation to be removed")
		}
		if _, ok := annotations["kubernetes.io/change-cause"]; ok {
			t.Error("expected change-cause annotation to be removed")
		}
		if annotations["custom-annotation"] != "keep" {
			t.Error("expected custom annotation to be preserved")
		}
	})

	t.Run("removes empty annotations map", func(t *testing.T) {
		resource := map[string]any{
			"metadata": map[string]any{
				"name": "test",
				"annotations": map[string]any{
					"kubectl.kubernetes.io/last-applied-configuration": "{}",
				},
			},
		}
		cleanForImport(resource)

		metadata := resource["metadata"].(map[string]any)
		if _, ok := metadata["annotations"]; ok {
			t.Error("expected empty annotations map to be removed")
		}
	})

	t.Run("converts Secret stringData to data", func(t *testing.T) {
		resource := map[string]any{
			"kind": "Secret",
			"metadata": map[string]any{
				"name": "test",
			},
			"stringData": map[string]any{
				"password": "secret123",
			},
		}
		cleanForImport(resource)

		if _, ok := resource["stringData"]; ok {
			t.Error("expected stringData to be removed")
		}
		data := resource["data"].(map[string]any)
		if data["password"] != "c2VjcmV0MTIz" { // base64("secret123")
			t.Errorf("expected base64 encoded data, got %v", data["password"])
		}
	})

	t.Run("removes service clusterIP fields", func(t *testing.T) {
		resource := map[string]any{
			"kind": "Service",
			"metadata": map[string]any{
				"name": "test",
			},
			"spec": map[string]any{
				"clusterIP":  "10.0.0.1",
				"clusterIPs": []any{"10.0.0.1"},
				"ports":      []any{map[string]any{"port": float64(80)}},
			},
		}
		cleanForImport(resource)

		spec := resource["spec"].(map[string]any)
		if _, ok := spec["clusterIP"]; ok {
			t.Error("expected clusterIP to be removed")
		}
		if _, ok := spec["clusterIPs"]; ok {
			t.Error("expected clusterIPs to be removed")
		}
		// ports should be preserved
		if spec["ports"] == nil {
			t.Error("expected ports to be preserved")
		}
	})
}

// TestShouldRemoveAnnotation tests the shouldRemoveAnnotation function.
func TestShouldRemoveAnnotation(t *testing.T) {
	tests := []struct {
		key    string
		remove bool
	}{
		{"kubectl.kubernetes.io/last-applied-configuration", true},
		{"kubectl.kubernetes.io/restartedAt", true},
		{"deployment.kubernetes.io/revision", true},
		{"kubernetes.io/change-cause", true},
		{"custom-annotation", false},
		{"app.kubernetes.io/name", false},
		{"nginx.ingress.kubernetes.io/rewrite-target", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := shouldRemoveAnnotation(tt.key)
			if got != tt.remove {
				t.Errorf("shouldRemoveAnnotation(%q) = %v, want %v", tt.key, got, tt.remove)
			}
		})
	}
}

// TestApplyResourceTool tests the apply_resource tool with dynamic client.
func TestApplyResourceTool(t *testing.T) {
	nsName := "test-apply-resource"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewApplyResourceTool(dynamicClient, mgr)

	t.Run("creates deployment from YAML", func(t *testing.T) {
		yaml := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: ar-deploy
  namespace: ` + nsName + `
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ar-deploy
  template:
    metadata:
      labels:
        app: ar-deploy
    spec:
      containers:
      - name: nginx
        image: nginx:1.25
`
		result, err := tool.Run(nil, map[string]any{
			"yaml": yaml,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
		if result["action"] != "created" {
			t.Errorf("expected 'created', got %v", result["action"])
		}
		if result["kind"] != "Deployment" {
			t.Errorf("expected kind 'Deployment', got %v", result["kind"])
		}
	})

	t.Run("updates existing resource", func(t *testing.T) {
		yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: ar-cm
  namespace: ` + nsName + `
data:
  key: value1
`
		// Create
		_, _ = tool.Run(nil, map[string]any{"yaml": yaml})

		// Update
		yaml2 := `apiVersion: v1
kind: ConfigMap
metadata:
  name: ar-cm
  namespace: ` + nsName + `
data:
  key: value2
`
		result, err := tool.Run(nil, map[string]any{"yaml": yaml2})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["action"] != "updated" {
			t.Errorf("expected 'updated', got %v", result["action"])
		}
	})

	t.Run("dry run does not persist", func(t *testing.T) {
		yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: ar-dryrun
  namespace: ` + nsName + `
data:
  key: value
`
		result, err := tool.Run(nil, map[string]any{
			"yaml":    yaml,
			"dry_run": true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["dry_run"] != true {
			t.Error("expected dry_run=true")
		}
	})

	t.Run("namespace override", func(t *testing.T) {
		yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: ar-ns-override
data:
  key: value
`
		result, err := tool.Run(nil, map[string]any{
			"yaml":      yaml,
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["namespace"] != nsName {
			t.Errorf("expected namespace %s, got %v", nsName, result["namespace"])
		}
	})

	t.Run("validates yaml required", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{})
		if result["error"] != "yaml is required" {
			t.Errorf("expected 'yaml is required', got: %v", result["error"])
		}
	})

	t.Run("rejects invalid YAML", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"yaml": "{{{invalid",
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error for invalid YAML")
		}
	})

	t.Run("rejects YAML without kind", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"yaml": "apiVersion: v1\nmetadata:\n  name: test\n",
		})
		if result["error"] != "YAML must contain a 'kind' field" {
			t.Errorf("expected 'kind' error, got: %v", result["error"])
		}
	})

	t.Run("rejects YAML without name", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"yaml": "apiVersion: v1\nkind: ConfigMap\ndata:\n  key: value\n",
		})
		if result["error"] != "YAML must contain metadata.name" {
			t.Errorf("expected 'metadata.name' error, got: %v", result["error"])
		}
	})
}

// TestDiffResourceTool tests the diff_resource tool.
func TestDiffResourceTool(t *testing.T) {
	mgr := newTestManifestManager(t)
	tool := NewDiffResourceTool(dynamicClient, mgr)

	nsName := "test-diff-resource"
	createTestNamespace(t, clientset, nsName)

	t.Run("validates required parameters", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{"app": "test", "type": "deployment"})
		if result["error"] != "namespace is required" {
			t.Errorf("expected namespace required, got: %v", result["error"])
		}

		result, _ = tool.Run(nil, map[string]any{"namespace": nsName, "type": "deployment"})
		if result["error"] != "app is required" {
			t.Errorf("expected app required, got: %v", result["error"])
		}

		result, _ = tool.Run(nil, map[string]any{"namespace": nsName, "app": "test"})
		if result["error"] != "type is required" {
			t.Errorf("expected type required, got: %v", result["error"])
		}
	})

	t.Run("returns error for missing manifest", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "nonexistent",
			"type":      "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["error"]; !ok {
			t.Error("expected error for missing manifest")
		}
	})

	t.Run("detects missing cluster resource", func(t *testing.T) {
		manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: diff-missing
  namespace: ` + nsName + `
spec:
  replicas: 1
  selector:
    matchLabels:
      app: diff-missing
  template:
    metadata:
      labels:
        app: diff-missing
    spec:
      containers:
      - name: nginx
        image: nginx:1.25
`
		writeTestManifest(t, mgr, nsName, "diff-missing", "deployment", manifest)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "diff-missing",
			"type":      "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["status"] != "missing" {
			t.Errorf("expected status 'missing', got %v", result["status"])
		}
	})
}

// TestSyncManifestsTool tests the sync_manifests tool.
func TestSyncManifestsTool(t *testing.T) {
	mgr := newTestManifestManager(t)
	tool := NewSyncManifestsTool(mgr)

	t.Run("returns error when no remote configured", func(t *testing.T) {
		result, err := tool.Run(nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != false {
			t.Error("expected failure when no remote")
		}
		if result["error"] != "no git remote configured" {
			t.Errorf("expected 'no git remote configured', got: %v", result["error"])
		}
	})
}

// TestApplyManifestToolServicePath tests the apply_manifest service path.
func TestApplyManifestToolServicePath(t *testing.T) {
	nsName := "test-apply-svc"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewApplyManifestTool(dynamicClient, mgr)

	t.Run("applies service manifest", func(t *testing.T) {
		manifest := `apiVersion: v1
kind: Service
metadata:
  name: apply-svc
spec:
  selector:
    app: test
  ports:
  - port: 80
    targetPort: 8080
`
		writeTestManifest(t, mgr, nsName, "apply-svc", "service", manifest)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "apply-svc",
			"type":      "service",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
		if result["action"] != "created" {
			t.Errorf("expected 'created', got %v", result["action"])
		}
	})

	t.Run("applies secret manifest", func(t *testing.T) {
		manifest := `apiVersion: v1
kind: Secret
metadata:
  name: apply-secret
stringData:
  password: secret123
`
		writeTestManifest(t, mgr, nsName, "apply-secret", "secret", manifest)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "apply-secret",
			"type":      "secret",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})

	t.Run("applies ingress manifest", func(t *testing.T) {
		manifest := `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: apply-ingress
spec:
  rules:
  - host: example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: my-svc
            port:
              number: 80
`
		writeTestManifest(t, mgr, nsName, "apply-ingress", "ingress", manifest)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "apply-ingress",
			"type":      "ingress",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})

	t.Run("type alias normalization", func(t *testing.T) {
		manifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: alias-cm
data:
  key: value
`
		writeTestManifest(t, mgr, nsName, "alias-cm", "configmap", manifest)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "alias-cm",
			"type":      "cm", // alias
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})
}

// TestGetResourceToolPodAndIngress tests pod and ingress paths of get_resource.
func TestGetResourceToolPodAndIngress(t *testing.T) {
	tool := NewGetResourceTool(clientset, dynamicClient)

	nsName := "test-get-pod-ingress"
	createTestNamespace(t, clientset, nsName)

	t.Run("gets pod", func(t *testing.T) {
		createTestPod(t, clientset, nsName, "get-test-pod", nil)

		result, err := tool.Run(nil, map[string]any{
			"kind":      "pod",
			"name":      "get-test-pod",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["error"]; ok {
			t.Fatalf("tool returned error: %v", result["error"])
		}
		resource := result["resource"].(map[string]any)
		metadata := resource["metadata"].(map[string]any)
		if metadata["name"] != "get-test-pod" {
			t.Errorf("expected name get-test-pod, got %v", metadata["name"])
		}
	})

	t.Run("gets ingress", func(t *testing.T) {
		// Create an ingress directly via apply_resource
		applyTool := NewApplyResourceTool(dynamicClient, nil)
		ingressYAML := `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: get-test-ingress
  namespace: ` + nsName + `
spec:
  rules:
  - host: test.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: test-svc
            port:
              number: 80
`
		_, err := applyTool.Run(nil, map[string]any{"yaml": ingressYAML})
		if err != nil {
			t.Fatalf("failed to create ingress: %v", err)
		}
		t.Cleanup(func() {
			_ = dynamicClient.Resource(CommonGVRs["ingress"]).Namespace(nsName).Delete(t.Context(), "get-test-ingress", metav1.DeleteOptions{})
		})

		result, err := tool.Run(nil, map[string]any{
			"kind":      "ingress",
			"name":      "get-test-ingress",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result["error"]; ok {
			t.Fatalf("tool returned error: %v", result["error"])
		}
	})
}

// TestListHelmReleasesTool tests the list_helm_releases tool.
func TestListHelmReleasesTool(t *testing.T) {
	tool := NewListHelmReleasesTool(clientset)

	t.Run("lists releases (empty cluster)", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["count"] != 0 {
			t.Errorf("expected 0 releases in empty cluster, got %v", result["count"])
		}
		if result["scope"] != "all namespaces" {
			t.Errorf("expected 'all namespaces' scope, got %v", result["scope"])
		}
	})

	t.Run("lists with namespace filter", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"namespace": "default",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["namespace"] != "default" {
			t.Errorf("expected namespace 'default', got %v", result["namespace"])
		}
	})
}

// TestGetHelmReleaseTool tests the get_helm_release tool validation.
func TestGetHelmReleaseTool(t *testing.T) {
	tool := NewGetHelmReleaseTool(clientset)

	t.Run("validates name required", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"namespace": "default",
		})
		if result["error"] != "name is required" {
			t.Errorf("expected 'name is required', got: %v", result["error"])
		}
	})

	t.Run("validates namespace required", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"name": "my-release",
		})
		if result["error"] != "namespace is required" {
			t.Errorf("expected 'namespace is required', got: %v", result["error"])
		}
	})

}

// TestGetHelmValuesTool tests the get_helm_values tool validation.
func TestGetHelmValuesTool(t *testing.T) {
	tool := NewGetHelmValuesTool(clientset)

	t.Run("validates name required", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"namespace": "default",
		})
		if result["error"] != "name is required" {
			t.Errorf("expected 'name is required', got: %v", result["error"])
		}
	})

	t.Run("validates namespace required", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"name": "my-release",
		})
		if result["error"] != "namespace is required" {
			t.Errorf("expected 'namespace is required', got: %v", result["error"])
		}
	})
}

// TestFormatDriftScanResults tests the FormatDriftScanResults function.
func TestFormatDriftScanResults(t *testing.T) {
	t.Run("empty results", func(t *testing.T) {
		result := FormatDriftScanResults(&DriftScanResults{Total: 0}, 80)
		if result != "" {
			t.Errorf("expected empty string for 0 total, got %q", result)
		}
	})

	t.Run("all in sync", func(t *testing.T) {
		result := FormatDriftScanResults(&DriftScanResults{
			Total:  3,
			InSync: 3,
		}, 80)
		if !containsSubstring(result, "all in sync") {
			t.Errorf("expected 'all in sync', got %q", result)
		}
	})

	t.Run("mixed results", func(t *testing.T) {
		result := FormatDriftScanResults(&DriftScanResults{
			Total:   4,
			InSync:  1,
			Drifted: 1,
			Missing: 1,
			Errors:  1,
			Results: []DriftResult{
				{Namespace: "default", Name: "app1", Kind: "deployment", Status: "in_sync"},
				{Namespace: "default", Name: "app2", Kind: "deployment", Status: "drifted", Diffs: []DiffEntry{{Path: "spec.replicas"}}},
				{Namespace: "default", Name: "app3", Kind: "service", Status: "missing"},
				{Namespace: "default", Name: "app4", Kind: "configmap", Status: "error", Error: "timeout"},
			},
		}, 80)
		if !containsSubstring(result, "OK") {
			t.Error("expected OK for in_sync resource")
		}
		if !containsSubstring(result, "DRIFTED") {
			t.Error("expected DRIFTED for drifted resource")
		}
		if !containsSubstring(result, "NOT IN CLUSTER") {
			t.Error("expected NOT IN CLUSTER for missing resource")
		}
		if !containsSubstring(result, "ERROR") {
			t.Error("expected ERROR for error resource")
		}
	})
}

func TestEllipsize(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"hello world", 10, "hell…world"},
		{"abcdefghijk", 5, "ab…jk"},
		{"ab", 1, "…"},
		{"", 10, ""},
		{"anything", 0, "anything"},
	}
	for _, tt := range tests {
		got := ellipsize(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("ellipsize(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

// TestFormatDriftContext tests the FormatDriftContext function.
func TestFormatDriftContext(t *testing.T) {
	t.Run("nil results", func(t *testing.T) {
		result := FormatDriftContext(nil)
		if result != "" {
			t.Errorf("expected empty string for nil, got %q", result)
		}
	})

	t.Run("empty results", func(t *testing.T) {
		result := FormatDriftContext(&DriftScanResults{Total: 0})
		if result != "" {
			t.Errorf("expected empty string for 0 total, got %q", result)
		}
	})

	t.Run("all in sync", func(t *testing.T) {
		result := FormatDriftContext(&DriftScanResults{
			Total:  2,
			InSync: 2,
		})
		if !containsSubstring(result, "all in sync") {
			t.Errorf("expected 'all in sync', got %q", result)
		}
	})

	t.Run("mixed results", func(t *testing.T) {
		result := FormatDriftContext(&DriftScanResults{
			Total:   3,
			InSync:  1,
			Drifted: 1,
			Missing: 1,
			Results: []DriftResult{
				{Namespace: "ns", Name: "a", Kind: "deployment", Status: "in_sync"},
				{Namespace: "ns", Name: "b", Kind: "service", Status: "drifted", Diffs: []DiffEntry{{Path: "p"}, {Path: "q"}}},
				{Namespace: "ns", Name: "c", Kind: "configmap", Status: "missing"},
			},
		})
		if !containsSubstring(result, "in sync") {
			t.Error("expected 'in sync'")
		}
		if !containsSubstring(result, "drifted") {
			t.Error("expected 'drifted'")
		}
		if !containsSubstring(result, "not found in cluster") {
			t.Error("expected 'not found in cluster'")
		}
		if !containsSubstring(result, "diff_resource") {
			t.Error("expected diff_resource hint")
		}
	})
}

// TestFetchAndCleanLiveResource tests FetchAndCleanLiveResource.
func TestFetchAndCleanLiveResource(t *testing.T) {
	nsName := "test-fetch-clean"
	createTestNamespace(t, clientset, nsName)
	createTestDeployment(t, clientset, nsName, "fc-deploy")

	t.Run("fetches and cleans deployment", func(t *testing.T) {
		result, err := FetchAndCleanLiveResource(t.Context(), dynamicClient, nsName, "fc-deploy", "deployment", "apps/v1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		metadata := result["metadata"].(map[string]any)
		// Should have been cleaned
		if _, ok := metadata["managedFields"]; ok {
			t.Error("expected managedFields to be removed")
		}
		if _, ok := metadata["uid"]; ok {
			t.Error("expected uid to be removed")
		}
		// status should be removed
		if _, ok := result["status"]; ok {
			t.Error("expected status to be removed")
		}
	})

	t.Run("returns error for unknown kind", func(t *testing.T) {
		_, err := FetchAndCleanLiveResource(t.Context(), dynamicClient, nsName, "test", "unknownthing", "")
		if err == nil {
			t.Error("expected error for unknown kind")
		}
	})

	t.Run("returns error for non-existent resource", func(t *testing.T) {
		_, err := FetchAndCleanLiveResource(t.Context(), dynamicClient, nsName, "nonexistent", "deployment", "apps/v1")
		if err == nil {
			t.Error("expected error for non-existent resource")
		}
	})
}

// TestCompareManifest tests the CompareManifest function.
func TestCompareManifest(t *testing.T) {
	nsName := "test-compare-manifest"
	createTestNamespace(t, clientset, nsName)

	t.Run("detects missing resource", func(t *testing.T) {
		yaml := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: compare-missing
spec:
  replicas: 1
`
		result := CompareManifest(t.Context(), dynamicClient, nsName, "compare-missing", "deployment", []byte(yaml))
		if result.Status != "missing" {
			t.Errorf("expected status 'missing', got %q", result.Status)
		}
	})

	t.Run("handles invalid YAML", func(t *testing.T) {
		result := CompareManifest(t.Context(), dynamicClient, nsName, "test", "deployment", []byte("{{{bad"))
		if result.Status != "error" {
			t.Errorf("expected status 'error', got %q", result.Status)
		}
	})
}

// containsSubstring checks if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
