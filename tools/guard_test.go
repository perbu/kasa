package tools

import (
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

func TestMutationGuardDefaults(t *testing.T) {
	g := NewMutationGuard()
	if !g.IsBlocked() {
		t.Fatal("new guard should be blocked by default")
	}
}

func TestMutationGuardToggle(t *testing.T) {
	g := NewMutationGuard()

	g.Allow()
	if g.IsBlocked() {
		t.Fatal("guard should be unblocked after Allow()")
	}

	g.Block()
	if !g.IsBlocked() {
		t.Fatal("guard should be blocked after Block()")
	}
}

// mockMutatingTool is a minimal mutating tool for testing guard enforcement.
type mockMutatingTool struct {
	name   string
	called bool
}

func (m *mockMutatingTool) Name() string                                            { return m.name }
func (m *mockMutatingTool) Description() string                                     { return "mock mutating tool" }
func (m *mockMutatingTool) IsLongRunning() bool                                     { return false }
func (m *mockMutatingTool) Category() ToolCategory                                  { return CategoryMutating }
func (m *mockMutatingTool) ProcessRequest(_ tool.Context, req *model.LLMRequest) error { return nil }
func (m *mockMutatingTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{Name: m.name, Description: "mock mutating"}
}
func (m *mockMutatingTool) Run(_ tool.Context, _ any) (map[string]any, error) {
	m.called = true
	return map[string]any{"result": "executed"}, nil
}

func TestGuardBlocksMutatingTool(t *testing.T) {
	guard := NewMutationGuard() // starts blocked
	counter := NewToolCallCounter(10)
	mock := &mockMutatingTool{name: "delete_resource"}
	ct := &countingTool{inner: mock, counter: counter, guard: guard}

	result, err := ct.Run(nil, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.called {
		t.Fatal("mutating tool should NOT have been called while guard is blocked")
	}
	errMsg, ok := result["error"].(string)
	if !ok {
		t.Fatalf("expected error string in result, got: %v", result)
	}
	if errMsg == "" {
		t.Fatal("error message should not be empty")
	}
	t.Logf("blocked with message: %s", errMsg)
}

func TestGuardAllowsMutatingToolAfterApproval(t *testing.T) {
	guard := NewMutationGuard()
	counter := NewToolCallCounter(10)
	mock := &mockMutatingTool{name: "apply_resource"}
	ct := &countingTool{inner: mock, counter: counter, guard: guard}

	// Simulate plan approval
	guard.Allow()

	result, err := ct.Run(nil, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.called {
		t.Fatal("mutating tool should have been called after guard.Allow()")
	}
	if result["result"] != "executed" {
		t.Errorf("expected result 'executed', got: %v", result)
	}
}

func TestGuardDoesNotBlockReadOnlyTool(t *testing.T) {
	guard := NewMutationGuard() // starts blocked
	counter := NewToolCallCounter(10)
	mock := &mockTool{name: "list_pods"} // Category: CategoryReadOnly
	ct := &countingTool{inner: mock, counter: counter, guard: guard}

	result, err := ct.Run(nil, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, hasError := result["error"]; hasError {
		t.Fatal("read-only tool should not be blocked by guard")
	}
}

func TestGuardReBlocksAfterExecution(t *testing.T) {
	guard := NewMutationGuard()
	counter := NewToolCallCounter(10)
	mock := &mockMutatingTool{name: "delete_resource"}
	ct := &countingTool{inner: mock, counter: counter, guard: guard}

	// Approve, execute, then re-block (simulating Reset)
	guard.Allow()
	_, _ = ct.Run(nil, map[string]any{})
	if !mock.called {
		t.Fatal("tool should have executed during allowed window")
	}

	// Simulate reset after execution
	guard.Block()
	mock.called = false

	result, err := ct.Run(nil, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.called {
		t.Fatal("mutating tool should NOT execute after guard is re-blocked")
	}
	if _, hasError := result["error"]; !hasError {
		t.Fatal("expected error in result when guard is blocked")
	}
}

func TestGuardNilIsPermissive(t *testing.T) {
	// A nil guard (e.g. in tests or non-interactive mode) should not block anything
	counter := NewToolCallCounter(10)
	mock := &mockMutatingTool{name: "apply_resource"}
	ct := &countingTool{inner: mock, counter: counter, guard: nil}

	_, err := ct.Run(nil, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.called {
		t.Fatal("nil guard should not block mutating tools")
	}
}

func TestGuardBlocksAllMutatingToolsInKubeTools(t *testing.T) {
	mgr := newTestManifestManager(t)
	kt := NewKubeTools(clientset, dynamicClient, mgr, "", 3, NewDirectIO())

	// Guard starts blocked
	if !kt.Guard().IsBlocked() {
		t.Fatal("guard should start blocked")
	}

	// Verify that every mutating tool is blocked
	for _, tl := range kt.All() {
		ct, ok := tl.(*countingTool)
		if !ok {
			continue
		}
		if ct.inner.Category() != CategoryMutating {
			continue
		}

		result, err := ct.Run(nil, map[string]any{})
		if err != nil {
			t.Errorf("tool %q: unexpected error: %v", tl.Name(), err)
			continue
		}
		errMsg, hasError := result["error"]
		if !hasError {
			t.Errorf("tool %q: mutating tool was NOT blocked by guard", tl.Name())
		} else {
			t.Logf("tool %q correctly blocked: %s", tl.Name(), errMsg)
		}
	}

	// After Allow(), mutating tools should no longer be blocked at the guard level
	kt.Guard().Allow()
	for _, tl := range kt.All() {
		ct, ok := tl.(*countingTool)
		if !ok {
			continue
		}
		if ct.inner.Category() != CategoryMutating {
			continue
		}

		// We just check that the guard doesn't block — the actual Run()
		// may fail due to missing K8s resources, which is fine.
		result, _ := ct.Run(nil, map[string]any{})
		errMsg, _ := result["error"].(string)
		if errMsg != "" && errMsg == "BLOCKED: This tool modifies cluster state and requires plan approval. "+
			"You MUST call propose_plan first with a description of what you intend to do "+
			"and wait for user approval before executing any mutating tools." {
			t.Errorf("tool %q: still blocked by guard after Allow()", tl.Name())
		}
	}
}
