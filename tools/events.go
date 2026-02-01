package tools

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// EventInfo contains information about a Kubernetes event.
type EventInfo struct {
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Count          int32  `json:"count"`
	FirstTimestamp string `json:"first_timestamp"`
	LastTimestamp  string `json:"last_timestamp"`
	Source         string `json:"source"`
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"involved_object"`
}

// GetEventsTool provides the get_events tool for the agent.
type GetEventsTool struct {
	clientset *kubernetes.Clientset
}

// NewGetEventsTool creates a new GetEventsTool.
func NewGetEventsTool(clientset *kubernetes.Clientset) *GetEventsTool {
	return &GetEventsTool{
		clientset: clientset,
	}
}

// Name returns the tool name.
func (t *GetEventsTool) Name() string {
	return "get_events"
}

// Description returns the tool description.
func (t *GetEventsTool) Description() string {
	return "List Kubernetes events in a namespace for debugging failed deployments, pods, and other resources"
}

// IsLongRunning returns false as this is a quick operation.
func (t *GetEventsTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *GetEventsTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *GetEventsTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *GetEventsTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "The namespace to list events from",
				},
				"resource_name": {
					Type:        "string",
					Description: "Optional: filter events by involved object name (e.g., 'my-pod')",
				},
				"resource_kind": {
					Type:        "string",
					Description: "Optional: filter events by involved object kind (e.g., 'Pod', 'Deployment', 'ReplicaSet')",
				},
			},
			Required: []string{"namespace"},
		},
	}
}

// Run executes the tool.
func (t *GetEventsTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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
	if namespace == "" {
		return map[string]any{"error": "namespace is required"}, nil
	}

	resourceName := ""
	if rn, ok := argsMap["resource_name"].(string); ok {
		resourceName = rn
	}

	resourceKind := ""
	if rk, ok := argsMap["resource_kind"].(string); ok {
		resourceKind = rk
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build field selector for filtering
	fieldSelector := ""
	if resourceName != "" {
		fieldSelector = "involvedObject.name=" + resourceName
	}
	if resourceKind != "" {
		if fieldSelector != "" {
			fieldSelector += ","
		}
		fieldSelector += "involvedObject.kind=" + resourceKind
	}

	events, err := t.clientset.CoreV1().Events(namespace).List(timeoutCtx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}

	// Sort by LastTimestamp descending (most recent first)
	sort.Slice(events.Items, func(i, j int) bool {
		ti := events.Items[i].LastTimestamp.Time
		tj := events.Items[j].LastTimestamp.Time
		// If LastTimestamp is zero, use EventTime
		if ti.IsZero() {
			ti = events.Items[i].EventTime.Time
		}
		if tj.IsZero() {
			tj = events.Items[j].EventTime.Time
		}
		return ti.After(tj)
	})

	// Limit to 50 events
	maxEvents := 50
	if len(events.Items) > maxEvents {
		events.Items = events.Items[:maxEvents]
	}

	result := make([]EventInfo, 0, len(events.Items))
	for _, event := range events.Items {
		info := EventInfo{
			Type:    event.Type,
			Reason:  event.Reason,
			Message: event.Message,
			Count:   event.Count,
		}

		// Format timestamps
		if !event.FirstTimestamp.IsZero() {
			info.FirstTimestamp = event.FirstTimestamp.Format(time.RFC3339)
		} else if !event.EventTime.IsZero() {
			info.FirstTimestamp = event.EventTime.Format(time.RFC3339)
		}

		if !event.LastTimestamp.IsZero() {
			info.LastTimestamp = event.LastTimestamp.Format(time.RFC3339)
		} else if !event.EventTime.IsZero() {
			info.LastTimestamp = event.EventTime.Format(time.RFC3339)
		}

		// Source component
		info.Source = event.Source.Component
		if event.Source.Host != "" {
			info.Source += "/" + event.Source.Host
		}

		// Involved object
		info.InvolvedObject.Kind = event.InvolvedObject.Kind
		info.InvolvedObject.Name = event.InvolvedObject.Name
		info.InvolvedObject.Namespace = event.InvolvedObject.Namespace

		result = append(result, info)
	}

	return map[string]any{
		"events": result,
		"count":  len(result),
	}, nil
}
