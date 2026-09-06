package repl

import (
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
)

type outputFlushedMsg struct{}

// Update owns the scrollback queue. Producing output is synchronous, so lines
// cannot be reordered by tea.Batch. Only one print is in flight at a time;
// the acknowledgement arrives after Bubble Tea has handled that print.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if _, ok := msg.(outputFlushedMsg); ok {
		m.outputActive = false
	} else {
		var next tea.Model
		next, cmd = m.update(msg)
		m = next.(model)
	}

	if m.outputActive {
		return m, cmd
	}
	if len(m.output) == 0 {
		m.output = nil
		if m.quitting {
			return m, tea.Quit
		}
		return m, cmd
	}

	// Rewrap at emission time as the terminal may have become narrower since
	// this row was queued. Never rely on the terminal's implicit autowrap:
	// Bubble Tea v2.0.0 overcounts rows for exact multiples of the width, and
	// insertAbove loses its cursor position for blocks taller than the screen.
	rows := outputLines(m.output[0], m.width)
	m.output[0] = ""
	m.output = m.output[1:]
	if len(rows) > 1 {
		m.output = append(rows[1:], m.output...)
	}
	m.outputActive = true
	flush := tea.Sequence(tea.Println(rows[0]), func() tea.Msg { return outputFlushedMsg{} })
	return m, tea.Batch(cmd, flush)
}

func (m *model) print(text string) {
	m.output = append(m.output, outputLines(text, m.width)...)
}

func (m *model) printLines(lines []string) {
	for _, line := range lines {
		m.print(line)
	}
}

// outputLines produces independent terminal rows. Preserve indentation and
// styles, but keep cursor movement, erase commands, tabs and carriage returns
// from changing the physical layout behind the renderer's back.
func outputLines(text string, width int) []string {
	if width <= 0 {
		width = 80
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	text = sanitizeOutput(text)
	text = ansi.Hardwrap(text, max(1, width-1), true)

	// Reset/reopen styling at every newline: Bubble Tea may repaint the
	// prompt between rows. An SGR or hyperlink must not leak into that view.
	var sb strings.Builder
	pen := cellbuf.NewPenWriter(&sb)
	_, _ = io.WriteString(pen, text)
	_ = pen.Close()
	rows := strings.Split(sb.String(), "\n")
	for i, row := range rows {
		if row == "" {
			rows[i] = " " // tea.Println("") doesn't insert a blank line in v2.
		}
	}
	return rows
}

func sanitizeOutput(text string) string {
	p := ansi.GetParser()
	defer ansi.PutParser(p)
	var sb strings.Builder
	var state byte
	for len(text) > 0 {
		seq, width, n, next := ansi.DecodeSequence(text, state, p)
		r, _ := utf8.DecodeRuneInString(seq)
		switch {
		case width > 0, seq == "\n":
			sb.WriteString(seq)
		case ansi.HasCsiPrefix(seq) && p.Command() == 'm':
			sb.WriteString(seq)
		case ansi.HasOscPrefix(seq) && p.Command() == 8:
			sb.WriteString(seq)
		case len(seq) > 0 && !unicode.IsControl(r) && !(seq[0] >= 0x80 && seq[0] <= 0x9f):
			sb.WriteString(seq)
		}
		text, state = text[n:], next
	}
	return sb.String()
}
