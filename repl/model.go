package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/perbu/kasa/manifest"
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

// model is the bubbletea Model for the interactive REPL.
type model struct {
	textarea textarea.Model
	spinner  spinner.Model
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
	modelName string

	// agent execution state
	agentBusy   bool
	agentCancel context.CancelFunc
	eventCh     chan agentEventMsg

	// status display
	statusText   string
	toolName     string
	toolReason   string
	inputTokens  int32
	outputTokens int32

	// terminal dimensions
	width  int
	height int

	// saved textarea content when navigating history
	savedInput string

	// clarification modal
	showClarification bool
	clarModal         clarificationModal

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

func newModel(r *runner.Runner, ss session.Service, debug bool, manifest *manifest.Manager, apiKey, modelName string) model {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Prompt = "> "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(5)
	ta.MaxHeight = 5

	// Clear background colors so the textarea blends with the terminal.
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.EndOfBuffer = lipgloss.NewStyle()
	ta.FocusedStyle.Text = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.EndOfBuffer = lipgloss.NewStyle()
	ta.BlurredStyle.Text = lipgloss.NewStyle()

	// Rebind: Enter no longer inserts newline (we handle it as submit).
	// Alt+Enter and Ctrl+J insert newlines.
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")

	ta.Focus()

	s := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("205"))),
	)

	// Use a fixed dark style to avoid terminal queries (OSC 11) that would
	// race with bubbletea's stdin reader and produce garbled input.
	md, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80),
	)

	return model{
		textarea:       ta,
		spinner:        s,
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
		modelName:      modelName,
		eventCh:        make(chan agentEventMsg, 64),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink, // cursor blink
		m.spinner.Tick,
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// Handle clarification modal results
	case clarificationAnswerMsg:
		return m.handleClarificationAnswer(msg)
	case clarificationCancelMsg:
		return m.handleClarificationCancel()

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

	case tea.KeyMsg:
		// Ctrl+C: cancel agent, dismiss modal, or quit
		if msg.String() == "ctrl+c" {
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

	case spinner.TickMsg:
		if m.agentBusy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case agentEventMsg:
		return m.handleAgentEvent(msg)
	}

	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	// Show clarification modal instead of normal UI
	if m.showClarification {
		return m.clarModal.View() + "\n"
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

	// Textarea (input area)
	sb.WriteString(m.textarea.View())

	return sb.String()
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

	// Handle exit/quit
	if input == "exit" || input == "quit" {
		m.history.Save()
		cmds = append(cmds, tea.Println("Goodbye!"))
		m.quitting = true
		cmds = append(cmds, tea.Quit)
		return m, tea.Batch(cmds...)
	}

	// Handle plan approval commands
	switch strings.ToLower(input) {
	case "/approve":
		if m.state.HasPendingPlan() {
			plan := m.state.ApprovePlan()
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
			cmds = append(cmds, tea.Println("Plan rejected."))
			m.updatePrompt()
		} else {
			cmds = append(cmds, tea.Println("No pending plan to reject."))
		}
		return m, tea.Batch(cmds...)

	case "/plan":
		if m.state.HasPendingPlan() {
			cmds = append(cmds, tea.Println(RenderPlan(m.state.PendingPlan)))
		} else {
			cmds = append(cmds, tea.Println("No pending plan."))
		}
		return m, tea.Batch(cmds...)

	case "/commit":
		return m.handleCommit(cmds)

	case "/push":
		return m.handlePush(cmds)

	case "/status":
		return m.handleStatus(cmds)

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
	m.inputTokens = 0
	m.outputTokens = 0
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
		for event, err := range m.runner.Run(ctx, "user1", m.sessionID, userMessage, agent.RunConfig{}) {
			if err != nil {
				ch <- agentEventMsg{err: err}
				return
			}
			ch <- agentEventMsg{event: event}
		}
	}()

	return tea.Batch(waitForAgent(ch), m.spinner.Tick)
}

// waitForAgent returns a Cmd that reads one event from the channel.
func waitForAgent(ch chan agentEventMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// handleAgentEvent processes a single event from the agent.
func (m model) handleAgentEvent(msg agentEventMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if msg.err != nil {
		m.agentBusy = false
		m.agentCancel = nil
		m.state.PendingClarification = nil // clear stale clarification from partial run
		cmds = append(cmds, m.textarea.Focus())
		m.updatePrompt()
		cmds = append(cmds, tea.Println(fmt.Sprintf("Error: %v", msg.err)))
		return m, tea.Batch(cmds...)
	}

	if msg.done {
		m.agentBusy = false
		m.agentCancel = nil

		// Display pending clarification — use interactive modal
		if m.state.PendingClarification != nil {
			m.showClarification = true
			m.clarModal = newClarificationModal(m.state.PendingClarification, m.width)
			// Don't focus textarea — modal handles input
			m.updatePrompt()
			return m, tea.Batch(cmds...)
		}

		cmds = append(cmds, m.textarea.Focus())

		// Display pending plan
		if m.state.HasPendingPlan() {
			cmds = append(cmds, tea.Println(RenderPlan(m.state.PendingPlan)))
		}

		// After plan execution, reset if no new plan was proposed
		if m.state.Mode == ModeExecuting && !m.state.HasPendingPlan() {
			m.state.Reset()
		}

		m.updatePrompt()
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
	}

	// Process content parts
	if event.Content != nil {
		for _, part := range event.Content.Parts {
			// Detect propose_plan
			if part.FunctionCall != nil && part.FunctionCall.Name == "propose_plan" {
				if part.FunctionCall.Args != nil {
					plan := ParsePlanFromResponse(part.FunctionCall.Args)
					if plan != nil {
						m.state.SetPendingPlan(plan)
					}
				}
			}

			// Detect ask_clarification
			if part.FunctionCall != nil && part.FunctionCall.Name == "ask_clarification" {
				if part.FunctionCall.Args != nil {
					clarification := ParseClarificationFromResponse(part.FunctionCall.Args)
					if clarification != nil {
						m.state.PendingClarification = clarification
					}
				}
			}

			// Update status for function calls
			if part.FunctionCall != nil {
				m.toolName = part.FunctionCall.Name
				m.toolReason = extractReason(part.FunctionCall.Args)
				m.statusText = ""
			}

			if part.FunctionResponse != nil {
				m.toolName = ""
				m.toolReason = ""
				m.statusText = "Thinking..."
			}

			// Print text output
			if part.Text != "" {
				rendered := m.renderMarkdown(part.Text)
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
	m.updatePrompt()
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

	diff, err := m.manifest.StagedDiff()
	if err != nil {
		cmds = append(cmds, tea.Println(fmt.Sprintf("Failed to get diff: %v", err)))
		return m, tea.Batch(cmds...)
	}

	// Extract conversation context for better commit messages
	conversationContext := m.extractConversationSummary()

	// Generate commit message via one-shot Gemini call
	commitMsg, err := m.generateCommitMessage(diff, conversationContext)
	if err != nil {
		cmds = append(cmds, tea.Println(fmt.Sprintf("Failed to generate commit message: %v", err)))
		return m, tea.Batch(cmds...)
	}

	if err := m.manifest.Commit(commitMsg); err != nil {
		cmds = append(cmds, tea.Println(fmt.Sprintf("Commit failed: %v", err)))
		return m, tea.Batch(cmds...)
	}

	// Show subject line prominently, full message below
	subject := commitMsg
	if idx := strings.Index(commitMsg, "\n"); idx >= 0 {
		subject = commitMsg[:idx]
	}
	cmds = append(cmds, tea.Println(fmt.Sprintf("Committed: %s", subject)))
	if m.manifest.HasRemote() {
		cmds = append(cmds, tea.Println("Push with /push when ready."))
	}
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

	if err := m.manifest.Push(); err != nil {
		cmds = append(cmds, tea.Println(fmt.Sprintf("Push failed: %v", err)))
		return m, tea.Batch(cmds...)
	}

	cmds = append(cmds, tea.Println("Pushed to remote."))
	return m, tea.Batch(cmds...)
}

// handleStatus implements the /status command.
func (m model) handleStatus(cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if m.manifest == nil {
		cmds = append(cmds, tea.Println("No manifest manager configured."))
		return m, tea.Batch(cmds...)
	}

	status, err := m.manifest.GetStatus()
	if err != nil {
		cmds = append(cmds, tea.Println(fmt.Sprintf("Failed to get status: %v", err)))
		return m, tea.Batch(cmds...)
	}

	if strings.TrimSpace(status) == "" {
		cmds = append(cmds, tea.Println("No changes."))
	} else {
		cmds = append(cmds, tea.Println(status))
	}
	return m, tea.Batch(cmds...)
}

// generateCommitMessage makes a one-shot Gemini call to generate a commit message from a diff
// and conversation context. Returns a conventional commit message with subject and body.
func (m *model) generateCommitMessage(diff, conversationContext string) (string, error) {
	ctx := context.Background()

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  m.apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return "", fmt.Errorf("creating genai client: %w", err)
	}

	// Truncate very large diffs to avoid excessive token usage
	const maxDiffLen = 8000
	truncated := diff
	if len(truncated) > maxDiffLen {
		truncated = truncated[:maxDiffLen] + "\n... (truncated)"
	}

	var prompt string
	if conversationContext != "" {
		prompt = fmt.Sprintf(`Generate a git commit message for these Kubernetes manifest changes.

Conversation context (what the user asked for and why):
%s

Diff:
%s

Format: A subject line (imperative mood, max 72 chars), then a blank line, then a brief body explaining the intent behind these changes. Do not use markdown formatting. Output the commit message only, nothing else.`, conversationContext, truncated)
	} else {
		prompt = fmt.Sprintf(`Generate a git commit message for these Kubernetes manifest changes.

Diff:
%s

Format: A subject line (imperative mood, max 72 chars), then a blank line, then a brief body describing what changed. Do not use markdown formatting. Output the commit message only, nothing else.`, truncated)
	}

	resp, err := client.Models.GenerateContent(ctx, m.modelName, []*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)}, nil)
	if err != nil {
		return "", fmt.Errorf("generating commit message: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return "", fmt.Errorf("empty response from model")
	}

	// Extract text from response
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			msg := strings.TrimSpace(part.Text)
			// Remove any markdown formatting the model might add
			msg = strings.Trim(msg, "`\"")
			return msg, nil
		}
	}

	return "", fmt.Errorf("no text in model response")
}

// extractConversationSummary pulls user messages and agent text from the ADK session
// to provide context for commit message generation.
func (m *model) extractConversationSummary() string {
	ctx := context.Background()

	resp, err := m.sessionService.Get(ctx, &session.GetRequest{
		AppName:   "kasa",
		UserID:    "user1",
		SessionID: m.sessionID,
	})
	if err != nil {
		return ""
	}

	var sb strings.Builder
	const maxLen = 2000

	for evt := range resp.Session.Events().All() {
		if evt.Content == nil {
			continue
		}
		for _, part := range evt.Content.Parts {
			if part.Text == "" {
				continue
			}
			prefix := ""
			if evt.Author == "user" || evt.Content.Role == "user" {
				prefix = "User: "
			} else {
				prefix = "Agent: "
			}
			line := prefix + part.Text + "\n"
			if sb.Len()+len(line) > maxLen {
				sb.WriteString("... (truncated)\n")
				return sb.String()
			}
			sb.WriteString(line)
		}
	}

	return sb.String()
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
	spin := m.spinner.View()

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

	// Add token info
	if m.inputTokens > 0 || m.outputTokens > 0 {
		status = fmt.Sprintf("%s  [%d↑ %d↓]", status, m.inputTokens, m.outputTokens)
	}

	// Truncate to terminal width
	if m.width > 0 {
		status = ansi.Truncate(status, m.width-1, "...")
	}

	return status
}

// updatePrompt sets the textarea prompt based on session state.
func (m *model) updatePrompt() {
	m.textarea.Prompt = "> "
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

// extractReason gets the "reason" field from tool call args.
func extractReason(args map[string]any) string {
	if args == nil {
		return ""
	}
	reason, ok := args["reason"].(string)
	if !ok || reason == "" {
		return ""
	}
	maxLen := 50
	if len(reason) > maxLen {
		reason = reason[:maxLen-3] + "..."
	}
	return reason
}
