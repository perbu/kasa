package repl

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// contextSelectedMsg is sent when the user picks a context from the modal.
type contextSelectedMsg struct {
	name string
}

// contextCancelMsg is sent when the user cancels the context selector.
type contextCancelMsg struct{}

// contextSelectorModal is a bubbletea sub-component for picking a kubeconfig context.
type contextSelectorModal struct {
	contexts []ContextInfo
	cursor   int
	width    int
}

func newContextSelectorModal(contexts []ContextInfo, width int) contextSelectorModal {
	// Start cursor on the active context.
	cursor := 0
	for i, c := range contexts {
		if c.Active {
			cursor = i
			break
		}
	}

	return contextSelectorModal{
		contexts: contexts,
		cursor:   cursor,
		width:    width,
	}
}

// Update handles key messages for the context selector modal.
func (m contextSelectorModal) Update(msg tea.Msg) (contextSelectorModal, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return contextCancelMsg{} }

		case "up", "shift+tab":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil

		case "down", "tab":
			if m.cursor < len(m.contexts)-1 {
				m.cursor++
			}
			return m, nil

		case "enter":
			selected := m.contexts[m.cursor]
			if selected.Active {
				// Already on this context — treat as cancel.
				return m, func() tea.Msg { return contextCancelMsg{} }
			}
			return m, func() tea.Msg {
				return contextSelectedMsg{name: selected.Name}
			}
		}
	}
	return m, nil
}

// View renders the context selector modal.
func (m contextSelectorModal) View() string {
	var sb strings.Builder

	sb.WriteString(modalTitleStyle.Render("Switch Context"))
	sb.WriteString("\n\n")

	for i, c := range m.contexts {
		isCursor := i == m.cursor

		var bullet string
		if c.Active {
			bullet = "●"
		} else {
			bullet = "○"
		}

		name := c.Name
		detail := ""
		if c.Cluster != "" && c.Cluster != c.Name {
			detail = " " + modalContextStyle.Render(fmt.Sprintf("(%s)", c.Cluster))
		}

		line := fmt.Sprintf("   %s %s%s", bullet, name, detail)

		if isCursor {
			// Replace leading spaces with cursor indicator.
			line = fmt.Sprintf(" ▸ %s %s%s", bullet, name, detail)
			sb.WriteString(modalCursorStyle.Render(line))
		} else if c.Active {
			sb.WriteString(modalSelectedStyle.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(modalHelpStyle.Render("↑↓ navigate · enter select · esc cancel"))

	boxWidth := max(m.width-4, 40)
	if boxWidth > 80 {
		boxWidth = 80
	}

	return modalBorderStyle.Width(boxWidth).Render(sb.String())
}
