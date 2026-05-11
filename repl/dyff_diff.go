package repl

import (
	"bytes"
	"fmt"

	"github.com/gonvenience/bunt"
	"github.com/gonvenience/ytbx"
	"github.com/homeport/dyff/pkg/dyff"
)

func init() {
	// dyff defaults to AUTO color detection. We render into a fenced code
	// block in the REPL's markdown output, where ANSI escapes would only
	// garble the result, so disable colors at the package level.
	bunt.SetColorSettings(bunt.OFF, bunt.OFF)
}

// dyffDiff renders a human-readable diff between liveYAML and proposedYAML
// using dyff. Returns "" when both inputs parse to equivalent documents.
func dyffDiff(liveYAML, proposedYAML string) (string, error) {
	fromDocs, err := ytbx.LoadYAMLDocuments([]byte(liveYAML))
	if err != nil {
		return "", fmt.Errorf("parsing live YAML: %w", err)
	}
	toDocs, err := ytbx.LoadYAMLDocuments([]byte(proposedYAML))
	if err != nil {
		return "", fmt.Errorf("parsing proposed YAML: %w", err)
	}

	report, err := dyff.CompareInputFiles(
		ytbx.InputFile{Location: "cluster", Documents: fromDocs},
		ytbx.InputFile{Location: "proposed", Documents: toDocs},
		dyff.KubernetesEntityDetection(true),
		dyff.DetectRenames(true),
	)
	if err != nil {
		return "", fmt.Errorf("comparing: %w", err)
	}
	if len(report.Diffs) == 0 {
		return "", nil
	}

	hr := &dyff.HumanReport{
		Report:     report,
		OmitHeader: true,
	}
	var buf bytes.Buffer
	if err := hr.WriteReport(&buf); err != nil {
		return "", fmt.Errorf("rendering: %w", err)
	}
	return buf.String(), nil
}
