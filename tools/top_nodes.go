package tools

import (
	"fmt"
	"sort"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// NodeUsageInfo describes a single node's live resource usage alongside its capacity.
type NodeUsageInfo struct {
	Name             string `json:"name"`
	CPUUsageMilli    int64  `json:"cpu_usage_milli"`
	CPUCapacityMilli int64  `json:"cpu_capacity_milli"`
	CPUPercent       int    `json:"cpu_percent"`
	MemUsageMi       int64  `json:"mem_usage_mi"`
	MemCapacityMi    int64  `json:"mem_capacity_mi"`
	MemPercent       int    `json:"mem_percent"`
}

// TopNodesTool reports CPU and memory usage for cluster nodes via metrics-server.
type TopNodesTool struct {
	clientset     *kubernetes.Clientset
	metricsClient metricsclient.Interface
}

// NewTopNodesTool creates a new TopNodesTool. metricsClient may be nil; in that case
// the tool will return a helpful error pointing at metrics-server.
func NewTopNodesTool(clientset *kubernetes.Clientset, metricsClient metricsclient.Interface) *TopNodesTool {
	return &TopNodesTool{clientset: clientset, metricsClient: metricsClient}
}

func (t *TopNodesTool) Name() string {
	return "top_nodes"
}

func (t *TopNodesTool) Description() string {
	return "Show live CPU and memory usage per node (requires metrics-server). Sorted by CPU% by default."
}

func (t *TopNodesTool) IsLongRunning() bool { return false }

func (t *TopNodesTool) Category() ToolCategory { return CategoryReadOnly }

func (t *TopNodesTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

func (t *TopNodesTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"sort_by": {
					Type:        "string",
					Description: "Sort key: 'cpu' or 'memory' (default 'cpu').",
				},
			},
		},
	}
}

func (t *TopNodesTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	if t.metricsClient == nil {
		return errorResult("metrics-server is not available in this cluster (install metrics.k8s.io APIService to get live CPU/mem)")
	}

	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}
	sortBy := "cpu"
	if v, ok := argsMap["sort_by"].(string); ok && v != "" {
		sortBy = v
	}

	timeoutCtx, cancel := withToolTimeout(ctx, 30*time.Second)
	defer cancel()

	metricsList, err := t.metricsClient.MetricsV1beta1().NodeMetricses().List(timeoutCtx, metav1.ListOptions{})
	if err != nil {
		return errorResult(fmt.Sprintf("listing node metrics: %v (is metrics-server installed?)", err))
	}

	nodes, err := t.clientset.CoreV1().Nodes().List(timeoutCtx, metav1.ListOptions{})
	if err != nil {
		return errorResult(fmt.Sprintf("listing nodes: %v", err))
	}

	capacityByName := make(map[string]corev1.ResourceList, len(nodes.Items))
	for _, n := range nodes.Items {
		capacityByName[n.Name] = n.Status.Allocatable
	}

	result := make([]NodeUsageInfo, 0, len(metricsList.Items))
	var totalCPUUsed, totalCPUCap, totalMemUsed, totalMemCap int64
	for _, m := range metricsList.Items {
		cpuUsed := milliCPU(m.Usage[corev1.ResourceCPU])
		memUsed := memMi(m.Usage[corev1.ResourceMemory])

		cap := capacityByName[m.Name]
		cpuCap := milliCPU(cap[corev1.ResourceCPU])
		memCap := memMi(cap[corev1.ResourceMemory])

		result = append(result, NodeUsageInfo{
			Name:             m.Name,
			CPUUsageMilli:    cpuUsed,
			CPUCapacityMilli: cpuCap,
			CPUPercent:       percent(cpuUsed, cpuCap),
			MemUsageMi:       memUsed,
			MemCapacityMi:    memCap,
			MemPercent:       percent(memUsed, memCap),
		})
		totalCPUUsed += cpuUsed
		totalCPUCap += cpuCap
		totalMemUsed += memUsed
		totalMemCap += memCap
	}

	sort.Slice(result, func(i, j int) bool {
		if sortBy == "memory" {
			return result[i].MemPercent > result[j].MemPercent
		}
		return result[i].CPUPercent > result[j].CPUPercent
	})

	return map[string]any{
		"nodes":              result,
		"count":              len(result),
		"cluster_cpu_used":   totalCPUUsed,
		"cluster_cpu_cap":    totalCPUCap,
		"cluster_cpu_pct":    percent(totalCPUUsed, totalCPUCap),
		"cluster_mem_used":   totalMemUsed,
		"cluster_mem_cap":    totalMemCap,
		"cluster_mem_pct":    percent(totalMemUsed, totalMemCap),
		"cpu_unit":           "millicores",
		"mem_unit":           "MiB",
	}, nil
}

// milliCPU converts a Quantity representing CPU to integer millicores.
func milliCPU(q resource.Quantity) int64 {
	return q.MilliValue()
}

// memMi converts a Quantity representing memory to mebibytes (rounded down).
func memMi(q resource.Quantity) int64 {
	return q.Value() / (1024 * 1024)
}

// percent returns 100*used/total, or 0 when total is non-positive.
func percent(used, total int64) int {
	if total <= 0 {
		return 0
	}
	return int((used * 100) / total)
}
