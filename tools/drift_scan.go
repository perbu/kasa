package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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

	// Filter out Secrets — the agent cannot access Secret data from the cluster,
	// so comparing stored manifests against live state produces misleading results.
	var filtered []manifest.ManifestInfo
	for _, m := range manifests {
		if strings.EqualFold(m.Type, "secret") {
			continue
		}
		filtered = append(filtered, m)
	}
	manifests = filtered

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

// FormatDriftSummary returns a short one-line summary of drift scan results,
// suitable for printing at startup. Returns "" when there are no results.
func FormatDriftSummary(results *DriftScanResults) string {
	if results == nil || results.Total == 0 {
		return ""
	}

	if results.InSync == results.Total {
		check := driftOKStyle.Render("✓")
		return fmt.Sprintf("%s %s",
			driftHeaderStyle.Render(fmt.Sprintf("Drift scan: %d manifests, all in sync", results.Total)),
			check)
	}

	parts := []string{}
	if results.InSync > 0 {
		parts = append(parts, driftOKStyle.Render(fmt.Sprintf("%d ok", results.InSync)))
	}
	if results.Drifted > 0 {
		parts = append(parts, driftDriftedStyle.Render(fmt.Sprintf("%d drifted", results.Drifted)))
	}
	if results.Missing > 0 {
		parts = append(parts, driftMissingStyle.Render(fmt.Sprintf("%d missing", results.Missing)))
	}
	if results.Errors > 0 {
		parts = append(parts, driftErrorStyle.Render(fmt.Sprintf("%d errors", results.Errors)))
	}

	return fmt.Sprintf("%s %s — use %s for details",
		driftHeaderStyle.Render(fmt.Sprintf("Drift scan: %d manifests:", results.Total)),
		strings.Join(parts, ", "),
		driftHeaderStyle.Render("/drift"))
}

// Styles for drift scan output.
var (
	driftHeaderStyle  = lipgloss.NewStyle().Bold(true)
	driftOKStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	driftDriftedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	driftMissingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	driftErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	driftResourceDim  = lipgloss.NewStyle().Faint(true)
)

// ellipsize truncates s to maxLen, replacing the middle with "…" if needed.
func ellipsize(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	// Keep more of the end (kind/name) than the beginning (namespace).
	headLen := (maxLen - 1) / 2
	tailLen := maxLen - 1 - headLen
	return s[:headLen] + "…" + s[len(s)-tailLen:]
}

// FormatDriftScanResults formats drift scan results as colored plain text.
// width is the terminal width; pass 0 or negative for a default of 80.
func FormatDriftScanResults(results *DriftScanResults, width int) string {
	if results.Total == 0 {
		return ""
	}

	if width <= 0 {
		width = 80
	}

	if results.InSync == results.Total {
		check := driftOKStyle.Render("✓")
		return fmt.Sprintf("%s %s\n",
			driftHeaderStyle.Render(fmt.Sprintf("Drift scan: %d manifests, all in sync", results.Total)),
			check)
	}

	var sb strings.Builder
	// Summary line
	summary := fmt.Sprintf("Drift scan: %d manifests — %d ok, %d drifted, %d missing, %d errors",
		results.Total, results.InSync, results.Drifted, results.Missing, results.Errors)
	sb.WriteString(driftHeaderStyle.Render(summary))
	sb.WriteString("\n\n")

	// Determine the longest status string to calculate resource column width.
	// Status strings: "OK", "DRIFTED (N fields)", "NOT IN CLUSTER", "ERROR: ..."
	// Reserve space: 2 padding + max status width.
	const minResourceWidth = 30
	maxStatusWidth := 2 // "OK"
	for _, r := range results.Results {
		sw := statusDisplayWidth(r)
		if sw > maxStatusWidth {
			maxStatusWidth = sw
		}
	}
	// resource column = width - indent(2) - gap(2) - status column - 1 (avoid terminal wrap)
	resourceWidth := width - 5 - maxStatusWidth
	if resourceWidth < minResourceWidth {
		resourceWidth = minResourceWidth
	}

	for _, r := range results.Results {
		resource := fmt.Sprintf("%s/%s/%s", r.Namespace, r.Name, r.Kind)
		display := ellipsize(resource, resourceWidth)

		// Pad resource to fixed width
		padded := display + strings.Repeat(" ", max(0, resourceWidth-len(display)))

		var status string
		switch r.Status {
		case "in_sync":
			status = driftOKStyle.Render("OK")
		case "drifted":
			status = driftDriftedStyle.Render(fmt.Sprintf("DRIFTED (%d fields)", len(r.Diffs)))
		case "missing":
			status = driftMissingStyle.Render("NOT IN CLUSTER")
		case "error":
			errMsg := ellipsize(r.Error, maxStatusWidth-7) // "ERROR: " = 7
			status = driftErrorStyle.Render("ERROR: " + errMsg)
		}

		sb.WriteString(fmt.Sprintf("  %s  %s\n", driftResourceDim.Render(padded), status))
	}

	return sb.String()
}

// statusDisplayWidth returns the plain-text width of the status column for a result.
func statusDisplayWidth(r DriftResult) int {
	switch r.Status {
	case "in_sync":
		return 2 // "OK"
	case "drifted":
		return len(fmt.Sprintf("DRIFTED (%d fields)", len(r.Diffs)))
	case "missing":
		return 14 // "NOT IN CLUSTER"
	case "error":
		return min(7+len(r.Error), 40) // cap error text
	default:
		return 10
	}
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
