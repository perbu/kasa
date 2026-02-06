package tools

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// DiffEntry represents a single field difference between stored and live resources.
type DiffEntry struct {
	Path       string `json:"path"`
	ChangeType string `json:"change_type"` // "changed", "added", "removed"
	Stored     any    `json:"stored,omitempty"`
	Live       any    `json:"live,omitempty"`
}

// DriftResult represents the comparison result for a single resource.
type DriftResult struct {
	Namespace string      `json:"namespace"`
	Name      string      `json:"name"`
	Kind      string      `json:"kind"`
	Status    string      `json:"status"` // "in_sync", "drifted", "missing", "error"
	Diffs     []DiffEntry `json:"diffs,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// DriftScanResults holds the aggregate results of scanning all manifests.
type DriftScanResults struct {
	Results []DriftResult `json:"results"`
	Total   int           `json:"total"`
	InSync  int           `json:"in_sync"`
	Drifted int           `json:"drifted"`
	Missing int           `json:"missing"`
	Errors  int           `json:"errors"`
}

// DiffMaps recursively compares two maps and returns field-level differences.
// The prefix parameter builds dotted paths (e.g., "spec.template.spec").
func DiffMaps(stored, live map[string]any, prefix string) []DiffEntry {
	var diffs []DiffEntry

	// Collect all keys from both maps
	allKeys := make(map[string]bool)
	for k := range stored {
		allKeys[k] = true
	}
	for k := range live {
		allKeys[k] = true
	}

	// Sort keys for deterministic output
	keys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		storedVal, inStored := stored[key]
		liveVal, inLive := live[key]

		if !inStored {
			diffs = append(diffs, DiffEntry{
				Path:       path,
				ChangeType: "added",
				Live:       liveVal,
			})
			continue
		}

		if !inLive {
			diffs = append(diffs, DiffEntry{
				Path:       path,
				ChangeType: "removed",
				Stored:     storedVal,
			})
			continue
		}

		// Both exist - compare recursively
		diffs = append(diffs, diffValues(storedVal, liveVal, path)...)
	}

	return diffs
}

// diffValues compares two values and returns differences.
func diffValues(stored, live any, path string) []DiffEntry {
	// Handle both being maps
	storedMap, storedIsMap := stored.(map[string]any)
	liveMap, liveIsMap := live.(map[string]any)
	if storedIsMap && liveIsMap {
		return DiffMaps(storedMap, liveMap, path)
	}

	// Handle both being slices
	storedSlice, storedIsSlice := stored.([]any)
	liveSlice, liveIsSlice := live.([]any)
	if storedIsSlice && liveIsSlice {
		return diffSlices(storedSlice, liveSlice, path)
	}

	// Scalar comparison
	if !reflect.DeepEqual(stored, live) {
		return []DiffEntry{{
			Path:       path,
			ChangeType: "changed",
			Stored:     stored,
			Live:       live,
		}}
	}

	return nil
}

// diffSlices compares two slices element by element.
func diffSlices(stored, live []any, path string) []DiffEntry {
	var diffs []DiffEntry

	maxLen := max(len(stored), len(live))

	for i := 0; i < maxLen; i++ {
		elemPath := fmt.Sprintf("%s[%d]", path, i)

		if i >= len(stored) {
			diffs = append(diffs, DiffEntry{
				Path:       elemPath,
				ChangeType: "added",
				Live:       live[i],
			})
			continue
		}

		if i >= len(live) {
			diffs = append(diffs, DiffEntry{
				Path:       elemPath,
				ChangeType: "removed",
				Stored:     stored[i],
			})
			continue
		}

		diffs = append(diffs, diffValues(stored[i], live[i], elemPath)...)
	}

	return diffs
}

// FetchAndCleanLiveResource fetches a resource from the cluster via dynamic client,
// applies cleanForImport, and returns the cleaned map.
func FetchAndCleanLiveResource(ctx context.Context, dynClient dynamic.Interface, namespace, name, kind, apiVersion string) (map[string]any, error) {
	gvr, found := BuildGVRFromKindAndAPIVersion(kind, apiVersion)
	if !found {
		return nil, fmt.Errorf("unknown resource kind '%s'", kind)
	}

	namespaced := IsNamespaced(kind)

	var resourceClient dynamic.ResourceInterface
	if namespaced {
		resourceClient = dynClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceClient = dynClient.Resource(gvr)
	}

	obj, err := resourceClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	liveMap := obj.Object
	cleanForImport(liveMap)

	return liveMap, nil
}

// CompareManifest compares a stored manifest YAML against the live cluster resource.
func CompareManifest(ctx context.Context, dynClient dynamic.Interface, namespace, name, kind string, storedYAML []byte) DriftResult {
	result := DriftResult{
		Namespace: namespace,
		Name:      name,
		Kind:      kind,
	}

	// Parse stored YAML into map
	var storedMap map[string]any
	if err := yaml.Unmarshal(storedYAML, &storedMap); err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("failed to parse stored manifest: %v", err)
		return result
	}

	// Extract apiVersion from stored manifest for GVR resolution
	apiVersion, _ := storedMap["apiVersion"].(string)

	// Fetch and clean live resource
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	liveMap, err := FetchAndCleanLiveResource(timeoutCtx, dynClient, namespace, name, kind, apiVersion)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "not found") {
			result.Status = "missing"
		} else {
			result.Status = "error"
			result.Error = errStr
		}
		return result
	}

	// Compare
	diffs := DiffMaps(storedMap, liveMap, "")
	if len(diffs) == 0 {
		result.Status = "in_sync"
	} else {
		result.Status = "drifted"
		result.Diffs = diffs
	}

	return result
}
