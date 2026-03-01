package repl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
	"github.com/perbu/kasa/manifest"
	"github.com/perbu/kasa/tools"
	openai "github.com/sashabaranov/go-openai"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// agentEventMsg wraps a single event from the ADK runner.
type agentEventMsg struct {
	event *session.Event
	err   error
	done  bool // true when the agent stream has ended
}

// cmdResultMsg is sent by async REPL commands (/commit, /push, /status).
type cmdResultMsg struct {
	lines []string
}

// contextSwitchMsg carries the result of an asynchronous context switch.
type contextSwitchMsg struct {
	result *ContextSwitchResult
	err    error
}

// planRenderedMsg carries a fully rendered plan string from an async computation.
type planRenderedMsg struct{ text string }

// secretInputRequestMsg wraps a SecretInputRequest from a tool that needs user input.
type secretInputRequestMsg struct {
	req tools.SecretInputRequest
}

// directOutputMsg carries a line of direct output to display to the user.
type directOutputMsg struct {
	lines []string
}

// model is the bubbletea Model for the interactive REPL.
type model struct {
	textarea textarea.Model
	wave     waveSpinner
	history  *History
	state    *SessionState

	runner         *runner.Runner
	sessionService session.Service
	sessionID      string
	sessionCounter int
	debug          bool
	mdRenderer     *glamour.TermRenderer

	// manifest management
	manifest  *manifest.Manager
	apiKey    string
	baseURL   string
	modelName string

	// agent execution state
	agentBusy        bool
	agentCancel      context.CancelFunc
	eventCh          chan agentEventMsg
	toolCallCount    int              // number of tool calls in current agent turn
	maxToolCalls     int              // configurable limit per turn
	toolCallResetter ToolCallResetter // reset per-tool counters between turns
	mutationGuard    *tools.MutationGuard // blocks mutating tools unless plan is approved

	// status display
	statusText        string
	toolName          string
	toolReason        string
	inputTokens       int32
	outputTokens      int32
	totalInputTokens  int32 // cumulative input tokens across the session
	totalOutputTokens int32 // cumulative output tokens across the session

	// streaming state
	streamTokens    int       // count of partial text chunks (≈ tokens)
	streamStartTime time.Time // when first partial text chunk arrived

	// terminal dimensions
	width  int
	height int

	// saved textarea content when navigating history
	savedInput string

	// clarification modal
	showClarification bool
	clarModal         clarificationModal

	// context selector modal
	showContextSelect bool
	ctxModal          contextSelectorModal
	listContexts      ContextListFunc
	switchContext     ContextSwitchFunc

	resourceFetcher ResourceFetcher
	contextName     string // active K8s context for window title

	// drift scan callback
	driftScan DriftScanFunc

	// direct IO for secret tools
	directIO           *tools.DirectIO
	secretInputActive  bool
	secretInput        textinput.Model
	secretInputRequest *tools.SecretInputRequest

	quitting bool
}

// statusStyle is the dim style for the status line.
var statusStyle = lipgloss.NewStyle().Faint(true)

// separatorStyle is the dim style for the horizontal rule between turns.
var separatorStyle = lipgloss.NewStyle().Faint(true)

// uncommittedStyle is the style for the uncommitted changes indicator.
var uncommittedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Faint(true)

// debugStyle is the dim gray style for debug output lines.
var debugStyle = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("8"))

// toolCallStyle is the dim style for persistent tool call log lines.
var toolCallStyle = lipgloss.NewStyle().Faint(true)

func newModel(r *runner.Runner, ss session.Service, debug bool, manifest *manifest.Manager, apiKey, baseURL, modelName string, maxToolCalls int, toolCallResetter ToolCallResetter, mutationGuard *tools.MutationGuard, listContexts ContextListFunc, switchContext ContextSwitchFunc, resourceFetcher ResourceFetcher, directIO *tools.DirectIO, contextName string, driftScan DriftScanFunc) model {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "> "
		}
		return "  "
	})
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(5)
	ta.MaxHeight = 20

	// Clear background colors so the textarea blends with the terminal.
	styles := ta.Styles()
	styles.Focused.Base = lipgloss.NewStyle()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Focused.EndOfBuffer = lipgloss.NewStyle()
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Blurred.Base = lipgloss.NewStyle()
	styles.Blurred.CursorLine = lipgloss.NewStyle()
	styles.Blurred.EndOfBuffer = lipgloss.NewStyle()
	styles.Blurred.Text = lipgloss.NewStyle()
	ta.SetStyles(styles)

	// Rebind: Enter no longer inserts newline (we handle it as submit).
	// Alt+Enter and Ctrl+J insert newlines.
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")

	ta.Focus()

	w := newWaveSpinner()

	// Use a fixed dark style to avoid terminal queries (OSC 11) that would
	// race with bubbletea's stdin reader and produce garbled input.
	md, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80),
	)

	// Secret input widget (masked)
	si := textinput.New()
	si.EchoMode = textinput.EchoPassword
	si.EchoCharacter = '*'

	return model{
		textarea:       ta,
		wave:           w,
		history:        NewHistory(),
		state:          NewSessionState(),
		runner:         r,
		sessionService: ss,
		sessionID:      "session1",
		sessionCounter: 1,
		debug:          debug,
		mdRenderer:     md,
		manifest:       manifest,
		apiKey:         apiKey,
		baseURL:        baseURL,
		modelName:        modelName,
		maxToolCalls:     maxToolCalls,
		toolCallResetter: toolCallResetter,
		mutationGuard:    mutationGuard,
		eventCh:          make(chan agentEventMsg, 64),
		listContexts:    listContexts,
		switchContext:   switchContext,
		resourceFetcher: resourceFetcher,
		contextName:     contextName,
		driftScan:       driftScan,
		directIO:        directIO,
		secretInput:     si,
	}
}

func (m model) Init() tea.Cmd {
	return m.wave.Tick()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// Handle clarification modal results
	case clarificationAnswerMsg:
		return m.handleClarificationAnswer(msg)
	case clarificationCancelMsg:
		return m.handleClarificationCancel()

	// Handle context selector modal results
	case contextSelectedMsg:
		return m.handleContextSelected(msg)
	case contextCancelMsg:
		return m.handleContextCancel()
	case contextSwitchMsg:
		return m.handleContextSwitch(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textarea.SetWidth(msg.Width)
		if m.mdRenderer != nil {
			m.mdRenderer, _ = glamour.NewTermRenderer(
				glamour.WithStandardStyle("dark"),
				glamour.WithWordWrap(msg.Width),
			)
		}
		return m, nil

	case tea.PasteMsg:
		// Forward paste events (bubbletea v2 delivers pasted text as
		// PasteMsg, not KeyPressMsg).
		if m.secretInputActive {
			var cmd tea.Cmd
			m.secretInput, cmd = m.secretInput.Update(msg)
			return m, cmd
		}
		if !m.agentBusy {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyPressMsg:
		// Ctrl+C: cancel agent, dismiss modal, or quit
		if msg.String() == "ctrl+c" {
			if m.secretInputActive {
				return m.handleSecretInputCancel()
			}
			if m.showContextSelect {
				return m.handleContextCancel()
			}
			if m.showClarification {
				return m.handleClarificationCancel()
			}
			if m.agentBusy && m.agentCancel != nil {
				m.agentCancel()
				m.statusText = "Cancelling..."
				return m, nil
			}
			m.quitting = true
			m.history.Save()
			return m, tea.Quit
		}

		// Delegate to secret input modal when active
		if m.secretInputActive {
			return m.handleSecretInputKey(msg)
		}

		// Delegate to context selector modal when active
		if m.showContextSelect {
			var cmd tea.Cmd
			m.ctxModal, cmd = m.ctxModal.Update(msg)
			return m, cmd
		}

		// Delegate to clarification modal when active
		if m.showClarification {
			var cmd tea.Cmd
			m.clarModal, cmd = m.clarModal.Update(msg)
			return m, cmd
		}

		// Don't process input keys while agent is busy
		if m.agentBusy {
			return m, nil
		}

		switch msg.String() {
		case "enter":
			return m.handleSubmit()

		case "esc":
			m.textarea.Reset()
			m.savedInput = ""
			m.history.ResetCursor()
			return m, nil

		case "up":
			// If cursor is on first line, navigate history
			if m.textarea.Line() == 0 {
				entry, ok := m.history.Previous()
				if ok {
					if m.history.cursor == len(m.history.entries)-1 {
						// Save current input before navigating
						m.savedInput = m.textarea.Value()
					}
					m.textarea.SetValue(entry)
					m.textarea.CursorEnd()
				}
				return m, nil
			}

		case "down":
			// If cursor is on last line, navigate history
			if m.textarea.Line() == m.textarea.LineCount()-1 {
				entry, ok := m.history.Next()
				if ok {
					m.textarea.SetValue(entry)
				} else {
					m.textarea.SetValue(m.savedInput)
					m.savedInput = ""
				}
				m.textarea.CursorEnd()
				return m, nil
			}
		}

		// Update textarea for all other keys
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case waveTickMsg:
		if m.agentBusy {
			var cmd tea.Cmd
			m.wave, cmd = m.wave.Update(msg)
			return m, cmd
		}
		return m, nil

	case agentEventMsg:
		return m.handleAgentEvent(msg)

	case cmdResultMsg:
		m.agentBusy = false
		var cmds []tea.Cmd
		cmds = append(cmds, m.textarea.Focus())
		for _, line := range msg.lines {
			cmds = append(cmds, tea.Println(line))
		}
		return m, tea.Batch(cmds...)

	case planRenderedMsg:
		return m, tea.Println(msg.text)

	case secretInputRequestMsg:
		return m.handleSecretInputRequest(msg)

	case directOutputMsg:
		var cmds []tea.Cmd
		for _, line := range msg.lines {
			cmds = append(cmds, tea.Println(line))
		}
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

func (m model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	// Show secret input prompt in a yellow "Direct Input" box
	if m.secretInputActive {
		var content strings.Builder
		if m.secretInputRequest != nil {
			content.WriteString(m.secretInputRequest.Prompt)
		}
		content.WriteString(m.secretInput.View())

		boxWidth := max(m.width-4, 40)
		if boxWidth > 80 {
			boxWidth = 80
		}
		var sb strings.Builder
		sb.WriteString(directInputTitleStyle.Render("Direct Input"))
		sb.WriteString("\n")
		sb.WriteString(directInputBorderStyle.Width(boxWidth).Render(content.String()))
		sb.WriteString("\n")
		return tea.NewView(sb.String())
	}

	// Show context selector modal instead of normal UI
	if m.showContextSelect {
		return tea.NewView(m.ctxModal.View() + "\n")
	}

	// Show clarification modal instead of normal UI
	if m.showClarification {
		return tea.NewView(m.clarModal.View() + "\n")
	}

	var sb strings.Builder

	// Status line when agent is busy
	if m.agentBusy {
		status := m.buildStatusLine()
		sb.WriteString(statusStyle.Render(status))
		sb.WriteString("\n")
	}

	// Uncommitted changes indicator when idle
	if !m.agentBusy && m.manifest != nil {
		if n := m.manifest.StagedChangeCount(); n > 0 {
			label := fmt.Sprintf("[%d uncommitted manifest change", n)
			if n != 1 {
				label += "s"
			}
			label += "]"
			sb.WriteString(uncommittedStyle.Render(label))
			sb.WriteString("\n")
		}
	}

	// Divider line with token counts
	sb.WriteString(m.buildDividerLine())
	sb.WriteString("\n")

	// Textarea (input area)
	sb.WriteString(m.textarea.View())

	v := tea.NewView(sb.String())
	if m.contextName != "" {
		v.WindowTitle = "kasa: " + m.contextName
	}
	v.Cursor = &tea.Cursor{
		Shape: tea.CursorBar,
	}
	return v
}

// handleSubmit processes the Enter key press.
func (m model) handleSubmit() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.textarea.Value())
	if input == "" {
		return m, nil
	}

	var cmds []tea.Cmd

	// Add to history and reset
	m.history.Add(input)
	m.history.ResetCursor()
	m.savedInput = ""

	// Clear textarea
	m.textarea.Reset()

	// Separator line between conversation turns
	sep := separatorStyle.Render(strings.Repeat("─", m.separatorWidth()))
	cmds = append(cmds, tea.Println(sep))

	// Echo the user input above
	cmds = append(cmds, tea.Println("> "+input))

	// Handle commands
	switch strings.ToLower(input) {
	case "/exit", "/quit":
		m.history.Save()
		cmds = append(cmds, tea.Println("Goodbye!"))
		m.quitting = true
		cmds = append(cmds, tea.Quit)
		return m, tea.Batch(cmds...)
	case "/approve":
		if m.state.HasPendingPlan() {
			plan := m.state.ApprovePlan()
			if m.mutationGuard != nil {
				m.mutationGuard.AllowTools(plan.ToolNames())
			}
			cmds = append(cmds, tea.Println("Plan approved. Executing..."))
			execPrompt := FormatExecutionPrompt(plan)
			cmd := m.startAgent(execPrompt)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
		cmds = append(cmds, tea.Println("No pending plan to approve."))
		return m, tea.Batch(cmds...)

	case "/abort", "/reject":
		if m.state.HasPendingPlan() {
			m.state.RejectPlan()
			if m.mutationGuard != nil {
				m.mutationGuard.Block()
			}
			cmds = append(cmds, tea.Println("Plan rejected."))
				} else {
			cmds = append(cmds, tea.Println("No pending plan to reject."))
		}
		return m, tea.Batch(cmds...)

	case "/plan":
		if m.state.HasPendingPlan() {
			plan := m.state.PendingPlan
			fetcher := m.resourceFetcher
			mReader := manifestReaderFrom(m.manifest)
			cmds = append(cmds, func() tea.Msg {
				diffs := computePlanDiffs(plan, fetcher, mReader)
				return planRenderedMsg{text: RenderPlan(plan, diffs)}
			})
		} else {
			cmds = append(cmds, tea.Println("No pending plan."))
		}
		return m, tea.Batch(cmds...)

	case "/copy":
		if !m.state.HasPendingPlan() {
			cmds = append(cmds, tea.Println("No pending plan to copy."))
			return m, tea.Batch(cmds...)
		}
		yaml := collectPlanYAML(m.state.PendingPlan)
		if yaml == "" {
			cmds = append(cmds, tea.Println("No YAML content in the pending plan."))
			return m, tea.Batch(cmds...)
		}
		cmds = append(cmds, tea.SetClipboard(yaml))
		cmds = append(cmds, tea.Println("Plan YAML copied to clipboard."))
		return m, tea.Batch(cmds...)

	case "/commit":
		return m.handleCommit(cmds)

	case "/pull":
		return m.handlePull(cmds)

	case "/push":
		return m.handlePush(cmds)

	case "/status":
		return m.handleStatus(cmds)

	case "/drift":
		return m.handleDrift(cmds)

	case "/dump":
		ctx := context.Background()
		path, eventCount, err := dumpSession(ctx, m.sessionService, m.sessionID, m.state)
		if err != nil {
			cmds = append(cmds, tea.Println(fmt.Sprintf("Dump failed: %v", err)))
		} else {
			cmds = append(cmds, tea.Println(fmt.Sprintf("Session dumped to %s (%d events)", path, eventCount)))
		}
		return m, tea.Batch(cmds...)

	case "/debug":
		m.debug = !m.debug
		if m.debug {
			cmds = append(cmds, tea.Println("Debug mode enabled."))
		} else {
			cmds = append(cmds, tea.Println("Debug mode disabled."))
		}
		return m, tea.Batch(cmds...)

	case "/clear":
		ctx := context.Background()
		// Delete old session
		_ = m.sessionService.Delete(ctx, &session.DeleteRequest{
			AppName:   "kasa",
			UserID:    "user1",
			SessionID: m.sessionID,
		})
		// Create new session with incremented counter
		m.sessionCounter++
		m.sessionID = fmt.Sprintf("session%d", m.sessionCounter)
		_, err := m.sessionService.Create(ctx, &session.CreateRequest{
			AppName:   "kasa",
			UserID:    "user1",
			SessionID: m.sessionID,
		})
		if err != nil {
			cmds = append(cmds, tea.Println(fmt.Sprintf("Failed to clear context: %v", err)))
			return m, tea.Batch(cmds...)
		}
		m.state = NewSessionState()
		cmds = append(cmds, tea.Println("Context cleared."))
		return m, tea.Batch(cmds...)

	case "/help":
		cmds = append(cmds, tea.Println(commandHelp()))
		return m, tea.Batch(cmds...)

	case "/context":
		if m.listContexts == nil || m.switchContext == nil {
			cmds = append(cmds, tea.Println("Context switching not available."))
			return m, tea.Batch(cmds...)
		}
		ctxs, err := m.listContexts()
		if err != nil {
			cmds = append(cmds, tea.Println(fmt.Sprintf("Failed to list contexts: %v", err)))
			return m, tea.Batch(cmds...)
		}
		if len(ctxs) == 0 {
			cmds = append(cmds, tea.Println("No contexts found."))
			return m, tea.Batch(cmds...)
		}
		m.showContextSelect = true
		m.ctxModal = newContextSelectorModal(ctxs, m.width)
		return m, tea.Batch(cmds...)

	default:
		if strings.HasPrefix(strings.ToLower(input), "/") {
			cmds = append(cmds, tea.Println(fmt.Sprintf("Unknown command: %s", input)))
			return m, tea.Batch(cmds...)
		}
	}

	// If there's a pending plan, warn
	if m.state.HasPendingPlan() {
		cmds = append(cmds, tea.Println("You have a pending plan. Type /approve to approve, /abort to reject, or /plan to review."))
		return m, tea.Batch(cmds...)
	}

	// Regular message: send to agent
	cmd := m.startAgent(input)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// startAgent launches the agent in a goroutine and returns a Cmd to wait for events.
func (m *model) startAgent(prompt string) tea.Cmd {
	m.agentBusy = true
	m.statusText = "Thinking..."
	m.toolName = ""
	m.toolReason = ""
	m.toolCallCount = 0
	if m.toolCallResetter != nil {
		m.toolCallResetter.Reset()
	}
	m.inputTokens = 0
	m.outputTokens = 0
	m.streamTokens = 0
	m.streamStartTime = time.Time{}
	m.textarea.Blur()

	ctx, cancel := context.WithCancel(context.Background())
	m.agentCancel = cancel

	// Fresh channel per agent run to avoid stale messages from previous runs.
	ch := make(chan agentEventMsg, 64)
	m.eventCh = ch

	go func() {
		defer func() {
			ch <- agentEventMsg{done: true}
		}()

		userMessage := genai.NewContentFromText(prompt, genai.RoleUser)
		for event, err := range m.runner.Run(ctx, "user1", m.sessionID, userMessage, agent.RunConfig{StreamingMode: agent.StreamingModeSSE}) {
			if err != nil {
				ch <- agentEventMsg{err: err}
				return
			}
			ch <- agentEventMsg{event: event}
		}
	}()

	cmds := []tea.Cmd{waitForAgent(ch), m.wave.Tick()}
	if m.directIO != nil {
		cmds = append(cmds, waitForSecretInput(m.directIO.InputCh))
	}
	return tea.Batch(cmds...)
}

// waitForAgent returns a Cmd that reads one event from the channel.
func waitForAgent(ch chan agentEventMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// waitForSecretInput returns a Cmd that waits for a secret input request from a tool.
func waitForSecretInput(ch chan tools.SecretInputRequest) tea.Cmd {
	return func() tea.Msg {
		req, ok := <-ch
		if !ok {
			return nil
		}
		return secretInputRequestMsg{req: req}
	}
}

// handleAgentEvent processes a single event from the agent.
func (m model) handleAgentEvent(msg agentEventMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if msg.err != nil {
		// When we cancel the agent context (e.g. after detecting a plan or
		// clarification), the runner returns context.Canceled. Treat this
		// as a normal completion so we don't show a spurious error.
		if errors.Is(msg.err, context.Canceled) {
			// Fall through to the done-handling path below.
			msg = agentEventMsg{done: true}
		} else {
			m.agentBusy = false
			m.agentCancel = nil
			m.state.PendingClarification = nil // clear stale clarification from partial run
			// If we were executing an approved plan, re-block mutations and
			// reset to planning mode so the user can't accidentally run
			// mutating tools without a new plan.
			if m.state.Mode == ModeExecuting {
				m.state.Reset()
				if m.mutationGuard != nil {
					m.mutationGuard.Block()
				}
			}
			cmds = append(cmds, m.textarea.Focus())
			cmds = append(cmds, tea.Println(fmt.Sprintf("Error: %v", msg.err)))
			return m, tea.Batch(cmds...)
		}
	}

	if msg.done {
		m.agentBusy = false
		m.agentCancel = nil

		// Display pending clarification — use interactive modal
		if m.state.PendingClarification != nil {
			m.showClarification = true
			m.clarModal = newClarificationModal(m.state.PendingClarification, m.width)
			// Don't focus textarea — modal handles input
					return m, tea.Batch(cmds...)
		}

		cmds = append(cmds, m.textarea.Focus())

		// Display pending plan (async to allow diff fetching without blocking UI)
		if m.state.HasPendingPlan() {
			plan := m.state.PendingPlan
			fetcher := m.resourceFetcher
			mReader := manifestReaderFrom(m.manifest)
			cmds = append(cmds, func() tea.Msg {
				diffs := computePlanDiffs(plan, fetcher, mReader)
				return planRenderedMsg{text: RenderPlan(plan, diffs)}
			})
		}

		// After plan execution, reset if no new plan was proposed
		if m.state.Mode == ModeExecuting && !m.state.HasPendingPlan() {
			m.state.Reset()
			if m.mutationGuard != nil {
				m.mutationGuard.Block()
			}
		}

			return m, tea.Batch(cmds...)
	}

	event := msg.event
	if event == nil {
		return m, waitForAgent(m.eventCh)
	}

	// Print debug lines when debug mode is enabled
	if m.debug {
		for _, line := range formatDebugLines(event) {
			cmds = append(cmds, tea.Println(line))
		}
	}

	// Update token counts
	if event.UsageMetadata != nil {
		m.inputTokens = event.UsageMetadata.PromptTokenCount
		m.outputTokens = event.UsageMetadata.CandidatesTokenCount
		m.totalInputTokens += event.UsageMetadata.PromptTokenCount
		m.totalOutputTokens += event.UsageMetadata.CandidatesTokenCount
	}

	// Process content parts
	if event.Content != nil {
		ev := ProcessEventParts(event.Content.Parts)

		if ev.Plan != nil {
			m.state.SetPendingPlan(ev.Plan)
			// Cancel the agent to prevent the model from continuing
			// past the plan proposal. Without this, the model may
			// hallucinate user approval and execute mutating tools.
			if m.agentCancel != nil {
				m.agentCancel()
			}
		}
		if ev.Clarification != nil {
			m.state.PendingClarification = ev.Clarification
			if m.agentCancel != nil {
				m.agentCancel()
			}
		}

		// Update status based on the last tool call / response in this event
		for _, tc := range ev.ToolCalls {
			m.toolCallCount++
			m.toolName = tc.Name
			m.toolReason = tc.Reason
			m.statusText = ""

			if line := formatToolCallLine(tc, m.width, m.state.Mode == ModeExecuting); line != "" {
				cmds = append(cmds, tea.Println(line))
			}
		}

		// Hard limit: cancel agent if it's making too many tool calls
		if m.maxToolCalls > 0 && m.toolCallCount >= m.maxToolCalls && m.agentCancel != nil {
			m.agentCancel()
			cmds = append(cmds, tea.Println(
				"\n⚠ Agent stopped: exceeded tool call limit ("+
					fmt.Sprintf("%d", m.maxToolCalls)+
					" calls). The model may be stuck in a loop."))
		}

		if len(ev.ToolResponses) > 0 {
			m.toolName = ""
			m.toolReason = ""
			m.statusText = "Thinking..."
			m.streamTokens = 0
			m.streamStartTime = time.Time{}

			// Drain direct output from tools (e.g., secret values)
			if m.directIO != nil {
				for _, line := range m.directIO.Drain() {
					rendered := renderDirectOutput(line, m.width)
					cmds = append(cmds, tea.Println(rendered))
				}
			}

			// Direct display of read-only tool responses
			for _, tr := range ev.ToolResponses {
				if rendered, ok := FormatDirectDisplay(tr.Name, tr.Response, m.width); ok {
					cmds = append(cmds, tea.Println(rendered))
				}
			}
		}

		// Partial events are streaming chunks — update status but skip
		// printing text (the final non-partial event has the full content).
		if event.Partial {
			if len(ev.TextParts) > 0 {
				m.streamTokens += len(ev.TextParts)
				if m.streamStartTime.IsZero() {
					m.streamStartTime = time.Now()
				}
				m.statusText = m.streamStatus()
				m.toolName = ""
				m.toolReason = ""
			}
		} else {
			for _, text := range ev.TextParts {
				rendered := m.renderMarkdown(text)
				cmds = append(cmds, tea.Println(rendered))
			}
		}
	}

	cmds = append(cmds, waitForAgent(m.eventCh))
	return m, tea.Batch(cmds...)
}

// handleClarificationAnswer processes submitted answers from the modal.
func (m model) handleClarificationAnswer(msg clarificationAnswerMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	c := m.state.PendingClarification
	m.showClarification = false
	m.state.PendingClarification = nil

	// Format answers and send to agent
	answerText := formatClarificationAnswers(c, msg.answers)
	cmds = append(cmds, tea.Println(answerText))
	cmd := m.startAgent(answerText)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// handleClarificationCancel dismisses the modal without sending answers.
func (m model) handleClarificationCancel() (tea.Model, tea.Cmd) {
	m.showClarification = false
	m.state.PendingClarification = nil
	cmds := []tea.Cmd{
		m.textarea.Focus(),
		tea.Println("Clarification cancelled."),
	}
	return m, tea.Batch(cmds...)
}

// handleSecretInputRequest shows the masked input field for a secret value.
func (m model) handleSecretInputRequest(msg secretInputRequestMsg) (tea.Model, tea.Cmd) {
	m.secretInputActive = true
	req := msg.req
	m.secretInputRequest = &req
	m.secretInput.Reset()
	m.secretInput.Placeholder = ""
	return m, m.secretInput.Focus()
}

// handleSecretInputKey processes key events while the secret input is active.
func (m model) handleSecretInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		value := m.secretInput.Value()
		m.secretInputActive = false
		req := m.secretInputRequest
		m.secretInputRequest = nil
		m.secretInput.Reset()

		// Send the value back to the waiting tool
		if req != nil {
			req.ResultCh <- value
		}

		// Restart the secret input listener
		var cmds []tea.Cmd
		if m.directIO != nil {
			cmds = append(cmds, waitForSecretInput(m.directIO.InputCh))
		}
		return m, tea.Batch(cmds...)

	case "esc":
		return m.handleSecretInputCancel()

	default:
		var cmd tea.Cmd
		m.secretInput, cmd = m.secretInput.Update(msg)
		return m, cmd
	}
}

// handleSecretInputCancel dismisses the secret input and sends an empty value.
func (m model) handleSecretInputCancel() (tea.Model, tea.Cmd) {
	m.secretInputActive = false
	req := m.secretInputRequest
	m.secretInputRequest = nil
	m.secretInput.Reset()

	// Send empty value to unblock the waiting tool
	if req != nil {
		req.ResultCh <- ""
	}

	var cmds []tea.Cmd
	cmds = append(cmds, tea.Println("Secret input cancelled."))
	if m.directIO != nil {
		cmds = append(cmds, waitForSecretInput(m.directIO.InputCh))
	}
	return m, tea.Batch(cmds...)
}

// handleContextSelected starts the async context switch after the user picks a context.
func (m model) handleContextSelected(msg contextSelectedMsg) (tea.Model, tea.Cmd) {
	m.showContextSelect = false
	m.agentBusy = true
	m.statusText = "Switching context..."
	m.toolName = ""
	m.toolReason = ""
	m.textarea.Blur()

	switchFn := m.switchContext
	name := msg.name
	cmd := func() tea.Msg {
		result, err := switchFn(name)
		return contextSwitchMsg{result: result, err: err}
	}
	return m, tea.Batch(cmd, m.wave.Tick())
}

// handleContextCancel dismisses the context selector without switching.
func (m model) handleContextCancel() (tea.Model, tea.Cmd) {
	m.showContextSelect = false
	cmds := []tea.Cmd{
		m.textarea.Focus(),
		tea.Println("Context switch cancelled."),
	}
	return m, tea.Batch(cmds...)
}

// handleContextSwitch processes the result of an async context switch.
func (m model) handleContextSwitch(msg contextSwitchMsg) (tea.Model, tea.Cmd) {
	m.agentBusy = false
	var cmds []tea.Cmd

	if msg.err != nil {
		cmds = append(cmds, m.textarea.Focus())
		cmds = append(cmds, tea.Println(fmt.Sprintf("Context switch failed: %v", msg.err)))
		return m, tea.Batch(cmds...)
	}

	// Swap the rebuilt stack into the model.
	m.runner = msg.result.Runner
	m.sessionService = msg.result.SessionService
	m.manifest = msg.result.Manifest
	m.resourceFetcher = msg.result.ResourceFetcher
	if msg.result.DirectIO != nil {
		m.directIO = msg.result.DirectIO
	}
	if msg.result.DriftScanFunc != nil {
		m.driftScan = msg.result.DriftScanFunc
	}
	if msg.result.MutationGuard != nil {
		m.mutationGuard = msg.result.MutationGuard
	}
	m.contextName = msg.result.ContextName

	// Reset session (same as /clear).
	ctx := context.Background()
	_ = m.sessionService.Delete(ctx, &session.DeleteRequest{
		AppName:   "kasa",
		UserID:    "user1",
		SessionID: m.sessionID,
	})
	m.sessionCounter++
	m.sessionID = fmt.Sprintf("session%d", m.sessionCounter)
	_, _ = m.sessionService.Create(ctx, &session.CreateRequest{
		AppName:   "kasa",
		UserID:    "user1",
		SessionID: m.sessionID,
	})
	m.state = NewSessionState()

	cmds = append(cmds, m.textarea.Focus())
	cmds = append(cmds, tea.Println(fmt.Sprintf("Switched to context: %s", msg.result.ContextName)))
	cmds = append(cmds, tea.Println("Tip: run /drift to check for manifest drift."))
	return m, tea.Batch(cmds...)
}

// handleCommit implements the /commit command.
func (m model) handleCommit(cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if m.manifest == nil {
		cmds = append(cmds, tea.Println("No manifest manager configured."))
		return m, tea.Batch(cmds...)
	}

	if m.manifest.StagedChangeCount() == 0 {
		cmds = append(cmds, tea.Println("No uncommitted changes."))
		return m, tea.Batch(cmds...)
	}

	// Run the slow work (git diff, Gemini API, git commit) asynchronously
	// so the event loop can render the input echo before blocking.
	m.agentBusy = true
	m.statusText = "Committing..."
	m.toolName = ""
	m.toolReason = ""
	m.textarea.Blur()

	cmds = append(cmds, m.commitAsync(), m.wave.Tick())
	return m, tea.Batch(cmds...)
}

// commitAsync returns a Cmd that performs the commit in a goroutine.
func (m *model) commitAsync() tea.Cmd {
	return func() tea.Msg {
		diff, err := m.manifest.StagedDiff()
		if err != nil {
			return cmdResultMsg{lines: []string{fmt.Sprintf("Failed to get diff: %v", err)}}
		}

		var changeLog string
		if changes := m.manifest.Changes(); len(changes) > 0 {
			changeLog = "- " + strings.Join(changes, "\n- ")
		}

		commitMsg, err := m.generateCommitMessage(diff, changeLog)
		if err != nil {
			return cmdResultMsg{lines: []string{fmt.Sprintf("Failed to generate commit message: %v", err)}}
		}

		if err := m.manifest.Commit(commitMsg); err != nil {
			return cmdResultMsg{lines: []string{fmt.Sprintf("Commit failed: %v", err)}}
		}

		subject := commitMsg
		if idx := strings.Index(commitMsg, "\n"); idx >= 0 {
			subject = commitMsg[:idx]
		}
		lines := []string{fmt.Sprintf("Committed: %s", subject)}
		if m.manifest.HasRemote() {
			lines = append(lines, "Push with /push when ready.")
		}
		return cmdResultMsg{lines: lines}
	}
}

// handlePull implements the /pull command.
func (m model) handlePull(cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if m.manifest == nil {
		cmds = append(cmds, tea.Println("No manifest manager configured."))
		return m, tea.Batch(cmds...)
	}

	if !m.manifest.HasRemote() {
		cmds = append(cmds, tea.Println("No git remote configured."))
		return m, tea.Batch(cmds...)
	}

	m.agentBusy = true
	m.statusText = "Pulling..."
	m.toolName = ""
	m.toolReason = ""
	m.textarea.Blur()

	mfst := m.manifest
	cmds = append(cmds, func() tea.Msg {
		if err := mfst.Pull(); err != nil {
			return cmdResultMsg{lines: []string{fmt.Sprintf("Pull failed: %v", err)}}
		}
		return cmdResultMsg{lines: []string{"Pulled from remote."}}
	}, m.wave.Tick())
	return m, tea.Batch(cmds...)
}

// handlePush implements the /push command.
func (m model) handlePush(cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if m.manifest == nil {
		cmds = append(cmds, tea.Println("No manifest manager configured."))
		return m, tea.Batch(cmds...)
	}

	if !m.manifest.HasRemote() {
		cmds = append(cmds, tea.Println("No git remote configured."))
		return m, tea.Batch(cmds...)
	}

	m.agentBusy = true
	m.statusText = "Pushing..."
	m.toolName = ""
	m.toolReason = ""
	m.textarea.Blur()

	mfst := m.manifest
	cmds = append(cmds, func() tea.Msg {
		if err := mfst.Push(); err != nil {
			return cmdResultMsg{lines: []string{fmt.Sprintf("Push failed: %v", err)}}
		}
		return cmdResultMsg{lines: []string{"Pushed to remote."}}
	}, m.wave.Tick())
	return m, tea.Batch(cmds...)
}

// handleStatus implements the /status command.
func (m model) handleStatus(cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if m.manifest == nil {
		cmds = append(cmds, tea.Println("No manifest manager configured."))
		return m, tea.Batch(cmds...)
	}

	m.agentBusy = true
	m.statusText = "Checking status..."
	m.toolName = ""
	m.toolReason = ""
	m.textarea.Blur()

	mfst := m.manifest
	cmds = append(cmds, func() tea.Msg {
		status, err := mfst.GetStatus()
		if err != nil {
			return cmdResultMsg{lines: []string{fmt.Sprintf("Failed to get status: %v", err)}}
		}
		if strings.TrimSpace(status) == "" {
			return cmdResultMsg{lines: []string{"No changes."}}
		}
		return cmdResultMsg{lines: []string{status}}
	}, m.wave.Tick())
	return m, tea.Batch(cmds...)
}

// handleDrift implements the /drift command.
func (m model) handleDrift(cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if m.driftScan == nil {
		cmds = append(cmds, tea.Println("Drift scanning not available."))
		return m, tea.Batch(cmds...)
	}
	if m.manifest == nil {
		cmds = append(cmds, tea.Println("No manifest manager configured."))
		return m, tea.Batch(cmds...)
	}

	m.agentBusy = true
	m.statusText = "Scanning for drift..."
	m.toolName = ""
	m.toolReason = ""
	m.textarea.Blur()

	cmds = append(cmds, m.driftAsync(), m.wave.Tick())
	return m, tea.Batch(cmds...)
}

// driftAsync returns a Cmd that runs a drift scan in a goroutine.
func (m *model) driftAsync() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		results, err := m.driftScan(ctx, m.manifest)
		if err != nil {
			return cmdResultMsg{lines: []string{fmt.Sprintf("Drift scan failed: %v", err)}}
		}

		md := tools.FormatDriftScanResults(results)
		if md == "" {
			return cmdResultMsg{lines: []string{"No drift detected."}}
		}

		rendered := m.renderMarkdown(md)
		return cmdResultMsg{lines: []string{rendered}}
	}
}

// generateCommitMessage makes a one-shot LLM call to generate a commit message from a diff
// and conversation context. Returns a conventional commit message with subject and body.
func (m *model) generateCommitMessage(diff, changeLog string) (string, error) {
	ctx := context.Background()

	cfg := openai.DefaultConfig(m.apiKey)
	cfg.BaseURL = m.baseURL
	client := openai.NewClientWithConfig(cfg)

	// Truncate very large diffs to avoid excessive token usage
	const maxDiffLen = 8000
	truncated := diff
	if len(truncated) > maxDiffLen {
		truncated = truncated[:maxDiffLen] + "\n... (truncated)"
	}

	var prompt string
	if changeLog != "" {
		prompt = fmt.Sprintf(`Generate a git commit message for these Kubernetes manifest changes.

Change log:
%s

Diff:
%s

Format: A subject line (imperative mood, max 72 chars), then a blank line, then a brief body explaining the intent behind these changes. Do not use markdown formatting. Output the commit message only, nothing else.`, changeLog, truncated)
	} else {
		prompt = fmt.Sprintf(`Generate a git commit message for these Kubernetes manifest changes.

Diff:
%s

Format: A subject line (imperative mood, max 72 chars), then a blank line, then a brief body describing what changed. Do not use markdown formatting. Output the commit message only, nothing else.`, truncated)
	}

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: m.modelName,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("generating commit message: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty response from model")
	}

	msg := strings.TrimSpace(resp.Choices[0].Message.Content)
	msg = strings.Trim(msg, "`\"")
	return msg, nil
}

// renderMarkdown renders text through glamour, falling back to plain text.
func (m *model) renderMarkdown(text string) string {
	if m.mdRenderer != nil {
		rendered, err := m.mdRenderer.Render(text)
		if err == nil {
			return strings.TrimRight(rendered, "\n")
		}
	}
	return text
}

// buildStatusLine constructs the status text for display.
func (m *model) buildStatusLine() string {
	var status string
	spin := m.wave.View()

	if m.toolName != "" {
		if m.toolReason != "" {
			status = fmt.Sprintf("%s %s: %s", spin, m.toolName, m.toolReason)
		} else {
			status = fmt.Sprintf("%s Calling: %s", spin, m.toolName)
		}
	} else if m.statusText != "" {
		status = fmt.Sprintf("%s %s", spin, m.statusText)
	} else {
		status = fmt.Sprintf("%s Thinking...", spin)
	}

	// Truncate to terminal width
	if m.width > 0 {
		status = ansi.Truncate(status, m.width-1, "...")
	}

	return status
}

// streamStatus returns a status string like "Streaming 42 tok (15.2 tok/s)".
func (m *model) streamStatus() string {
	elapsed := time.Since(m.streamStartTime).Seconds()
	if elapsed < 0.1 {
		return fmt.Sprintf("Streaming %d tok", m.streamTokens)
	}
	rate := float64(m.streamTokens) / elapsed
	return fmt.Sprintf("Streaming %d tok (%.0f tok/s)", m.streamTokens, rate)
}

// divider styles
var (
	dividerLineStyle = lipgloss.NewStyle().Faint(true)
	safeStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	executingStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	planPendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	noPlanStyle      = lipgloss.NewStyle().Faint(true)
	tokenStyle       = lipgloss.NewStyle().Faint(true)
)

// buildDividerLine returns a horizontal divider with status right-aligned.
func (m model) buildDividerLine() string {
	w := m.separatorWidth()

	// Build status segments with color
	var modeStr string
	if m.state.Mode == ModeExecuting {
		modeStr = executingStyle.Render("EXECUTING")
	} else {
		modeStr = safeStyle.Render("SAFE")
	}

	var planStr string
	if m.state.HasPendingPlan() {
		planStr = planPendingStyle.Render("plan pending")
	} else {
		planStr = noPlanStyle.Render("no plan")
	}

	var ctxStr string
	var ctxPlain string
	if m.inputTokens > 0 {
		ctxPlain = fmt.Sprintf("ctx: %s", formatTokenCount(m.inputTokens))
		ctxStr = tokenStyle.Render(ctxPlain)
	}

	var tokenStr string
	var tokenPlain string
	if m.totalInputTokens > 0 {
		tokenPlain = fmt.Sprintf("%s in, %s out",
			formatTokenCount(m.totalInputTokens), formatTokenCount(m.totalOutputTokens))
		tokenStr = tokenStyle.Render(tokenPlain)
	}

	// Assemble the right-side label (plain text length for padding calculation)
	var parts []string
	var plainLen int

	parts = append(parts, modeStr)
	if m.state.Mode == ModeExecuting {
		plainLen += len("EXECUTING")
	} else {
		plainLen += len("SAFE")
	}

	parts = append(parts, planStr)
	if m.state.HasPendingPlan() {
		plainLen += len("plan pending")
	} else {
		plainLen += len("no plan")
	}

	if ctxStr != "" {
		parts = append(parts, ctxStr)
		plainLen += len(ctxPlain)
	}

	if tokenStr != "" {
		parts = append(parts, tokenStr)
		plainLen += len(tokenPlain)
	}

	sep := dividerLineStyle.Render(" | ")
	sepPlain := 3 // " | "
	label := strings.Join(parts, sep)
	labelPlainLen := plainLen + sepPlain*(len(parts)-1) + 2 // +2 for surrounding spaces

	lineLen := w - labelPlainLen
	if lineLen < 2 {
		lineLen = 2
	}

	return dividerLineStyle.Render(strings.Repeat("─", lineLen)+" ") + label + dividerLineStyle.Render(" ")
}

// separatorWidth returns the width for the horizontal separator line.
func (m model) separatorWidth() int {
	w := m.width
	if w <= 0 {
		w = 80
	}
	return w
}

// formatDebugLines returns styled debug lines for an agent event.
func formatDebugLines(event *session.Event) []string {
	var lines []string

	if event.Author != "" || event.UsageMetadata != nil {
		var parts []string
		if event.Author != "" {
			parts = append(parts, fmt.Sprintf("author=%s", event.Author))
		}
		if event.UsageMetadata != nil {
			parts = append(parts, fmt.Sprintf("tokens=[%d↑ %d↓]",
				event.UsageMetadata.PromptTokenCount,
				event.UsageMetadata.CandidatesTokenCount))
		}
		lines = append(lines, debugStyle.Render("[debug] event: "+strings.Join(parts, " ")))
	}

	if event.Content == nil {
		return lines
	}

	for _, part := range event.Content.Parts {
		if part.FunctionCall != nil {
			argsJSON, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				argsJSON = []byte("{...}")
			}
			lines = append(lines, debugStyle.Render(
				fmt.Sprintf("[debug] → tool: %s %s", part.FunctionCall.Name, argsJSON)))
		}

		if part.FunctionResponse != nil {
			result := fmt.Sprintf("%v", part.FunctionResponse.Response)
			if len(result) > 200 {
				result = result[:200] + "..."
			}
			lines = append(lines, debugStyle.Render(
				fmt.Sprintf("[debug] ← tool: %s %s", part.FunctionResponse.Name, result)))
		}

		if part.Text != "" {
			lines = append(lines, debugStyle.Render(
				fmt.Sprintf("[debug] text: %d chars", len(part.Text))))
		}
	}

	return lines
}

// formatToolCallLine returns a styled one-liner for a tool call, or "" if
// the tool has its own dedicated rendering (plan, clarification).
// width is the terminal width used for truncation (0 means no truncation).
// When executing is true (plan execution), args are omitted since the plan
// already showed what each tool will do.
func formatToolCallLine(tc ToolCallInfo, width int, executing bool) string {
	switch tc.Name {
	case "propose_plan", "ask_clarification":
		return ""
	}

	var line string
	if executing {
		line = fmt.Sprintf("  ⎿ %s", tc.Name)
	} else {
		argStr := formatToolArgs(tc.Args)
		if argStr != "" {
			line = fmt.Sprintf("  ⎿ %s (%s)", tc.Name, argStr)
		} else {
			line = fmt.Sprintf("  ⎿ %s", tc.Name)
		}
	}

	if width > 0 {
		line = ansi.Truncate(line, width-1, "…")
	}

	return toolCallStyle.Render(line)
}

// formatToolArgs formats tool call arguments as a compact key=value string.
// The "reason" key is placed last since it's the descriptive summary.
func formatToolArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}

	// Collect keys sorted alphabetically, with "reason" last.
	keys := make([]string, 0, len(args))
	hasReason := false
	for k := range args {
		if k == "reason" {
			hasReason = true
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if hasReason {
		keys = append(keys, "reason")
	}

	var parts []string
	for _, k := range keys {
		v := fmt.Sprintf("%v", args[k])
		if v == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ", ")
}

// directOutputStyle wraps direct-to-user output (secrets, etc.) in a bordered
// box so it's visually distinct from LLM-generated text.
var (
	directOutputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("10")). // green
				Padding(0, 2)

	directOutputTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("10"))

	// directInputBorderStyle wraps the secret input prompt in a yellow box.
	directInputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("3")). // yellow
				Padding(0, 2)

	directInputTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("3"))
)

// renderDirectOutput wraps tool-to-user text in a styled box.
func renderDirectOutput(text string, width int) string {
	var sb strings.Builder
	sb.WriteString(directOutputTitleStyle.Render("Direct Output"))
	sb.WriteString("\n")
	sb.WriteString(strings.TrimSpace(text))

	boxWidth := max(width-4, 40)
	if boxWidth > 80 {
		boxWidth = 80
	}
	return directOutputBorderStyle.Width(boxWidth).Render(sb.String())
}

// manifestReaderFrom wraps a manifest.Manager into a ManifestReader callback.
func manifestReaderFrom(mgr *manifest.Manager) ManifestReader {
	if mgr == nil {
		return nil
	}
	return func(namespace, app, resourceType string) (string, error) {
		data, err := mgr.ReadManifest(namespace, app, resourceType)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

// collectPlanYAML extracts all YAML content from a plan's actions.
func collectPlanYAML(plan *Plan) string {
	var parts []string
	for _, action := range plan.Actions {
		if yaml, ok := action.Parameters["yaml"].(string); ok && yaml != "" {
			parts = append(parts, strings.TrimSpace(yaml))
		}
	}
	return strings.Join(parts, "\n---\n")
}

// truncateToWidth truncates a string to fit within the given terminal width.
func truncateToWidth(s string, width int) string {
	if width <= 0 || s == "" {
		return s
	}
	return ansi.Truncate(s, width-1, "…")
}
