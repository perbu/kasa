package repl

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"golang.org/x/term"
	"github.com/perbu/kasa/manifest"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// ContextInfo describes a single kubeconfig context.
type ContextInfo struct {
	Name    string
	Cluster string
	Active  bool
}

// ContextListFunc returns all known kubeconfig contexts.
type ContextListFunc func() ([]ContextInfo, error)

// ContextSwitchResult carries the rebuilt stack after a context switch.
type ContextSwitchResult struct {
	Runner         *runner.Runner
	SessionService session.Service
	Manifest       *manifest.Manager
	ContextName    string
}

// ContextSwitchFunc rebuilds the entire agent stack for a new context.
type ContextSwitchFunc func(contextName string) (*ContextSwitchResult, error)

// ToolCallResetter is implemented by objects that track tool call counts
// and need to be reset between agent turns.
type ToolCallResetter interface {
	Reset()
}

// REPL manages the interactive read-eval-print loop.
type REPL struct {
	runner           *runner.Runner
	sessionService   session.Service
	debug            bool
	manifest         *manifest.Manager
	apiKey           string
	baseURL          string
	modelName        string
	maxToolCalls     int
	toolCallResetter ToolCallResetter
	listContexts     ContextListFunc
	switchContext     ContextSwitchFunc
}

// New creates a new REPL instance.
func New(r *runner.Runner, ss session.Service, debug bool, manifest *manifest.Manager, apiKey, baseURL, modelName string, maxToolCalls int, toolCallResetter ToolCallResetter, listContexts ContextListFunc, switchContext ContextSwitchFunc) *REPL {
	return &REPL{
		runner:           r,
		sessionService:   ss,
		debug:            debug,
		manifest:         manifest,
		apiKey:           apiKey,
		baseURL:          baseURL,
		modelName:        modelName,
		maxToolCalls:     maxToolCalls,
		toolCallResetter: toolCallResetter,
		listContexts:     listContexts,
		switchContext:     switchContext,
	}
}

// Run starts the interactive REPL loop using bubbletea.
func (r *REPL) Run(ctx context.Context) error {
	// Drain any stale terminal query responses (OSC, CPR) from stdin.
	// Libraries like termenv/lipgloss/glamour query the terminal for
	// background color and capabilities during init. Responses that arrive
	// late end up in stdin and get interpreted as user input by bubbletea.
	drainStdin()

	m := newModel(r.runner, r.sessionService, r.debug, r.manifest, r.apiKey, r.baseURL, r.modelName, r.maxToolCalls, r.toolCallResetter, r.listContexts, r.switchContext)
	p := tea.NewProgram(m, tea.WithContext(ctx))
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
			ev := ProcessEventParts(event.Content.Parts)

			if state != nil {
				if ev.Plan != nil {
					state.SetPendingPlan(ev.Plan)
				}
				if ev.Clarification != nil {
					state.PendingClarification = ev.Clarification
				}
			}

			for _, text := range ev.TextParts {
				status.ClearForOutput()
				if mdRenderer != nil {
					rendered, renderErr := mdRenderer.Render(text)
					if renderErr == nil {
						fmt.Print(rendered)
						continue
					}
				}
				fmt.Print(text)
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

// PrintWelcome displays the colorized logo and session info.
func (r *REPL) PrintWelcome(version, model string, toolCount int, deploymentsDir string) {
	// Colorized ASCII art logo
	fmt.Print(RenderLogo(version))
	fmt.Println()

	// Build cluster context list from kubeconfig (via callback), falling back
	// to the manifest directory listing if the callback is unavailable.
	var contextDisplay string
	activeCtx := r.manifest.Context()
	if r.listContexts != nil {
		if ctxs, err := r.listContexts(); err == nil {
			contextDisplay = formatContextInfoList(ctxs)
		}
	}
	if contextDisplay == "" {
		contexts := r.manifest.ListContexts()
		contextDisplay = formatContextList(contexts, activeCtx)
	}

	// Session info rendered as markdown
	info := fmt.Sprintf(`| Setting | Value |
|---------|-------|
| Model | %s |
| Tools | %d |
| Clusters | %s |
| Deployments | %s |

Commands: **/approve** **/abort** plans · **/commit** **/push** **/status** manifests · **/contexts** **/context** cluster · **/debug** **/dump** **/clear** · **exit**
`, model, toolCount, contextDisplay, deploymentsDir)

	renderer, err := setupMarkdownRenderer()
	if err != nil {
		fmt.Printf("Model: %s | Tools: %d | Context: %s | Deployments: %s\n", model, toolCount, activeCtx, deploymentsDir)
		fmt.Printf("Type 'exit' or 'quit' to exit.\n\n")
		return
	}

	rendered, err := renderer.Render(info)
	if err != nil {
		fmt.Printf("Model: %s | Tools: %d | Context: %s | Deployments: %s\n", model, toolCount, activeCtx, deploymentsDir)
		fmt.Printf("Type 'exit' or 'quit' to exit.\n\n")
		return
	}

	fmt.Print(rendered)
}

// formatContextInfoList formats ContextInfo entries for the welcome screen.
// The active context is bolded; others are listed plain.
func formatContextInfoList(ctxs []ContextInfo) string {
	if len(ctxs) == 0 {
		return ""
	}
	var parts []string
	for _, c := range ctxs {
		if c.Active {
			parts = append(parts, "**"+c.Name+"**")
		} else {
			parts = append(parts, c.Name)
		}
	}
	return strings.Join(parts, ", ")
}

// formatContextList formats cluster contexts for display.
// The active context is bolded; others are listed plain.
func formatContextList(contexts []string, active string) string {
	if len(contexts) == 0 {
		return "**" + active + "**"
	}
	var parts []string
	for _, ctx := range contexts {
		if ctx == active {
			parts = append(parts, "**"+ctx+"**")
		} else {
			parts = append(parts, ctx)
		}
	}
	return strings.Join(parts, ", ")
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

// renderMarkdownSimple renders markdown with glamour using a dark style and 80-char wrap.
// Falls back to the raw markdown if rendering fails.
func renderMarkdownSimple(md string) string {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		return md
	}
	out, err := renderer.Render(md)
	if err != nil {
		return md
	}
	return out
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
