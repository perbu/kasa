package repl

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// clarificationAnswerMsg is sent when the user submits answers via the modal.
type clarificationAnswerMsg struct {
	answers []string // one answer per question
}

// clarificationCancelMsg is sent when the user cancels the modal.
type clarificationCancelMsg struct{}

// clarificationModal is a bubbletea sub-component for interactive clarification.
// Questions with predefined options use radio-button selection.
// Questions without options use a text input field.
type clarificationModal struct {
	clarification *Clarification
	cursor        int   // flat index across all interactive rows + submit
	selected      []int // per-question selected option index (-1 = unanswered); unused for text questions

	textInputs []textinput.Model // one per question; only initialised for text questions
	width      int
}

func newClarificationModal(c *Clarification, width int) clarificationModal {
	selected := make([]int, len(c.Questions))
	textInputs := make([]textinput.Model, len(c.Questions))

	for i, q := range c.Questions {
		selected[i] = -1
		if len(q.Options) == 0 {
			ti := textinput.New()
			ti.Placeholder = "Type your answer..."
			ti.CharLimit = 256
			ti.SetWidth(50)
			textInputs[i] = ti
		}
	}

	// Focus the first text input if the first question is a text question
	if len(c.Questions) > 0 && len(c.Questions[0].Options) == 0 {
		textInputs[0].Focus()
	}

	return clarificationModal{
		clarification: c,
		cursor:        0,
		selected:      selected,
		textInputs:    textInputs,
		width:         width,
	}
}

// isTextQuestion returns true if the question has no predefined options.
func (m *clarificationModal) isTextQuestion(qi int) bool {
	return len(m.clarification.Questions[qi].Options) == 0
}

// totalRows returns the total number of interactive rows (options + text inputs + submit).
func (m *clarificationModal) totalRows() int {
	n := 0
	for qi, q := range m.clarification.Questions {
		if m.isTextQuestion(qi) {
			n++ // one row for the text input
		} else {
			n += len(q.Options)
		}
	}
	if m.allAnswered() {
		n++ // submit row
	}
	return n
}

// cursorToQuestionOption maps a flat cursor index to (question index, option index).
// For text questions, option index is -1.
// Returns (-1, -1) if the cursor is on the submit row.
func (m *clarificationModal) cursorToQuestionOption() (int, int) {
	pos := 0
	for qi, q := range m.clarification.Questions {
		if m.isTextQuestion(qi) {
			if pos == m.cursor {
				return qi, -1
			}
			pos++
		} else {
			for oi := range q.Options {
				if pos == m.cursor {
					return qi, oi
				}
				pos++
			}
		}
	}
	return -1, -1 // submit row
}

// allAnswered returns true if every question has been answered.
func (m *clarificationModal) allAnswered() bool {
	for qi := range m.clarification.Questions {
		if m.isTextQuestion(qi) {
			if strings.TrimSpace(m.textInputs[qi].Value()) == "" {
				return false
			}
		} else {
			if m.selected[qi] < 0 {
				return false
			}
		}
	}
	return true
}

// buildAnswers returns the answer text for each question.
func (m *clarificationModal) buildAnswers() []string {
	answers := make([]string, len(m.clarification.Questions))
	for qi, q := range m.clarification.Questions {
		if m.isTextQuestion(qi) {
			answers[qi] = strings.TrimSpace(m.textInputs[qi].Value())
		} else if m.selected[qi] >= 0 && m.selected[qi] < len(q.Options) {
			answers[qi] = q.Options[m.selected[qi]]
		}
	}
	return answers
}

// advanceToNextUnanswered moves the cursor to the next unanswered question.
func (m *clarificationModal) advanceToNextUnanswered() {
	qi, _ := m.cursorToQuestionOption()
	startQ := max(qi+1, 0)

	for pass := range 2 {
		from := startQ
		if pass == 1 {
			from = 0
		}
		to := len(m.clarification.Questions)
		if pass == 1 {
			to = startQ
		}
		for i := from; i < to; i++ {
			if m.isTextQuestion(i) {
				if strings.TrimSpace(m.textInputs[i].Value()) == "" {
					m.cursor = m.questionFirstRow(i)
					m.focusTextInput(i)
					return
				}
			} else {
				if m.selected[i] < 0 {
					m.cursor = m.questionFirstRow(i)
					m.blurAllTextInputs()
					return
				}
			}
		}
	}

	// All answered — move to submit row
	m.blurAllTextInputs()
	if m.allAnswered() {
		m.cursor = m.totalRows() - 1
	}
}

// questionFirstRow returns the flat cursor index for the first row of question qi.
func (m *clarificationModal) questionFirstRow(qi int) int {
	pos := 0
	for i := range qi {
		if m.isTextQuestion(i) {
			pos++
		} else {
			pos += len(m.clarification.Questions[i].Options)
		}
	}
	return pos
}

// focusTextInput focuses the text input for question qi and blurs all others.
func (m *clarificationModal) focusTextInput(qi int) {
	for i := range m.textInputs {
		if m.isTextQuestion(i) {
			if i == qi {
				m.textInputs[i].Focus()
			} else {
				m.textInputs[i].Blur()
			}
		}
	}
}

// blurAllTextInputs blurs all text inputs.
func (m *clarificationModal) blurAllTextInputs() {
	for i := range m.textInputs {
		if m.isTextQuestion(i) {
			m.textInputs[i].Blur()
		}
	}
}

// Update handles key messages for the modal.
func (m clarificationModal) Update(msg tea.Msg) (clarificationModal, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		// Forward paste to the focused text input
		qi, _ := m.cursorToQuestionOption()
		if qi >= 0 && m.isTextQuestion(qi) {
			var cmd tea.Cmd
			m.textInputs[qi], cmd = m.textInputs[qi].Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg {
				return clarificationCancelMsg{}
			}
		case "up", "shift+tab":
			if m.cursor > 0 {
				m.cursor--
			}
			qi, _ := m.cursorToQuestionOption()
			if qi >= 0 && m.isTextQuestion(qi) {
				m.focusTextInput(qi)
			} else {
				m.blurAllTextInputs()
			}
			return m, nil
		case "down", "tab":
			maxRow := m.totalRows() - 1
			if m.cursor < maxRow {
				m.cursor++
			}
			qi, _ := m.cursorToQuestionOption()
			if qi >= 0 && m.isTextQuestion(qi) {
				m.focusTextInput(qi)
			} else {
				m.blurAllTextInputs()
			}
			return m, nil
		case "enter":
			qi, oi := m.cursorToQuestionOption()
			if qi >= 0 {
				if m.isTextQuestion(qi) {
					// Enter on a text input: advance if non-empty
					if strings.TrimSpace(m.textInputs[qi].Value()) != "" {
						m.advanceToNextUnanswered()
					}
				} else {
					// Select this option
					m.selected[qi] = oi
					m.advanceToNextUnanswered()
				}
			} else {
				// On submit row
				if m.allAnswered() {
					return m, func() tea.Msg {
						return clarificationAnswerMsg{answers: m.buildAnswers()}
					}
				}
			}
			return m, nil
		case " ":
			// Space selects options but types in text inputs
			qi, oi := m.cursorToQuestionOption()
			if qi >= 0 && !m.isTextQuestion(qi) {
				m.selected[qi] = oi
				m.advanceToNextUnanswered()
				return m, nil
			}
		}

		// Pass remaining keys to the focused text input
		qi, _ := m.cursorToQuestionOption()
		if qi >= 0 && m.isTextQuestion(qi) {
			var cmd tea.Cmd
			m.textInputs[qi], cmd = m.textInputs[qi].Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

var (
	modalBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(1, 2)

	modalTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205"))

	modalContextStyle = lipgloss.NewStyle().
				Faint(true)

	modalQuestionStyle = lipgloss.NewStyle().
				Bold(true)

	modalCursorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("205"))

	modalSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("10")) // green

	modalHelpStyle = lipgloss.NewStyle().
			Faint(true)
)

// View renders the modal.
func (m clarificationModal) View() string {
	var sb strings.Builder

	sb.WriteString(modalTitleStyle.Render("Clarification Needed"))
	sb.WriteString("\n\n")

	if m.clarification.Context != "" {
		sb.WriteString(modalContextStyle.Render(m.clarification.Context))
		sb.WriteString("\n\n")
	}

	pos := 0
	for qi, q := range m.clarification.Questions {
		sb.WriteString(modalQuestionStyle.Render(fmt.Sprintf("%d. %s", qi+1, q.Question)))
		sb.WriteString("\n")

		if m.isTextQuestion(qi) {
			// Render text input
			isCursor := pos == m.cursor
			if isCursor {
				sb.WriteString(" ▸ ")
			} else {
				sb.WriteString("   ")
			}
			sb.WriteString(m.textInputs[qi].View())
			sb.WriteString("\n")
			pos++
		} else {
			// Render radio options
			for oi, opt := range q.Options {
				isCursor := pos == m.cursor
				isSelected := m.selected[qi] == oi

				var bullet string
				if isSelected {
					bullet = "●"
				} else {
					bullet = "○"
				}

				line := fmt.Sprintf("   %s %s", bullet, opt)

				if isCursor {
					line = " ▸" + line[2:]
					sb.WriteString(modalCursorStyle.Render(line))
				} else if isSelected {
					sb.WriteString(modalSelectedStyle.Render(line))
				} else {
					sb.WriteString(line)
				}
				sb.WriteString("\n")
				pos++
			}
		}
		sb.WriteString("\n")
	}

	// Submit row
	if m.allAnswered() {
		isCursor := m.cursor == m.totalRows()-1
		if isCursor {
			sb.WriteString(modalCursorStyle.Render(" ▸ Submit"))
		} else {
			sb.WriteString("   Submit")
		}
		sb.WriteString("\n\n")
	}

	sb.WriteString(modalHelpStyle.Render("↑↓/tab navigate · enter select/confirm · esc cancel"))

	boxWidth := max(m.width-4, 40)
	if boxWidth > 80 {
		boxWidth = 80
	}

	return modalBorderStyle.Width(boxWidth).Render(sb.String())
}
