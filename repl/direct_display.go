package repl

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

// DirectDisplayFormatter extracts a title and markdown body from a tool response.
// Returns ok=false if the response should not be directly displayed (e.g., errors).
type DirectDisplayFormatter func(response map[string]any) (title, body string, ok bool)

// directDisplayFormatters maps tool names to their display formatters.
var directDisplayFormatters = map[string]DirectDisplayFormatter{
	"get_logs":      formatGetLogs,
	"read_manifest": formatReadManifest,
	"get_resource":  formatGetResource,
}

// FormatDirectDisplay looks up a formatter for the given tool name and renders
// the response as markdown. Returns the rendered string and true if the tool
// has a formatter and the response was successfully formatted.
func FormatDirectDisplay(name string, response map[string]any, width int) (string, bool) {
	formatter, exists := directDisplayFormatters[name]
	if !exists {
		return "", false
	}

	title, body, ok := formatter(response)
	if !ok {
		return "", false
	}

	md := fmt.Sprintf("**%s**\n\n%s", title, body)
	return renderMarkdownSimple(md), true
}

// formatGetLogs formats get_logs output as a plain code block.
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

	body := fmt.Sprintf("```\n%s\n```", strings.TrimRight(logs, "\n"))
	return title, body, true
}

// formatReadManifest formats read_manifest output as a YAML code block.
func formatReadManifest(response map[string]any) (string, string, bool) {
	if _, hasErr := response["error"]; hasErr {
		return "", "", false
	}

	content, ok := response["content"].(string)
	if !ok || content == "" {
		return "", "", false
	}

	path, _ := response["path"].(string)
	title := "Manifest"
	if path != "" {
		title = fmt.Sprintf("Manifest: %s", path)
	}

	body := fmt.Sprintf("```yaml\n%s\n```", strings.TrimRight(content, "\n"))
	return title, body, true
}

// formatGetResource formats get_resource output by serializing the resource map to YAML.
func formatGetResource(response map[string]any) (string, string, bool) {
	if _, hasErr := response["error"]; hasErr {
		return "", "", false
	}

	resource, ok := response["resource"].(map[string]any)
	if !ok || resource == nil {
		return "", "", false
	}

	// Extract kind and name from metadata for the title
	kind, _ := resource["kind"].(string)
	name := ""
	if metadata, ok := resource["metadata"].(map[string]any); ok {
		name, _ = metadata["name"].(string)
	}

	title := "Resource"
	if kind != "" && name != "" {
		title = fmt.Sprintf("Resource: %s/%s", kind, name)
	} else if kind != "" {
		title = fmt.Sprintf("Resource: %s", kind)
	}

	yamlBytes, err := yaml.Marshal(resource)
	if err != nil {
		return "", "", false
	}

	body := fmt.Sprintf("```yaml\n%s\n```", strings.TrimRight(string(yamlBytes), "\n"))
	return title, body, true
}
