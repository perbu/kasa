package repl

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/chzyer/readline"
	"golang.org/x/term"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"
	"k8s.io/client-go/util/homedir"
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

// Run starts the interactive REPL loop.
func (r *REPL) Run(ctx context.Context) error {
	// Initialize session state for plan/approval workflow
	state := NewSessionState()

	// Set up readline with history
	historyFile := ""
	if home := homedir.HomeDir(); home != "" {
		kasaDir := filepath.Join(home, ".kasa")
		if err := os.MkdirAll(kasaDir, 0755); err == nil {
			historyFile = filepath.Join(kasaDir, "history")
		}
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:            "> ",
		HistoryFile:       historyFile,
		HistorySearchFold: true,
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
	})
	if err != nil {
		return fmt.Errorf("failed to initialize readline: %w", err)
	}
	defer rl.Close()

	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				continue // Ctrl+C, just show new prompt
			}
			if err == io.EOF {
				fmt.Println("Goodbye!")
				break
			}
			return fmt.Errorf("error reading input: %w", err)
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			break
		}

		// Handle special commands for plan approval
		switch strings.ToLower(input) {
		case "yes", "y", "/approve":
			if state.HasPendingPlan() {
				plan := state.ApprovePlan()
				fmt.Println("Plan approved. Executing...")
				execPrompt := FormatExecutionPrompt(plan)
				if err := r.runAgent(ctx, state, execPrompt); err != nil {
					fmt.Printf("Error: %v\n", err)
				}
				state.Reset()
			} else {
				fmt.Println("No pending plan to approve.")
			}
			continue
		case "no", "n", "/reject":
			if state.HasPendingPlan() {
				state.RejectPlan()
				fmt.Println("Plan rejected.")
			} else {
				fmt.Println("No pending plan to reject.")
			}
			continue
		case "/plan":
			if state.HasPendingPlan() {
				DisplayPlan(state.PendingPlan)
			} else {
				fmt.Println("No pending plan.")
			}
			continue
		}

		// If there's a pending plan, warn the user
		if state.HasPendingPlan() {
			fmt.Println("You have a pending plan. Type 'yes' to approve, 'no' to reject, or '/plan' to review.")
			continue
		}

		// Send message and handle response
		if err := r.runAgent(ctx, state, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}

	return nil
}

// RunSinglePrompt runs the agent with a single prompt (non-interactive mode).
func (r *REPL) RunSinglePrompt(ctx context.Context, prompt string) error {
	return r.runAgent(ctx, nil, prompt)
}

// runAgent runs the agent with the given prompt.
// If state is provided, it will detect propose_plan calls and update the state.
func (r *REPL) runAgent(ctx context.Context, state *SessionState, prompt string) error {
	if r.debug {
		fmt.Printf("[DEBUG] Sending message: %s\n", prompt)
	}

	// Setup markdown renderer
	mdRenderer, mdErr := setupMarkdownRenderer()
	if mdErr != nil && r.debug {
		fmt.Printf("[DEBUG] Markdown renderer setup failed: %v\n", mdErr)
	}

	// Create user message content
	userMessage := genai.NewContentFromText(prompt, genai.RoleUser)

	// Initialize and start status line
	status := NewStatusLine()
	status.Start()

	// Execute agent with the user message
	for event, err := range r.runner.Run(ctx, "user1", "session1", userMessage, agent.RunConfig{}) {
		if err != nil {
			status.Stop()
			return fmt.Errorf("agent execution failed: %w", err)
		}

		// Update status line with event info
		status.Update(event)

		if event != nil && event.Content != nil {
			// Extract text from all parts in the content
			for _, part := range event.Content.Parts {
				// Detect propose_plan function call
				if part.FunctionCall != nil && part.FunctionCall.Name == "propose_plan" {
					if state != nil && part.FunctionCall.Args != nil {
						plan := ParsePlanFromResponse(part.FunctionCall.Args)
						if plan != nil {
							state.SetPendingPlan(plan)
							if r.debug {
								fmt.Printf("[DEBUG] Detected propose_plan call\n")
							}
						}
					}
				}

				if part.Text != "" {
					// Clear status line before printing content
					status.ClearForOutput()

					// Try to render as markdown
					if mdRenderer != nil {
						rendered, renderErr := mdRenderer.Render(part.Text)
						if renderErr == nil {
							fmt.Print(rendered)
							continue
						}
					}
					// Fallback to plain text
					fmt.Print(part.Text)
				}
			}
		}
	}

	// Stop and clear status line
	status.Stop()
	fmt.Println()

	// Display pending plan if one was proposed
	if state != nil && state.HasPendingPlan() {
		DisplayPlan(state.PendingPlan)
	}

	return nil
}

// PrintWelcome displays a fancy markdown-rendered welcome message.
func (r *REPL) PrintWelcome(model string, toolCount int, deploymentsDir string) {
	welcome := fmt.Sprintf(`# Kasa

**Kubernetes Deployment Assistant** _(Safe Mode)_

| Setting | Value |
|---------|-------|
| Model | %s |
| Tools | %d |
| Deployments folder | %s |

Commands: **yes**/**no** to approve/reject plans, **exit** to quit.
`, model, toolCount, deploymentsDir)

	renderer, err := setupMarkdownRenderer()
	if err != nil {
		// Fallback to plain text
		fmt.Printf("Kasa - Kubernetes Deployment Assistant (Safe Mode)\n")
		fmt.Printf("Model: %s | Tools: %d | Deployments: %s\n", model, toolCount, deploymentsDir)
		fmt.Printf("Type 'exit' or 'quit' to exit.\n\n")
		return
	}

	rendered, err := renderer.Render(welcome)
	if err != nil {
		// Fallback to plain text
		fmt.Printf("Kasa - Kubernetes Deployment Assistant (Safe Mode)\n")
		fmt.Printf("Model: %s | Tools: %d | Deployments: %s\n", model, toolCount, deploymentsDir)
		fmt.Printf("Type 'exit' or 'quit' to exit.\n\n")
		return
	}

	fmt.Print(rendered)
}

// setupMarkdownRenderer creates a glamour renderer configured for the terminal.
func setupMarkdownRenderer() (*glamour.TermRenderer, error) {
	// Detect terminal width
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}

	// Create renderer with auto style detection
	return glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
}
