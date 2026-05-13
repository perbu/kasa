package repl

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// DirectDisplayFormatter extracts a title and plain-text body from a tool response.
// Returns ok=false if the response should not be directly displayed (e.g., errors).
type DirectDisplayFormatter func(response map[string]any) (title, body string, ok bool)

// directDisplayFormatters maps tool names to their display formatters.
var directDisplayFormatters = map[string]DirectDisplayFormatter{
	"get_logs": formatGetLogs,
}

// directDisplayTitleStyle renders the bold title line above tool output.
var directDisplayTitleStyle = lipgloss.NewStyle().Bold(true)

// directDisplayBodyStyle renders the body content in a faint style.
var directDisplayBodyStyle = lipgloss.NewStyle().Faint(true)

// FormatDirectDisplay looks up a formatter for the given tool name and renders
// the response as styled plain text. Returns the rendered string and true if
// the tool has a formatter and the response was successfully formatted.
//
// Uses lipgloss instead of glamour to avoid ANSI background sequences that
// cause bubbletea to miscalculate view region height.
func FormatDirectDisplay(name string, response map[string]any, width int) (string, bool) {
	formatter, exists := directDisplayFormatters[name]
	if !exists {
		return "", false
	}

	title, body, ok := formatter(response)
	if !ok {
		return "", false
	}

	var sb strings.Builder
	sb.WriteString(directDisplayTitleStyle.Render(title))
	sb.WriteString("\n")
	sb.WriteString(directDisplayBodyStyle.Render(body))
	return sb.String(), true
}

// formatGetLogs formats get_logs output as plain text.
func formatGetLogs(response map[string]any) (string, string, bool) {
	if _, hasErr := response["error"]; hasErr {
		return "", "", false
	}

	logs, ok := response["logs"].(string)
	if !ok || logs == "" {
		return "", "", false
	}

	ns, _ := response["namespace"].(string)
	pod, _ := response["pod"].(string)
	container, _ := response["container"].(string)

	title := fmt.Sprintf("Logs: %s/%s", ns, pod)
	if container != "" {
		title += fmt.Sprintf(" (%s)", container)
	}

	return title, strings.TrimRight(logs, "\n"), true
}

