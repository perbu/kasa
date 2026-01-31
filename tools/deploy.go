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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// CreateDeploymentTool provides the create_deployment tool for the agent.
type CreateDeploymentTool struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewCreateDeploymentTool creates a new CreateDeploymentTool.
func NewCreateDeploymentTool(clientset *kubernetes.Clientset, manifest *manifest.Manager) *CreateDeploymentTool {
	return &CreateDeploymentTool{
		clientset: clientset,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *CreateDeploymentTool) Name() string {
	return "create_deployment"
}

// Description returns the tool description.
func (t *CreateDeploymentTool) Description() string {
	return "Create or update a Kubernetes deployment. Saves the manifest to git and applies it to the cluster."
}

// IsLongRunning returns false as this is a quick operation.
func (t *CreateDeploymentTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *CreateDeploymentTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *CreateDeploymentTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"name": {
					Type:        "string",
					Description: "The name of the deployment",
				},
				"namespace": {
					Type:        "string",
					Description: "The target Kubernetes namespace",
				},
				"image": {
					Type:        "string",
					Description: "The container image with tag (e.g., nginx:1.25)",
				},
				"replicas": {
					Type:        "integer",
					Description: "Number of replicas (default: 1)",
				},
				"port": {
					Type:        "integer",
					Description: "Container port to expose",
				},
				"health_path": {
					Type:        "string",
					Description: "HTTP path for health checks (e.g., /health)",
				},
				"env": {
					Type:        "object",
					Description: "Environment variables as key-value pairs",
				},
			},
			Required: []string{"name", "namespace", "image"},
		},
	}
}

// Run executes the tool.
func (t *CreateDeploymentTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	// Extract required parameters
	name, ok := argsMap["name"].(string)
	if !ok || name == "" {
		return map[string]any{"error": "name is required"}, nil
	}

	namespace, ok := argsMap["namespace"].(string)
	if !ok || namespace == "" {
		return map[string]any{"error": "namespace is required"}, nil
	}

	image, ok := argsMap["image"].(string)
	if !ok || image == "" {
		return map[string]any{"error": "image is required"}, nil
	}

	// Extract optional parameters
	replicas := int32(1)
	if r, ok := argsMap["replicas"].(float64); ok {
		replicas = int32(r)
	}

	var containerPort int32
	if p, ok := argsMap["port"].(float64); ok {
		containerPort = int32(p)
	}

	healthPath := ""
	if hp, ok := argsMap["health_path"].(string); ok {
		healthPath = hp
	}

	var envVars []corev1.EnvVar
	if env, ok := argsMap["env"].(map[string]any); ok {
		for k, v := range env {
			if vs, ok := v.(string); ok {
				envVars = append(envVars, corev1.EnvVar{
					Name:  k,
					Value: vs,
				})
			}
		}
	}

	// Build the deployment
	labels := map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "kasa",
	}

	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
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
							Image: image,
							Env:   envVars,
						},
					},
				},
			},
		},
	}

	// Add container port if specified
	if containerPort > 0 {
		deployment.Spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{
			{
				ContainerPort: containerPort,
				Protocol:      corev1.ProtocolTCP,
			},
		}
	}

	// Add health check if path specified
	if healthPath != "" && containerPort > 0 {
		probe := &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: healthPath,
					Port: intstr.FromInt32(containerPort),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		}
		deployment.Spec.Template.Spec.Containers[0].LivenessProbe = probe
		deployment.Spec.Template.Spec.Containers[0].ReadinessProbe = probe
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(deployment)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to marshal deployment: %v", err)}, nil
	}

	// Save manifest
	manifestPath, err := t.manifest.SaveManifest(namespace, name, "deployment", yamlBytes)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to save manifest: %v", err)}, nil
	}

	// Apply to cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var action string
	existing, err := t.clientset.AppsV1().Deployments(namespace).Get(timeoutCtx, name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return map[string]any{"error": fmt.Sprintf("failed to check existing deployment: %v", err)}, nil
		}
		// Create new deployment
		_, err = t.clientset.AppsV1().Deployments(namespace).Create(timeoutCtx, deployment, metav1.CreateOptions{})
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to create deployment: %v", err)}, nil
		}
		action = "created"
	} else {
		// Update existing deployment
		deployment.ResourceVersion = existing.ResourceVersion
		_, err = t.clientset.AppsV1().Deployments(namespace).Update(timeoutCtx, deployment, metav1.UpdateOptions{})
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to update deployment: %v", err)}, nil
		}
		action = "updated"
	}

	return map[string]any{
		"success":       true,
		"action":        action,
		"name":          name,
		"namespace":     namespace,
		"image":         image,
		"replicas":      replicas,
		"manifest_path": manifestPath,
		"message":       fmt.Sprintf("Deployment %s %s in namespace %s", name, action, namespace),
	}, nil
}
