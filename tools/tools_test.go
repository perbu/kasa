package tools

import (
	"os"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	testEnv       *envtest.Environment
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
)

func TestMain(m *testing.M) {
	// Check if we should skip envtest
	if os.Getenv("SKIP_ENVTEST") != "" {
		os.Exit(0)
	}

	testEnv = &envtest.Environment{}

	cfg, err := testEnv.Start()
	if err != nil {
		// If envtest fails to start, print helpful message and exit
		println("Failed to start envtest environment:", err.Error())
		println("To install envtest binaries, run:")
		println("  go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use --bin-dir /usr/local/kubebuilder/bin")
		println("Or set SKIP_ENVTEST=1 to skip these tests")
		os.Exit(1)
	}

	clientset, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		println("Failed to create clientset:", err.Error())
		testEnv.Stop()
		os.Exit(1)
	}

	dynamicClient, err = dynamic.NewForConfig(cfg)
	if err != nil {
		println("Failed to create dynamic client:", err.Error())
		testEnv.Stop()
		os.Exit(1)
	}

	code := m.Run()

	if err := testEnv.Stop(); err != nil {
		println("Failed to stop envtest:", err.Error())
	}

	os.Exit(code)
}

// TestListNamespacesTool tests the list_namespaces tool.
func TestListNamespacesTool(t *testing.T) {
	tool := NewListNamespacesTool(clientset)

	t.Run("lists default namespaces", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		count, ok := result["count"].(int)
		if !ok {
			t.Fatal("expected count in result")
		}

		// envtest creates default, kube-system, etc.
		if count < 1 {
			t.Errorf("expected at least 1 namespace, got %d", count)
		}
	})

	t.Run("includes created namespace", func(t *testing.T) {
		nsName := "test-list-ns-abc123"
		createTestNamespace(t, clientset, nsName)

		result, err := tool.Run(nil, map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		namespaces, ok := result["namespaces"].([]NamespaceInfo)
		if !ok {
			t.Fatal("expected namespaces in result")
		}

		found := false
		for _, ns := range namespaces {
			if ns.Name == nsName {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("expected to find namespace %s in list", nsName)
		}
	})
}

// TestListPodsTool tests the list_pods tool.
func TestListPodsTool(t *testing.T) {
	tool := NewListPodsTool(clientset)

	t.Run("lists pods in namespace", func(t *testing.T) {
		nsName := "test-pods-ns"
		createTestNamespace(t, clientset, nsName)
		createTestPod(t, clientset, nsName, "test-pod-1", nil)
		createTestPod(t, clientset, nsName, "test-pod-2", nil)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		count, ok := result["count"].(int)
		if !ok {
			t.Fatal("expected count in result")
		}

		if count != 2 {
			t.Errorf("expected 2 pods, got %d", count)
		}
	})

	t.Run("filters by label selector", func(t *testing.T) {
		nsName := "test-pods-filter"
		createTestNamespace(t, clientset, nsName)

		createTestPod(t, clientset, nsName, "app-a", map[string]string{"app": "a"})
		createTestPod(t, clientset, nsName, "app-b", map[string]string{"app": "b"})

		result, err := tool.Run(nil, map[string]any{
			"namespace":      nsName,
			"label_selector": "app=a",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		count := result["count"].(int)
		if count != 1 {
			t.Errorf("expected 1 pod with label app=a, got %d", count)
		}
	})

	t.Run("returns empty for non-existent namespace", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"namespace": "non-existent-ns-xyz",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		count := result["count"].(int)
		if count != 0 {
			t.Errorf("expected 0 pods, got %d", count)
		}
	})
}

// TestGetResourceTool tests the get_resource tool.
func TestGetResourceTool(t *testing.T) {
	tool := NewGetResourceTool(clientset, dynamicClient)

	nsName := "test-get-resource"
	createTestNamespace(t, clientset, nsName)

	t.Run("gets deployment", func(t *testing.T) {
		createTestDeployment(t, clientset, nsName, "my-deploy")

		result, err := tool.Run(nil, map[string]any{
			"kind":      "deployment",
			"name":      "my-deploy",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		resource, ok := result["resource"].(map[string]any)
		if !ok {
			t.Fatal("expected resource in result")
		}

		metadata := resource["metadata"].(map[string]any)
		if metadata["name"] != "my-deploy" {
			t.Errorf("expected name my-deploy, got %v", metadata["name"])
		}
	})

	t.Run("gets service", func(t *testing.T) {
		createTestService(t, clientset, nsName, "my-svc")

		result, err := tool.Run(nil, map[string]any{
			"kind":      "service",
			"name":      "my-svc",
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
		if metadata["name"] != "my-svc" {
			t.Errorf("expected name my-svc, got %v", metadata["name"])
		}
	})

	t.Run("gets configmap", func(t *testing.T) {
		createTestConfigMap(t, clientset, nsName, "my-config", map[string]string{"key": "value"})

		result, err := tool.Run(nil, map[string]any{
			"kind":      "configmap",
			"name":      "my-config",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := result["error"]; ok {
			t.Fatalf("tool returned error: %v", result["error"])
		}

		resource := result["resource"].(map[string]any)
		data := resource["data"].(map[string]any)
		if data["key"] != "value" {
			t.Errorf("expected key=value, got %v", data["key"])
		}
	})

	t.Run("gets secret with redacted data", func(t *testing.T) {
		createTestSecret(t, clientset, nsName, "my-secret", map[string][]byte{"password": []byte("secret123")})

		result, err := tool.Run(nil, map[string]any{
			"kind":      "secret",
			"name":      "my-secret",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := result["error"]; ok {
			t.Fatalf("tool returned error: %v", result["error"])
		}

		resource := result["resource"].(map[string]any)
		data := resource["data"].(map[string]any)
		if data["password"] != "[REDACTED]" {
			t.Errorf("expected redacted data, got %v", data["password"])
		}
	})

	t.Run("returns error for non-existent resource", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"kind":      "deployment",
			"name":      "non-existent",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := result["error"]; !ok {
			t.Error("expected error for non-existent resource")
		}
	})

	t.Run("returns error for unsupported kind", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"kind":      "unsupportedkind",
			"name":      "test",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		errMsg, ok := result["error"].(string)
		if !ok {
			t.Error("expected error for unsupported kind")
		}
		if errMsg == "" {
			t.Error("expected non-empty error message")
		}
	})
}

// TestGetEventsTool tests the get_events tool.
func TestGetEventsTool(t *testing.T) {
	tool := NewGetEventsTool(clientset)

	nsName := "test-events-ns"
	createTestNamespace(t, clientset, nsName)

	t.Run("lists events in namespace", func(t *testing.T) {
		// Events are generated by K8s controllers; envtest may not generate them automatically.
		// We test that the tool runs without error.
		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		// Events may be empty, but count should be present
		if _, ok := result["count"]; !ok {
			t.Error("expected count in result")
		}
	})

	t.Run("requires namespace", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		errMsg, ok := result["error"].(string)
		if !ok || errMsg != "namespace is required" {
			t.Errorf("expected 'namespace is required' error, got: %v", result["error"])
		}
	})
}

// TestCheckDeploymentHealthTool tests the check_deployment_health tool.
func TestCheckDeploymentHealthTool(t *testing.T) {
	tool := NewCheckDeploymentHealthTool(clientset)

	nsName := "test-health-ns"
	createTestNamespace(t, clientset, nsName)

	t.Run("checks deployment health", func(t *testing.T) {
		createTestDeployment(t, clientset, nsName, "healthy-app")

		result, err := tool.Run(nil, map[string]any{
			"name":      "healthy-app",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		// In envtest, pods don't actually run, so readyReplicas will be 0
		if _, ok := result["replicas"]; !ok {
			t.Error("expected replicas in result")
		}

		if _, ok := result["ready_replicas"]; !ok {
			t.Error("expected ready_replicas in result")
		}

		if _, ok := result["healthy"]; !ok {
			t.Error("expected healthy in result")
		}
	})

	t.Run("returns error for non-existent deployment", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":      "non-existent",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := result["error"]; !ok {
			t.Error("expected error for non-existent deployment")
		}
	})
}

// TestCreateDeploymentTool tests the create_deployment tool.
func TestCreateDeploymentTool(t *testing.T) {
	nsName := "test-create-deploy"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewCreateDeploymentTool(clientset, mgr)

	t.Run("creates new deployment", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":      "new-nginx",
			"namespace": nsName,
			"image":     "nginx:1.25",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		if result["action"] != "created" {
			t.Errorf("expected action 'created', got %v", result["action"])
		}

		// Verify in cluster
		deploy, err := clientset.AppsV1().Deployments(nsName).Get(t.Context(), "new-nginx", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("failed to get deployment: %v", err)
		}

		if deploy.Spec.Template.Spec.Containers[0].Image != "nginx:1.25" {
			t.Errorf("expected image nginx:1.25, got %s", deploy.Spec.Template.Spec.Containers[0].Image)
		}

		// Verify manifest was saved
		content, err := mgr.ReadManifest(nsName, "new-nginx", "deployment")
		if err != nil {
			t.Fatalf("failed to read manifest: %v", err)
		}
		if len(content) == 0 {
			t.Error("expected non-empty manifest content")
		}
	})

	t.Run("updates existing deployment", func(t *testing.T) {
		// First create
		_, err := tool.Run(nil, map[string]any{
			"name":      "update-nginx",
			"namespace": nsName,
			"image":     "nginx:1.24",
		})
		if err != nil {
			t.Fatalf("unexpected error on create: %v", err)
		}

		// Then update
		result, err := tool.Run(nil, map[string]any{
			"name":      "update-nginx",
			"namespace": nsName,
			"image":     "nginx:1.25",
		})
		if err != nil {
			t.Fatalf("unexpected error on update: %v", err)
		}

		if result["action"] != "updated" {
			t.Errorf("expected action 'updated', got %v", result["action"])
		}

		// Verify image was updated
		deploy, _ := clientset.AppsV1().Deployments(nsName).Get(t.Context(), "update-nginx", metav1.GetOptions{})
		if deploy.Spec.Template.Spec.Containers[0].Image != "nginx:1.25" {
			t.Errorf("expected image nginx:1.25, got %s", deploy.Spec.Template.Spec.Containers[0].Image)
		}
	})

	t.Run("creates with optional parameters", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":        "full-nginx",
			"namespace":   nsName,
			"image":       "nginx:1.25",
			"replicas":    float64(3),
			"port":        float64(80),
			"health_path": "/health",
			"env": map[string]any{
				"ENV_VAR": "value",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		deploy, _ := clientset.AppsV1().Deployments(nsName).Get(t.Context(), "full-nginx", metav1.GetOptions{})

		if *deploy.Spec.Replicas != 3 {
			t.Errorf("expected 3 replicas, got %d", *deploy.Spec.Replicas)
		}

		if len(deploy.Spec.Template.Spec.Containers[0].Ports) != 1 {
			t.Error("expected 1 port")
		}

		if deploy.Spec.Template.Spec.Containers[0].LivenessProbe == nil {
			t.Error("expected liveness probe")
		}
	})

	t.Run("validates required parameters", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"image":     "nginx:1.25",
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error when name is missing")
		}

		result, _ = tool.Run(nil, map[string]any{
			"name":  "test",
			"image": "nginx:1.25",
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error when namespace is missing")
		}

		result, _ = tool.Run(nil, map[string]any{
			"name":      "test",
			"namespace": nsName,
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error when image is missing")
		}
	})
}

// TestCreateServiceTool tests the create_service tool.
func TestCreateServiceTool(t *testing.T) {
	nsName := "test-create-svc"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewCreateServiceTool(clientset, mgr)

	t.Run("creates ClusterIP service", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":      "my-svc",
			"namespace": nsName,
			"selector": map[string]any{
				"app.kubernetes.io/name": "myapp",
			},
			"port": float64(80),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		if result["action"] != "created" {
			t.Errorf("expected action 'created', got %v", result["action"])
		}

		// Verify in cluster
		svc, err := clientset.CoreV1().Services(nsName).Get(t.Context(), "my-svc", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("failed to get service: %v", err)
		}

		if svc.Spec.Type != corev1.ServiceTypeClusterIP {
			t.Errorf("expected ClusterIP type, got %v", svc.Spec.Type)
		}
	})

	t.Run("creates NodePort service", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"name":      "nodeport-svc",
			"namespace": nsName,
			"selector": map[string]any{
				"app": "test",
			},
			"port": float64(80),
			"type": "NodePort",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		svc, _ := clientset.CoreV1().Services(nsName).Get(t.Context(), "nodeport-svc", metav1.GetOptions{})
		if svc.Spec.Type != corev1.ServiceTypeNodePort {
			t.Errorf("expected NodePort type, got %v", svc.Spec.Type)
		}
	})

	t.Run("validates required parameters", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"selector":  map[string]any{"app": "test"},
			"port":      float64(80),
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error when name is missing")
		}
	})
}

// TestManifestTools tests list_manifests, read_manifest, and commit_manifests tools.
func TestManifestTools(t *testing.T) {
	mgr := newTestManifestManager(t)

	t.Run("list_manifests", func(t *testing.T) {
		tool := NewListManifestsTool(mgr)

		// Initially empty
		result, err := tool.Run(nil, map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["count"] != 0 {
			t.Errorf("expected 0 manifests initially, got %v", result["count"])
		}

		// Add some manifests
		writeTestManifest(t, mgr, "default", "app1", "deployment", "apiVersion: apps/v1\nkind: Deployment")
		writeTestManifest(t, mgr, "default", "app1", "service", "apiVersion: v1\nkind: Service")
		writeTestManifest(t, mgr, "prod", "app2", "deployment", "apiVersion: apps/v1\nkind: Deployment")

		result, _ = tool.Run(nil, map[string]any{})
		if result["count"] != 3 {
			t.Errorf("expected 3 manifests, got %v", result["count"])
		}

		// Filter by namespace
		result, _ = tool.Run(nil, map[string]any{"namespace": "default"})
		if result["count"] != 2 {
			t.Errorf("expected 2 manifests in default namespace, got %v", result["count"])
		}

		// Filter by app
		result, _ = tool.Run(nil, map[string]any{"app": "app1"})
		if result["count"] != 2 {
			t.Errorf("expected 2 manifests for app1, got %v", result["count"])
		}

		// Filter by both
		result, _ = tool.Run(nil, map[string]any{"namespace": "default", "app": "app1"})
		if result["count"] != 2 {
			t.Errorf("expected 2 manifests for default/app1, got %v", result["count"])
		}
	})

	t.Run("read_manifest", func(t *testing.T) {
		readMgr := newTestManifestManager(t)
		tool := NewReadManifestTool(readMgr)

		writeTestManifest(t, readMgr, "default", "myapp", "deployment", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: myapp")

		result, err := tool.Run(nil, map[string]any{
			"namespace": "default",
			"app":       "myapp",
			"type":      "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		content := result["content"].(string)
		if content == "" {
			t.Error("expected non-empty content")
		}

		// Test non-existent manifest
		result, _ = tool.Run(nil, map[string]any{
			"namespace": "default",
			"app":       "non-existent",
			"type":      "deployment",
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error for non-existent manifest")
		}
	})

	t.Run("commit_manifests", func(t *testing.T) {
		commitMgr := newTestManifestManager(t)
		tool := NewCommitManifestsTool(commitMgr)

		// Commit without staged changes should fail
		result, err := tool.Run(nil, map[string]any{
			"message": "Test commit",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != false {
			t.Error("expected failure when no staged changes")
		}

		// Add and commit
		writeTestManifest(t, commitMgr, "default", "app", "deployment", "apiVersion: apps/v1\nkind: Deployment")

		result, err = tool.Run(nil, map[string]any{
			"message": "Add deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})
}

// TestDeleteManifestTool tests the delete_manifest tool.
func TestDeleteManifestTool(t *testing.T) {
	nsName := "test-delete-manifest"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewDeleteManifestTool(clientset, mgr)

	t.Run("deletes single manifest", func(t *testing.T) {
		// Create a deployment in cluster and manifest
		createTool := NewCreateDeploymentTool(clientset, mgr)
		_, err := createTool.Run(nil, map[string]any{
			"name":      "to-delete",
			"namespace": nsName,
			"image":     "nginx:1.25",
		})
		if err != nil {
			t.Fatalf("failed to create deployment: %v", err)
		}

		result, err := tool.Run(nil, map[string]any{
			"namespace":           nsName,
			"app":                 "to-delete",
			"type":                "deployment",
			"delete_from_cluster": true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}

		// Verify manifest is deleted
		_, err = mgr.ReadManifest(nsName, "to-delete", "deployment")
		if err == nil {
			t.Error("expected manifest to be deleted")
		}

		// The cluster deletion may be async (foreground deletion policy).
		// We check that no error was returned in the result for cluster delete.
		if clusterErrs, ok := result["cluster_errors"].([]string); ok && len(clusterErrs) > 0 {
			t.Errorf("unexpected cluster delete errors: %v", clusterErrs)
		}
	})

	t.Run("deletes manifest without cluster deletion", func(t *testing.T) {
		// Create deployment in cluster
		createTestDeployment(t, clientset, nsName, "manifest-only")

		// Create manifest
		writeTestManifest(t, mgr, nsName, "manifest-only", "deployment", "apiVersion: apps/v1\nkind: Deployment")

		result, err := tool.Run(nil, map[string]any{
			"namespace":           nsName,
			"app":                 "manifest-only",
			"type":                "deployment",
			"delete_from_cluster": false,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}

		// Verify manifest is deleted
		_, err = mgr.ReadManifest(nsName, "manifest-only", "deployment")
		if err == nil {
			t.Error("expected manifest to be deleted")
		}

		// Verify resource still exists in cluster
		_, err = clientset.AppsV1().Deployments(nsName).Get(t.Context(), "manifest-only", metav1.GetOptions{})
		if err != nil {
			t.Error("expected deployment to still exist in cluster")
		}
	})
}

// TestImportResourceTool tests the import_resource tool.
func TestImportResourceTool(t *testing.T) {
	nsName := "test-import"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewImportResourceTool(clientset, dynamicClient, mgr)

	t.Run("imports deployment", func(t *testing.T) {
		createTestDeployment(t, clientset, nsName, "existing-deploy")

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"name":      "existing-deploy",
			"kind":      "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if errMsg, ok := result["error"].(string); ok {
			t.Fatalf("tool returned error: %s", errMsg)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}

		// Verify manifest was created
		content, err := mgr.ReadManifest(nsName, "existing-deploy", "deployment")
		if err != nil {
			t.Fatalf("failed to read imported manifest: %v", err)
		}
		if len(content) == 0 {
			t.Error("expected non-empty manifest content")
		}
	})

	t.Run("imports service", func(t *testing.T) {
		createTestService(t, clientset, nsName, "existing-svc")

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"name":      "existing-svc",
			"kind":      "service",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})

	t.Run("refuses overwrite without flag", func(t *testing.T) {
		createTestDeployment(t, clientset, nsName, "dup-deploy")

		// First import
		_, _ = tool.Run(nil, map[string]any{
			"namespace": nsName,
			"name":      "dup-deploy",
			"kind":      "deployment",
		})

		// Second import should fail
		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"name":      "dup-deploy",
			"kind":      "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["exists"] != true {
			t.Error("expected 'exists' flag for duplicate")
		}
	})

	t.Run("allows overwrite with flag", func(t *testing.T) {
		createTestDeployment(t, clientset, nsName, "overwrite-deploy")

		// First import
		_, _ = tool.Run(nil, map[string]any{
			"namespace": nsName,
			"name":      "overwrite-deploy",
			"kind":      "deployment",
		})

		// Second import with overwrite
		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"name":      "overwrite-deploy",
			"kind":      "deployment",
			"overwrite": true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success with overwrite, got: %v", result)
		}
	})
}

// TestDryRunApplyTool tests the dry_run_apply tool.
func TestDryRunApplyTool(t *testing.T) {
	nsName := "test-dryrun"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewDryRunApplyTool(clientset, mgr)

	t.Run("validates correct manifest", func(t *testing.T) {
		// Create a valid deployment manifest
		validManifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: valid-deploy
spec:
  replicas: 1
  selector:
    matchLabels:
      app: valid
  template:
    metadata:
      labels:
        app: valid
    spec:
      containers:
      - name: nginx
        image: nginx:1.25
`
		writeTestManifest(t, mgr, nsName, "valid-deploy", "deployment", validManifest)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "valid-deploy",
			"type":      "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["valid"] != true {
			t.Errorf("expected valid=true, got: %v", result)
		}
	})

	t.Run("rejects invalid manifest", func(t *testing.T) {
		// Create an invalid manifest (missing required fields)
		invalidManifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: invalid-deploy
spec:
  replicas: 1
`
		writeTestManifest(t, mgr, nsName, "invalid-deploy", "deployment", invalidManifest)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"app":       "invalid-deploy",
			"type":      "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["valid"] != false {
			t.Errorf("expected valid=false for invalid manifest, got: %v", result)
		}
	})
}

// TestGetReferenceTool tests the get_reference tool.
func TestGetReferenceTool(t *testing.T) {
	tool := NewGetReferenceTool()

	t.Run("lists available topics", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		topics, ok := result["available_topics"].([]map[string]string)
		if !ok {
			t.Fatal("expected available_topics in result")
		}

		if len(topics) == 0 {
			t.Error("expected at least one topic")
		}
	})

	t.Run("returns content for valid topic", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"topic": "deployment",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, ok := result["content"].(string)
		if !ok {
			t.Fatal("expected content in result")
		}

		if content == "" {
			t.Error("expected non-empty content")
		}
	})

	t.Run("returns error for invalid topic", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"topic": "invalid-topic-xyz",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := result["error"]; !ok {
			t.Error("expected error for invalid topic")
		}
	})
}

// TestDeleteResourceTool tests the delete_resource tool.
func TestDeleteResourceTool(t *testing.T) {
	nsName := "test-delete-resource"
	createTestNamespace(t, clientset, nsName)
	mgr := newTestManifestManager(t)

	tool := NewDeleteResourceTool(clientset, dynamicClient, mgr)

	t.Run("deletes deployment from cluster", func(t *testing.T) {
		// Create deployment directly (not using helper to avoid cleanup conflict)
		replicas := int32(1)
		labels := map[string]string{"app": "deploy-to-delete"}
		deploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "deploy-to-delete",
				Namespace: nsName,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:1.25"}}},
				},
			},
		}
		_, err := clientset.AppsV1().Deployments(nsName).Create(t.Context(), deploy, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("failed to create deployment: %v", err)
		}

		result, err := tool.Run(nil, map[string]any{
			"type":            "deployment",
			"name":            "deploy-to-delete",
			"namespace":       nsName,
			"delete_manifest": false,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}

		// The deletion uses foreground propagation which is async.
		// We verify the tool reported success - actual deletion happens async.
		if result["type"] != "deployment" {
			t.Errorf("expected type=deployment, got %v", result["type"])
		}
	})

	t.Run("deletes pod from cluster", func(t *testing.T) {
		createTestPod(t, clientset, nsName, "pod-to-delete", nil)

		result, err := tool.Run(nil, map[string]any{
			"type":      "pod",
			"name":      "pod-to-delete",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})

	t.Run("deletes service from cluster", func(t *testing.T) {
		createTestService(t, clientset, nsName, "svc-to-delete")

		result, err := tool.Run(nil, map[string]any{
			"type":      "service",
			"name":      "svc-to-delete",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})

	t.Run("deletes configmap from cluster", func(t *testing.T) {
		createTestConfigMap(t, clientset, nsName, "cm-to-delete", map[string]string{"key": "value"})

		result, err := tool.Run(nil, map[string]any{
			"type":      "configmap",
			"name":      "cm-to-delete",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})

	t.Run("deletes secret from cluster", func(t *testing.T) {
		createTestSecret(t, clientset, nsName, "secret-to-delete", map[string][]byte{"pass": []byte("secret")})

		result, err := tool.Run(nil, map[string]any{
			"type":      "secret",
			"name":      "secret-to-delete",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}
	})

	t.Run("also deletes manifest when requested", func(t *testing.T) {
		// Create deployment with manifest
		createTool := NewCreateDeploymentTool(clientset, mgr)
		_, err := createTool.Run(nil, map[string]any{
			"name":      "with-manifest",
			"namespace": nsName,
			"image":     "nginx:1.25",
		})
		if err != nil {
			t.Fatalf("failed to create deployment: %v", err)
		}

		// Verify manifest exists
		_, err = mgr.ReadManifest(nsName, "with-manifest", "deployment")
		if err != nil {
			t.Fatalf("expected manifest to exist: %v", err)
		}

		result, err := tool.Run(nil, map[string]any{
			"type":            "deployment",
			"name":            "with-manifest",
			"namespace":       nsName,
			"delete_manifest": true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success, got: %v", result)
		}

		// Verify manifest is also deleted
		_, err = mgr.ReadManifest(nsName, "with-manifest", "deployment")
		if err == nil {
			t.Error("expected manifest to be deleted")
		}
	})

	t.Run("handles type aliases", func(t *testing.T) {
		createTestDeployment(t, clientset, nsName, "alias-deploy")

		result, err := tool.Run(nil, map[string]any{
			"type":            "deploy", // alias
			"name":            "alias-deploy",
			"namespace":       nsName,
			"delete_manifest": false,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != true {
			t.Errorf("expected success with alias, got: %v", result)
		}
	})

	t.Run("returns error for non-existent resource", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"type":      "deployment",
			"name":      "non-existent",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result["success"] != false {
			t.Error("expected failure for non-existent resource")
		}

		if _, ok := result["error"]; !ok {
			t.Error("expected error message")
		}
	})

	t.Run("returns error for unsupported type", func(t *testing.T) {
		result, err := tool.Run(nil, map[string]any{
			"type":      "unsupported",
			"name":      "test",
			"namespace": nsName,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := result["error"]; !ok {
			t.Error("expected error for unsupported type")
		}
	})

	t.Run("validates required parameters", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"name":      "test",
			"namespace": nsName,
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error when type is missing")
		}

		result, _ = tool.Run(nil, map[string]any{
			"type":      "deployment",
			"namespace": nsName,
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error when name is missing")
		}

		result, _ = tool.Run(nil, map[string]any{
			"type": "deployment",
			"name": "test",
		})
		if _, ok := result["error"]; !ok {
			t.Error("expected error when namespace is missing")
		}
	})
}

// TestGetLogsTool tests the get_logs tool (parameter validation only).
func TestGetLogsTool(t *testing.T) {
	tool := NewGetLogsTool(clientset)

	t.Run("validates required parameters", func(t *testing.T) {
		result, _ := tool.Run(nil, map[string]any{
			"pod": "test-pod",
		})
		errMsg, ok := result["error"].(string)
		if !ok || errMsg != "namespace is required" {
			t.Errorf("expected 'namespace is required' error, got: %v", result["error"])
		}

		result, _ = tool.Run(nil, map[string]any{
			"namespace": "default",
		})
		errMsg, ok = result["error"].(string)
		if !ok || errMsg != "pod is required" {
			t.Errorf("expected 'pod is required' error, got: %v", result["error"])
		}
	})

	t.Run("returns error for non-existent pod", func(t *testing.T) {
		nsName := "test-logs"
		createTestNamespace(t, clientset, nsName)

		result, err := tool.Run(nil, map[string]any{
			"namespace": nsName,
			"pod":       "non-existent-pod",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should return an error about pod not found
		if _, ok := result["error"]; !ok {
			t.Error("expected error for non-existent pod")
		}
	})
}

// TestKubeToolsAll tests that All() returns all expected tools.
func TestKubeToolsAll(t *testing.T) {
	mgr := newTestManifestManager(t)
	kt := NewKubeTools(clientset, dynamicClient, mgr, "")

	tools := kt.All()

	expectedTools := []string{
		"list_namespaces",
		"create_namespace",
		"delete_namespace",
		"list_pods",
		"get_logs",
		"get_events",
		"get_resource",
		"get_reference",
		"create_deployment",
		"create_service",
		"create_configmap",
		"create_secret",
		"create_ingress",
		"check_deployment_health",
		"commit_manifests",
		"list_manifests",
		"read_manifest",
		"delete_manifest",
		"delete_resource",
		"import_resource",
		"apply_manifest",
		"dry_run_apply",
		"propose_plan",
		"apply_resource",
		"list_resources",
		"sleep",
		"fetch_url",
	}

	if len(tools) != len(expectedTools) {
		t.Errorf("expected %d tools, got %d", len(expectedTools), len(tools))
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name()] = true
	}

	for _, expected := range expectedTools {
		if !toolNames[expected] {
			t.Errorf("expected tool %s not found", expected)
		}
	}
}
