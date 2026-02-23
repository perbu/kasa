package tools

import (
	"fmt"
	"sync"
	"sync/atomic"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// ToolCallCounter tracks per-tool invocation counts across a single agent turn.
// Call Reset() at the start of each turn to clear the counts.
type ToolCallCounter struct {
	mu        sync.Mutex
	counts    map[string]int
	threshold int // inject warning after this many calls to the same tool
	total     atomic.Int32
}

// NewToolCallCounter creates a counter that warns after threshold calls per tool.
func NewToolCallCounter(threshold int) *ToolCallCounter {
	return &ToolCallCounter{
		counts:    make(map[string]int),
		threshold: threshold,
	}
}

// Increment records a call and returns the new count for that tool.
func (c *ToolCallCounter) Increment(name string) int {
	c.total.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[name]++
	return c.counts[name]
}

// Total returns the total number of tool calls across all tools.
func (c *ToolCallCounter) Total() int {
	return int(c.total.Load())
}

// Reset clears all counts. Call at the start of each agent turn.
func (c *ToolCallCounter) Reset() {
	c.total.Store(0)
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.counts {
		delete(c.counts, k)
	}
}

// runnableTool combines all interfaces that ADK expects a tool to implement.
type runnableTool interface {
	functionTool
	ProcessRequest(ctx tool.Context, req *model.LLMRequest) error
	Run(ctx tool.Context, args any) (map[string]any, error)
}

// countingTool wraps a runnableTool to track invocations and inject warnings.
// It satisfies both the tool.Tool interface and ADK's internal
// RequestProcessor / FunctionTool interfaces so the runner dispatches it correctly.
type countingTool struct {
	inner   runnableTool
	counter *ToolCallCounter
}

// tool.Tool interface
func (ct *countingTool) Name() string       { return ct.inner.Name() }
func (ct *countingTool) Description() string { return ct.inner.Description() }
func (ct *countingTool) IsLongRunning() bool { return ct.inner.IsLongRunning() }

// ADK toolinternal.RequestProcessor interface (duck-typed).
// We delegate to the inner tool to register the declaration, then overwrite
// the req.Tools entry so ADK dispatches Run() on us (the wrapper) instead
// of the unwrapped inner tool.
func (ct *countingTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if err := ct.inner.ProcessRequest(ctx, req); err != nil {
		return err
	}
	if req.Tools != nil {
		req.Tools[ct.inner.Name()] = ct
	}
	return nil
}

// ADK toolinternal.FunctionTool interface (duck-typed)
func (ct *countingTool) Declaration() *genai.FunctionDeclaration {
	return ct.inner.Declaration()
}

func (ct *countingTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	count := ct.counter.Increment(ct.inner.Name())
	result, err := ct.inner.Run(ctx, args)
	if err != nil {
		return result, err
	}

	if ct.counter.threshold > 0 && count >= ct.counter.threshold {
		if result == nil {
			result = make(map[string]any)
		}
		result["_warning"] = fmt.Sprintf(
			"You have called %s %d times this turn. "+
				"Stop and consider: are you stuck in a loop? "+
				"If the information you need isn't available after multiple attempts, "+
				"tell the user what you found and what is missing instead of retrying.",
			ct.inner.Name(), count)
	}

	return result, err
}

// Category delegates to the inner tool.
func (ct *countingTool) Category() ToolCategory {
	return ct.inner.Category()
}
