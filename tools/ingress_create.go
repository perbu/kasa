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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// CreateIngressTool provides the create_ingress tool for the agent.
type CreateIngressTool struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewCreateIngressTool creates a new CreateIngressTool.
func NewCreateIngressTool(clientset *kubernetes.Clientset, manifest *manifest.Manager) *CreateIngressTool {
	return &CreateIngressTool{
		clientset: clientset,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *CreateIngressTool) Name() string {
	return "create_ingress"
}

// Description returns the tool description.
func (t *CreateIngressTool) Description() string {
	return "Create or update a Kubernetes Ingress. Saves the manifest to git and applies it to the cluster."
}

// IsLongRunning returns false as this is a quick operation.
func (t *CreateIngressTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *CreateIngressTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *CreateIngressTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"name": {
					Type:        "string",
					Description: "The name of the Ingress",
				},
				"namespace": {
					Type:        "string",
					Description: "The target Kubernetes namespace",
				},
				"host": {
					Type:        "string",
					Description: "The hostname for the ingress rule (e.g., example.com)",
				},
				"service_name": {
					Type:        "string",
					Description: "The backend service name",
				},
				"service_port": {
					Type:        "integer",
					Description: "The backend service port",
				},
				"path": {
					Type:        "string",
					Description: "The URL path prefix (default: /)",
				},
				"tls_secret": {
					Type:        "string",
					Description: "Name of the TLS secret for HTTPS (optional)",
				},
				"ingress_class": {
					Type:        "string",
					Description: "The ingress class name (e.g., nginx, traefik)",
				},
				"annotations": {
					Type:        "object",
					Description: "Optional annotations (e.g., for rewrite rules)",
				},
			},
			Required: []string{"name", "namespace", "host", "service_name", "service_port"},
		},
	}
}

// Run executes the tool.
func (t *CreateIngressTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	host, ok := argsMap["host"].(string)
	if !ok || host == "" {
		return map[string]any{"error": "host is required"}, nil
	}

	serviceName, ok := argsMap["service_name"].(string)
	if !ok || serviceName == "" {
		return map[string]any{"error": "service_name is required"}, nil
	}

	servicePortFloat, ok := argsMap["service_port"].(float64)
	if !ok || servicePortFloat <= 0 {
		return map[string]any{"error": "service_port is required"}, nil
	}
	servicePort := int32(servicePortFloat)

	// Extract optional parameters
	path := "/"
	if p, ok := argsMap["path"].(string); ok && p != "" {
		path = p
	}

	var tlsSecret string
	if tls, ok := argsMap["tls_secret"].(string); ok {
		tlsSecret = tls
	}

	var ingressClass *string
	if ic, ok := argsMap["ingress_class"].(string); ok && ic != "" {
		ingressClass = &ic
	}

	// Build annotations
	annotations := map[string]string{}
	if customAnnotations, ok := argsMap["annotations"].(map[string]any); ok {
		for k, v := range customAnnotations {
			if vs, ok := v.(string); ok {
				annotations[k] = vs
			}
		}
	}

	// Build labels
	labels := map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "kasa",
	}

	// Build the Ingress
	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ingressClass,
			Rules: []networkingv1.IngressRule{
				{
					Host: host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{
												Number: servicePort,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Add TLS configuration if secret provided
	if tlsSecret != "" {
		ingress.Spec.TLS = []networkingv1.IngressTLS{
			{
				Hosts:      []string{host},
				SecretName: tlsSecret,
			},
		}
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(ingress)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to marshal ingress: %v", err)}, nil
	}

	// Save manifest
	manifestPath, err := t.manifest.SaveManifest(namespace, name, "ingress", yamlBytes)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to save manifest: %v", err)}, nil
	}

	// Apply to cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var action string
	existing, err := t.clientset.NetworkingV1().Ingresses(namespace).Get(timeoutCtx, name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return map[string]any{"error": fmt.Sprintf("failed to check existing ingress: %v", err)}, nil
		}
		// Create new ingress
		_, err = t.clientset.NetworkingV1().Ingresses(namespace).Create(timeoutCtx, ingress, metav1.CreateOptions{})
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to create ingress: %v", err)}, nil
		}
		action = "created"
	} else {
		// Update existing ingress
		ingress.ResourceVersion = existing.ResourceVersion
		_, err = t.clientset.NetworkingV1().Ingresses(namespace).Update(timeoutCtx, ingress, metav1.UpdateOptions{})
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to update ingress: %v", err)}, nil
		}
		action = "updated"
	}

	result := map[string]any{
		"success":       true,
		"action":        action,
		"name":          name,
		"namespace":     namespace,
		"host":          host,
		"path":          path,
		"service":       serviceName,
		"port":          servicePort,
		"manifest_path": manifestPath,
		"message":       fmt.Sprintf("Ingress %s %s in namespace %s", name, action, namespace),
	}

	if tlsSecret != "" {
		result["tls_enabled"] = true
		result["tls_secret"] = tlsSecret
	}

	return result, nil
}
