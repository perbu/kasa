package tools

import (
	"fmt"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ListHelmReleasesTool provides the list_helm_releases tool.
type ListHelmReleasesTool struct {
	clientset *kubernetes.Clientset
}

// NewListHelmReleasesTool creates a new ListHelmReleasesTool.
func NewListHelmReleasesTool(clientset *kubernetes.Clientset) *ListHelmReleasesTool {
	return &ListHelmReleasesTool{clientset: clientset}
}

func (t *ListHelmReleasesTool) Name() string { return "list_helm_releases" }
func (t *ListHelmReleasesTool) Description() string {
	return "List Helm releases deployed in the cluster. Shows release name, namespace, chart, version, status, and last updated time."
}
func (t *ListHelmReleasesTool) IsLongRunning() bool    { return false }
func (t *ListHelmReleasesTool) Category() ToolCategory { return CategoryReadOnly }

func (t *ListHelmReleasesTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

func (t *ListHelmReleasesTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "Filter by namespace. If empty, lists releases across all namespaces.",
				},
			},
		},
	}
}

func (t *ListHelmReleasesTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, _ := parseToolArgs(args)
	if argsMap == nil {
		argsMap = map[string]any{}
	}

	namespace := ""
	if ns, ok := argsMap["namespace"].(string); ok {
		namespace = ns
	}

	timeoutCtx, cancel := withToolTimeout(ctx, 30*time.Second)
	defer cancel()

	secrets, err := t.clientset.CoreV1().Secrets(namespace).List(timeoutCtx, metav1.ListOptions{
		LabelSelector: "owner=helm",
	})
	if err != nil {
		return errorResult(fmt.Sprintf("failed to list Helm secrets: %v", err))
	}

	// Decode all release secrets and keep only the highest revision per release.
	// Helm writes a new secret per revision, so filtering only on owner=helm returns all history.
	type releaseEntry struct {
		data     map[string]any
		revision int
	}
	byRelease := make(map[string]releaseEntry)

	for _, secret := range secrets.Items {
		releaseData, ok := secret.Data["release"]
		if !ok {
			continue
		}

		rel, err := decodeHelmRelease(releaseData)
		if err != nil {
			continue
		}

		key := rel.Namespace + "/" + rel.Name
		existing, seen := byRelease[key]
		if !seen || rel.Version > existing.revision {
			byRelease[key] = releaseEntry{
				revision: rel.Version,
				data: map[string]any{
					"name":          rel.Name,
					"namespace":     rel.Namespace,
					"chart":         rel.Chart.Metadata.Name,
					"chart_version": rel.Chart.Metadata.Version,
					"app_version":   rel.Chart.Metadata.AppVersion,
					"status":        rel.Info.Status,
					"revision":      rel.Version,
					"updated":       rel.Info.LastDeployed,
				},
			}
		}
	}

	releases := make([]map[string]any, 0, len(byRelease))
	for _, entry := range byRelease {
		releases = append(releases, entry.data)
	}

	result := map[string]any{
		"count":    len(releases),
		"releases": releases,
	}
	if namespace != "" {
		result["namespace"] = namespace
	} else {
		result["scope"] = "all namespaces"
	}

	return result, nil
}
