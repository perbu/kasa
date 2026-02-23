package repl

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
	"google.golang.org/adk/session"
)

// StatusLine manages the status display at the bottom of the terminal.
type StatusLine struct {
	mu                sync.Mutex
	state             string
	toolName          string
	toolReason        string // reason/context for the tool call (from Args["reason"])
	inputTokens       int32
	outputTokens      int32
	totalInputTokens  int32     // cumulative input tokens across the session
	totalOutputTokens int32     // cumulative output tokens across the session
	toolStateTime     time.Time // when we entered tool state
	spinIdx           int
	ticker            *time.Ticker
	done              chan struct{}
	termWidth         int
	isTTY             bool
}

const minToolDisplayTime = 500 * time.Millisecond

var spinChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NewStatusLine creates a new status line manager.
func NewStatusLine() *StatusLine {
	width := 80
	isTTY := term.IsTerminal(int(os.Stderr.Fd()))
	if w, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && w > 0 {
		width = w
	}
	return &StatusLine{
		state:     "idle",
		termWidth: width,
		isTTY:     isTTY,
	}
}

// Start begins the status line animation.
func (s *StatusLine) Start() {
	s.mu.Lock()
	s.state = "thinking"
	s.toolName = ""
	s.toolReason = ""
	s.inputTokens = 0
	s.outputTokens = 0
	s.done = make(chan struct{})
	s.ticker = time.NewTicker(80 * time.Millisecond)
	s.mu.Unlock()

	go func() {
		for {
			select {
			case <-s.done:
				return
			case <-s.ticker.C:
				s.mu.Lock()
				s.spinIdx = (s.spinIdx + 1) % len(spinChars)
				s.render()
				s.mu.Unlock()
			}
		}
	}()

	s.mu.Lock()
	s.render()
	s.mu.Unlock()
}

// Stop clears and stops the status line.
func (s *StatusLine) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.done != nil {
		close(s.done)
	}
	s.clear()
	s.state = "idle"
}

// Update processes an event and updates the status line.
func (s *StatusLine) Update(event *session.Event) {
	if event == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Update token counts if available
	if event.UsageMetadata != nil {
		s.inputTokens = event.UsageMetadata.PromptTokenCount
		s.outputTokens = event.UsageMetadata.CandidatesTokenCount
		s.totalInputTokens += event.UsageMetadata.PromptTokenCount
		s.totalOutputTokens += event.UsageMetadata.CandidatesTokenCount
	}

	// Check for function calls in the content
	if event.Content != nil {
		for _, part := range event.Content.Parts {
			if part.FunctionCall != nil {
				s.state = "tool"
				s.toolName = part.FunctionCall.Name
				// Extract reason from the tool's Args if provided
				s.toolReason = s.extractReasonFromArgs(part.FunctionCall.Args)
				s.toolStateTime = time.Now()
				s.render()
				return
			}
			if part.FunctionResponse != nil {
				s.waitForToolDisplay()
				s.state = "receiving"
				s.toolReason = ""
				s.render()
				return
			}
			if part.Text != "" {
				s.waitForToolDisplay()
				if event.Partial {
					s.state = "streaming"
				} else {
					s.state = "receiving"
				}
			}
		}
	}

	s.render()
}

// waitForToolDisplay ensures tool state is visible for minimum time.
// Must be called with mutex held.
func (s *StatusLine) waitForToolDisplay() {
	if s.state != "tool" {
		return
	}
	elapsed := time.Since(s.toolStateTime)
	if elapsed < minToolDisplayTime {
		s.mu.Unlock()
		time.Sleep(minToolDisplayTime - elapsed)
		s.mu.Lock()
	}
}

// extractReasonFromArgs extracts the "reason" field from tool call arguments.
// No truncation here — render() truncates the full line to terminal width.
func (s *StatusLine) extractReasonFromArgs(args map[string]any) string {
	if args == nil {
		return ""
	}

	reason, ok := args["reason"].(string)
	if !ok || reason == "" {
		return ""
	}

	return reason
}

// ClearForOutput clears the status line before printing content.
func (s *StatusLine) ClearForOutput() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clear()
}

// RestoreAfterOutput restores the status line after printing content.
func (s *StatusLine) RestoreAfterOutput() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.render()
}

func (s *StatusLine) render() {
	if !s.isTTY {
		return
	}

	var status string
	spin := spinChars[s.spinIdx]

	switch s.state {
	case "thinking":
		status = fmt.Sprintf("%s 🧠 Thinking...", spin)
	case "tool":
		if s.toolReason != "" {
			status = fmt.Sprintf("%s 🔧 %s: %s", spin, s.toolName, s.toolReason)
		} else {
			status = fmt.Sprintf("%s 🔧 Calling: %s", spin, s.toolName)
		}
	case "streaming":
		status = fmt.Sprintf("%s 📥 Receiving...", spin)
	case "receiving":
		status = fmt.Sprintf("%s 📥 Receiving...", spin)
	default:
		status = ""
	}

	// Add cumulative token totals
	if s.totalInputTokens > 0 {
		status = fmt.Sprintf("%s  [%s in, %s out]",
			status, formatTokenCount(s.totalInputTokens), formatTokenCount(s.totalOutputTokens))
	}

	// Pad and truncate to terminal width
	if len(status) > s.termWidth-1 {
		status = status[:s.termWidth-4] + "..."
	}
	status = status + strings.Repeat(" ", max(0, s.termWidth-len(status)-1))

	// Use dim color for the status line
	fmt.Fprintf(os.Stderr, "\r\033[2m%s\033[0m", status)
}

func (s *StatusLine) clear() {
	if !s.isTTY {
		return
	}
	// Clear the line with spaces and return cursor to start
	fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", s.termWidth-1))
}

// formatTokenCount formats a token count for display (e.g., 1234 → "1.2k", 1234567 → "1.2M").
func formatTokenCount(count int32) string {
	switch {
	case count >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(count)/1_000_000)
	case count >= 1_000:
		return fmt.Sprintf("%.1fk", float64(count)/1_000)
	default:
		return fmt.Sprintf("%d", count)
	}
}
