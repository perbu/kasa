package repl

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestOutputLines(t *testing.T) {
	for _, width := range []int{20, 80, 160} {
		for _, length := range []int{width - 1, width, 2 * width, 10 * width} {
			text := strings.Repeat("x", length)
			rows := outputLines(text, width)
			if strings.Join(rows, "") != text {
				t.Fatalf("width %d, length %d: content changed", width, length)
			}
			for _, row := range rows {
				if got := ansi.StringWidth(row); got >= width {
					t.Fatalf("width %d: row is %d cells wide", width, got)
				}
			}
		}
	}
	t.Run("controls", func(t *testing.T) {
		text := "a\tb\r\nc\rd\b\x1b[2J\x1b[H\x1b]0;title\a\x1b[3Ae\n\n"
		want := []string{"a    b", "c", "de", " ", " "}
		if got := outputLines(text, 80); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("styles and wide characters", func(t *testing.T) {
		text := "\x1b[31m" + strings.Repeat("界", 30) + "\n  indented\x1b[0m"
		rows := outputLines(text, 20)
		for _, row := range rows {
			if ansi.StringWidth(row) >= 20 || !strings.HasPrefix(row, "\x1b[31m") || !strings.HasSuffix(row, "\x1b[m") && !strings.HasSuffix(row, "\x1b[0m") {
				t.Errorf("row is not independently styled and wrapped: %q", row)
			}
		}
		if !strings.Contains(ansi.Strip(strings.Join(rows, "")), "  indented") {
			t.Fatal("indentation lost")
		}
	})
	t.Run("hyperlinks", func(t *testing.T) {
		open := ansi.SetHyperlink("https://example.com", "")
		close := ansi.ResetHyperlink()
		rows := outputLines(open+"first\nsecond"+close, 80)
		want := []string{open + "first" + close, open + "second" + close}
		if !reflect.DeepEqual(rows, want) {
			t.Fatalf("hyperlink leaks across rows: %q", rows)
		}
	})
}

func TestOutputQueueOrder(t *testing.T) {
	m := newModel(nil, nil, false, nil, "", "", "", 25, nil, nil, nil, nil, nil, nil, "", nil, nil, nil)
	m.width = 80
	m.outputActive = true // a previous row is still being printed
	lines := []string{"git: diff", "git: commit", "Committed: test", "Push with /push when ready."}
	next, _ := m.Update(cmdResultMsg{lines: lines})
	m = next.(model)
	next, _ = m.Update(bgPrintMsg{lines: []string{"background"}})
	m = next.(model)
	want := append(lines, "background")
	if !reflect.DeepEqual(m.output, want) {
		t.Fatalf("got %q, want %q", m.output, want)
	}
	for i := range want {
		next, _ = m.Update(outputFlushedMsg{})
		m = next.(model)
		if !reflect.DeepEqual(m.output, want[i+1:]) {
			t.Fatalf("queue after row %d: %q", i, m.output)
		}
	}
	next, _ = m.Update(outputFlushedMsg{})
	m = next.(model)
	if m.outputActive || m.output != nil {
		t.Fatal("queue did not finish and release its storage")
	}
}

func TestOutputQueueResize(t *testing.T) {
	m := newModel(nil, nil, false, nil, "", "", "", 25, nil, nil, nil, nil, nil, nil, "", nil, nil, nil)
	m.width = 80
	m.outputActive = true
	m.print(strings.Repeat("x", 70))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 24})
	m = next.(model)
	next, _ = m.Update(outputFlushedMsg{})
	m = next.(model)
	want := []string{strings.Repeat("x", 19), strings.Repeat("x", 19), strings.Repeat("x", 13)}
	if !reflect.DeepEqual(m.output, want) {
		t.Fatalf("pending output wasn't rewrapped at the new width: %q", m.output)
	}
}

type terminalStartMsg struct{}
type terminalCommitMsg struct{}
type terminalStopMsg struct{}

type terminalFixture struct {
	model
	legacy    bool
	committed bool
	stopping  bool
}

func (m terminalFixture) Init() tea.Cmd {
	// Give the real renderer its initial frame before the burst of output.
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return terminalStartMsg{} })
}

func (m terminalFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case terminalStartMsg:
		var logs strings.Builder
		for i := 0; i < 150; i++ {
			fmt.Fprintf(&logs, "row-%04d %s\n", i, strings.Repeat("x", 151))
		}
		logs.WriteString("short log entry\n  indented continuation\n\nlast log entry\n")
		text, _ := FormatDirectDisplay("get_logs", map[string]any{
			"namespace": "default", "pod": "test", "logs": logs.String(),
		}, m.width)
		if m.legacy {
			return m, tea.Sequence(tea.Println(text), func() tea.Msg { return terminalCommitMsg{} })
		}
		next, cmd := m.model.Update(directOutputMsg{lines: []string{text}})
		m.model = next.(model)
		return m, tea.Batch(cmd, func() tea.Msg { return terminalCommitMsg{} })
	case terminalCommitMsg:
		m.committed = true
		msg = cmdResultMsg{lines: []string{"git: commit", "Committed: test", "Push with /push when ready."}}
	case terminalStopMsg:
		return m, tea.Quit
	}
	next, cmd := m.model.Update(msg)
	m.model = next.(model)
	if m.committed && !m.stopping && !m.outputActive && len(m.output) == 0 {
		m.stopping = true
		return m, tea.Batch(cmd, tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return terminalStopMsg{} }))
	}
	return m, cmd
}

// TestTerminalOutput exercises the actual Bubble Tea renderer without a cluster
// or an LLM. KASA_TERMINAL_CAPTURE optionally saves its byte stream for replay
// with testdata/check_terminal.py; the legacy case demonstrates the old bug.
func TestTerminalOutput(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "queued"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			m := newModel(nil, nil, false, nil, "", "", "", 25, nil, nil, nil, nil, nil, nil, "", nil, nil, nil)
			m.agentBusy = true
			var out bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			p := tea.NewProgram(terminalFixture{model: m, legacy: legacy}, tea.WithContext(ctx),
				tea.WithInput(nil), tea.WithOutput(&out), tea.WithWindowSize(80, 24), tea.WithoutSignalHandler(),
				tea.WithEnvironment([]string{"TERM=xterm-256color"}))
			if _, err := p.Run(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "row-0149") || !strings.Contains(out.String(), "Push with /push when ready.") {
				t.Fatal("output did not complete")
			}
			if dir := os.Getenv("KASA_TERMINAL_CAPTURE"); dir != "" {
				if err := os.WriteFile(filepath.Join(dir, name+".ansi"), out.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
