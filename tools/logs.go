package tools

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// GetLogsTool provides access to container logs.
type GetLogsTool struct {
	clientset *kubernetes.Clientset
}

// NewGetLogsTool creates a new GetLogsTool.
func NewGetLogsTool(clientset *kubernetes.Clientset) *GetLogsTool {
	return &GetLogsTool{
		clientset: clientset,
	}
}

// Name returns the tool name.
func (t *GetLogsTool) Name() string {
	return "get_logs"
}

// Description returns the tool description.
func (t *GetLogsTool) Description() string {
	return "Get logs from a container in a pod. Can retrieve current or previous container logs."
}

// IsLongRunning returns false as this is a quick operation.
func (t *GetLogsTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *GetLogsTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *GetLogsTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *GetLogsTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "The namespace of the pod",
				},
				"pod": {
					Type:        "string",
					Description: "The name of the pod",
				},
				"container": {
					Type:        "string",
					Description: "The name of the container. Optional if the pod has only one container.",
				},
				"previous": {
					Type:        "boolean",
					Description: "If true, get logs from the previous terminated container instance. Useful for debugging crashes.",
				},
				"tail_lines": {
					Type:        "integer",
					Description: "Number of lines from the end of the logs to retrieve. Defaults to 100.",
				},
			},
			Required: []string{"namespace", "pod"},
		},
	}
}

// Run executes the tool.
func (t *GetLogsTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	pod, ok := argsMap["pod"].(string)
	if !ok || pod == "" {
		return map[string]any{"error": "pod is required"}, nil
	}

	container := ""
	if c, ok := argsMap["container"].(string); ok {
		container = c
	}

	previous := false
	if p, ok := argsMap["previous"].(bool); ok {
		previous = p
	}

	tailLines := int64(100)
	if tl, ok := argsMap["tail_lines"].(float64); ok {
		tailLines = int64(tl)
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build log options
	opts := &corev1.PodLogOptions{
		Container: container,
		Previous:  previous,
		TailLines: &tailLines,
	}

	// Get logs
	req := t.clientset.CoreV1().Pods(namespace).GetLogs(pod, opts)
	stream, err := req.Stream(timeoutCtx)
	if err != nil {
		return map[string]any{
			"error":     err.Error(),
			"namespace": namespace,
			"pod":       pod,
			"container": container,
			"previous":  previous,
		}, nil
	}
	defer stream.Close()

	logs, err := io.ReadAll(stream)
	if err != nil {
		return map[string]any{
			"error": "failed to read logs: " + err.Error(),
		}, nil
	}

	return map[string]any{
		"namespace":  namespace,
		"pod":        pod,
		"container":  container,
		"previous":   previous,
		"tail_lines": tailLines,
		"logs":       string(logs),
	}, nil
}
