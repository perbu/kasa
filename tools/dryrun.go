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

// DryRunApplyTool provides the dry_run_apply tool for the agent.
type DryRunApplyTool struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewDryRunApplyTool creates a new DryRunApplyTool.
func NewDryRunApplyTool(clientset *kubernetes.Clientset, manifest *manifest.Manager) *DryRunApplyTool {
	return &DryRunApplyTool{
		clientset: clientset,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *DryRunApplyTool) Name() string {
	return "dry_run_apply"
}

// Description returns the tool description.
func (t *DryRunApplyTool) Description() string {
	return "Validate a manifest against the cluster without applying it. Uses Kubernetes server-side dry-run to check for errors."
}

// IsLongRunning returns false as this is a quick operation.
func (t *DryRunApplyTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *DryRunApplyTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *DryRunApplyTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *DryRunApplyTool) Declaration() *genai.FunctionDeclaration {
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
			},
			Required: []string{"namespace", "app", "type"},
		},
	}
}

// Run executes the tool.
func (t *DryRunApplyTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	// Validate with dry-run
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = t.dryRunApply(timeoutCtx, namespace, resourceType, content)
	if err != nil {
		return map[string]any{
			"valid":   false,
			"error":   err.Error(),
			"message": fmt.Sprintf("Manifest validation failed: %v", err),
		}, nil
	}

	return map[string]any{
		"valid":     true,
		"namespace": namespace,
		"app":       app,
		"type":      resourceType,
		"message":   fmt.Sprintf("Manifest %s/%s/%s is valid", namespace, app, resourceType),
	}, nil
}

// dryRunApply validates a manifest using Kubernetes server-side dry-run.
func (t *DryRunApplyTool) dryRunApply(ctx context.Context, namespace, resourceType string, content []byte) error {
	dryRunOpts := metav1.CreateOptions{
		DryRun: []string{metav1.DryRunAll},
	}

	switch resourceType {
	case "deployment":
		var deployment appsv1.Deployment
		if err := yaml.Unmarshal(content, &deployment); err != nil {
			return fmt.Errorf("invalid YAML: %v", err)
		}
		deployment.Namespace = namespace

		_, err := t.clientset.AppsV1().Deployments(namespace).Create(ctx, &deployment, dryRunOpts)
		if errors.IsAlreadyExists(err) {
			// Try update instead
			return t.dryRunUpdate(ctx, namespace, resourceType, &deployment)
		}
		return err

	case "service":
		var service corev1.Service
		if err := yaml.Unmarshal(content, &service); err != nil {
			return fmt.Errorf("invalid YAML: %v", err)
		}
		service.Namespace = namespace

		_, err := t.clientset.CoreV1().Services(namespace).Create(ctx, &service, dryRunOpts)
		if errors.IsAlreadyExists(err) {
			return t.dryRunUpdate(ctx, namespace, resourceType, &service)
		}
		return err

	case "configmap":
		var configmap corev1.ConfigMap
		if err := yaml.Unmarshal(content, &configmap); err != nil {
			return fmt.Errorf("invalid YAML: %v", err)
		}
		configmap.Namespace = namespace

		_, err := t.clientset.CoreV1().ConfigMaps(namespace).Create(ctx, &configmap, dryRunOpts)
		if errors.IsAlreadyExists(err) {
			return t.dryRunUpdate(ctx, namespace, resourceType, &configmap)
		}
		return err

	case "secret":
		var secret corev1.Secret
		if err := yaml.Unmarshal(content, &secret); err != nil {
			return fmt.Errorf("invalid YAML: %v", err)
		}
		secret.Namespace = namespace

		_, err := t.clientset.CoreV1().Secrets(namespace).Create(ctx, &secret, dryRunOpts)
		if errors.IsAlreadyExists(err) {
			return t.dryRunUpdate(ctx, namespace, resourceType, &secret)
		}
		return err

	case "ingress":
		var ingress networkingv1.Ingress
		if err := yaml.Unmarshal(content, &ingress); err != nil {
			return fmt.Errorf("invalid YAML: %v", err)
		}
		ingress.Namespace = namespace

		_, err := t.clientset.NetworkingV1().Ingresses(namespace).Create(ctx, &ingress, dryRunOpts)
		if errors.IsAlreadyExists(err) {
			return t.dryRunUpdate(ctx, namespace, resourceType, &ingress)
		}
		return err

	default:
		return fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}

// dryRunUpdate performs a dry-run update for an existing resource.
func (t *DryRunApplyTool) dryRunUpdate(ctx context.Context, namespace, resourceType string, obj any) error {
	dryRunOpts := metav1.UpdateOptions{
		DryRun: []string{metav1.DryRunAll},
	}

	switch resourceType {
	case "deployment":
		deployment := obj.(*appsv1.Deployment)
		// Get existing to set ResourceVersion
		existing, err := t.clientset.AppsV1().Deployments(namespace).Get(ctx, deployment.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		deployment.ResourceVersion = existing.ResourceVersion
		_, err = t.clientset.AppsV1().Deployments(namespace).Update(ctx, deployment, dryRunOpts)
		return err

	case "service":
		service := obj.(*corev1.Service)
		existing, err := t.clientset.CoreV1().Services(namespace).Get(ctx, service.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		service.ResourceVersion = existing.ResourceVersion
		// Preserve clusterIP for updates
		service.Spec.ClusterIP = existing.Spec.ClusterIP
		service.Spec.ClusterIPs = existing.Spec.ClusterIPs
		_, err = t.clientset.CoreV1().Services(namespace).Update(ctx, service, dryRunOpts)
		return err

	case "configmap":
		configmap := obj.(*corev1.ConfigMap)
		existing, err := t.clientset.CoreV1().ConfigMaps(namespace).Get(ctx, configmap.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		configmap.ResourceVersion = existing.ResourceVersion
		_, err = t.clientset.CoreV1().ConfigMaps(namespace).Update(ctx, configmap, dryRunOpts)
		return err

	case "secret":
		secret := obj.(*corev1.Secret)
		existing, err := t.clientset.CoreV1().Secrets(namespace).Get(ctx, secret.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		secret.ResourceVersion = existing.ResourceVersion
		_, err = t.clientset.CoreV1().Secrets(namespace).Update(ctx, secret, dryRunOpts)
		return err

	case "ingress":
		ingress := obj.(*networkingv1.Ingress)
		existing, err := t.clientset.NetworkingV1().Ingresses(namespace).Get(ctx, ingress.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		ingress.ResourceVersion = existing.ResourceVersion
		_, err = t.clientset.NetworkingV1().Ingresses(namespace).Update(ctx, ingress, dryRunOpts)
		return err

	default:
		return fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}
