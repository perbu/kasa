package tools

import (
	"testing"

	"google.golang.org/adk/model"
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
	kt := NewKubeTools(clientset, dynamicClient, mgr, "", 3)

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
	kt := NewKubeTools(clientset, dynamicClient, mgr, "", 3)

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
	kt := NewKubeTools(clientset, dynamicClient, mgr, "", 3)

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
