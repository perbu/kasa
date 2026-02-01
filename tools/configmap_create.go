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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// CreateConfigMapTool provides the create_configmap tool for the agent.
type CreateConfigMapTool struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewCreateConfigMapTool creates a new CreateConfigMapTool.
func NewCreateConfigMapTool(clientset *kubernetes.Clientset, manifest *manifest.Manager) *CreateConfigMapTool {
	return &CreateConfigMapTool{
		clientset: clientset,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *CreateConfigMapTool) Name() string {
	return "create_configmap"
}

// Description returns the tool description.
func (t *CreateConfigMapTool) Description() string {
	return "Create or update a Kubernetes ConfigMap. Saves the manifest to git and applies it to the cluster."
}

// IsLongRunning returns false as this is a quick operation.
func (t *CreateConfigMapTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *CreateConfigMapTool) Category() ToolCategory {
	return CategoryMutating
}

// ProcessRequest adds this tool to the LLM request.
func (t *CreateConfigMapTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *CreateConfigMapTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"name": {
					Type:        "string",
					Description: "The name of the ConfigMap",
				},
				"namespace": {
					Type:        "string",
					Description: "The target Kubernetes namespace",
				},
				"data": {
					Type:        "object",
					Description: "Key-value pairs for the ConfigMap data",
				},
				"labels": {
					Type:        "object",
					Description: "Optional labels to add to the ConfigMap",
				},
			},
			Required: []string{"name", "namespace", "data"},
		},
	}
}

// Run executes the tool.
func (t *CreateConfigMapTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	dataMap, ok := argsMap["data"].(map[string]any)
	if !ok || len(dataMap) == 0 {
		return map[string]any{"error": "data is required"}, nil
	}

	// Convert data to string map
	data := make(map[string]string)
	for k, v := range dataMap {
		if vs, ok := v.(string); ok {
			data[k] = vs
		} else {
			// Convert non-string values to JSON string
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				return map[string]any{"error": fmt.Sprintf("failed to convert value for key %s: %v", k, err)}, nil
			}
			data[k] = string(jsonBytes)
		}
	}

	// Build labels
	labels := map[string]string{
		"app.kubernetes.io/name":       name,
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

	// Build the ConfigMap
	configMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Data: data,
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(configMap)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to marshal configmap: %v", err)}, nil
	}

	// Save manifest
	manifestPath, err := t.manifest.SaveManifest(namespace, name, "configmap", yamlBytes)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to save manifest: %v", err)}, nil
	}

	// Apply to cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var action string
	existing, err := t.clientset.CoreV1().ConfigMaps(namespace).Get(timeoutCtx, name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return map[string]any{"error": fmt.Sprintf("failed to check existing configmap: %v", err)}, nil
		}
		// Create new configmap
		_, err = t.clientset.CoreV1().ConfigMaps(namespace).Create(timeoutCtx, configMap, metav1.CreateOptions{})
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to create configmap: %v", err)}, nil
		}
		action = "created"
	} else {
		// Update existing configmap
		configMap.ResourceVersion = existing.ResourceVersion
		_, err = t.clientset.CoreV1().ConfigMaps(namespace).Update(timeoutCtx, configMap, metav1.UpdateOptions{})
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to update configmap: %v", err)}, nil
		}
		action = "updated"
	}

	return map[string]any{
		"success":       true,
		"action":        action,
		"name":          name,
		"namespace":     namespace,
		"keys":          len(data),
		"manifest_path": manifestPath,
		"message":       fmt.Sprintf("ConfigMap %s %s in namespace %s", name, action, namespace),
	}, nil
}
