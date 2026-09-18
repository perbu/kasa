package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// RemoveSecretKeyTool removes individual keys from a Kubernetes secret. It is
// the inverse of create_secret, which merges keys into an existing secret.
// To delete an entire secret, use delete_resource instead.
type RemoveSecretKeyTool struct {
	clientset *kubernetes.Clientset
}

// NewRemoveSecretKeyTool creates a new RemoveSecretKeyTool.
func NewRemoveSecretKeyTool(clientset *kubernetes.Clientset) *RemoveSecretKeyTool {
	return &RemoveSecretKeyTool{clientset: clientset}
}

func (t *RemoveSecretKeyTool) Name() string { return "remove_secret_key" }
func (t *RemoveSecretKeyTool) Description() string {
	return "Remove one or more keys from a Kubernetes secret, leaving the remaining keys intact. To delete an entire secret, use delete_resource."
}
func (t *RemoveSecretKeyTool) IsLongRunning() bool    { return false }
func (t *RemoveSecretKeyTool) Category() ToolCategory { return CategoryMutating }

func (t *RemoveSecretKeyTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

func (t *RemoveSecretKeyTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"name": {
					Type:        "string",
					Description: "The secret name",
				},
				"namespace": {
					Type:        "string",
					Description: "The namespace holding the secret",
				},
				"keys": {
					Type:        "array",
					Description: "Names of the keys to remove from the secret's data",
					Items: &genai.Schema{
						Type:        "string",
						Description: "Key name",
					},
				},
			},
			Required: []string{"name", "namespace", "keys"},
		},
	}
}

func (t *RemoveSecretKeyTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	name, _ := argsMap["name"].(string)
	if name == "" {
		return errorResult("name is required")
	}

	namespace, _ := argsMap["namespace"].(string)
	if namespace == "" {
		return errorResult("namespace is required")
	}

	keysRaw, ok := argsMap["keys"].([]any)
	if !ok || len(keysRaw) == 0 {
		return errorResult("keys is required and must be a non-empty array of key names")
	}

	requested := make([]string, 0, len(keysRaw))
	for _, k := range keysRaw {
		s, ok := k.(string)
		if !ok || s == "" {
			return errorResult("each entry in keys must be a non-empty string")
		}
		requested = append(requested, s)
	}

	timeoutCtx, cancel := withToolTimeout(ctx, 30*time.Second)
	defer cancel()

	secret, err := t.clientset.CoreV1().Secrets(namespace).Get(timeoutCtx, name, metav1.GetOptions{})
	if err != nil {
		return errorResultf("failed to get secret: %v", err)
	}

	// Split the requested keys into those present on the secret and those that
	// are not. A JSON patch "remove" on a missing path fails, so only patch the
	// ones that exist and report the rest back.
	var present, missing []string
	seen := make(map[string]bool, len(requested))
	for _, k := range requested {
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, ok := secret.Data[k]; ok {
			present = append(present, k)
		} else {
			missing = append(missing, k)
		}
	}

	if len(present) == 0 {
		return errorResultf("secret %s/%s has none of the requested keys: %s", namespace, name, strings.Join(missing, ", "))
	}

	// Patch rather than update, so keys added concurrently by another writer
	// (e.g. create_secret) are not clobbered.
	ops := make([]map[string]any, 0, len(present))
	for _, k := range present {
		ops = append(ops, map[string]any{
			"op":   "remove",
			"path": "/data/" + escapeJSONPointer(k),
		})
	}
	patch, err := json.Marshal(ops)
	if err != nil {
		return errorResultf("failed to build patch: %v", err)
	}

	updated, err := t.clientset.CoreV1().Secrets(namespace).Patch(timeoutCtx, name, types.JSONPatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return errorResultf("failed to remove keys from secret: %v", err)
	}

	remaining := make([]string, 0, len(updated.Data))
	for k := range updated.Data {
		remaining = append(remaining, k)
	}
	sort.Strings(remaining)
	sort.Strings(present)
	sort.Strings(missing)

	result := map[string]any{
		"success":        true,
		"name":           name,
		"namespace":      namespace,
		"removed_keys":   present,
		"remaining_keys": remaining,
		"message":        fmt.Sprintf("Removed %d key(s) from secret %s/%s", len(present), namespace, name),
	}
	if len(missing) > 0 {
		result["missing_keys"] = missing
	}
	if len(remaining) == 0 {
		result["note"] = "The secret now has no data keys. Use delete_resource to remove the secret itself."
	}
	return result, nil
}

// escapeJSONPointer escapes a key for use as a JSON Pointer path segment
// (RFC 6901): '~' becomes '~0' and '/' becomes '~1'.
func escapeJSONPointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
