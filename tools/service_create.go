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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// CreateServiceTool provides the create_service tool for the agent.
type CreateServiceTool struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewCreateServiceTool creates a new CreateServiceTool.
func NewCreateServiceTool(clientset *kubernetes.Clientset, manifest *manifest.Manager) *CreateServiceTool {
	return &CreateServiceTool{
		clientset: clientset,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *CreateServiceTool) Name() string {
	return "create_service"
}

// Description returns the tool description.
func (t *CreateServiceTool) Description() string {
	return "Create or update a Kubernetes service. Saves the manifest to git and applies it to the cluster."
}

// IsLongRunning returns false as this is a quick operation.
func (t *CreateServiceTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *CreateServiceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *CreateServiceTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"name": {
					Type:        "string",
					Description: "The name of the service",
				},
				"namespace": {
					Type:        "string",
					Description: "The target Kubernetes namespace",
				},
				"selector": {
					Type:        "object",
					Description: "Pod selector labels (e.g., {\"app.kubernetes.io/name\": \"myapp\"})",
				},
				"port": {
					Type:        "integer",
					Description: "The service port",
				},
				"target_port": {
					Type:        "integer",
					Description: "The target port on the pod (defaults to port)",
				},
				"type": {
					Type:        "string",
					Description: "Service type: ClusterIP, NodePort, or LoadBalancer (default: ClusterIP)",
				},
			},
			Required: []string{"name", "namespace", "selector", "port"},
		},
	}
}

// Run executes the tool.
func (t *CreateServiceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	selectorMap, ok := argsMap["selector"].(map[string]any)
	if !ok || len(selectorMap) == 0 {
		return map[string]any{"error": "selector is required"}, nil
	}

	// Convert selector to string map
	selector := make(map[string]string)
	for k, v := range selectorMap {
		if vs, ok := v.(string); ok {
			selector[k] = vs
		}
	}

	port, ok := argsMap["port"].(float64)
	if !ok || port <= 0 {
		return map[string]any{"error": "port is required"}, nil
	}
	servicePort := int32(port)

	// Extract optional parameters
	targetPort := servicePort
	if tp, ok := argsMap["target_port"].(float64); ok && tp > 0 {
		targetPort = int32(tp)
	}

	serviceType := corev1.ServiceTypeClusterIP
	if st, ok := argsMap["type"].(string); ok {
		switch st {
		case "ClusterIP":
			serviceType = corev1.ServiceTypeClusterIP
		case "NodePort":
			serviceType = corev1.ServiceTypeNodePort
		case "LoadBalancer":
			serviceType = corev1.ServiceTypeLoadBalancer
		}
	}

	// Build labels for the service itself
	labels := map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "kasa",
	}

	// Build the service
	service := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     serviceType,
			Selector: selector,
			Ports: []corev1.ServicePort{
				{
					Port:       servicePort,
					TargetPort: intstr.FromInt32(targetPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(service)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to marshal service: %v", err)}, nil
	}

	// Save manifest
	manifestPath, err := t.manifest.SaveManifest(namespace, name, "service", yamlBytes)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to save manifest: %v", err)}, nil
	}

	// Apply to cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var action string
	existing, err := t.clientset.CoreV1().Services(namespace).Get(timeoutCtx, name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return map[string]any{"error": fmt.Sprintf("failed to check existing service: %v", err)}, nil
		}
		// Create new service
		_, err = t.clientset.CoreV1().Services(namespace).Create(timeoutCtx, service, metav1.CreateOptions{})
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to create service: %v", err)}, nil
		}
		action = "created"
	} else {
		// Update existing service - need to preserve ClusterIP and ResourceVersion
		service.Spec.ClusterIP = existing.Spec.ClusterIP
		service.ResourceVersion = existing.ResourceVersion
		_, err = t.clientset.CoreV1().Services(namespace).Update(timeoutCtx, service, metav1.UpdateOptions{})
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to update service: %v", err)}, nil
		}
		action = "updated"
	}

	return map[string]any{
		"success":       true,
		"action":        action,
		"name":          name,
		"namespace":     namespace,
		"type":          string(serviceType),
		"port":          servicePort,
		"target_port":   targetPort,
		"manifest_path": manifestPath,
		"message":       fmt.Sprintf("Service %s %s in namespace %s", name, action, namespace),
	}, nil
}
