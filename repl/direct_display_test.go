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
		if body != "```\nline1\nline2\nline3\n```" {
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

func TestFormatReadManifest(t *testing.T) {
	t.Run("valid manifest", func(t *testing.T) {
		resp := map[string]any{
			"content": "apiVersion: apps/v1\nkind: Deployment",
			"path":    "default/nginx/deployment.yaml",
		}
		title, body, ok := formatReadManifest(resp)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if title != "Manifest: default/nginx/deployment.yaml" {
			t.Errorf("unexpected title: %s", title)
		}
		if body != "```yaml\napiVersion: apps/v1\nkind: Deployment\n```" {
			t.Errorf("unexpected body: %s", body)
		}
	})

	t.Run("error response", func(t *testing.T) {
		resp := map[string]any{
			"error": "manifest not found",
		}
		_, _, ok := formatReadManifest(resp)
		if ok {
			t.Fatal("expected ok=false for error response")
		}
	})

	t.Run("empty content", func(t *testing.T) {
		resp := map[string]any{
			"content": "",
			"path":    "some/path",
		}
		_, _, ok := formatReadManifest(resp)
		if ok {
			t.Fatal("expected ok=false for empty content")
		}
	})

	t.Run("no path", func(t *testing.T) {
		resp := map[string]any{
			"content": "apiVersion: v1",
		}
		title, _, ok := formatReadManifest(resp)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if title != "Manifest" {
			t.Errorf("unexpected title: %s", title)
		}
	})
}

func TestFormatGetResource(t *testing.T) {
	t.Run("valid resource", func(t *testing.T) {
		resp := map[string]any{
			"resource": map[string]any{
				"kind":       "Deployment",
				"apiVersion": "apps/v1",
				"metadata": map[string]any{
					"name":      "nginx",
					"namespace": "default",
				},
			},
		}
		title, body, ok := formatGetResource(resp)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if title != "Resource: Deployment/nginx" {
			t.Errorf("unexpected title: %s", title)
		}
		if body == "" {
			t.Error("expected non-empty body")
		}
		// Body should be a yaml code block
		if len(body) < 10 || body[:8] != "```yaml\n" {
			t.Errorf("expected yaml code block, got: %s", body[:min(50, len(body))])
		}
	})

	t.Run("error response", func(t *testing.T) {
		resp := map[string]any{
			"error": "resource not found",
		}
		_, _, ok := formatGetResource(resp)
		if ok {
			t.Fatal("expected ok=false for error response")
		}
	})

	t.Run("nil resource", func(t *testing.T) {
		resp := map[string]any{
			"resource": nil,
		}
		_, _, ok := formatGetResource(resp)
		if ok {
			t.Fatal("expected ok=false for nil resource")
		}
	})

	t.Run("resource without metadata", func(t *testing.T) {
		resp := map[string]any{
			"resource": map[string]any{
				"kind": "ConfigMap",
			},
		}
		title, _, ok := formatGetResource(resp)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if title != "Resource: ConfigMap" {
			t.Errorf("unexpected title: %s", title)
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
