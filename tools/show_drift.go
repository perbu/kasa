package tools

import (
	"fmt"
	"time"

	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
	"k8s.io/client-go/dynamic"
)

// ShowDriftTool provides the show_drift tool for the agent.
type ShowDriftTool struct {
	cache       *DriftCache
	dynClient   dynamic.Interface
	manifest    *manifest.Manager
}

// NewShowDriftTool creates a new ShowDriftTool.
func NewShowDriftTool(cache *DriftCache, dynClient dynamic.Interface, manifest *manifest.Manager) *ShowDriftTool {
	return &ShowDriftTool{
		cache:     cache,
		dynClient: dynClient,
		manifest:  manifest,
	}
}

// Name returns the tool name.
func (t *ShowDriftTool) Name() string {
	return "show_drift"
}

// Description returns the tool description.
func (t *ShowDriftTool) Description() string {
	return "Compare stored manifests against the live cluster and report drift. " +
		"Returns cached results from the most recent background scan (with an 'age' field) " +
		"when available; otherwise runs a fresh scan (several seconds). " +
		"Pass refresh=true to force a fresh scan even when a cached result exists. " +
		"Response includes status counts (in_sync, drifted, missing, errors) and per-resource diffs."
}

// IsLongRunning returns false as cache lookup is instant.
func (t *ShowDriftTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *ShowDriftTool) Category() ToolCategory {
	return CategoryReadOnly
}

// ProcessRequest adds this tool to the LLM request.
func (t *ShowDriftTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *ShowDriftTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"refresh": {
					Type:        "boolean",
					Description: "If true, force a fresh scan against the cluster instead of returning cached results.",
				},
			},
		},
	}
}

// Run executes the tool.
func (t *ShowDriftTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, err := parseToolArgs(args)
	if err != nil {
		return errorResult(err.Error())
	}

	refresh, _ := argsMap["refresh"].(bool)

	if refresh || t.cache == nil {
		// Prevent concurrent scans (e.g. background scan + show_drift refresh).
		if t.cache != nil && !t.cache.StartScan() {
			return map[string]any{
				"status":  "scan_in_progress",
				"message": "A drift scan is already running. Please wait and try again, or omit refresh to see the last cached results.",
			}, nil
		}
		if t.cache != nil {
			defer t.cache.EndScan()
		}

		var gen time.Time
		if t.cache != nil {
			gen = t.cache.Generation()
		}
		scanCtx, cancel := withToolTimeout(ctx, 60*time.Second)
		defer cancel()
		results, err := RunDriftScan(scanCtx, t.dynClient, t.manifest, nil)
		if err != nil {
			return errorResult(fmt.Sprintf("drift scan failed: %v", err))
		}
		if t.cache != nil && results != nil {
			// SaveIfCurrent skips the write if a mutation invalidated the cache
			// while this scan was running; otherwise the in-flight (now-stale)
			// snapshot would overwrite the invalidation.
			_, _ = t.cache.SaveIfCurrent(results, gen)
		}
		return formatDriftToolResponse(results, time.Now()), nil
	}

	results, lastScan, ok := t.cache.Load()
	if !ok {
		return map[string]any{
			"status":  "no_scan",
			"message": "No drift scan has been run yet. A background scan may be in progress. Use refresh=true to trigger a scan now.",
		}, nil
	}

	response := formatDriftToolResponse(results, lastScan)
	response["age"] = time.Since(lastScan).Truncate(time.Second).String()
	return response, nil
}

// formatDriftToolResponse formats DriftScanResults for return to the LLM.
func formatDriftToolResponse(results *DriftScanResults, scannedAt time.Time) map[string]any {
	if results == nil || results.Total == 0 {
		return map[string]any{
			"status":     "no_manifests",
			"message":    "No stored manifests to compare.",
			"total":      0,
			"scanned_at": scannedAt.Format(time.RFC3339),
		}
	}

	response := map[string]any{
		"status":     "complete",
		"total":      results.Total,
		"in_sync":    results.InSync,
		"drifted":    results.Drifted,
		"missing":    results.Missing,
		"errors":     results.Errors,
		"scanned_at": scannedAt.Format(time.RFC3339),
	}

	// Build per-resource results
	resourceResults := make([]map[string]any, len(results.Results))
	for i, r := range results.Results {
		entry := map[string]any{
			"namespace": r.Namespace,
			"name":      r.Name,
			"kind":      r.Kind,
			"status":    r.Status,
		}
		if r.Error != "" {
			entry["error"] = r.Error
		}
		if len(r.Diffs) > 0 {
			diffs := make([]map[string]any, len(r.Diffs))
			for j, d := range r.Diffs {
				diffEntry := map[string]any{
					"path":        d.Path,
					"change_type": d.ChangeType,
				}
				if d.Stored != nil {
					diffEntry["stored"] = d.Stored
				}
				if d.Live != nil {
					diffEntry["live"] = d.Live
				}
				diffs[j] = diffEntry
			}
			entry["diffs"] = diffs
		}
		resourceResults[i] = entry
	}
	response["resources"] = resourceResults

	return response
}
