package tools

import (
	"fmt"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/kubernetes"
)

// GetHelmValuesTool provides the get_helm_values tool.
type GetHelmValuesTool struct {
	clientset *kubernetes.Clientset
}

// NewGetHelmValuesTool creates a new GetHelmValuesTool.
func NewGetHelmValuesTool(clientset *kubernetes.Clientset) *GetHelmValuesTool {
	return &GetHelmValuesTool{clientset: clientset}
}

func (t *GetHelmValuesTool) Name() string { return "get_helm_values" }
func (t *GetHelmValuesTool) Description() string {
	return "Get the user-supplied values for a Helm release. Returns the values as YAML, showing what was customized from chart defaults."
}
func (t *GetHelmValuesTool) IsLongRunning() bool    { return false }
func (t *GetHelmValuesTool) Category() ToolCategory { return CategoryReadOnly }

func (t *GetHelmValuesTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

func (t *GetHelmValuesTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"name": {
					Type:        "string",
					Description: "The Helm release name.",
				},
				"namespace": {
					Type:        "string",
					Description: "The namespace of the Helm release.",
				},
			},
			Required: []string{"name", "namespace"},
		},
	}
}

func (t *GetHelmValuesTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	name, ok := argsMap["name"].(string)
	if !ok || name == "" {
		return errorResult("name is required")
	}

	namespace, ok := argsMap["namespace"].(string)
	if !ok || namespace == "" {
		return errorResult("namespace is required")
	}

	rel, err := findHelmReleaseSecret(ctx, t.clientset, name, namespace)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to get Helm release: %v", err))
	}

	if len(rel.Config) == 0 {
		return map[string]any{
			"name":      rel.Name,
			"namespace": rel.Namespace,
			"values":    "# No user-supplied values (all defaults)",
		}, nil
	}

	yamlBytes, err := yaml.Marshal(rel.Config)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to marshal values to YAML: %v", err))
	}

	return map[string]any{
		"name":      rel.Name,
		"namespace": rel.Namespace,
		"values":    string(yamlBytes),
	}, nil
}
