package tools

import (
	"strings"
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

	// Simulate plan approval (unrestricted)
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
	kt := NewKubeTools(clientset, dynamicClient, nil, mgr, nil, "", 3, NewDirectIO(), nil)

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
		if strings.HasPrefix(errMsg, "BLOCKED:") {
			t.Errorf("tool %q: still blocked by guard after Allow(): %s", tl.Name(), errMsg)
		}
	}
}

func TestGuardAllowPlanSpecificTools(t *testing.T) {
	guard := NewMutationGuard()
	counter := NewToolCallCounter(10)

	allowed := &mockMutatingTool{name: "apply_resource"}
	blocked := &mockMutatingTool{name: "delete_resource"}

	ctAllowed := &countingTool{inner: allowed, counter: counter, guard: guard}
	ctBlocked := &countingTool{inner: blocked, counter: counter, guard: guard}

	// Approve only apply_resource
	guard.AllowPlan([]GuardedAction{
		{Tool: "apply_resource", PinnedArgs: map[string]string{}},
	})

	// Allowed tool should execute
	result, err := ctAllowed.Run(nil, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed.called {
		t.Fatal("apply_resource should have been called")
	}
	if result["result"] != "executed" {
		t.Errorf("expected 'executed', got: %v", result)
	}

	// Non-listed tool should be blocked
	result, err = ctBlocked.Run(nil, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if blocked.called {
		t.Fatal("delete_resource should NOT have been called")
	}
	errMsg, ok := result["error"].(string)
	if !ok || !strings.Contains(errMsg, "not part of the approved plan") {
		t.Fatalf("expected 'not part of the approved plan' error, got: %v", result)
	}
}

func TestGuardAllowPermitsAll(t *testing.T) {
	guard := NewMutationGuard()
	counter := NewToolCallCounter(10)

	tool1 := &mockMutatingTool{name: "apply_resource"}
	tool2 := &mockMutatingTool{name: "delete_resource"}

	ct1 := &countingTool{inner: tool1, counter: counter, guard: guard}
	ct2 := &countingTool{inner: tool2, counter: counter, guard: guard}

	// Allow() = unrestricted
	guard.Allow()

	if _, err := ct1.Run(nil, map[string]any{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := ct2.Run(nil, map[string]any{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tool1.called || !tool2.called {
		t.Fatal("Allow() should permit all mutating tools")
	}
}

func TestGuardBlockClearsActions(t *testing.T) {
	guard := NewMutationGuard()

	guard.AllowPlan([]GuardedAction{
		{Tool: "apply_resource", PinnedArgs: map[string]string{}},
	})
	if guard.IsBlocked() {
		t.Fatal("guard should not be blocked after AllowPlan")
	}

	guard.Block()
	if !guard.IsBlocked() {
		t.Fatal("guard should be blocked after Block()")
	}

	// After Block(), even previously-allowed tools should be blocked
	err := guard.CheckAccess("apply_resource", nil)
	if err == nil {
		t.Fatal("expected error after Block()")
	}
	if !strings.Contains(err.Error(), "requires plan approval") {
		t.Errorf("expected 'requires plan approval' error, got: %s", err)
	}
}

func TestCheckAccessDistinctErrors(t *testing.T) {
	guard := NewMutationGuard()

	// Blocked state: "requires plan approval"
	err := guard.CheckAccess("apply_resource", nil)
	if err == nil {
		t.Fatal("expected error when blocked")
	}
	if !strings.Contains(err.Error(), "requires plan approval") {
		t.Errorf("expected 'requires plan approval', got: %s", err)
	}

	// Allowed with restriction: unlisted tool gets "not part of the approved plan"
	guard.AllowPlan([]GuardedAction{
		{Tool: "apply_resource", PinnedArgs: map[string]string{}},
	})
	err = guard.CheckAccess("delete_resource", nil)
	if err == nil {
		t.Fatal("expected error for unlisted tool")
	}
	if !strings.Contains(err.Error(), "not part of the approved plan") {
		t.Errorf("expected 'not part of the approved plan', got: %s", err)
	}

	// Listed tool: no error
	err = guard.CheckAccess("apply_resource", nil)
	if err != nil {
		t.Errorf("expected no error for listed tool, got: %s", err)
	}
}

// --- Strict plan enforcement tests ---

func TestGuardEnforcesCallCount(t *testing.T) {
	guard := NewMutationGuard()

	// Plan allows apply_resource exactly once
	guard.AllowPlan([]GuardedAction{
		{Tool: "apply_resource", PinnedArgs: map[string]string{"namespace": "default"}},
	})

	// First call succeeds
	err := guard.CheckAccess("apply_resource", map[string]any{"namespace": "default"})
	if err != nil {
		t.Fatalf("first call should succeed: %v", err)
	}

	// Second call is blocked (budget exceeded)
	err = guard.CheckAccess("apply_resource", map[string]any{"namespace": "default"})
	if err == nil {
		t.Fatal("second call should be blocked (budget exceeded)")
	}
	if !strings.Contains(err.Error(), "already been executed") {
		t.Errorf("expected 'already been executed' error, got: %s", err)
	}
}

func TestGuardEnforcesCallCountMultiple(t *testing.T) {
	guard := NewMutationGuard()

	// Plan allows apply_resource twice with different targets
	guard.AllowPlan([]GuardedAction{
		{Tool: "apply_resource", PinnedArgs: map[string]string{"namespace": "default", "name": "nginx"}},
		{Tool: "apply_resource", PinnedArgs: map[string]string{"namespace": "default", "name": "redis"}},
	})

	// Both calls succeed
	err := guard.CheckAccess("apply_resource", map[string]any{"namespace": "default", "name": "nginx"})
	if err != nil {
		t.Fatalf("first call should succeed: %v", err)
	}
	err = guard.CheckAccess("apply_resource", map[string]any{"namespace": "default", "name": "redis"})
	if err != nil {
		t.Fatalf("second call should succeed: %v", err)
	}

	// Third call is blocked
	err = guard.CheckAccess("apply_resource", map[string]any{"namespace": "default", "name": "nginx"})
	if err == nil {
		t.Fatal("third call should be blocked")
	}
	if !strings.Contains(err.Error(), "already been executed") {
		t.Errorf("expected 'already been executed', got: %s", err)
	}
}

func TestGuardEnforcesPinnedParams(t *testing.T) {
	guard := NewMutationGuard()

	guard.AllowPlan([]GuardedAction{
		{Tool: "delete_resource", PinnedArgs: map[string]string{
			"namespace": "default",
			"name":      "nginx",
			"kind":      "deployment",
		}},
	})

	// Wrong namespace
	err := guard.CheckAccess("delete_resource", map[string]any{
		"namespace": "kube-system",
		"name":      "nginx",
		"kind":      "deployment",
	})
	if err == nil {
		t.Fatal("should block wrong namespace")
	}
	if !strings.Contains(err.Error(), "don't match") {
		t.Errorf("expected parameter mismatch error, got: %s", err)
	}

	// Wrong name
	err = guard.CheckAccess("delete_resource", map[string]any{
		"namespace": "default",
		"name":      "redis",
		"kind":      "deployment",
	})
	if err == nil {
		t.Fatal("should block wrong name")
	}

	// Correct parameters
	err = guard.CheckAccess("delete_resource", map[string]any{
		"namespace": "default",
		"name":      "nginx",
		"kind":      "deployment",
	})
	if err != nil {
		t.Fatalf("correct params should succeed: %v", err)
	}
}

func TestGuardPinnedParamsMissing(t *testing.T) {
	guard := NewMutationGuard()

	guard.AllowPlan([]GuardedAction{
		{Tool: "delete_resource", PinnedArgs: map[string]string{
			"namespace": "default",
			"name":      "nginx",
		}},
	})

	// Missing pinned param "name"
	err := guard.CheckAccess("delete_resource", map[string]any{
		"namespace": "default",
	})
	if err == nil {
		t.Fatal("should block when pinned param is missing from args")
	}
}

func TestGuardNoPinnedParamsMatchesAnything(t *testing.T) {
	guard := NewMutationGuard()

	// Action with no pinned params (e.g. commit_manifests)
	guard.AllowPlan([]GuardedAction{
		{Tool: "commit_manifests", PinnedArgs: map[string]string{}},
	})

	// Should match with any args
	err := guard.CheckAccess("commit_manifests", map[string]any{"message": "deploy nginx"})
	if err != nil {
		t.Fatalf("no pinned params should match any args: %v", err)
	}

	// But only once
	err = guard.CheckAccess("commit_manifests", map[string]any{"message": "something else"})
	if err == nil {
		t.Fatal("second call should be blocked (budget exceeded)")
	}
}

func TestGuardExtraArgsAreIgnored(t *testing.T) {
	guard := NewMutationGuard()

	guard.AllowPlan([]GuardedAction{
		{Tool: "apply_resource", PinnedArgs: map[string]string{"namespace": "default"}},
	})

	// Extra non-pinned args (like "yaml") should not affect matching
	err := guard.CheckAccess("apply_resource", map[string]any{
		"namespace": "default",
		"yaml":      "apiVersion: v1\nkind: ConfigMap\n...",
	})
	if err != nil {
		t.Fatalf("extra non-pinned args should be ignored: %v", err)
	}
}

func TestGuardActionsConsumedOutOfOrder(t *testing.T) {
	guard := NewMutationGuard()

	guard.AllowPlan([]GuardedAction{
		{Tool: "apply_resource", PinnedArgs: map[string]string{"namespace": "default", "name": "nginx"}},
		{Tool: "delete_resource", PinnedArgs: map[string]string{"namespace": "default", "name": "old-app"}},
		{Tool: "apply_resource", PinnedArgs: map[string]string{"namespace": "staging", "name": "redis"}},
	})

	// Execute action 3, then 1, then 2 — all should succeed
	err := guard.CheckAccess("apply_resource", map[string]any{"namespace": "staging", "name": "redis"})
	if err != nil {
		t.Fatalf("action 3 out of order should succeed: %v", err)
	}
	err = guard.CheckAccess("apply_resource", map[string]any{"namespace": "default", "name": "nginx"})
	if err != nil {
		t.Fatalf("action 1 out of order should succeed: %v", err)
	}
	err = guard.CheckAccess("delete_resource", map[string]any{"namespace": "default", "name": "old-app"})
	if err != nil {
		t.Fatalf("action 2 out of order should succeed: %v", err)
	}

	// All consumed — nothing left
	err = guard.CheckAccess("apply_resource", map[string]any{"namespace": "default", "name": "nginx"})
	if err == nil {
		t.Fatal("should block after all actions consumed")
	}
}

func TestGuardNilArgsWithPinnedParams(t *testing.T) {
	guard := NewMutationGuard()

	guard.AllowPlan([]GuardedAction{
		{Tool: "delete_resource", PinnedArgs: map[string]string{"namespace": "default"}},
	})

	// nil args should not panic, and should fail because pinned param "namespace" is missing
	err := guard.CheckAccess("delete_resource", nil)
	if err == nil {
		t.Fatal("nil args should not match action with pinned params")
	}
	if !strings.Contains(err.Error(), "don't match") {
		t.Errorf("expected parameter mismatch error, got: %s", err)
	}
}

func TestGuardMixedPinnedAndUnpinnedActions(t *testing.T) {
	guard := NewMutationGuard()

	guard.AllowPlan([]GuardedAction{
		{Tool: "apply_resource", PinnedArgs: map[string]string{"namespace": "default", "name": "nginx"}},
		{Tool: "commit_manifests", PinnedArgs: map[string]string{}}, // no pinned params
		{Tool: "apply_resource", PinnedArgs: map[string]string{"namespace": "prod", "name": "api"}},
	})

	// commit_manifests with any args
	err := guard.CheckAccess("commit_manifests", map[string]any{"message": "whatever"})
	if err != nil {
		t.Fatalf("unpinned action should match: %v", err)
	}

	// apply_resource must match pinned params
	err = guard.CheckAccess("apply_resource", map[string]any{"namespace": "prod", "name": "api"})
	if err != nil {
		t.Fatalf("matching pinned action should succeed: %v", err)
	}

	// Wrong params for remaining apply_resource action
	err = guard.CheckAccess("apply_resource", map[string]any{"namespace": "prod", "name": "api"})
	if err == nil {
		t.Fatal("should not match already-consumed action")
	}
	if !strings.Contains(err.Error(), "don't match") {
		t.Errorf("expected parameter mismatch, got: %s", err)
	}

	// Correct params for the remaining action
	err = guard.CheckAccess("apply_resource", map[string]any{"namespace": "default", "name": "nginx"})
	if err != nil {
		t.Fatalf("last remaining action should match: %v", err)
	}

	// All consumed
	err = guard.CheckAccess("commit_manifests", map[string]any{"message": "again"})
	if err == nil {
		t.Fatal("should block — commit_manifests budget exhausted")
	}
}

func TestExtractPinnedArgs(t *testing.T) {
	params := map[string]any{
		"namespace": "default",
		"name":      "nginx",
		"yaml":      "big yaml body...",
		"message":   "deploy",
		"kind":      "deployment",
	}

	pinned := ExtractPinnedArgs(params)

	if pinned["namespace"] != "default" {
		t.Errorf("expected namespace=default, got %s", pinned["namespace"])
	}
	if pinned["name"] != "nginx" {
		t.Errorf("expected name=nginx, got %s", pinned["name"])
	}
	if pinned["kind"] != "deployment" {
		t.Errorf("expected kind=deployment, got %s", pinned["kind"])
	}
	if _, ok := pinned["yaml"]; ok {
		t.Error("yaml should not be pinned")
	}
	if _, ok := pinned["message"]; ok {
		t.Error("message should not be pinned")
	}
}
