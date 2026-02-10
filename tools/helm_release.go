package tools

import (
	"encoding/json"
	"fmt"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	"k8s.io/client-go/kubernetes"
)

// GetHelmReleaseTool provides the get_helm_release tool.
type GetHelmReleaseTool struct {
	clientset *kubernetes.Clientset
}

// NewGetHelmReleaseTool creates a new GetHelmReleaseTool.
func NewGetHelmReleaseTool(clientset *kubernetes.Clientset) *GetHelmReleaseTool {
	return &GetHelmReleaseTool{clientset: clientset}
}

func (t *GetHelmReleaseTool) Name() string { return "get_helm_release" }
func (t *GetHelmReleaseTool) Description() string {
	return "Get detailed information about a specific Helm release, including chart metadata, status, deployment times, and release notes."
}
func (t *GetHelmReleaseTool) IsLongRunning() bool    { return false }
func (t *GetHelmReleaseTool) Category() ToolCategory { return CategoryReadOnly }

func (t *GetHelmReleaseTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

func (t *GetHelmReleaseTool) Declaration() *genai.FunctionDeclaration {
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

func (t *GetHelmReleaseTool) Run(ctx tool.Context, args any) (map[string]any, error) {
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

	name, ok := argsMap["name"].(string)
	if !ok || name == "" {
		return map[string]any{"error": "name is required"}, nil
	}

	namespace, ok := argsMap["namespace"].(string)
	if !ok || namespace == "" {
		return map[string]any{"error": "namespace is required"}, nil
	}

	rel, err := findHelmReleaseSecret(ctx, t.clientset, name, namespace)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("failed to get Helm release: %v", err)}, nil
	}

	result := map[string]any{
		"name":      rel.Name,
		"namespace": rel.Namespace,
		"revision":  rel.Version,
		"status":    rel.Info.Status,
		"chart": map[string]any{
			"name":        rel.Chart.Metadata.Name,
			"version":     rel.Chart.Metadata.Version,
			"app_version": rel.Chart.Metadata.AppVersion,
			"description": rel.Chart.Metadata.Description,
		},
		"first_deployed": rel.Info.FirstDeployed,
		"last_deployed":  rel.Info.LastDeployed,
		"description":    rel.Info.Description,
	}

	if rel.Info.Notes != "" {
		result["notes"] = rel.Info.Notes
	}

	return result, nil
}
