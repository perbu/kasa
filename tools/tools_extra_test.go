package tools

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestIsMutating tests the IsMutating function.
func TestIsMutating(t *testing.T) {
	mgr := newTestManifestManager(t)

	t.Run("mutating tools return true", func(t *testing.T) {
		tool := NewCreateDeploymentTool(clientset, mgr)
		if !IsMutating(tool) {
			t.Error("expected create_deployment to be mutating")
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
	kt := NewKubeTools(clientset, dynamicClient, mgr, "", "")

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
	kt := NewKubeTools(clientset, dynamicClient, mgr, "", "")

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
	kt := NewKubeTools(clientset, dynamicClient, mgr, "", "")

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
	if !containsSubstring(docs, "create_deployment") {
		t.Error("expected create_deployment in docs")
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
					"tool":       "create_deployment",
					"parameters": map[string]any{"name": "nginx", "image": "nginx:1.25"},
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
		if result["error"] != "action missing tool name" {
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
		if result["error"] != "action missing reason" {
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
		if result["error"] != "question missing text" {
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
		if result["error"] != "invalid question format" {
			t.Errorf("unexpected error: %v", result["error"])
		}
	})
}

// TestCreateNamespaceTool tests the create_namespace tool.
func TestCreateNamespaceTool(t *testing.T) {
	tool := NewCreateNamespaceTool(clientset)

	t.Run("creates namespace", func(t *testing.T) {
		nsName := "test-create-ns-new"
		t.Cleanup(func() {
			_ = clientset.CoreV1().Namespaces().Delete(t.Context(), nsName, metav1.DeleteOptions{})
		})

		result, err := tool.Run(nil, map[string]any{
			"name": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}

		// Verify namespace exists
		ns, err := clientset.CoreV1().Namespaces().Get(t.Context(), nsName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("namespace not found: %v", err)
		}
		if ns.Labels["app.kubernetes.io/managed-by"] != "kasa" {
			t.Error("expected managed-by label")
		}
	})

	t.Run("creates namespace with labels", func(t *testing.T) {
		nsName := "test-create-ns-labels"
		t.Cleanup(func() {
			_ = clientset.CoreV1().Namespaces().Delete(t.Context(), nsName, metav1.DeleteOptions{})
		})

		result, err := tool.Run(nil, map[string]any{
			"name": nsName,
			"labels": map[string]any{
				"env": "test",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}

		ns, _ := clientset.CoreV1().Namespaces().Get(t.Context(), nsName, metav1.GetOptions{})
		if ns.Labels["env"] != "test" {
			t.Error("expected env=test label")
		}
	})

	t.Run("detects existing namespace", func(t *testing.T) {
		nsName := "test-create-ns-exists"
		createTestNamespace(t, clientset, nsName)

		result, err := tool.Run(nil, map[string]any{
			"name": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["exists"] != true {
			t.Errorf("expected exists=true, got: %v", result)
		}
	})

	t.Run("validates required name", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{})
		if result["error"] != "name is required" {
			t.Errorf("expected 'name is required' error, got: %v", result["error"])
		}
	})

	t.Run("invalid args type", func(t *testing.T) {
		result, _ := tool.Run(nil, 42)
		if result["error"] != "invalid arguments type" {
			t.Errorf("expected 'invalid arguments type', got: %v", result["error"])
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

// TestCreateConfigMapTool tests the create_configmap tool.
func TestCreateConfigMapTool(t *testing.T) {
	nsName := "test-create-cm"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewCreateConfigMapTool(clientset, mgr)

	t.Run("creates configmap", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":      "my-config",
			"namespace": nsName,
			"data": map[string]any{
				"key1": "value1",
				"key2": "value2",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["action"] != "created" {
			t.Errorf("expected action 'created', got %v", result["action"])
		}
		if result["keys"] != 2 {
			t.Errorf("expected 2 keys, got %v", result["keys"])
		}

		// Verify in cluster
		cm, err := clientset.CoreV1().ConfigMaps(nsName).Get(t.Context(), "my-config", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("configmap not found: %v", err)
		}
		if cm.Data["key1"] != "value1" {
			t.Errorf("expected key1=value1, got %s", cm.Data["key1"])
		}
	})

	t.Run("updates existing configmap", func(t *testing.T) {
		// First create
		_, _ = tool.Run(nil, map[string]any{
			"name":      "update-cm",
			"namespace": nsName,
			"data":      map[string]any{"old": "value"},
		})

		// Then update
		result, err := tool.Run(nil, map[string]any{
			"name":      "update-cm",
			"namespace": nsName,
			"data":      map[string]any{"new": "value"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["action"] != "updated" {
			t.Errorf("expected action 'updated', got %v", result["action"])
		}
	})

	t.Run("handles non-string data values", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":      "json-cm",
			"namespace": nsName,
			"data": map[string]any{
				"count": float64(42),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["action"] != "created" {
			t.Errorf("expected created, got %v", result["action"])
		}
	})

	t.Run("validates required parameters", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"data":      map[string]any{"k": "v"},
		})
		if result["error"] != "name is required" {
			t.Errorf("expected name required error, got: %v", result["error"])
		}

		result, _ = tool.Run(nil, map[string]any{
			"name": "test",
			"data": map[string]any{"k": "v"},
		})
		if result["error"] != "namespace is required" {
			t.Errorf("expected namespace required error, got: %v", result["error"])
		}

		result, _ = tool.Run(nil, map[string]any{
			"name":      "test",
			"namespace": nsName,
		})
		if result["error"] != "data is required" {
			t.Errorf("expected data required error, got: %v", result["error"])
		}
	})
}

// TestCreateSecretTool tests the create_secret tool.
func TestCreateSecretTool(t *testing.T) {
	nsName := "test-create-secret"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewCreateSecretTool(clientset, mgr)

	t.Run("creates secret", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":      "my-secret",
			"namespace": nsName,
			"string_data": map[string]any{
				"password": "secret123",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["action"] != "created" {
			t.Errorf("expected action 'created', got %v", result["action"])
		}
		if result["type"] != "Opaque" {
			t.Errorf("expected type Opaque, got %v", result["type"])
		}
	})

	t.Run("creates with custom type", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":      "tls-secret",
			"namespace": nsName,
			"type":      "kubernetes.io/tls",
			"string_data": map[string]any{
				"tls.crt": "cert-data",
				"tls.key": "key-data",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["type"] != "kubernetes.io/tls" {
			t.Errorf("expected type kubernetes.io/tls, got %v", result["type"])
		}
	})

	t.Run("updates existing secret", func(t *testing.T) {
		_, _ = tool.Run(nil, map[string]any{
			"name":        "upd-secret",
			"namespace":   nsName,
			"string_data": map[string]any{"old": "val"},
		})
		result, err := tool.Run(nil, map[string]any{
			"name":        "upd-secret",
			"namespace":   nsName,
			"string_data": map[string]any{"new": "val"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["action"] != "updated" {
			t.Errorf("expected 'updated', got %v", result["action"])
		}
	})

	t.Run("validates required parameters", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"namespace":   nsName,
			"string_data": map[string]any{"k": "v"},
		})
		if result["error"] != "name is required" {
			t.Errorf("expected name required, got: %v", result["error"])
		}

		result, _ = tool.Run(nil, map[string]any{
			"name":        "test",
			"string_data": map[string]any{"k": "v"},
		})
		if result["error"] != "namespace is required" {
			t.Errorf("expected namespace required, got: %v", result["error"])
		}

		result, _ = tool.Run(nil, map[string]any{
			"name":      "test",
			"namespace": nsName,
		})
		if result["error"] != "string_data is required" {
			t.Errorf("expected string_data required, got: %v", result["error"])
		}
	})
}

// TestCreateIngressTool tests the create_ingress tool.
func TestCreateIngressTool(t *testing.T) {
	nsName := "test-create-ingress"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewCreateIngressTool(clientset, mgr)

	t.Run("creates basic ingress", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":         "my-ingress",
			"namespace":    nsName,
			"host":         "example.com",
			"service_name": "my-svc",
			"service_port": float64(80),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["action"] != "created" {
			t.Errorf("expected 'created', got %v", result["action"])
		}
		if result["host"] != "example.com" {
			t.Errorf("expected host example.com, got %v", result["host"])
		}
		if result["path"] != "/" {
			t.Errorf("expected default path /, got %v", result["path"])
		}
	})

	t.Run("creates ingress with TLS", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":         "tls-ingress",
			"namespace":    nsName,
			"host":         "secure.example.com",
			"service_name": "my-svc",
			"service_port": float64(443),
			"tls_secret":   "my-tls-secret",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["tls_enabled"] != true {
			t.Error("expected tls_enabled=true")
		}
		if result["tls_secret"] != "my-tls-secret" {
			t.Errorf("expected tls_secret 'my-tls-secret', got %v", result["tls_secret"])
		}
	})

	t.Run("creates ingress with custom path and class", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":          "custom-ingress",
			"namespace":     nsName,
			"host":          "api.example.com",
			"service_name":  "api-svc",
			"service_port":  float64(8080),
			"path":          "/api",
			"ingress_class": "nginx",
			"annotations": map[string]any{
				"nginx.ingress.kubernetes.io/rewrite-target": "/",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["path"] != "/api" {
			t.Errorf("expected path /api, got %v", result["path"])
		}
	})

	t.Run("validates required parameters", func(t *testing.T) {
		// Missing name
		result, _ := tool.Run(nil, map[string]any{
			"namespace": nsName, "host": "h", "service_name": "s", "service_port": float64(80),
		})
		if result["error"] != "name is required" {
			t.Errorf("expected name required, got: %v", result["error"])
		}

		// Missing namespace
		result, _ = tool.Run(nil, map[string]any{
			"name": "t", "host": "h", "service_name": "s", "service_port": float64(80),
		})
		if result["error"] != "namespace is required" {
			t.Errorf("expected namespace required, got: %v", result["error"])
		}

		// Missing host
		result, _ = tool.Run(nil, map[string]any{
			"name": "t", "namespace": nsName, "service_name": "s", "service_port": float64(80),
		})
		if result["error"] != "host is required" {
			t.Errorf("expected host required, got: %v", result["error"])
		}

		// Missing service_name
		result, _ = tool.Run(nil, map[string]any{
			"name": "t", "namespace": nsName, "host": "h", "service_port": float64(80),
		})
		if result["error"] != "service_name is required" {
			t.Errorf("expected service_name required, got: %v", result["error"])
		}

		// Missing service_port
		result, _ = tool.Run(nil, map[string]any{
			"name": "t", "namespace": nsName, "host": "h", "service_name": "s",
		})
		if result["error"] != "service_port is required" {
			t.Errorf("expected service_port required, got: %v", result["error"])
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
		if result["error"] != "invalid arguments" {
			t.Errorf("expected 'invalid arguments', got: %v", result["error"])
		}
	})
}

// TestApplyManifestTool tests the apply_manifest tool.
func TestApplyManifestTool(t *testing.T) {
	nsName := "test-apply-manifest"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewApplyManifestTool(clientset, mgr)

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
