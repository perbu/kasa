package tools

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// base62Charset is the character set used for generated passwords.
	base62Charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	// defaultPasswordLength is the default length for generated passwords.
	defaultPasswordLength = 32
)

// CreateSecretTool creates Kubernetes secrets with generated or user-provided values.
type CreateSecretTool struct {
	clientset *kubernetes.Clientset
	directIO  *DirectIO
}

// NewCreateSecretTool creates a new CreateSecretTool.
func NewCreateSecretTool(clientset *kubernetes.Clientset, directIO *DirectIO) *CreateSecretTool {
	return &CreateSecretTool{
		clientset: clientset,
		directIO:  directIO,
	}
}

func (t *CreateSecretTool) Name() string        { return "create_secret" }
func (t *CreateSecretTool) Description() string  { return "Create a Kubernetes secret with generated passwords or user-provided values. Secret values never pass through the LLM." }
func (t *CreateSecretTool) IsLongRunning() bool  { return false }
func (t *CreateSecretTool) Category() ToolCategory { return CategoryMutating }

func (t *CreateSecretTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

func (t *CreateSecretTool) Declaration() *genai.FunctionDeclaration {
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
					Description: "The target namespace",
				},
				"keys": {
					Type:        "array",
					Description: "Secret keys to create",
					Items: &genai.Schema{
						Type: "object",
						Properties: map[string]*genai.Schema{
							"name": {
								Type:        "string",
								Description: "The key name (e.g., 'password', 'api-key')",
							},
							"source": {
								Type:        "string",
								Description: "Where the value comes from: 'generated' (random password) or 'user' (prompt user for input)",
								Enum:        []string{"generated", "user"},
							},
						},
						Required: []string{"name", "source"},
					},
				},
				"length": {
					Type:        "integer",
					Description: "Password length for generated keys (default: 32)",
				},
				"type": {
					Type:        "string",
					Description: "Secret type (default: Opaque). Common types: Opaque, kubernetes.io/tls, kubernetes.io/dockerconfigjson",
				},
			},
			Required: []string{"name", "namespace", "keys"},
		},
	}
}

func (t *CreateSecretTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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
		return errorResult("keys is required and must be a non-empty array")
	}

	passwordLength := defaultPasswordLength
	if l, ok := argsMap["length"].(float64); ok && l > 0 {
		passwordLength = int(l)
	}

	secretType := corev1.SecretTypeOpaque
	if st, ok := argsMap["type"].(string); ok && st != "" {
		secretType = corev1.SecretType(st)
	}

	// Process each key
	data := make(map[string][]byte)
	keyNames := make([]string, 0, len(keysRaw))
	var generatedKeys []string

	for _, keyRaw := range keysRaw {
		keyMap, ok := keyRaw.(map[string]any)
		if !ok {
			return errorResult("each key must be an object with 'name' and 'source'")
		}

		keyName, _ := keyMap["name"].(string)
		if keyName == "" {
			return errorResult("each key must have a 'name'")
		}

		source, _ := keyMap["source"].(string)
		if source == "" {
			return errorResult("each key must have a 'source' ('generated' or 'user')")
		}

		keyNames = append(keyNames, keyName)

		switch source {
		case "generated":
			password, genErr := generatePassword(passwordLength)
			if genErr != nil {
				return errorResultf("failed to generate password for key '%s': %v", keyName, genErr)
			}
			data[keyName] = []byte(password)
			generatedKeys = append(generatedKeys, keyName)

		case "user":
			value, inputErr := t.directIO.RequestInput(fmt.Sprintf("Enter value for key '%s': ", keyName))
			if inputErr != nil {
				return errorResultf("failed to get input for key '%s': %v", keyName, inputErr)
			}
			if value == "" {
				return errorResultf("empty value provided for key '%s'", keyName)
			}
			data[keyName] = []byte(value)

		default:
			return errorResultf("invalid source '%s' for key '%s': must be 'generated' or 'user'", source, keyName)
		}
	}

	// Create the K8s secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type: secretType,
		Data: data,
	}

	timeoutCtx, cancel := withToolTimeout(ctx, 30*time.Second)
	defer cancel()

	// Try create first; on AlreadyExists, merge keys into the existing secret
	var action string
	_, createErr := t.clientset.CoreV1().Secrets(namespace).Create(timeoutCtx, secret, metav1.CreateOptions{})
	if createErr == nil {
		action = "created"
	} else if !k8serrors.IsAlreadyExists(createErr) {
		return errorResultf("failed to create secret: %v", createErr)
	} else {
		// Secret exists — fetch, merge keys, and update
		existing, getErr := t.clientset.CoreV1().Secrets(namespace).Get(timeoutCtx, name, metav1.GetOptions{})
		if getErr != nil {
			return errorResultf("failed to get existing secret: %v", getErr)
		}
		if existing.Data == nil {
			existing.Data = make(map[string][]byte)
		}
		for k, v := range data {
			existing.Data[k] = v
		}
		// Only override secret type if explicitly provided; preserve existing type otherwise
		if _, ok := argsMap["type"]; ok {
			existing.Type = secretType
		}
		_, updateErr := t.clientset.CoreV1().Secrets(namespace).Update(timeoutCtx, existing, metav1.UpdateOptions{})
		if updateErr != nil {
			return errorResultf("failed to update secret: %v", updateErr)
		}
		action = "updated"
	}

	// Display generated passwords directly to the user (not to the LLM)
	if len(generatedKeys) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Secret '%s/%s' %s. Generated values:\n\n", namespace, name, action))
		for _, keyName := range generatedKeys {
			sb.WriteString(fmt.Sprintf("  %-20s %s\n", keyName, string(data[keyName])))
		}
		t.directIO.Print(sb.String())
	}

	return map[string]any{
		"status":    action,
		"name":      name,
		"namespace": namespace,
		"key_names": keyNames,
	}, nil
}

// generatePassword generates a cryptographically random password of the given
// length using the base62 charset (a-zA-Z0-9).
func generatePassword(length int) (string, error) {
	charsetLen := big.NewInt(int64(len(base62Charset)))
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", err
		}
		b[i] = base62Charset[n.Int64()]
	}
	return string(b), nil
}
