package repl

import "testing"

func TestFormatGetLogs(t *testing.T) {
	t.Run("valid logs", func(t *testing.T) {
		resp := map[string]any{
			"namespace": "default",
			"pod":       "nginx-abc123",
			"container": "",
			"logs":      "line1\nline2\nline3",
		}
		title, body, ok := formatGetLogs(resp)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if title != "Logs: default/nginx-abc123" {
			t.Errorf("unexpected title: %s", title)
		}
		if body != "line1\nline2\nline3" {
			t.Errorf("unexpected body: %s", body)
		}
	})

	t.Run("with container", func(t *testing.T) {
		resp := map[string]any{
			"namespace": "kube-system",
			"pod":       "coredns-xyz",
			"container": "coredns",
			"logs":      "some logs",
		}
		title, _, ok := formatGetLogs(resp)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if title != "Logs: kube-system/coredns-xyz (coredns)" {
			t.Errorf("unexpected title: %s", title)
		}
	})

	t.Run("error response", func(t *testing.T) {
		resp := map[string]any{
			"error":     "pod not found",
			"namespace": "default",
			"pod":       "missing",
		}
		_, _, ok := formatGetLogs(resp)
		if ok {
			t.Fatal("expected ok=false for error response")
		}
	})

	t.Run("empty logs", func(t *testing.T) {
		resp := map[string]any{
			"namespace": "default",
			"pod":       "nginx",
			"logs":      "",
		}
		_, _, ok := formatGetLogs(resp)
		if ok {
			t.Fatal("expected ok=false for empty logs")
		}
	})
}

func TestFormatDirectDisplay(t *testing.T) {
	t.Run("unknown tool", func(t *testing.T) {
		_, ok := FormatDirectDisplay("list_pods", map[string]any{}, 80)
		if ok {
			t.Fatal("expected ok=false for unknown tool")
		}
	})

	t.Run("known tool with valid response", func(t *testing.T) {
		resp := map[string]any{
			"namespace": "default",
			"pod":       "test",
			"logs":      "hello world",
		}
		rendered, ok := FormatDirectDisplay("get_logs", resp, 80)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if rendered == "" {
			t.Error("expected non-empty rendered output")
		}
	})

	t.Run("known tool with error response", func(t *testing.T) {
		resp := map[string]any{
			"error": "something went wrong",
		}
		_, ok := FormatDirectDisplay("get_logs", resp, 80)
		if ok {
			t.Fatal("expected ok=false for error response")
		}
	})

	t.Run("nil response map", func(t *testing.T) {
		_, ok := FormatDirectDisplay("get_logs", nil, 80)
		if ok {
			t.Fatal("expected ok=false for nil response")
		}
	})
}
