package tools

import (
	"fmt"
	"sync"
)

// SecretInputRequest is sent from a tool to the REPL to request masked user input.
// The tool blocks on ResultCh until the REPL sends the user's response.
type SecretInputRequest struct {
	Prompt   string
	ResultCh chan string // REPL sends the value back here
}

// DirectIO provides a side channel for tools to communicate directly with the
// user, bypassing the LLM. Tools write output to a buffer (Print) that the REPL
// drains after each tool response event. Tools can also request masked input
// from the user (RequestInput) for secrets.
type DirectIO struct {
	mu      sync.Mutex
	output  []string
	InputCh chan SecretInputRequest // tool → REPL
}

// NewDirectIO creates a new DirectIO instance.
func NewDirectIO() *DirectIO {
	return &DirectIO{
		InputCh: make(chan SecretInputRequest),
	}
}

// Print appends text to the direct output buffer for the REPL to display.
func (d *DirectIO) Print(text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.output = append(d.output, text)
}

// Drain returns and clears the buffered output lines.
func (d *DirectIO) Drain() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.output) == 0 {
		return nil
	}
	out := d.output
	d.output = nil
	return out
}

// RequestInput sends a secret input request to the REPL and blocks until the
// user responds. Returns an error if the input channel is nil (non-interactive
// mode should handle input differently).
func (d *DirectIO) RequestInput(prompt string) (string, error) {
	if d.InputCh == nil {
		return "", fmt.Errorf("secret input not available (non-interactive mode)")
	}
	req := SecretInputRequest{
		Prompt:   prompt,
		ResultCh: make(chan string, 1),
	}
	d.InputCh <- req
	value := <-req.ResultCh
	return value, nil
}
