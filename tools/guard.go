package tools

import (
	"fmt"
	"sync"
)

// MutationGuard controls whether mutating tool calls are allowed to execute.
// When blocked (the default), mutating tools return an error instructing the
// LLM to use propose_plan instead. The REPL toggles the guard based on the
// plan/approval workflow: AllowTools() after plan approval, Block() after
// execution completes or the plan is rejected.
//
// When a plan is approved, the guard can optionally restrict execution to only
// the specific tools listed in the plan. If the LLM tries to call a mutating
// tool not in the approved plan, it gets a distinct error.
//
// Thread-safe via mutex; shared between the REPL (which toggles it) and
// countingTool wrappers (which check it in Run()).
type MutationGuard struct {
	mu           sync.Mutex
	blocked      bool
	allowedTools map[string]bool // nil = unrestricted, non-nil = only these tools
}

// NewMutationGuard creates a guard that starts in blocked state (safe mode on).
func NewMutationGuard() *MutationGuard {
	return &MutationGuard{blocked: true}
}

// AllowTools permits mutating tool calls, optionally restricted to the given
// tool names. Pass nil for unrestricted access (e.g. non-interactive mode).
// A non-nil slice restricts execution to only the listed tools.
func (g *MutationGuard) AllowTools(toolNames []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocked = false
	if toolNames == nil {
		g.allowedTools = nil
	} else {
		g.allowedTools = make(map[string]bool, len(toolNames))
		for _, name := range toolNames {
			g.allowedTools[name] = true
		}
	}
}

// Allow permits all mutating tool calls without restriction.
// Convenience method; equivalent to AllowTools(nil).
func (g *MutationGuard) Allow() { g.AllowTools(nil) }

// Block prevents all mutating tool calls and clears the allowed-tools set.
func (g *MutationGuard) Block() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocked = true
	g.allowedTools = nil
}

// CheckAccess checks whether a mutating tool is allowed to run.
// Returns nil if allowed, or an error describing why the tool is blocked.
func (g *MutationGuard) CheckAccess(toolName string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blocked {
		return fmt.Errorf(
			"BLOCKED: This tool modifies cluster state and requires plan approval. " +
				"You MUST call propose_plan first with a description of what you intend to do " +
				"and wait for user approval before executing any mutating tools.")
	}
	if g.allowedTools != nil && !g.allowedTools[toolName] {
		return fmt.Errorf(
			"BLOCKED: %q was not part of the approved plan. "+
				"You may only call the mutating tools listed in the plan the user approved. "+
				"If you need to call additional tools, call propose_plan with a revised plan "+
				"and wait for user approval.", toolName)
	}
	return nil
}

// IsBlocked returns true if all mutating tool calls are currently blocked.
func (g *MutationGuard) IsBlocked() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blocked
}
