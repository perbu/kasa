package repl

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// clarificationAnswerMsg is sent when the user submits answers via the modal.
type clarificationAnswerMsg struct {
	answers []string // one answer per question
}

// clarificationCancelMsg is sent when the user cancels the modal.
type clarificationCancelMsg struct{}

// clarificationModal is a bubbletea sub-component for interactive clarification selection.
type clarificationModal struct {
	clarification *Clarification
	cursor        int      // flat index across all options + submit row
	selected      []int    // per-question selected option index, -1 = unanswered
	width         int
}

func newClarificationModal(c *Clarification, width int) clarificationModal {
	selected := make([]int, len(c.Questions))
	for i := range selected {
		selected[i] = -1
	}
	return clarificationModal{
		clarification: c,
		cursor:        0,
		selected:      selected,
		width:         width,
	}
}

// totalRows returns the total number of selectable rows (all options + submit if all answered).
func (m *clarificationModal) totalRows() int {
	n := 0
	for _, q := range m.clarification.Questions {
		n += len(q.Options)
	}
	if m.allAnswered() {
		n++ // submit row
	}
	return n
}

// cursorToQuestionOption maps a flat cursor index to (question index, option index).
// Returns (-1, -1) if the cursor is on the submit row.
func (m *clarificationModal) cursorToQuestionOption() (int, int) {
	pos := 0
	for qi, q := range m.clarification.Questions {
		for oi := range q.Options {
			if pos == m.cursor {
				return qi, oi
			}
			pos++
		}
	}
	return -1, -1 // submit row
}

// allAnswered returns true if every question has a selected answer.
func (m *clarificationModal) allAnswered() bool {
	for _, s := range m.selected {
		if s < 0 {
			return false
		}
	}
	return true
}

// buildAnswers returns the selected option text for each question.
func (m *clarificationModal) buildAnswers() []string {
	answers := make([]string, len(m.clarification.Questions))
	for i, q := range m.clarification.Questions {
		if m.selected[i] >= 0 && m.selected[i] < len(q.Options) {
			answers[i] = q.Options[m.selected[i]]
		}
	}
	return answers
}

// advanceToNextUnanswered moves the cursor to the first option of the next unanswered question.
func (m *clarificationModal) advanceToNextUnanswered() {
	// Find next unanswered question after current
	qi, _ := m.cursorToQuestionOption()
	startQ := max(qi+1, 0)

	// Search from startQ to end, then wrap around
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
			if m.selected[i] < 0 {
				m.cursor = m.questionFirstOption(i)
				return
			}
		}
	}

	// All answered — move to submit row
	if m.allAnswered() {
		m.cursor = m.totalRows() - 1
	}
}

// questionFirstOption returns the flat cursor index for the first option of question qi.
func (m *clarificationModal) questionFirstOption(qi int) int {
	pos := 0
	for i := range qi {
		pos += len(m.clarification.Questions[i].Options)
	}
	return pos
}

// Update handles key messages for the modal.
func (m clarificationModal) Update(msg tea.Msg) (clarificationModal, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			max := m.totalRows() - 1
			if m.cursor < max {
				m.cursor++
			}
		case "enter", " ":
			qi, oi := m.cursorToQuestionOption()
			if qi >= 0 {
				// Select this option
				m.selected[qi] = oi
				m.advanceToNextUnanswered()
			} else {
				// On submit row
				if m.allAnswered() {
					return m, func() tea.Msg {
						return clarificationAnswerMsg{answers: m.buildAnswers()}
					}
				}
			}
		case "esc":
			return m, func() tea.Msg {
				return clarificationCancelMsg{}
			}
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

	sb.WriteString(modalHelpStyle.Render("↑↓ navigate · enter select · esc cancel"))

	boxWidth := max(m.width-4, 40)
	if boxWidth > 80 {
		boxWidth = 80
	}

	return modalBorderStyle.Width(boxWidth).Render(sb.String())
}
