package tools

import (
	"context"
	"fmt"

	"github.com/perbu/kasa/manifest"
	"k8s.io/client-go/dynamic"
)

// ProgressFunc is called during drift scan to report progress.
// It receives the current index (0-based) and total count plus the resource being checked.
type ProgressFunc func(current, total int, namespace, name, kind string)

// RunDriftScan iterates over all stored manifests and compares each against
// the live cluster state. Returns nil, nil if there are no manifests.
// The optional progress callback is called before each resource is checked.
func RunDriftScan(ctx context.Context, dynClient dynamic.Interface, mgr *manifest.Manager, progress ProgressFunc) (*DriftScanResults, error) {
	manifests, err := mgr.ListManifests("", "")
	if err != nil {
		return nil, err
	}

	if len(manifests) == 0 {
		return nil, nil
	}

	results := &DriftScanResults{
		Total: len(manifests),
	}

	for i, m := range manifests {
		if progress != nil {
			progress(i, len(manifests), m.Namespace, m.App, m.Type)
		}

		content, err := mgr.ReadManifest(m.Namespace, m.App, m.Type)
		if err != nil {
			results.Results = append(results.Results, DriftResult{
				Namespace: m.Namespace,
				Name:      m.App,
				Kind:      m.Type,
				Status:    "error",
				Error:     err.Error(),
			})
			results.Errors++
			continue
		}

		dr := CompareManifest(ctx, dynClient, m.Namespace, m.App, m.Type, content)
		results.Results = append(results.Results, dr)

		switch dr.Status {
		case "in_sync":
			results.InSync++
		case "drifted":
			results.Drifted++
		case "missing":
			results.Missing++
		case "error":
			results.Errors++
		}
	}

	return results, nil
}

// FormatDriftScanResults formats drift scan results as a markdown string.
func FormatDriftScanResults(results *DriftScanResults) string {
	if results.Total == 0 {
		return ""
	}

	if results.InSync == results.Total {
		return fmt.Sprintf("**Drift scan:** %d manifests, all in sync\n", results.Total)
	}

	s := fmt.Sprintf("**Drift scan:** %d manifests\n\n", results.Total)
	s += "| Resource | Status |\n"
	s += "|----------|--------|\n"

	for _, r := range results.Results {
		resource := fmt.Sprintf("%s/%s/%s", r.Namespace, r.Name, r.Kind)
		switch r.Status {
		case "in_sync":
			s += fmt.Sprintf("| %s | OK |\n", resource)
		case "drifted":
			s += fmt.Sprintf("| %s | DRIFTED (%d fields) |\n", resource, len(r.Diffs))
		case "missing":
			s += fmt.Sprintf("| %s | NOT IN CLUSTER |\n", resource)
		case "error":
			s += fmt.Sprintf("| %s | ERROR: %s |\n", resource, r.Error)
		}
	}

	return s
}

// FormatDriftContext formats drift scan results as plain text suitable for
// injection into the LLM system prompt so the agent is aware of drift state.
func FormatDriftContext(results *DriftScanResults) string {
	if results == nil || results.Total == 0 {
		return ""
	}

	if results.InSync == results.Total {
		return fmt.Sprintf("\n## Drift scan results\n%d stored manifests, all in sync with the cluster.\n", results.Total)
	}

	s := fmt.Sprintf("\n## Drift scan results\n%d stored manifests: %d in sync, %d drifted, %d not in cluster, %d errors.\n",
		results.Total, results.InSync, results.Drifted, results.Missing, results.Errors)

	for _, r := range results.Results {
		resource := fmt.Sprintf("%s/%s/%s", r.Namespace, r.Name, r.Kind)
		switch r.Status {
		case "in_sync":
			s += fmt.Sprintf("- %s: in sync\n", resource)
		case "drifted":
			s += fmt.Sprintf("- %s: drifted (%d fields differ)\n", resource, len(r.Diffs))
		case "missing":
			s += fmt.Sprintf("- %s: stored manifest exists but resource not found in cluster\n", resource)
		case "error":
			s += fmt.Sprintf("- %s: error (%s)\n", resource, r.Error)
		}
	}

	s += "\nUse the diff_resource tool to see detailed field-level differences for drifted resources.\n"
	return s
}
