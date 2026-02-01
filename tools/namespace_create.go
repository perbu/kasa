package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// CreateNamespaceTool provides the create_namespace tool for the agent.
type CreateNamespaceTool struct {
	clientset *kubernetes.Clientset
}

// NewCreateNamespaceTool creates a new CreateNamespaceTool.
func NewCreateNamespaceTool(clientset *kubernetes.Clientset) *CreateNamespaceTool {
	return &CreateNamespaceTool{
		clientset: clientset,
	}
}

// Name returns the tool name.
func (t *CreateNamespaceTool) Name() string {
	return "create_namespace"
}

// Description returns the tool description.
func (t *CreateNamespaceTool) Description() string {
	return "Create a new Kubernetes namespace. Namespaces are cluster-scoped and are not stored as manifests."
}

// IsLongRunning returns false as this is a quick operation.
func (t *CreateNamespaceTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *CreateNamespaceTool) Category() ToolCategory {
	return CategoryMutating
}

// ProcessRequest adds this tool to the LLM request.
func (t *CreateNamespaceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *CreateNamespaceTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"name": {
					Type:        "string",
					Description: "The name of the namespace to create",
				},
				"labels": {
					Type:        "object",
					Description: "Optional labels to add to the namespace",
				},
			},
			Required: []string{"name"},
		},
	}
}

// Run executes the tool.
func (t *CreateNamespaceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	// Build labels
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "kasa",
	}

	// Add custom labels if provided
	if customLabels, ok := argsMap["labels"].(map[string]any); ok {
		for k, v := range customLabels {
			if vs, ok := v.(string); ok {
				labels[k] = vs
			}
		}
	}

	// Build the namespace
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}

	// Create in cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Check if namespace already exists
	_, err := t.clientset.CoreV1().Namespaces().Get(timeoutCtx, name, metav1.GetOptions{})
	if err == nil {
		return map[string]any{
			"success": false,
			"exists":  true,
			"name":    name,
			"message": fmt.Sprintf("Namespace %s already exists", name),
		}, nil
	}
	if !errors.IsNotFound(err) {
		return map[string]any{"error": fmt.Sprintf("failed to check existing namespace: %v", err)}, nil
	}

	// Create the namespace
	_, err = t.clientset.CoreV1().Namespaces().Create(timeoutCtx, namespace, metav1.CreateOptions{})
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to create namespace: %v", err)}, nil
	}

	return map[string]any{
		"success": true,
		"name":    name,
		"labels":  labels,
		"message": fmt.Sprintf("Namespace %s created", name),
	}, nil
}
