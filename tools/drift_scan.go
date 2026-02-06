package tools

import (
	"context"

	"github.com/perbu/kasa/manifest"
	"k8s.io/client-go/dynamic"
)

// RunDriftScan iterates over all stored manifests and compares each against
// the live cluster state. Returns nil, nil if there are no manifests.
func RunDriftScan(ctx context.Context, dynClient dynamic.Interface, mgr *manifest.Manager) (*DriftScanResults, error) {
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

	for _, m := range manifests {
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
