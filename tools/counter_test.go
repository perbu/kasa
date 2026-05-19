package tools

import (
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

func TestToolCallCounter(t *testing.T) {
	c := NewToolCallCounter(3)

	// First two calls: no warning expected
	if n := c.Increment("list_pods"); n != 1 {
		t.Errorf("expected count 1, got %d", n)
	}
	if n := c.Increment("list_pods"); n != 2 {
		t.Errorf("expected count 2, got %d", n)
	}

	// Third call: hits threshold
	if n := c.Increment("list_pods"); n != 3 {
		t.Errorf("expected count 3, got %d", n)
	}

	// Different tool has its own count
	if n := c.Increment("get_logs"); n != 1 {
		t.Errorf("expected count 1 for get_logs, got %d", n)
	}

	// Total across all tools
	if total := c.Total(); total != 4 {
		t.Errorf("expected total 4, got %d", total)
	}

	// Reset clears everything
	c.Reset()
	if n := c.Increment("list_pods"); n != 1 {
		t.Errorf("expected count 1 after reset, got %d", n)
	}
	if total := c.Total(); total != 1 {
		t.Errorf("expected total 1 after reset, got %d", total)
	}
}

func TestAllToolsWrapped(t *testing.T) {
	mgr := newTestManifestManager(t)
	kt := NewKubeTools(clientset, dynamicClient, nil, mgr, nil, "", 3, NewDirectIO(), nil)

	for _, tool := range kt.All() {
		ct, ok := tool.(*countingTool)
		if !ok {
			t.Errorf("tool %q (%T) was not wrapped as countingTool", tool.Name(), tool)
			continue
		}
		// Verify inner satisfies runnableTool
		_ = ct.inner
	}
	t.Logf("all %d tools wrapped successfully", len(kt.All()))
}

func TestCountingToolInjectsWarning(t *testing.T) {
	mgr := newTestManifestManager(t)
	kt := NewKubeTools(clientset, dynamicClient, nil, mgr, nil, "", 3, NewDirectIO(), nil)

	// Find list_namespaces tool
	var nsTool *countingTool
	for _, tool := range kt.All() {
		if tool.Name() == "list_namespaces" {
			nsTool = tool.(*countingTool)
			break
		}
	}
	if nsTool == nil {
		t.Fatal("list_namespaces tool not found")
	}

	// Call it 4 times, check warning appears on 3rd+
	for i := 1; i <= 4; i++ {
		result, err := nsTool.Run(nil, map[string]any{})
		if err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
		warning, hasWarning := result["_warning"]
		if i < 3 && hasWarning {
			t.Errorf("call %d: unexpected _warning: %v", i, warning)
		}
		if i >= 3 && !hasWarning {
			t.Errorf("call %d: expected _warning but none found. keys: %v", i, mapKeys(result))
		}
		if i >= 3 && hasWarning {
			t.Logf("call %d: _warning = %s", i, warning)
		}
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestCountingToolRegistersItselfInReqTools(t *testing.T) {
	mgr := newTestManifestManager(t)
	kt := NewKubeTools(clientset, dynamicClient, nil, mgr, nil, "", 3, NewDirectIO(), nil)

	for _, tl := range kt.All() {
		ct, ok := tl.(*countingTool)
		if !ok {
			t.Fatalf("tool %q not wrapped", tl.Name())
		}

		// Simulate what ADK does: call ProcessRequest, then check req.Tools
		req := &model.LLMRequest{}
		if err := ct.ProcessRequest(nil, req); err != nil {
			t.Fatalf("ProcessRequest failed for %q: %v", tl.Name(), err)
		}

		registered, found := req.Tools[tl.Name()]
		if !found {
			t.Errorf("tool %q not found in req.Tools after ProcessRequest", tl.Name())
			continue
		}

		// The registered tool should be the countingTool wrapper, not the inner
		if _, ok := registered.(*countingTool); !ok {
			t.Errorf("req.Tools[%q] is %T, want *countingTool", tl.Name(), registered)
		}
	}
}

func TestToolCallCounterDisabled(t *testing.T) {
	c := NewToolCallCounter(0) // disabled

	for i := 0; i < 10; i++ {
		c.Increment("list_pods")
	}
	// threshold=0 means warnings are disabled; counter still counts
	if total := c.Total(); total != 10 {
		t.Errorf("expected total 10, got %d", total)
	}
}

func TestIsConnectionError(t *testing.T) {
	tests := []struct {
		name   string
		result map[string]any
		want   bool
	}{
		{"i/o timeout", map[string]any{"error": "Get https://...: i/o timeout"}, true},
		{"connection refused", map[string]any{"error": "dial tcp 10.0.0.1:443: connection refused"}, true},
		{"connection reset", map[string]any{"error": "read: connection reset by peer"}, true},
		{"no such host", map[string]any{"error": "dial tcp: lookup api.example.com: no such host"}, true},
		{"network unreachable", map[string]any{"error": "connect: network is unreachable"}, true},
		{"tls handshake timeout", map[string]any{"error": "net/http: TLS handshake timeout"}, true},
		{"dial tcp", map[string]any{"error": "dial tcp 10.96.0.1:443: connect: connection refused"}, true},
		{"context deadline exceeded", map[string]any{"error": "context deadline exceeded"}, true},
		{"no route to host", map[string]any{"error": "connect: no route to host"}, true},
		{"connection timed out", map[string]any{"error": "dial tcp 10.0.0.1:443: connection timed out"}, true},
		{"case insensitive", map[string]any{"error": "DIAL TCP 10.0.0.1:443: CONNECTION REFUSED"}, true},
		{"404 not found", map[string]any{"error": "the server could not find the requested resource (404)"}, false},
		{"auth error", map[string]any{"error": "Unauthorized"}, false},
		{"validation error", map[string]any{"error": "namespace not specified"}, false},
		{"no error key", map[string]any{"result": "success"}, false},
		{"nil result", nil, false},
		{"non-string error", map[string]any{"error": 42}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConnectionError(tt.result)
			if got != tt.want {
				t.Errorf("isConnectionError(%v) = %v, want %v", tt.result, got, tt.want)
			}
		})
	}
}

func TestConsecutiveConnectionErrors(t *testing.T) {
	c := NewToolCallCounter(10)

	// First two errors: not triggered yet
	if triggered := c.RecordConnectionError(); triggered {
		t.Error("should not trigger after 1 error")
	}
	if triggered := c.RecordConnectionError(); triggered {
		t.Error("should not trigger after 2 errors")
	}

	// Third error: hits threshold
	if triggered := c.RecordConnectionError(); !triggered {
		t.Error("should trigger after 3 errors")
	}

	// Fourth error: still triggered
	if triggered := c.RecordConnectionError(); !triggered {
		t.Error("should still be triggered after 4 errors")
	}

	// ClearConnectionErrors resets streak but not triggered flag
	c.ClearConnectionErrors()
	if !c.ConnectionErrorTriggered() {
		t.Error("triggered flag should persist after ClearConnectionErrors")
	}

	// Next error triggers immediately (streak=1 but flag already set)
	if triggered := c.RecordConnectionError(); !triggered {
		t.Error("should trigger immediately after ClearConnectionErrors when flag is set")
	}

	// Full Reset clears everything
	c.Reset()
	if c.ConnectionErrorTriggered() {
		t.Error("triggered flag should be cleared after Reset")
	}
	if triggered := c.RecordConnectionError(); triggered {
		t.Error("should not trigger after Reset with only 1 error")
	}
}

func TestConnectionErrorStreakReset(t *testing.T) {
	c := NewToolCallCounter(10)

	// Two connection errors
	c.RecordConnectionError()
	c.RecordConnectionError()

	// Successful call resets streak
	c.ClearConnectionErrors()

	// Two more errors — streak restarted, not at threshold yet
	if triggered := c.RecordConnectionError(); triggered {
		t.Error("should not trigger: streak was reset")
	}
	if triggered := c.RecordConnectionError(); triggered {
		t.Error("should not trigger: only 2 in new streak")
	}

	// Third in new streak hits threshold
	if triggered := c.RecordConnectionError(); !triggered {
		t.Error("should trigger after 3 consecutive errors")
	}

	// Now clear and verify immediate re-trigger
	c.ClearConnectionErrors()
	if triggered := c.RecordConnectionError(); !triggered {
		t.Error("should trigger immediately: flag is sticky")
	}
}

// mockTool is a minimal tool implementation for testing the countingTool wrapper.
type mockTool struct {
	name    string
	results []map[string]any // results to return in order
	callIdx int
}

func (m *mockTool) Name() string                                            { return m.name }
func (m *mockTool) Description() string                                     { return "mock tool" }
func (m *mockTool) IsLongRunning() bool                                     { return false }
func (m *mockTool) Category() ToolCategory                                  { return CategoryReadOnly }
func (m *mockTool) ProcessRequest(_ tool.Context, req *model.LLMRequest) error { return nil }
func (m *mockTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{Name: m.name, Description: "mock"}
}
func (m *mockTool) Run(_ tool.Context, _ any) (map[string]any, error) {
	if m.callIdx < len(m.results) {
		r := m.results[m.callIdx]
		m.callIdx++
		return r, nil
	}
	return map[string]any{"result": "ok"}, nil
}

func TestCountingToolConnectionErrorWarning(t *testing.T) {
	counter := NewToolCallCounter(10)
	connErr := map[string]any{"error": "dial tcp 10.96.0.1:443: i/o timeout"}
	okResult := map[string]any{"namespaces": []string{"default"}}

	mock := &mockTool{
		name: "list_namespaces",
		results: []map[string]any{
			connErr,  // 1: error, no warning
			connErr,  // 2: error, no warning
			connErr,  // 3: error, WARNING injected
			connErr,  // 4: error, WARNING injected
			okResult, // 5: success, clears streak
			connErr,  // 6: error, WARNING (flag sticky)
		},
	}

	ct := &countingTool{inner: mock, counter: counter}

	for i := 0; i < 6; i++ {
		result, err := ct.Run(nil, map[string]any{})
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}

		_, hasWarning := result["_connection_error"]
		switch i + 1 {
		case 1, 2:
			if hasWarning {
				t.Errorf("call %d: unexpected _connection_error", i+1)
			}
		case 3, 4:
			if !hasWarning {
				t.Errorf("call %d: expected _connection_error", i+1)
			}
		case 5:
			if hasWarning {
				t.Errorf("call %d: success should not have _connection_error", i+1)
			}
		case 6:
			if !hasWarning {
				t.Errorf("call %d: expected _connection_error (sticky flag)", i+1)
			}
		}
	}
}
