package tools

import (
	"fmt"
	"sync"
)

// pinnedParamNames are parameter names that the guard enforces during plan
// execution. These are "targeting" parameters that determine which resource
// is affected. Large body parameters (like "yaml" or "message") are not
// pinned — call-count enforcement is sufficient for those.
var pinnedParamNames = map[string]bool{
	"namespace": true,
	"name":      true,
	"app":       true,
	"kind":      true,
	"type":      true,
}

// GuardedAction represents a single approved action with its pinned parameters.
// Each action can be executed exactly once.
type GuardedAction struct {
	Tool       string
	PinnedArgs map[string]string
	executed   bool
}

// ExtractPinnedArgs extracts pinned parameter values from a tool parameter map.
func ExtractPinnedArgs(params map[string]any) map[string]string {
	pinned := make(map[string]string)
	for k, v := range params {
		if pinnedParamNames[k] {
			pinned[k] = fmt.Sprintf("%v", v)
		}
	}
	return pinned
}

// MutationGuard controls whether mutating tool calls are allowed to execute.
// When blocked (the default), mutating tools return an error instructing the
// LLM to use propose_plan instead. The REPL toggles the guard based on the
// plan/approval workflow.
//
// Two unblocked modes:
//   - Unrestricted (actions == nil): any mutating tool is allowed. Used by
//     non-interactive mode and Allow().
//   - Strict (actions != nil): each call must match an unexecuted action by
//     tool name and pinned parameters. Set via AllowPlan().
//
// Thread-safe via mutex; shared between the REPL (which toggles it) and
// countingTool wrappers (which check it in Run()).
type MutationGuard struct {
	mu      sync.Mutex
	blocked bool
	actions []GuardedAction // nil = unrestricted, non-nil = strict per-action tracking
}

// NewMutationGuard creates a guard that starts in blocked state (safe mode on).
func NewMutationGuard() *MutationGuard {
	return &MutationGuard{blocked: true}
}

// AllowPlan permits mutating tool calls restricted to the given actions.
// Each action can be executed exactly once, and pinned parameters must match.
func (g *MutationGuard) AllowPlan(actions []GuardedAction) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocked = false
	g.actions = make([]GuardedAction, len(actions))
	copy(g.actions, actions)
}

// Allow permits all mutating tool calls without restriction.
// Used by non-interactive mode where there is no plan/approval workflow.
func (g *MutationGuard) Allow() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocked = false
	g.actions = nil
}

// Block prevents all mutating tool calls and clears the action list.
func (g *MutationGuard) Block() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocked = true
	g.actions = nil
}

// CheckAccess checks whether a mutating tool call with the given arguments is
// allowed to run. In unrestricted mode, only the tool name matters. In strict
// mode (after AllowPlan), the call must match an unexecuted action by tool
// name and pinned parameters. Returns nil if allowed.
func (g *MutationGuard) CheckAccess(toolName string, args map[string]any) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.blocked {
		return fmt.Errorf(
			"BLOCKED: This tool modifies cluster state and requires plan approval. " +
				"You MUST call propose_plan first with a description of what you intend to do " +
				"and wait for user approval before executing any mutating tools.")
	}

	// Unrestricted mode (non-interactive or Allow())
	if g.actions == nil {
		return nil
	}

	// Strict mode: find a matching unexecuted action.
	toolExists := false
	hasUnexecuted := false
	for i := range g.actions {
		if g.actions[i].Tool != toolName {
			continue
		}
		toolExists = true
		if g.actions[i].executed {
			continue
		}
		hasUnexecuted = true
		if matchesPinnedArgs(g.actions[i].PinnedArgs, args) {
			g.actions[i].executed = true
			return nil
		}
	}

	if !toolExists {
		return fmt.Errorf(
			"BLOCKED: %q was not part of the approved plan. "+
				"You may only call the mutating tools listed in the plan the user approved. "+
				"If you need to call additional tools, call propose_plan with a revised plan "+
				"and wait for user approval.", toolName)
	}

	if !hasUnexecuted {
		return fmt.Errorf(
			"BLOCKED: All approved %q actions have already been executed. "+
				"The plan allowed %d call(s) to this tool and they have all been used. "+
				"If you need additional calls, call propose_plan with a revised plan "+
				"and wait for user approval.", toolName, countTool(g.actions, toolName))
	}

	return fmt.Errorf(
		"BLOCKED: The parameters for %q don't match any approved action in the plan. "+
			"The guard checks these targeting parameters: namespace, name, app, kind, type. "+
			"If you need different parameters, call propose_plan with a revised plan "+
			"and wait for user approval.", toolName)
}

// IsBlocked returns true if all mutating tool calls are currently blocked.
func (g *MutationGuard) IsBlocked() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blocked
}

// matchesPinnedArgs returns true if all pinned parameters match the call args.
func matchesPinnedArgs(pinned map[string]string, args map[string]any) bool {
	for k, expected := range pinned {
		actual, ok := args[k]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", actual) != expected {
			return false
		}
	}
	return true
}

// countTool counts how many actions in the list use the given tool name.
func countTool(actions []GuardedAction, toolName string) int {
	n := 0
	for _, a := range actions {
		if a.Tool == toolName {
			n++
		}
	}
	return n
}
