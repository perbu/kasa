package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// WaitForConditionTool provides the wait_for_condition tool for the agent.
type WaitForConditionTool struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
}

// NewWaitForConditionTool creates a new WaitForConditionTool.
func NewWaitForConditionTool(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface) *WaitForConditionTool {
	return &WaitForConditionTool{
		clientset:     clientset,
		dynamicClient: dynamicClient,
	}
}

// Name returns the tool name.
func (t *WaitForConditionTool) Name() string {
	return "wait_for_condition"
}

// Description returns the tool description.
func (t *WaitForConditionTool) Description() string {
	return `Wait for a Kubernetes resource to reach a desired condition. Polls the resource until the condition is met or timeout occurs.

Supported conditions by resource type:
- deployment: available, progressing, complete
- pod: ready, running, succeeded, deleted
- job: complete, failed
- statefulset: ready
- pvc: bound
- any resource: deleted, exists`
}

// IsLongRunning returns true as this tool may poll for extended periods.
func (t *WaitForConditionTool) IsLongRunning() bool {
	return true
}

// ProcessRequest adds this tool to the LLM request.
func (t *WaitForConditionTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *WaitForConditionTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"kind": {
					Type:        "string",
					Description: "The resource type (deployment, pod, job, statefulset, pvc, etc.)",
				},
				"name": {
					Type:        "string",
					Description: "The name of the resource",
				},
				"namespace": {
					Type:        "string",
					Description: "The namespace of the resource (default: 'default')",
				},
				"condition": {
					Type:        "string",
					Description: "The condition to wait for (e.g., available, ready, complete, deleted, exists)",
				},
				"timeout": {
					Type:        "integer",
					Description: "Maximum time to wait in seconds (default: 120, max: 300)",
				},
			},
			Required: []string{"kind", "name", "condition"},
		},
	}
}

// Run executes the tool.
func (t *WaitForConditionTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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
	kind, ok := argsMap["kind"].(string)
	if !ok || kind == "" {
		return map[string]any{"error": "kind is required"}, nil
	}

	name, ok := argsMap["name"].(string)
	if !ok || name == "" {
		return map[string]any{"error": "name is required"}, nil
	}

	condition, ok := argsMap["condition"].(string)
	if !ok || condition == "" {
		return map[string]any{"error": "condition is required"}, nil
	}

	// Extract optional parameters
	namespace := "default"
	if ns, ok := argsMap["namespace"].(string); ok && ns != "" {
		namespace = ns
	}

	timeout := 120
	if to, ok := argsMap["timeout"].(float64); ok {
		timeout = int(to)
	} else if to, ok := argsMap["timeout"].(int); ok {
		timeout = to
	}
	// Cap timeout at 5 minutes
	if timeout > 300 {
		timeout = 300
	}
	if timeout < 1 {
		timeout = 1
	}

	// Normalize kind name
	normalizedKind := NormalizeKindName(kind)

	// Start polling
	startTime := time.Now()
	pollInterval := 2 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	timeoutDuration := time.Duration(timeout) * time.Second
	polls := 0

	for {
		polls++

		met, state, err := t.checkCondition(normalizedKind, name, namespace, condition)
		if err != nil {
			// For "deleted" condition, NotFound error means success
			if condition == "deleted" && errors.IsNotFound(err) {
				elapsed := time.Since(startTime).Seconds()
				return map[string]any{
					"success":         true,
					"condition_met":   true,
					"elapsed_seconds": int(elapsed),
					"polls":           polls,
					"final_state":     "Resource deleted",
					"message":         fmt.Sprintf("%s %s/%s has been deleted", kind, namespace, name),
				}, nil
			}
			// For other errors, report them
			return map[string]any{
				"success":         false,
				"condition_met":   false,
				"elapsed_seconds": int(time.Since(startTime).Seconds()),
				"polls":           polls,
				"final_state":     state,
				"message":         fmt.Sprintf("Error checking condition: %v", err),
			}, nil
		}

		if met {
			elapsed := time.Since(startTime).Seconds()
			return map[string]any{
				"success":         true,
				"condition_met":   true,
				"elapsed_seconds": int(elapsed),
				"polls":           polls,
				"final_state":     state,
				"message":         fmt.Sprintf("%s %s/%s is %s", kind, namespace, name, condition),
			}, nil
		}

		// Check timeout
		if time.Since(startTime) >= timeoutDuration {
			return map[string]any{
				"success":         false,
				"condition_met":   false,
				"elapsed_seconds": timeout,
				"polls":           polls,
				"final_state":     state,
				"message":         fmt.Sprintf("Timeout waiting for %s %s/%s to be %s", kind, namespace, name, condition),
			}, nil
		}

		// Wait for next poll
		select {
		case <-ticker.C:
			continue
		case <-time.After(timeoutDuration - time.Since(startTime)):
			// Final check before timeout
			met, state, err := t.checkCondition(normalizedKind, name, namespace, condition)
			if err == nil && met {
				elapsed := time.Since(startTime).Seconds()
				return map[string]any{
					"success":         true,
					"condition_met":   true,
					"elapsed_seconds": int(elapsed),
					"polls":           polls + 1,
					"final_state":     state,
					"message":         fmt.Sprintf("%s %s/%s is %s", kind, namespace, name, condition),
				}, nil
			}
			return map[string]any{
				"success":         false,
				"condition_met":   false,
				"elapsed_seconds": timeout,
				"polls":           polls,
				"final_state":     state,
				"message":         fmt.Sprintf("Timeout waiting for %s %s/%s to be %s", kind, namespace, name, condition),
			}, nil
		}
	}
}

// checkCondition checks if the resource meets the specified condition.
// Returns (conditionMet, statusMessage, error).
func (t *WaitForConditionTool) checkCondition(kind, name, namespace, condition string) (bool, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch kind {
	case "deployment":
		return t.checkDeploymentCondition(ctx, name, namespace, condition)
	case "pod":
		return t.checkPodCondition(ctx, name, namespace, condition)
	case "job":
		return t.checkJobCondition(ctx, name, namespace, condition)
	case "statefulset":
		return t.checkStatefulSetCondition(ctx, name, namespace, condition)
	case "persistentvolumeclaim":
		return t.checkPVCCondition(ctx, name, namespace, condition)
	default:
		// For unknown kinds, only support exists/deleted conditions
		return t.checkGenericCondition(ctx, kind, name, namespace, condition)
	}
}

// checkDeploymentCondition checks deployment-specific conditions.
func (t *WaitForConditionTool) checkDeploymentCondition(ctx context.Context, name, namespace, condition string) (bool, string, error) {
	dep, err := t.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, "", err
	}

	replicas := int32(1)
	if dep.Spec.Replicas != nil {
		replicas = *dep.Spec.Replicas
	}
	state := fmt.Sprintf("Ready: %d/%d replicas", dep.Status.ReadyReplicas, replicas)

	switch condition {
	case "available":
		for _, cond := range dep.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
				return true, state, nil
			}
		}
		return false, state, nil

	case "progressing":
		for _, cond := range dep.Status.Conditions {
			if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionTrue {
				return true, state, nil
			}
		}
		return false, state, nil

	case "complete":
		// All replicas are ready and updated
		if dep.Status.ReadyReplicas == replicas &&
			dep.Status.UpdatedReplicas == replicas &&
			dep.Status.Replicas == replicas {
			return true, state, nil
		}
		return false, state, nil

	case "exists":
		return true, state, nil

	case "deleted":
		// If we got here, the resource exists, so it's not deleted
		return false, state, nil

	default:
		return false, state, fmt.Errorf("unsupported condition '%s' for deployment", condition)
	}
}

// checkPodCondition checks pod-specific conditions.
func (t *WaitForConditionTool) checkPodCondition(ctx context.Context, name, namespace, condition string) (bool, string, error) {
	pod, err := t.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, "", err
	}

	state := fmt.Sprintf("Phase: %s", pod.Status.Phase)

	switch condition {
	case "ready":
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, "Ready: all containers ready", nil
			}
		}
		return false, state, nil

	case "running":
		if pod.Status.Phase == corev1.PodRunning {
			return true, state, nil
		}
		return false, state, nil

	case "succeeded":
		if pod.Status.Phase == corev1.PodSucceeded {
			return true, state, nil
		}
		return false, state, nil

	case "exists":
		return true, state, nil

	case "deleted":
		return false, state, nil

	default:
		return false, state, fmt.Errorf("unsupported condition '%s' for pod", condition)
	}
}

// checkJobCondition checks job-specific conditions.
func (t *WaitForConditionTool) checkJobCondition(ctx context.Context, name, namespace, condition string) (bool, string, error) {
	job, err := t.clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, "", err
	}

	state := fmt.Sprintf("Active: %d, Succeeded: %d, Failed: %d", job.Status.Active, job.Status.Succeeded, job.Status.Failed)

	switch condition {
	case "complete":
		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				return true, state, nil
			}
		}
		return false, state, nil

	case "failed":
		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return true, state, nil
			}
		}
		return false, state, nil

	case "exists":
		return true, state, nil

	case "deleted":
		return false, state, nil

	default:
		return false, state, fmt.Errorf("unsupported condition '%s' for job", condition)
	}
}

// checkStatefulSetCondition checks statefulset-specific conditions.
func (t *WaitForConditionTool) checkStatefulSetCondition(ctx context.Context, name, namespace, condition string) (bool, string, error) {
	sts, err := t.clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, "", err
	}

	replicas := int32(1)
	if sts.Spec.Replicas != nil {
		replicas = *sts.Spec.Replicas
	}
	state := fmt.Sprintf("Ready: %d/%d replicas", sts.Status.ReadyReplicas, replicas)

	switch condition {
	case "ready":
		if sts.Status.ReadyReplicas == replicas {
			return true, state, nil
		}
		return false, state, nil

	case "exists":
		return true, state, nil

	case "deleted":
		return false, state, nil

	default:
		return false, state, fmt.Errorf("unsupported condition '%s' for statefulset", condition)
	}
}

// checkPVCCondition checks pvc-specific conditions.
func (t *WaitForConditionTool) checkPVCCondition(ctx context.Context, name, namespace, condition string) (bool, string, error) {
	pvc, err := t.clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, "", err
	}

	state := fmt.Sprintf("Phase: %s", pvc.Status.Phase)

	switch condition {
	case "bound":
		if pvc.Status.Phase == corev1.ClaimBound {
			return true, state, nil
		}
		return false, state, nil

	case "exists":
		return true, state, nil

	case "deleted":
		return false, state, nil

	default:
		return false, state, fmt.Errorf("unsupported condition '%s' for pvc", condition)
	}
}

// checkGenericCondition checks exists/deleted conditions for any resource type.
func (t *WaitForConditionTool) checkGenericCondition(ctx context.Context, kind, name, namespace, condition string) (bool, string, error) {
	// Look up GVR for the kind
	gvr, ok := LookupGVR(kind)
	if !ok {
		return false, "", fmt.Errorf("unknown resource kind: %s", kind)
	}

	var err error
	if IsNamespaced(kind) {
		_, err = t.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		_, err = t.dynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}

	if err != nil {
		if errors.IsNotFound(err) {
			if condition == "deleted" {
				return true, "Resource deleted", nil
			}
			return false, "Resource not found", err
		}
		return false, "", err
	}

	// Resource exists
	state := "Resource exists"
	switch condition {
	case "exists":
		return true, state, nil
	case "deleted":
		return false, state, nil
	default:
		return false, state, fmt.Errorf("unsupported condition '%s' for %s (only 'exists' and 'deleted' are supported for this resource type)", condition, kind)
	}
}
