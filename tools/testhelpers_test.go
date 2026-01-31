package tools

import (
	"os"
	"os/exec"
	"testing"

	"github.com/perbu/kasa/manifest"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// createTestNamespace creates a namespace for testing and registers cleanup.
func createTestNamespace(t *testing.T, clientset *kubernetes.Clientset, name string) {
	t.Helper()

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	_, err := clientset.CoreV1().Namespaces().Create(t.Context(), ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create test namespace %s: %v", name, err)
	}

	t.Cleanup(func() {
		_ = clientset.CoreV1().Namespaces().Delete(t.Context(), name, metav1.DeleteOptions{})
	})
}

// createTestDeployment creates a deployment for testing.
func createTestDeployment(t *testing.T, clientset *kubernetes.Clientset, namespace, name string) *appsv1.Deployment {
	t.Helper()

	replicas := int32(1)
	labels := map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "kasa-test",
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  name,
							Image: "nginx:1.25",
						},
					},
				},
			},
		},
	}

	created, err := clientset.AppsV1().Deployments(namespace).Create(t.Context(), deployment, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create test deployment %s/%s: %v", namespace, name, err)
	}

	t.Cleanup(func() {
		_ = clientset.AppsV1().Deployments(namespace).Delete(t.Context(), name, metav1.DeleteOptions{})
	})

	return created
}

// createTestService creates a service for testing.
func createTestService(t *testing.T, clientset *kubernetes.Clientset, namespace, name string) *corev1.Service {
	t.Helper()

	labels := map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "kasa-test",
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Port: 80,
				},
			},
		},
	}

	created, err := clientset.CoreV1().Services(namespace).Create(t.Context(), service, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create test service %s/%s: %v", namespace, name, err)
	}

	t.Cleanup(func() {
		_ = clientset.CoreV1().Services(namespace).Delete(t.Context(), name, metav1.DeleteOptions{})
	})

	return created
}

// createTestConfigMap creates a configmap for testing.
func createTestConfigMap(t *testing.T, clientset *kubernetes.Clientset, namespace, name string, data map[string]string) *corev1.ConfigMap {
	t.Helper()

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}

	created, err := clientset.CoreV1().ConfigMaps(namespace).Create(t.Context(), configMap, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create test configmap %s/%s: %v", namespace, name, err)
	}

	t.Cleanup(func() {
		_ = clientset.CoreV1().ConfigMaps(namespace).Delete(t.Context(), name, metav1.DeleteOptions{})
	})

	return created
}

// createTestSecret creates a secret for testing.
func createTestSecret(t *testing.T, clientset *kubernetes.Clientset, namespace, name string, data map[string][]byte) *corev1.Secret {
	t.Helper()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}

	created, err := clientset.CoreV1().Secrets(namespace).Create(t.Context(), secret, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create test secret %s/%s: %v", namespace, name, err)
	}

	t.Cleanup(func() {
		_ = clientset.CoreV1().Secrets(namespace).Delete(t.Context(), name, metav1.DeleteOptions{})
	})

	return created
}

// createTestPod creates a pod for testing.
func createTestPod(t *testing.T, clientset *kubernetes.Clientset, namespace, name string, labels map[string]string) *corev1.Pod {
	t.Helper()

	if labels == nil {
		labels = map[string]string{
			"app.kubernetes.io/name": name,
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  name,
					Image: "nginx:1.25",
				},
			},
		},
	}

	created, err := clientset.CoreV1().Pods(namespace).Create(t.Context(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create test pod %s/%s: %v", namespace, name, err)
	}

	t.Cleanup(func() {
		_ = clientset.CoreV1().Pods(namespace).Delete(t.Context(), name, metav1.DeleteOptions{})
	})

	return created
}

// newTestManifestManager creates a manifest.Manager using t.TempDir() with git initialized.
func newTestManifestManager(t *testing.T) *manifest.Manager {
	t.Helper()

	tempDir := t.TempDir()

	mgr, err := manifest.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create manifest manager: %v", err)
	}

	// Initialize git
	if err := mgr.EnsureGitInit(); err != nil {
		t.Fatalf("failed to init git: %v", err)
	}

	// Configure git user for commits in the test directory
	configureGitUser(t, tempDir)

	return mgr
}

// configureGitUser sets up git user config in the test directory.
func configureGitUser(t *testing.T, dir string) {
	t.Helper()

	cmd := exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to configure git user email: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to configure git user name: %v", err)
	}
}

// writeTestManifest writes a manifest file directly to the test manager directory.
func writeTestManifest(t *testing.T, mgr *manifest.Manager, namespace, app, resourceType, content string) {
	t.Helper()

	_, err := mgr.SaveManifest(namespace, app, resourceType, []byte(content))
	if err != nil {
		t.Fatalf("failed to write test manifest: %v", err)
	}
}

// skipIfNoEnvtest skips the test if envtest binaries are not available.
func skipIfNoEnvtest(t *testing.T) {
	t.Helper()

	// Check for KUBEBUILDER_ASSETS environment variable
	if os.Getenv("KUBEBUILDER_ASSETS") != "" {
		return
	}

	// Check common locations
	paths := []string{
		"/usr/local/kubebuilder/bin",
		os.ExpandEnv("$HOME/.local/share/kubebuilder-envtest"),
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}

	t.Skip("envtest binaries not found; set KUBEBUILDER_ASSETS or run: go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use --bin-dir /usr/local/kubebuilder/bin")
}
