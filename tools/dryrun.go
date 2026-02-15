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
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// DryRunApplyTool provides the dry_run_apply tool for the agent.
type DryRunApplyTool struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	manifest      *manifest.Manager
}

// NewDryRunApplyTool creates a new DryRunApplyTool.
func NewDryRunApplyTool(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, manifest *manifest.Manager) *DryRunApplyTool {
	return &DryRunApplyTool{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		manifest:      manifest,
	}
}

// Name returns the tool name.
func (t *DryRunApplyTool) Name() string {
	return "dry_run_apply"
}

// Description returns the tool description.
func (t *DryRunApplyTool) Description() string {
	return "Validate a manifest against the cluster without applying it. Accepts either inline YAML or a reference to a stored manifest. Uses Kubernetes server-side dry-run to check for errors."
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
				"yaml": {
					Type:        "string",
					Description: "Inline YAML manifest to validate. When provided, namespace/app/type are ignored.",
				},
				"namespace": {
					Type:        "string",
					Description: "The namespace of the stored manifest (used when yaml is not provided)",
				},
				"app": {
					Type:        "string",
					Description: "The app name / manifest directory name (used when yaml is not provided)",
				},
				"type": {
					Type:        "string",
					Description: "The resource type: deployment, service, configmap, secret, ingress (used when yaml is not provided)",
				},
			},
		},
	}
}

// Run executes the tool.
func (t *DryRunApplyTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	// Check if inline YAML is provided
	if yamlContent, ok := argsMap["yaml"].(string); ok && yamlContent != "" {
		return t.runInlineYAML(yamlContent)
	}

	// Fall back to stored manifest path
	return t.runStoredManifest(argsMap)
}

// runInlineYAML validates inline YAML using the dynamic client.
func (t *DryRunApplyTool) runInlineYAML(yamlContent string) (map[string]any, error) {
	obj, err := ParseYAMLToUnstructured([]byte(yamlContent))
	if err != nil {
		return map[string]any{
			"valid":   false,
			"error":   fmt.Sprintf("failed to parse YAML: %v", err),
			"message": fmt.Sprintf("YAML parsing failed: %v", err),
		}, nil
	}

	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" {
		return map[string]any{
			"valid":   false,
			"error":   "YAML must contain a 'kind' field",
			"message": "YAML must contain a 'kind' field",
		}, nil
	}

	name := obj.GetName()
	if name == "" {
		return map[string]any{
			"valid":   false,
			"error":   "YAML must contain metadata.name",
			"message": "YAML must contain metadata.name",
		}, nil
	}

	gvr := GVKToGVR(gvk)
	namespace := obj.GetNamespace()
	namespaced := IsNamespaced(gvk.Kind)
	if namespaced && namespace == "" {
		namespace = "default"
		obj.SetNamespace(namespace)
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var resourceClient dynamic.ResourceInterface
	if namespaced {
		resourceClient = t.dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceClient = t.dynamicClient.Resource(gvr)
	}

	dryRunCreate := metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}
	dryRunUpdate := metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}}

	// Try create first; if already exists, try update
	_, err = resourceClient.Create(timeoutCtx, obj, dryRunCreate)
	if errors.IsAlreadyExists(err) {
		existing, getErr := resourceClient.Get(timeoutCtx, name, metav1.GetOptions{})
		if getErr != nil {
			return map[string]any{
				"valid":   false,
				"error":   getErr.Error(),
				"message": fmt.Sprintf("Failed to get existing resource for update validation: %v", getErr),
			}, nil
		}
		obj.SetResourceVersion(existing.GetResourceVersion())
		_, err = resourceClient.Update(timeoutCtx, obj, dryRunUpdate)
	}

	if err != nil {
		return map[string]any{
			"valid":   false,
			"error":   err.Error(),
			"message": fmt.Sprintf("Manifest validation failed: %v", err),
		}, nil
	}

	result := map[string]any{
		"valid":   true,
		"kind":    gvk.Kind,
		"name":    name,
		"message": fmt.Sprintf("%s/%s is valid", gvk.Kind, name),
	}
	if namespaced {
		result["namespace"] = namespace
	}
	return result, nil
}

// runStoredManifest validates a manifest from storage using the typed client.
func (t *DryRunApplyTool) runStoredManifest(argsMap map[string]any) (map[string]any, error) {
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

	resourceType = NormalizeKindName(resourceType)

	content, err := t.manifest.ReadManifest(namespace, app, resourceType)
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}

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

// dryRunApply validates a manifest using Kubernetes server-side dry-run (typed client).
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
