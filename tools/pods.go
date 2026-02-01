package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ContainerInfo contains information about a container in a pod.
type ContainerInfo struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restart_count"`
}

// PodInfo contains information about a Kubernetes pod.
type PodInfo struct {
	Name       string          `json:"name"`
	Namespace  string          `json:"namespace"`
	Status     string          `json:"status"`
	Containers []ContainerInfo `json:"containers"`
	Ready      string          `json:"ready"`
	Restarts   int32           `json:"restarts"`
	Age        string          `json:"age"`
}

// ListPodsTool provides the list_pods tool for the agent.
type ListPodsTool struct {
	clientset *kubernetes.Clientset
}

// NewListPodsTool creates a new ListPodsTool.
func NewListPodsTool(clientset *kubernetes.Clientset) *ListPodsTool {
	return &ListPodsTool{
		clientset: clientset,
	}
}

// Name returns the tool name.
func (t *ListPodsTool) Name() string {
	return "list_pods"
}

// Description returns the tool description.
func (t *ListPodsTool) Description() string {
	return "List pods in a Kubernetes namespace with their status, containers, readiness, and age"
}

// IsLongRunning returns false as this is a quick operation.
func (t *ListPodsTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *ListPodsTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *ListPodsTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ListPodsTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "The namespace to list pods from. Use empty string for all namespaces.",
				},
				"label_selector": {
					Type:        "string",
					Description: "Optional label selector to filter pods (e.g., 'app=nginx')",
				},
			},
			Required: []string{"namespace"},
		},
	}
}

// Run executes the tool.
func (t *ListPodsTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	namespace := ""
	if ns, ok := argsMap["namespace"].(string); ok {
		namespace = ns
	}

	labelSelector := ""
	if ls, ok := argsMap["label_selector"].(string); ok {
		labelSelector = ls
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pods, err := t.clientset.CoreV1().Pods(namespace).List(timeoutCtx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}

	result := make([]PodInfo, 0, len(pods.Items))
	for _, pod := range pods.Items {
		containers := make([]ContainerInfo, 0, len(pod.Spec.Containers))
		var totalRestarts int32

		// Map container statuses by name for lookup
		statusMap := make(map[string]struct {
			ready        bool
			restartCount int32
		})
		for _, cs := range pod.Status.ContainerStatuses {
			statusMap[cs.Name] = struct {
				ready        bool
				restartCount int32
			}{
				ready:        cs.Ready,
				restartCount: cs.RestartCount,
			}
			totalRestarts += cs.RestartCount
		}

		for _, c := range pod.Spec.Containers {
			status := statusMap[c.Name]
			containers = append(containers, ContainerInfo{
				Name:         c.Name,
				Image:        c.Image,
				Ready:        status.ready,
				RestartCount: status.restartCount,
			})
		}

		// Calculate ready count
		readyCount := 0
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				readyCount++
			}
		}

		// Calculate age
		age := time.Since(pod.CreationTimestamp.Time)
		ageStr := formatDuration(age)

		result = append(result, PodInfo{
			Name:       pod.Name,
			Namespace:  pod.Namespace,
			Status:     string(pod.Status.Phase),
			Containers: containers,
			Ready:      formatReady(readyCount, len(pod.Spec.Containers)),
			Restarts:   totalRestarts,
			Age:        ageStr,
		})
	}

	return map[string]any{
		"pods":  result,
		"count": len(result),
	}, nil
}

// formatReady formats the ready count as "ready/total".
func formatReady(ready, total int) string {
	return fmt.Sprintf("%d/%d", ready, total)
}

// formatDuration formats a duration in a human-readable way.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%dd", days)
}
