package tools

import "sync/atomic"

// MutationGuard controls whether mutating tool calls are allowed to execute.
// When blocked (the default), mutating tools return an error instructing the
// LLM to use propose_plan instead. The REPL toggles the guard based on the
// plan/approval workflow: Allow() after plan approval, Block() after execution
// completes or the plan is rejected.
//
// Thread-safe via atomic operations; shared between the REPL (which toggles it)
// and countingTool wrappers (which check it in Run()).
type MutationGuard struct {
	blocked atomic.Bool
}

// NewMutationGuard creates a guard that starts in blocked state (safe mode on).
func NewMutationGuard() *MutationGuard {
	g := &MutationGuard{}
	g.blocked.Store(true)
	return g
}

// Allow permits mutating tool calls (called when a plan is approved).
func (g *MutationGuard) Allow() { g.blocked.Store(false) }

// Block prevents mutating tool calls (called after execution or plan rejection).
func (g *MutationGuard) Block() { g.blocked.Store(true) }

// IsBlocked returns true if mutating tool calls are currently blocked.
func (g *MutationGuard) IsBlocked() bool { return g.blocked.Load() }
