package tools

import (
	"encoding/json"
	"fmt"
)

// parseToolArgs normalizes the args parameter from ADK tool calls into a map.
// ADK may pass args as map[string]any or as a JSON string.
func parseToolArgs(args any) (map[string]any, error) {
	if args == nil {
		return make(map[string]any), nil
	}
	if m, ok := args.(map[string]any); ok {
		return m, nil
	}
	if s, ok := args.(string); ok {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, fmt.Errorf("invalid arguments format")
		}
		return m, nil
	}
	return nil, fmt.Errorf("invalid arguments type")
}

// errorResult returns a tool error response. This is the standard way tools
// report non-fatal errors back to the LLM.
func errorResult(msg string) (map[string]any, error) {
	return map[string]any{"error": msg}, nil
}

// errorResultf returns a formatted tool error response.
func errorResultf(format string, args ...any) (map[string]any, error) {
	return map[string]any{"error": fmt.Sprintf(format, args...)}, nil
}
