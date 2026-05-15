package tools

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ShowSecretTool displays secret values directly to the user, bypassing the LLM.
type ShowSecretTool struct {
	clientset *kubernetes.Clientset
	directIO  *DirectIO
}

// NewShowSecretTool creates a new ShowSecretTool.
func NewShowSecretTool(clientset *kubernetes.Clientset, directIO *DirectIO) *ShowSecretTool {
	return &ShowSecretTool{
		clientset: clientset,
		directIO:  directIO,
	}
}

func (t *ShowSecretTool) Name() string        { return "show_secret" }
func (t *ShowSecretTool) Description() string  { return "Display secret values directly to the user (values bypass the LLM entirely)" }
func (t *ShowSecretTool) IsLongRunning() bool  { return false }
func (t *ShowSecretTool) Category() ToolCategory { return CategoryReadOnly }

func (t *ShowSecretTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

func (t *ShowSecretTool) Declaration() *genai.FunctionDeclaration {
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
					Description: "The namespace (defaults to 'default')",
				},
				"keys": {
					Type:        "array",
					Description: "Specific keys to show (default: all keys)",
					Items: &genai.Schema{
						Type:        "string",
						Description: "Key name",
					},
				},
			},
			Required: []string{"name", "namespace"},
		},
	}
}

func (t *ShowSecretTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	name, _ := argsMap["name"].(string)
	if name == "" {
		return errorResult("name is required")
	}

	namespace := "default"
	if ns, _ := argsMap["namespace"].(string); ns != "" {
		namespace = ns
	}

	// Parse optional key filter
	var keyFilter map[string]bool
	if keysRaw, ok := argsMap["keys"].([]any); ok && len(keysRaw) > 0 {
		keyFilter = make(map[string]bool, len(keysRaw))
		for _, k := range keysRaw {
			if s, ok := k.(string); ok {
				keyFilter[s] = true
			}
		}
	}

	// Fetch the secret
	timeoutCtx, cancel := withToolTimeout(ctx, 30*time.Second)
	defer cancel()

	secret, err := t.clientset.CoreV1().Secrets(namespace).Get(timeoutCtx, name, metav1.GetOptions{})
	if err != nil {
		return errorResultf("failed to get secret: %v", err)
	}

	// Decode and display values
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Secret: %s/%s (type: %s)\n\n", namespace, name, secret.Type))

	// Collect and sort keys for stable output
	keys := make([]string, 0, len(secret.Data))
	for k := range secret.Data {
		if keyFilter != nil && !keyFilter[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	displayedKeys := make([]string, 0, len(keys))
	for _, k := range keys {
		value := secret.Data[k]
		// The K8s API returns data already base64-decoded in the typed client,
		// but check if it looks like it was double-encoded
		decoded := string(value)
		if tryDecoded, err := base64.StdEncoding.DecodeString(decoded); err == nil && len(tryDecoded) > 0 && isPrintable(tryDecoded) {
			// Likely double-encoded, use the decoded version
			decoded = string(tryDecoded)
		}
		sb.WriteString(fmt.Sprintf("  %-20s %s\n", k, decoded))
		displayedKeys = append(displayedKeys, k)
	}

	if len(displayedKeys) == 0 {
		sb.WriteString("  (no matching keys)\n")
	}

	t.directIO.Print(sb.String())

	return map[string]any{
		"status":    "displayed_to_user",
		"key_names": displayedKeys,
	}, nil
}

// isPrintable returns true if all bytes in b are printable ASCII or common whitespace.
func isPrintable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' {
			return false
		}
		if c > 0x7e {
			return false
		}
	}
	return true
}
