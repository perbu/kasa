package tools

import (
	"fmt"
	"sort"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// ContainerUsageInfo describes one container's live CPU and memory usage.
type ContainerUsageInfo struct {
	Name          string `json:"name"`
	CPUUsageMilli int64  `json:"cpu_usage_milli"`
	MemUsageMi    int64  `json:"mem_usage_mi"`
}

// PodUsageInfo describes a pod's live resource usage, summed across containers.
type PodUsageInfo struct {
	Name          string               `json:"name"`
	Namespace     string               `json:"namespace"`
	CPUUsageMilli int64                `json:"cpu_usage_milli"`
	MemUsageMi    int64                `json:"mem_usage_mi"`
	Containers    []ContainerUsageInfo `json:"containers,omitempty"`
}

// TopPodsTool reports CPU and memory usage for pods via metrics-server.
type TopPodsTool struct {
	metricsClient metricsclient.Interface
}

// NewTopPodsTool creates a new TopPodsTool. metricsClient may be nil; in that case
// the tool will return a helpful error pointing at metrics-server.
func NewTopPodsTool(metricsClient metricsclient.Interface) *TopPodsTool {
	return &TopPodsTool{metricsClient: metricsClient}
}

func (t *TopPodsTool) Name() string {
	return "top_pods"
}

func (t *TopPodsTool) Description() string {
	return "Show live CPU and memory usage per pod (requires metrics-server). Sorted by CPU by default; limit results with 'top'."
}

func (t *TopPodsTool) IsLongRunning() bool { return false }

func (t *TopPodsTool) Category() ToolCategory { return CategoryReadOnly }

func (t *TopPodsTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

func (t *TopPodsTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"namespace": {
					Type:        "string",
					Description: "Namespace to query. Empty string means all namespaces.",
				},
				"label_selector": {
					Type:        "string",
					Description: "Optional label selector (e.g., 'app=nginx').",
				},
				"sort_by": {
					Type:        "string",
					Description: "Sort key: 'cpu' or 'memory' (default 'cpu').",
				},
				"top": {
					Type:        "integer",
					Description: "Return only the top N pods after sorting. 0 (default) means all.",
				},
				"containers": {
					Type:        "boolean",
					Description: "If true, include per-container breakdown for each pod.",
				},
			},
			Required: []string{"namespace"},
		},
	}
}

func (t *TopPodsTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	if t.metricsClient == nil {
		return errorResult("metrics-server is not available in this cluster (install metrics.k8s.io APIService to get live CPU/mem)")
	}

	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	namespace := ""
	if v, ok := argsMap["namespace"].(string); ok {
		namespace = v
	}
	labelSelector := ""
	if v, ok := argsMap["label_selector"].(string); ok {
		labelSelector = v
	}
	sortBy := "cpu"
	if v, ok := argsMap["sort_by"].(string); ok && v != "" {
		sortBy = v
	}
	topN := 0
	switch v := argsMap["top"].(type) {
	case float64:
		topN = int(v)
	case int:
		topN = v
	}
	includeContainers := false
	if v, ok := argsMap["containers"].(bool); ok {
		includeContainers = v
	}

	timeoutCtx, cancel := withToolTimeout(ctx, 30*time.Second)
	defer cancel()

	podMetricsList, err := t.metricsClient.MetricsV1beta1().PodMetricses(namespace).List(timeoutCtx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return errorResult(fmt.Sprintf("listing pod metrics: %v (is metrics-server installed?)", err))
	}

	result := make([]PodUsageInfo, 0, len(podMetricsList.Items))
	for _, pm := range podMetricsList.Items {
		var cpuTotal, memTotal int64
		var containers []ContainerUsageInfo
		if includeContainers {
			containers = make([]ContainerUsageInfo, 0, len(pm.Containers))
		}
		for _, c := range pm.Containers {
			cpu := milliCPU(c.Usage[corev1.ResourceCPU])
			mem := memMi(c.Usage[corev1.ResourceMemory])
			cpuTotal += cpu
			memTotal += mem
			if includeContainers {
				containers = append(containers, ContainerUsageInfo{
					Name:          c.Name,
					CPUUsageMilli: cpu,
					MemUsageMi:    mem,
				})
			}
		}
		result = append(result, PodUsageInfo{
			Name:          pm.Name,
			Namespace:     pm.Namespace,
			CPUUsageMilli: cpuTotal,
			MemUsageMi:    memTotal,
			Containers:    containers,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if sortBy == "memory" {
			return result[i].MemUsageMi > result[j].MemUsageMi
		}
		return result[i].CPUUsageMilli > result[j].CPUUsageMilli
	})

	truncated := false
	if topN > 0 && topN < len(result) {
		result = result[:topN]
		truncated = true
	}

	return map[string]any{
		"pods":      result,
		"count":     len(result),
		"truncated": truncated,
		"cpu_unit":  "millicores",
		"mem_unit":  "MiB",
	}, nil
}
