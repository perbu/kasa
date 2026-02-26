package tools

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const managedByLabel = "app.kubernetes.io/managed-by"
const managedByValue = "kasa"

// ensureManagedByLabel sets the app.kubernetes.io/managed-by: kasa label on an
// unstructured object, preserving any existing labels.
func ensureManagedByLabel(obj *unstructured.Unstructured) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[managedByLabel] = managedByValue
	obj.SetLabels(labels)
}

// parseToolArgs normalizes the args parameter from ADK tool calls into a map.
// ADK may pass args as map[string]any or as a JSON string.
func parseToolArgs(args any) (map[string]any, error) {
	if args == nil {
		return make(map[string]any), nil
	}
	if m, ok := args.(map[string]any); ok {
		return m, nil
	}
	if s, ok := args.(string); ok {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, fmt.Errorf("invalid arguments format")
		}
		return m, nil
	}
	return nil, fmt.Errorf("invalid arguments type")
}

// errorResult returns a tool error response. This is the standard way tools
// report non-fatal errors back to the LLM.
func errorResult(msg string) (map[string]any, error) {
	return map[string]any{"error": msg}, nil
}

// errorResultf returns a formatted tool error response.
func errorResultf(format string, args ...any) (map[string]any, error) {
	return map[string]any{"error": fmt.Sprintf(format, args...)}, nil
}

// namespacedClient returns the appropriate dynamic resource client for a given
// GVR, scoped to a namespace for namespaced resources or cluster-wide otherwise.
func namespacedClient(dc dynamic.Interface, gvr schema.GroupVersionResource, namespace string, namespaced bool) dynamic.ResourceInterface {
	if namespaced && namespace != "" {
		return dc.Resource(gvr).Namespace(namespace)
	}
	return dc.Resource(gvr)
}
