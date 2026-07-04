package repl

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"golang.org/x/term"
	"github.com/perbu/kasa/manifest"
	"github.com/perbu/kasa/tools"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// ResourceFetcher returns the inputs needed to render a plan diff against the
// current cluster state, given the proposed YAML:
//
//   - live is the current cluster object as clean YAML.
//   - projected is the merged object a server-side dry-run apply of the
//     proposed YAML would produce, also cleaned. Diffing live against
//     projected (instead of against the raw proposed YAML) lets cluster-set
//     defaults wash out so only changes the apply will actually cause appear.
//
// Returns ("", "", nil) when the resource doesn't exist yet (nothing to diff).
type ResourceFetcher func(yamlContent string) (live, projected string, err error)

// ManifestReader reads a stored manifest file and returns its YAML content.
type ManifestReader func(namespace, app, resourceType string) (string, error)

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
	Runner          *runner.Runner
	SessionService  session.Service
	Manifest        *manifest.Manager
	ContextName     string
	ResourceFetcher ResourceFetcher
	DirectIO        *tools.DirectIO
	DriftScanFunc   DriftScanFunc
	DriftCache      *tools.DriftCache
	MutationGuard   *tools.MutationGuard
}

// ContextSwitchFunc rebuilds the entire agent stack for a new context.
type ContextSwitchFunc func(contextName string) (*ContextSwitchResult, error)

// DriftScanFunc runs an on-demand drift scan against the active cluster.
type DriftScanFunc func(ctx context.Context, mgr *manifest.Manager) (*tools.DriftScanResults, error)

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
	mutationGuard    *tools.MutationGuard
	listContexts     ContextListFunc
	switchContext    ContextSwitchFunc
	resourceFetcher  ResourceFetcher
	directIO         *tools.DirectIO
	contextName      string
	driftScan        DriftScanFunc
	driftCache       *tools.DriftCache
}

// New creates a new REPL instance.
func New(r *runner.Runner, ss session.Service, debug bool, manifest *manifest.Manager, apiKey, baseURL, modelName string, maxToolCalls int, toolCallResetter ToolCallResetter, mutationGuard *tools.MutationGuard, listContexts ContextListFunc, switchContext ContextSwitchFunc, resourceFetcher ResourceFetcher, directIO *tools.DirectIO, contextName string, driftScan DriftScanFunc, driftCache *tools.DriftCache) *REPL {
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
		mutationGuard:    mutationGuard,
		listContexts:     listContexts,
		switchContext:    switchContext,
		resourceFetcher:  resourceFetcher,
		directIO:         directIO,
		contextName:      contextName,
		driftScan:        driftScan,
		driftCache:       driftCache,
	}
}

// Run starts the interactive REPL loop using bubbletea.
func (r *REPL) Run(ctx context.Context) error {
	m := newModel(r.runner, r.sessionService, r.debug, r.manifest, r.apiKey, r.baseURL, r.modelName, r.maxToolCalls, r.toolCallResetter, r.mutationGuard, r.listContexts, r.switchContext, r.resourceFetcher, r.directIO, r.contextName, r.driftScan, r.driftCache)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

// RunSinglePrompt runs the agent with a single prompt (non-interactive mode).
// In non-interactive mode, secret input uses terminal password prompt and
// direct output goes straight to stdout.
func (r *REPL) RunSinglePrompt(ctx context.Context, prompt string) error {
	// Handle secret input requests in non-interactive mode
	if r.directIO != nil {
		go r.handleNonInteractiveSecretInput()
	}
	return r.runAgentSync(ctx, nil, prompt)
}

// handleNonInteractiveSecretInput reads from DirectIO.InputCh and prompts
// for passwords using term.ReadPassword when running without the REPL.
func (r *REPL) handleNonInteractiveSecretInput() {
	for req := range r.directIO.InputCh {
		fmt.Print(req.Prompt)
		fd := int(os.Stdin.Fd())
		if term.IsTerminal(fd) {
			password, err := term.ReadPassword(fd)
			fmt.Println() // newline after masked input
			if err != nil {
				req.ResultCh <- ""
			} else {
				req.ResultCh <- string(password)
			}
		} else {
			req.ResultCh <- ""
		}
	}
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

	for event, err := range r.runner.Run(ctx, "user1", "session1", userMessage, agent.RunConfig{StreamingMode: agent.StreamingModeSSE}) {
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

			// Drain direct output (secret values displayed to user)
			if r.directIO != nil && len(ev.ToolResponses) > 0 {
				for _, line := range r.directIO.Drain() {
					status.ClearForOutput()
					fmt.Println(renderDirectOutput(line, 80))
				}
			}

			// Direct display of read-only tool responses
			for _, tr := range ev.ToolResponses {
				if rendered, ok := FormatDirectDisplay(tr.Name, tr.Response, 80); ok {
					status.ClearForOutput()
					fmt.Print(rendered)
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
		DisplayPlan(state.PendingPlan, nil)
	}

	return nil
}

// PrintWelcome displays the colorized logo and session info.
func (r *REPL) PrintWelcome(version, model string, toolCount int, deploymentsDir, remote string) {
	// Colorized ASCII art logo
	fmt.Print("\n")
	fmt.Print(RenderLogo())
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
	versionDisplay := "`" + version + "`"
	modelDisplay := "`" + model + "`"
	deploymentsDisplay := "`" + deploymentsDir + "`"
	remoteDisplay := "`" + remote + "`"
	if remote == "" {
		remoteDisplay = "none"
	}
	info := fmt.Sprintf(`| Setting | Value |
|---------|-------|
| Version | %s |
| Model | %s |
| Tools | %d |
| Clusters | %s |
| Deployments | %s |
| Remote | %s |

%s

%s
`, versionDisplay, modelDisplay, toolCount, contextDisplay, deploymentsDisplay, remoteDisplay, commandSummary(), randomTip())

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
		glamour.WithStandardStyle("dark"),
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
	return strings.TrimRight(out, "\n")
}

