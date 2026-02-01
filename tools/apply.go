package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// ApplyManifestTool provides the apply_manifest tool for the agent.
type ApplyManifestTool struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewApplyManifestTool creates a new ApplyManifestTool.
func NewApplyManifestTool(clientset *kubernetes.Clientset, manifest *manifest.Manager) *ApplyManifestTool {
	return &ApplyManifestTool{
		clientset: clientset,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *ApplyManifestTool) Name() string {
	return "apply_manifest"
}

// Description returns the tool description.
func (t *ApplyManifestTool) Description() string {
	return "Apply a stored manifest to the Kubernetes cluster. Reads the manifest from storage and creates or updates the resource in the cluster."
}

// IsLongRunning returns false as this is a quick operation.
func (t *ApplyManifestTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *ApplyManifestTool) Category() ToolCategory {
	return CategoryMutating
}

// ProcessRequest adds this tool to the LLM request.
func (t *ApplyManifestTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ApplyManifestTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "The namespace of the manifest",
				},
				"app": {
					Type:        "string",
					Description: "The app name (manifest directory name)",
				},
				"type": {
					Type:        "string",
					Description: "The resource type: deployment, service, configmap, secret, ingress",
				},
				"dry_run": {
					Type:        "boolean",
					Description: "If true, validate without applying (default: false)",
				},
			},
			Required: []string{"namespace", "app", "type"},
		},
	}
}

// Run executes the tool.
func (t *ApplyManifestTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	// Parse arguments
	argsMap, ok := args.(map[string]any)
	if !ok {
		if argsStr, ok := args.(string); ok {
			if err := json.Unmarshal([]byte(argsStr), &argsMap); err != nil {
				return map[string]any{"error": "invalid arguments format"}, nil
			}
		} else {
			return map[string]any{"error": "invalid arguments type"}, nil
		}
	}

	namespace, ok := argsMap["namespace"].(string)
	if !ok || namespace == "" {
		return map[string]any{"error": "namespace is required"}, nil
	}

	app, ok := argsMap["app"].(string)
	if !ok || app == "" {
		return map[string]any{"error": "app is required"}, nil
	}

	resourceType, ok := argsMap["type"].(string)
	if !ok || resourceType == "" {
		return map[string]any{"error": "type is required"}, nil
	}

	dryRun := false
	if dr, ok := argsMap["dry_run"].(bool); ok {
		dryRun = dr
	}

	// Normalize resource type
	resourceType = normalizeKind(resourceType)
	if resourceType == "" {
		return map[string]any{
			"error": "unsupported resource type. Supported: deployment, service, configmap, secret, ingress",
		}, nil
	}

	// Read manifest from storage
	content, err := t.manifest.ReadManifest(namespace, app, resourceType)
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}

	// Apply to cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	action, err := t.applyResource(timeoutCtx, namespace, resourceType, content, dryRun)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, nil
	}

	result := map[string]any{
		"success":   true,
		"action":    action,
		"namespace": namespace,
		"app":       app,
		"type":      resourceType,
	}

	if dryRun {
		result["dry_run"] = true
		result["message"] = fmt.Sprintf("Dry run: %s/%s/%s would be %s", namespace, app, resourceType, action)
	} else {
		result["message"] = fmt.Sprintf("Successfully %s %s/%s in namespace %s", action, resourceType, app, namespace)
	}

	return result, nil
}

// applyResource applies a resource to the cluster.
func (t *ApplyManifestTool) applyResource(ctx context.Context, namespace, resourceType string, content []byte, dryRun bool) (string, error) {
	var createOpts metav1.CreateOptions
	var updateOpts metav1.UpdateOptions

	if dryRun {
		createOpts.DryRun = []string{metav1.DryRunAll}
		updateOpts.DryRun = []string{metav1.DryRunAll}
	}

	switch resourceType {
	case "deployment":
		return t.applyDeployment(ctx, namespace, content, createOpts, updateOpts)
	case "service":
		return t.applyService(ctx, namespace, content, createOpts, updateOpts)
	case "configmap":
		return t.applyConfigMap(ctx, namespace, content, createOpts, updateOpts)
	case "secret":
		return t.applySecret(ctx, namespace, content, createOpts, updateOpts)
	case "ingress":
		return t.applyIngress(ctx, namespace, content, createOpts, updateOpts)
	default:
		return "", fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}

func (t *ApplyManifestTool) applyDeployment(ctx context.Context, namespace string, content []byte, createOpts metav1.CreateOptions, updateOpts metav1.UpdateOptions) (string, error) {
	var deployment appsv1.Deployment
	if err := yaml.Unmarshal(content, &deployment); err != nil {
		return "", fmt.Errorf("invalid YAML: %v", err)
	}
	deployment.Namespace = namespace

	existing, err := t.clientset.AppsV1().Deployments(namespace).Get(ctx, deployment.Name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", fmt.Errorf("failed to check existing deployment: %v", err)
		}
		_, err = t.clientset.AppsV1().Deployments(namespace).Create(ctx, &deployment, createOpts)
		if err != nil {
			return "", fmt.Errorf("failed to create deployment: %v", err)
		}
		return "created", nil
	}

	deployment.ResourceVersion = existing.ResourceVersion
	_, err = t.clientset.AppsV1().Deployments(namespace).Update(ctx, &deployment, updateOpts)
	if err != nil {
		return "", fmt.Errorf("failed to update deployment: %v", err)
	}
	return "updated", nil
}

func (t *ApplyManifestTool) applyService(ctx context.Context, namespace string, content []byte, createOpts metav1.CreateOptions, updateOpts metav1.UpdateOptions) (string, error) {
	var service corev1.Service
	if err := yaml.Unmarshal(content, &service); err != nil {
		return "", fmt.Errorf("invalid YAML: %v", err)
	}
	service.Namespace = namespace

	existing, err := t.clientset.CoreV1().Services(namespace).Get(ctx, service.Name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", fmt.Errorf("failed to check existing service: %v", err)
		}
		_, err = t.clientset.CoreV1().Services(namespace).Create(ctx, &service, createOpts)
		if err != nil {
			return "", fmt.Errorf("failed to create service: %v", err)
		}
		return "created", nil
	}

	// Preserve ClusterIP for updates
	service.ResourceVersion = existing.ResourceVersion
	service.Spec.ClusterIP = existing.Spec.ClusterIP
	service.Spec.ClusterIPs = existing.Spec.ClusterIPs
	_, err = t.clientset.CoreV1().Services(namespace).Update(ctx, &service, updateOpts)
	if err != nil {
		return "", fmt.Errorf("failed to update service: %v", err)
	}
	return "updated", nil
}

func (t *ApplyManifestTool) applyConfigMap(ctx context.Context, namespace string, content []byte, createOpts metav1.CreateOptions, updateOpts metav1.UpdateOptions) (string, error) {
	var configmap corev1.ConfigMap
	if err := yaml.Unmarshal(content, &configmap); err != nil {
		return "", fmt.Errorf("invalid YAML: %v", err)
	}
	configmap.Namespace = namespace

	existing, err := t.clientset.CoreV1().ConfigMaps(namespace).Get(ctx, configmap.Name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", fmt.Errorf("failed to check existing configmap: %v", err)
		}
		_, err = t.clientset.CoreV1().ConfigMaps(namespace).Create(ctx, &configmap, createOpts)
		if err != nil {
			return "", fmt.Errorf("failed to create configmap: %v", err)
		}
		return "created", nil
	}

	configmap.ResourceVersion = existing.ResourceVersion
	_, err = t.clientset.CoreV1().ConfigMaps(namespace).Update(ctx, &configmap, updateOpts)
	if err != nil {
		return "", fmt.Errorf("failed to update configmap: %v", err)
	}
	return "updated", nil
}

func (t *ApplyManifestTool) applySecret(ctx context.Context, namespace string, content []byte, createOpts metav1.CreateOptions, updateOpts metav1.UpdateOptions) (string, error) {
	var secret corev1.Secret
	if err := yaml.Unmarshal(content, &secret); err != nil {
		return "", fmt.Errorf("invalid YAML: %v", err)
	}
	secret.Namespace = namespace

	existing, err := t.clientset.CoreV1().Secrets(namespace).Get(ctx, secret.Name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", fmt.Errorf("failed to check existing secret: %v", err)
		}
		_, err = t.clientset.CoreV1().Secrets(namespace).Create(ctx, &secret, createOpts)
		if err != nil {
			return "", fmt.Errorf("failed to create secret: %v", err)
		}
		return "created", nil
	}

	secret.ResourceVersion = existing.ResourceVersion
	_, err = t.clientset.CoreV1().Secrets(namespace).Update(ctx, &secret, updateOpts)
	if err != nil {
		return "", fmt.Errorf("failed to update secret: %v", err)
	}
	return "updated", nil
}

func (t *ApplyManifestTool) applyIngress(ctx context.Context, namespace string, content []byte, createOpts metav1.CreateOptions, updateOpts metav1.UpdateOptions) (string, error) {
	var ingress networkingv1.Ingress
	if err := yaml.Unmarshal(content, &ingress); err != nil {
		return "", fmt.Errorf("invalid YAML: %v", err)
	}
	ingress.Namespace = namespace

	existing, err := t.clientset.NetworkingV1().Ingresses(namespace).Get(ctx, ingress.Name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", fmt.Errorf("failed to check existing ingress: %v", err)
		}
		_, err = t.clientset.NetworkingV1().Ingresses(namespace).Create(ctx, &ingress, createOpts)
		if err != nil {
			return "", fmt.Errorf("failed to create ingress: %v", err)
		}
		return "created", nil
	}

	ingress.ResourceVersion = existing.ResourceVersion
	_, err = t.clientset.NetworkingV1().Ingresses(namespace).Update(ctx, &ingress, updateOpts)
	if err != nil {
		return "", fmt.Errorf("failed to update ingress: %v", err)
	}
	return "updated", nil
}
