package tools

import (
	"context"
	"encoding/json"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// NamespaceInfo contains information about a Kubernetes namespace.
type NamespaceInfo struct {
	Name      string            `json:"name"`
	Status    string            `json:"status"`
	Labels    map[string]string `json:"labels"`
	CreatedAt string            `json:"created_at"`
}

// ListNamespacesTool provides the list_namespaces tool for the agent.
type ListNamespacesTool struct {
	clientset *kubernetes.Clientset
}

// NewListNamespacesTool creates a new ListNamespacesTool.
func NewListNamespacesTool(clientset *kubernetes.Clientset) *ListNamespacesTool {
	return &ListNamespacesTool{
		clientset: clientset,
	}
}

// Name returns the tool name.
func (t *ListNamespacesTool) Name() string {
	return "list_namespaces"
}

// Description returns the tool description.
func (t *ListNamespacesTool) Description() string {
	return "List all namespaces in the Kubernetes cluster with their status, labels, and creation time"
}

// IsLongRunning returns false as this is a quick operation.
func (t *ListNamespacesTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *ListNamespacesTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *ListNamespacesTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ListNamespacesTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type:       "object",
			Properties: map[string]*genai.Schema{},
		},
	}
}

// Run executes the tool.
func (t *ListNamespacesTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	// Parse arguments (none required for this tool)
	if args != nil {
		if _, ok := args.(map[string]any); !ok {
			if argsStr, ok := args.(string); ok {
				var argsMap map[string]any
				if err := json.Unmarshal([]byte(argsStr), &argsMap); err != nil {
					return map[string]any{"error": "invalid arguments format"}, nil
				}
			}
		}
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	namespaces, err := t.clientset.CoreV1().Namespaces().List(timeoutCtx, metav1.ListOptions{})
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}

	result := make([]NamespaceInfo, 0, len(namespaces.Items))
	for _, ns := range namespaces.Items {
		result = append(result, NamespaceInfo{
			Name:      ns.Name,
			Status:    string(ns.Status.Phase),
			Labels:    ns.Labels,
			CreatedAt: ns.CreationTimestamp.Format(time.RFC3339),
		})
	}

	return map[string]any{
		"namespaces": result,
		"count":      len(result),
	}, nil
}
