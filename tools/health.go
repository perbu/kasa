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

// HealthPodInfo contains pod health information.
type HealthPodInfo struct {
	Name   string `json:"name"`
	Ready  bool   `json:"ready"`
	Status string `json:"status"`
	Age    string `json:"age"`
}

// HealthEventInfo contains event information.
type HealthEventInfo struct {
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Age     string `json:"age"`
}

// CheckDeploymentHealthTool provides the check_deployment_health tool for the agent.
type CheckDeploymentHealthTool struct {
	clientset *kubernetes.Clientset
}

// NewCheckDeploymentHealthTool creates a new CheckDeploymentHealthTool.
func NewCheckDeploymentHealthTool(clientset *kubernetes.Clientset) *CheckDeploymentHealthTool {
	return &CheckDeploymentHealthTool{
		clientset: clientset,
	}
}

// Name returns the tool name.
func (t *CheckDeploymentHealthTool) Name() string {
	return "check_deployment_health"
}

// Description returns the tool description.
func (t *CheckDeploymentHealthTool) Description() string {
	return "Check the health status of a Kubernetes deployment, including pod status and recent events"
}

// IsLongRunning returns false as this is a quick operation.
func (t *CheckDeploymentHealthTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *CheckDeploymentHealthTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *CheckDeploymentHealthTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *CheckDeploymentHealthTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"name": {
					Type:        "string",
					Description: "The name of the deployment to check",
				},
				"namespace": {
					Type:        "string",
					Description: "The namespace of the deployment",
				},
			},
			Required: []string{"name", "namespace"},
		},
	}
}

// Run executes the tool.
func (t *CheckDeploymentHealthTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	// Extract parameters
	name, ok := argsMap["name"].(string)
	if !ok || name == "" {
		return map[string]any{"error": "name is required"}, nil
	}

	namespace, ok := argsMap["namespace"].(string)
	if !ok || namespace == "" {
		return map[string]any{"error": "namespace is required"}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get deployment
	deployment, err := t.clientset.AppsV1().Deployments(namespace).Get(timeoutCtx, name, metav1.GetOptions{})
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to get deployment: %v", err)}, nil
	}

	// Get pods for this deployment
	labelSelector := fmt.Sprintf("app.kubernetes.io/name=%s", name)
	pods, err := t.clientset.CoreV1().Pods(namespace).List(timeoutCtx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to list pods: %v", err)}, nil
	}

	// Collect pod info
	podInfos := make([]HealthPodInfo, 0, len(pods.Items))
	for _, pod := range pods.Items {
		// Check if pod is ready
		ready := false
		for _, cond := range pod.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = true
				break
			}
		}

		age := time.Since(pod.CreationTimestamp.Time)
		podInfos = append(podInfos, HealthPodInfo{
			Name:   pod.Name,
			Ready:  ready,
			Status: string(pod.Status.Phase),
			Age:    formatDuration(age),
		})
	}

	// Get recent events
	events, err := t.clientset.CoreV1().Events(namespace).List(timeoutCtx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", name),
	})
	if err != nil {
		// Non-fatal, continue without events
		events = nil
	}

	eventInfos := make([]HealthEventInfo, 0)
	if events != nil {
		// Get last 5 events
		start := 0
		if len(events.Items) > 5 {
			start = len(events.Items) - 5
		}
		for _, event := range events.Items[start:] {
			age := time.Since(event.LastTimestamp.Time)
			eventInfos = append(eventInfos, HealthEventInfo{
				Type:    event.Type,
				Reason:  event.Reason,
				Message: event.Message,
				Age:     formatDuration(age),
			})
		}
	}

	// Determine overall health
	replicas := int32(1)
	if deployment.Spec.Replicas != nil {
		replicas = *deployment.Spec.Replicas
	}
	readyReplicas := deployment.Status.ReadyReplicas

	healthy := readyReplicas >= replicas

	message := ""
	if healthy {
		message = fmt.Sprintf("Deployment %s is healthy: %d/%d replicas ready", name, readyReplicas, replicas)
	} else {
		message = fmt.Sprintf("Deployment %s is not healthy: %d/%d replicas ready", name, readyReplicas, replicas)
	}

	return map[string]any{
		"healthy":        healthy,
		"replicas":       replicas,
		"ready_replicas": readyReplicas,
		"pods":           podInfos,
		"events":         eventInfos,
		"message":        message,
	}, nil
}
