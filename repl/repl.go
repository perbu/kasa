package repl

import (
	"context"
	"fmt"
	"os"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"golang.org/x/term"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"
)

// REPL manages the interactive read-eval-print loop.
type REPL struct {
	runner *runner.Runner
	debug  bool
}

// New creates a new REPL instance.
func New(r *runner.Runner, debug bool) *REPL {
	return &REPL{
		runner: r,
		debug:  debug,
	}
}

// Run starts the interactive REPL loop using bubbletea.
func (r *REPL) Run(ctx context.Context) error {
	// Drain any stale terminal query responses (OSC, CPR) from stdin.
	// Libraries like termenv/lipgloss/glamour query the terminal for
	// background color and capabilities during init. Responses that arrive
	// late end up in stdin and get interpreted as user input by bubbletea.
	drainStdin()

	m := newModel(r.runner, r.debug)
	p := tea.NewProgram(m, tea.WithContext(ctx))

	// Store program reference so the model can call Println.
	// m.program is a *programRef (shared pointer), so this propagates
	// to the copy held inside the tea.Program.
	m.program.p = p

	_, err := p.Run()
	return err
}

// RunSinglePrompt runs the agent with a single prompt (non-interactive mode).
func (r *REPL) RunSinglePrompt(ctx context.Context, prompt string) error {
	return r.runAgentSync(ctx, nil, prompt)
}

// runAgentSync runs the agent synchronously with the given prompt.
// Used for non-interactive mode. Uses the hand-rolled StatusLine.
func (r *REPL) runAgentSync(ctx context.Context, state *SessionState, prompt string) error {
	if r.debug {
		fmt.Printf("[DEBUG] Sending message: %s\n", prompt)
	}

	mdRenderer, mdErr := setupMarkdownRenderer()
	if mdErr != nil && r.debug {
		fmt.Printf("[DEBUG] Markdown renderer setup failed: %v\n", mdErr)
	}

	userMessage := genai.NewContentFromText(prompt, genai.RoleUser)

	status := NewStatusLine()
	status.Start()

	for event, err := range r.runner.Run(ctx, "user1", "session1", userMessage, agent.RunConfig{}) {
		if err != nil {
			status.Stop()
			return fmt.Errorf("agent execution failed: %w", err)
		}

		status.Update(event)

		if event != nil && event.Content != nil {
			for _, part := range event.Content.Parts {
				if part.FunctionCall != nil && part.FunctionCall.Name == "propose_plan" {
					if state != nil && part.FunctionCall.Args != nil {
						plan := ParsePlanFromResponse(part.FunctionCall.Args)
						if plan != nil {
							state.SetPendingPlan(plan)
						}
					}
				}

				if part.FunctionCall != nil && part.FunctionCall.Name == "ask_clarification" {
					if state != nil && part.FunctionCall.Args != nil {
						clarification := ParseClarificationFromResponse(part.FunctionCall.Args)
						if clarification != nil {
							state.PendingClarification = clarification
						}
					}
				}

				if part.Text != "" {
					status.ClearForOutput()
					if mdRenderer != nil {
						rendered, renderErr := mdRenderer.Render(part.Text)
						if renderErr == nil {
							fmt.Print(rendered)
							continue
						}
					}
					fmt.Print(part.Text)
				}
			}
		}
	}

	status.Stop()
	fmt.Println()

	if state != nil && state.PendingClarification != nil {
		DisplayClarification(state.PendingClarification)
		state.PendingClarification = nil
	}

	if state != nil && state.HasPendingPlan() {
		DisplayPlan(state.PendingPlan)
	}

	return nil
}

// PrintWelcome displays a fancy markdown-rendered welcome message.
func (r *REPL) PrintWelcome(version, model string, toolCount int, deploymentsDir string) {
	welcome := fmt.Sprintf(`# Kasa %s

**Kubernetes Deployment Assistant** _(Safe Mode)_

| Setting | Value |
|---------|-------|
| Model | %s |
| Tools | %d |
| Deployments folder | %s |

Commands: **yes**/**no** to approve/reject plans, **exit** to quit.
`, version, model, toolCount, deploymentsDir)

	renderer, err := setupMarkdownRenderer()
	if err != nil {
		fmt.Printf("Kasa %s - Kubernetes Deployment Assistant (Safe Mode)\n", version)
		fmt.Printf("Model: %s | Tools: %d | Deployments: %s\n", model, toolCount, deploymentsDir)
		fmt.Printf("Type 'exit' or 'quit' to exit.\n\n")
		return
	}

	rendered, err := renderer.Render(welcome)
	if err != nil {
		fmt.Printf("Kasa %s - Kubernetes Deployment Assistant (Safe Mode)\n", version)
		fmt.Printf("Model: %s | Tools: %d | Deployments: %s\n", model, toolCount, deploymentsDir)
		fmt.Printf("Type 'exit' or 'quit' to exit.\n\n")
		return
	}

	fmt.Print(rendered)
}

// setupMarkdownRenderer creates a glamour renderer configured for the terminal.
func setupMarkdownRenderer() (*glamour.TermRenderer, error) {
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}

	return glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
}

// drainStdin discards any bytes sitting in the terminal input buffer.
// This prevents stale escape sequence responses (from terminal color/capability
// queries) from being interpreted as user input by bubbletea.
func drainStdin() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return
	}

	// Enter raw mode so escape sequences (which lack newlines) become readable.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return
	}
	defer term.Restore(fd, oldState)

	// Set non-blocking so we only read what's already buffered.
	if err := syscall.SetNonblock(fd, true); err != nil {
		return
	}
	defer syscall.SetNonblock(fd, false)

	buf := make([]byte, 256)
	for {
		n, _ := syscall.Read(fd, buf)
		if n <= 0 {
			break
		}
	}
}
