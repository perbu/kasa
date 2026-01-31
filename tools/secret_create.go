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

// CreateSecretTool provides the create_secret tool for the agent.
type CreateSecretTool struct {
	clientset *kubernetes.Clientset
	manifest  *manifest.Manager
}

// NewCreateSecretTool creates a new CreateSecretTool.
func NewCreateSecretTool(clientset *kubernetes.Clientset, manifest *manifest.Manager) *CreateSecretTool {
	return &CreateSecretTool{
		clientset: clientset,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *CreateSecretTool) Name() string {
	return "create_secret"
}

// Description returns the tool description.
func (t *CreateSecretTool) Description() string {
	return "Create or update a Kubernetes Secret. Saves the manifest to git and applies it to the cluster. WARNING: Secret data will be stored in plaintext in the git repository."
}

// IsLongRunning returns false as this is a quick operation.
func (t *CreateSecretTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *CreateSecretTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *CreateSecretTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"name": {
					Type:        "string",
					Description: "The name of the Secret",
				},
				"namespace": {
					Type:        "string",
					Description: "The target Kubernetes namespace",
				},
				"type": {
					Type:        "string",
					Description: "The secret type (default: Opaque). Common types: Opaque, kubernetes.io/tls, kubernetes.io/dockerconfigjson",
				},
				"string_data": {
					Type:        "object",
					Description: "Key-value pairs for the secret data (as strings, will be base64 encoded by Kubernetes)",
				},
				"labels": {
					Type:        "object",
					Description: "Optional labels to add to the Secret",
				},
			},
			Required: []string{"name", "namespace", "string_data"},
		},
	}
}

// Run executes the tool.
func (t *CreateSecretTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	stringDataMap, ok := argsMap["string_data"].(map[string]any)
	if !ok || len(stringDataMap) == 0 {
		return map[string]any{"error": "string_data is required"}, nil
	}

	// Convert string_data to string map
	stringData := make(map[string]string)
	for k, v := range stringDataMap {
		if vs, ok := v.(string); ok {
			stringData[k] = vs
		} else {
			// Convert non-string values to JSON string
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				return map[string]any{"error": fmt.Sprintf("failed to convert value for key %s: %v", k, err)}, nil
			}
			stringData[k] = string(jsonBytes)
		}
	}

	// Extract optional parameters
	secretType := corev1.SecretTypeOpaque
	if st, ok := argsMap["type"].(string); ok && st != "" {
		secretType = corev1.SecretType(st)
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

	// Build the Secret
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Type:       secretType,
		StringData: stringData,
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(secret)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to marshal secret: %v", err)}, nil
	}

	// Save manifest
	manifestPath, err := t.manifest.SaveManifest(namespace, name, "secret", yamlBytes)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to save manifest: %v", err)}, nil
	}

	// Apply to cluster
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var action string
	existing, err := t.clientset.CoreV1().Secrets(namespace).Get(timeoutCtx, name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return map[string]any{"error": fmt.Sprintf("failed to check existing secret: %v", err)}, nil
		}
		// Create new secret
		_, err = t.clientset.CoreV1().Secrets(namespace).Create(timeoutCtx, secret, metav1.CreateOptions{})
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to create secret: %v", err)}, nil
		}
		action = "created"
	} else {
		// Update existing secret
		secret.ResourceVersion = existing.ResourceVersion
		_, err = t.clientset.CoreV1().Secrets(namespace).Update(timeoutCtx, secret, metav1.UpdateOptions{})
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("failed to update secret: %v", err)}, nil
		}
		action = "updated"
	}

	return map[string]any{
		"success":       true,
		"action":        action,
		"name":          name,
		"namespace":     namespace,
		"type":          string(secretType),
		"keys":          len(stringData),
		"manifest_path": manifestPath,
		"message":       fmt.Sprintf("Secret %s %s in namespace %s", name, action, namespace),
		"warning":       "Secret data is stored in plaintext in the manifest file. Ensure the repository is properly secured.",
	}, nil
}
