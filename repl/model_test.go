package repl

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func TestModelCtrlCQuits(t *testing.T) {
	m := newModel(nil, nil, false, nil, "", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	fm := tm.FinalModel(t).(model)
	if !fm.quitting {
		t.Error("expected model to be in quitting state after ctrl+c")
	}
}
