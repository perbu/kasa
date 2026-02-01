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
	"k8s.io/client-go/dynamic"
)

// ListResourcesTool provides the list_resources tool for listing any Kubernetes resources.
type ListResourcesTool struct {
	dynamicClient dynamic.Interface
}

// NewListResourcesTool creates a new ListResourcesTool.
func NewListResourcesTool(dynamicClient dynamic.Interface) *ListResourcesTool {
	return &ListResourcesTool{
		dynamicClient: dynamicClient,
	}
}

// Name returns the tool name.
func (t *ListResourcesTool) Name() string {
	return "list_resources"
}

// Description returns the tool description.
func (t *ListResourcesTool) Description() string {
	return "List any type of Kubernetes resources. Supports core resources and CRDs like HTTPRoute, Gateway, Certificate. Use 'kind' to specify the resource type (e.g., httproute, gateway, certificate, deployment)."
}

// IsLongRunning returns false as this is a quick operation.
func (t *ListResourcesTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *ListResourcesTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ListResourcesTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"kind": {
					Type:        "string",
					Description: "The resource kind to list (e.g., httproute, gateway, certificate, deployment, pod, service). Aliases like 'gw', 'deploy', 'svc' are supported.",
				},
				"namespace": {
					Type:        "string",
					Description: "Filter by namespace. If empty, lists across all namespaces for namespaced resources.",
				},
				"api_version": {
					Type:        "string",
					Description: "API version for unknown CRDs (e.g., 'gateway.networking.k8s.io/v1'). Required only for resources not in the known list.",
				},
				"label_selector": {
					Type:        "string",
					Description: "Filter by label selector (e.g., 'app=nginx,env=prod')",
				},
			},
			Required: []string{"kind"},
		},
	}
}

// Run executes the tool.
func (t *ListResourcesTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	kind, ok := argsMap["kind"].(string)
	if !ok || kind == "" {
		return map[string]any{"error": "kind is required"}, nil
	}

	namespace := ""
	if ns, ok := argsMap["namespace"].(string); ok {
		namespace = ns
	}

	apiVersion := ""
	if av, ok := argsMap["api_version"].(string); ok {
		apiVersion = av
	}

	labelSelector := ""
	if ls, ok := argsMap["label_selector"].(string); ok {
		labelSelector = ls
	}

	// Build GVR from kind and api_version
	gvr, found := BuildGVRFromKindAndAPIVersion(kind, apiVersion)
	if !found && apiVersion == "" {
		return map[string]any{
			"error": fmt.Sprintf("Unknown resource kind '%s'. Provide api_version for custom resources. Known kinds include: deployment, service, pod, configmap, secret, ingress, httproute, gateway, certificate.", kind),
		}, nil
	}

	// Check if resource is namespaced
	namespaced := IsNamespaced(kind)

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	listOptions := metav1.ListOptions{}
	if labelSelector != "" {
		listOptions.LabelSelector = labelSelector
	}

	// Get the resource interface
	var resourceClient dynamic.ResourceInterface
	if namespaced && namespace != "" {
		resourceClient = t.dynamicClient.Resource(gvr).Namespace(namespace)
	} else if namespaced {
		// List across all namespaces
		resourceClient = t.dynamicClient.Resource(gvr)
	} else {
		// Cluster-scoped resource
		resourceClient = t.dynamicClient.Resource(gvr)
	}

	list, err := resourceClient.List(timeoutCtx, listOptions)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to list %s: %v", kind, err)}, nil
	}

	// Build result summary
	items := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		summary := map[string]any{
			"name": item.GetName(),
		}
		if ns := item.GetNamespace(); ns != "" {
			summary["namespace"] = ns
		}
		if created := item.GetCreationTimestamp(); !created.IsZero() {
			summary["created"] = created.Format(time.RFC3339)
		}
		if labels := item.GetLabels(); len(labels) > 0 {
			summary["labels"] = labels
		}

		// Extract status if present
		if status, found, _ := unstructuredNestedField(item.Object, "status"); found {
			// For common resource types, extract meaningful status info
			statusSummary := extractStatusSummary(status, NormalizeKindName(kind))
			if statusSummary != nil {
				summary["status"] = statusSummary
			}
		}

		items = append(items, summary)
	}

	result := map[string]any{
		"kind":       NormalizeKindName(kind),
		"apiVersion": gvr.GroupVersion().String(),
		"count":      len(items),
		"items":      items,
	}

	if namespace != "" {
		result["namespace"] = namespace
	} else if namespaced {
		result["scope"] = "all namespaces"
	} else {
		result["scope"] = "cluster"
	}

	return result, nil
}

// unstructuredNestedField gets a nested field from an unstructured object.
func unstructuredNestedField(obj map[string]any, fields ...string) (any, bool, error) {
	var val any = obj
	for _, field := range fields {
		if m, ok := val.(map[string]any); ok {
			val, ok = m[field]
			if !ok {
				return nil, false, nil
			}
		} else {
			return nil, false, nil
		}
	}
	return val, true, nil
}

// extractStatusSummary extracts a meaningful status summary based on the resource kind.
func extractStatusSummary(status any, kind string) map[string]any {
	statusMap, ok := status.(map[string]any)
	if !ok {
		return nil
	}

	summary := make(map[string]any)

	switch kind {
	case "deployment":
		if replicas, ok := statusMap["replicas"]; ok {
			summary["replicas"] = replicas
		}
		if ready, ok := statusMap["readyReplicas"]; ok {
			summary["ready"] = ready
		}
		if available, ok := statusMap["availableReplicas"]; ok {
			summary["available"] = available
		}

	case "pod":
		if phase, ok := statusMap["phase"]; ok {
			summary["phase"] = phase
		}

	case "service":
		if loadBalancer, ok := statusMap["loadBalancer"].(map[string]any); ok {
			if ingress, ok := loadBalancer["ingress"].([]any); ok && len(ingress) > 0 {
				summary["loadBalancerIP"] = ingress
			}
		}

	case "httproute", "gateway", "tcproute", "grpcroute":
		// Gateway API resources often have conditions
		if conditions, ok := statusMap["conditions"].([]any); ok && len(conditions) > 0 {
			conditionSummary := make([]string, 0)
			for _, c := range conditions {
				if cond, ok := c.(map[string]any); ok {
					condType, _ := cond["type"].(string)
					condStatus, _ := cond["status"].(string)
					if condType != "" {
						conditionSummary = append(conditionSummary, fmt.Sprintf("%s=%s", condType, condStatus))
					}
				}
			}
			if len(conditionSummary) > 0 {
				summary["conditions"] = conditionSummary
			}
		}

	case "certificate":
		// cert-manager certificates
		if conditions, ok := statusMap["conditions"].([]any); ok {
			for _, c := range conditions {
				if cond, ok := c.(map[string]any); ok {
					if condType, _ := cond["type"].(string); condType == "Ready" {
						summary["ready"] = cond["status"]
						if reason, ok := cond["reason"].(string); ok {
							summary["reason"] = reason
						}
					}
				}
			}
		}
		if notAfter, ok := statusMap["notAfter"]; ok {
			summary["notAfter"] = notAfter
		}

	default:
		// For unknown types, just check for common fields
		if conditions, ok := statusMap["conditions"].([]any); ok && len(conditions) > 0 {
			summary["conditionCount"] = len(conditions)
		}
		if phase, ok := statusMap["phase"]; ok {
			summary["phase"] = phase
		}
	}

	if len(summary) == 0 {
		return nil
	}
	return summary
}
