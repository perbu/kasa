package repl

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

	runner     *runner.Runner
	debug      bool
	mdRenderer *glamour.TermRenderer

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

func newModel(r *runner.Runner, debug bool) model {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Prompt = "> "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.MaxHeight = 10

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
		textarea:   ta,
		spinner:    s,
		history:    NewHistory(),
		state:      NewSessionState(),
		runner:     r,
		debug:      debug,
		mdRenderer: md,
		eventCh:    make(chan agentEventMsg, 64),
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
					// Resize textarea for multi-line entries
					m.resizeTextarea()
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
				m.resizeTextarea()
				return m, nil
			}
		}

		// Update textarea for all other keys
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
		m.resizeTextarea()
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
	m.textarea.SetHeight(1)

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
	case "yes", "y", "/approve":
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

	case "no", "n", "/reject":
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
	}

	// If there's a pending plan, warn
	if m.state.HasPendingPlan() {
		cmds = append(cmds, tea.Println("You have a pending plan. Type 'yes' to approve, 'no' to reject, or '/plan' to review."))
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

	ch := m.eventCh

	go func() {
		defer func() {
			ch <- agentEventMsg{done: true}
		}()

		userMessage := genai.NewContentFromText(prompt, genai.RoleUser)
		for event, err := range m.runner.Run(ctx, "user1", "session1", userMessage, agent.RunConfig{}) {
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

// resizeTextarea adjusts textarea height based on content lines.
func (m *model) resizeTextarea() {
	lines := m.textarea.LineCount()
	if lines < 1 {
		lines = 1
	}
	maxHeight := 10
	if m.height > 0 {
		maxHeight = min(10, m.height/3)
	}
	if lines > maxHeight {
		lines = maxHeight
	}
	m.textarea.SetHeight(lines)
}

// updatePrompt sets the textarea prompt based on session state.
func (m *model) updatePrompt() {
	if m.state.HasPendingPlan() {
		m.textarea.Prompt = "approve> "
	} else {
		m.textarea.Prompt = "> "
	}
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
