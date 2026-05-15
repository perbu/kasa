package repl

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestModelCtrlCQuits(t *testing.T) {
	m := newModel(nil, nil, false, nil, "", "", "", 25, nil, nil, nil, nil, nil, nil, "", nil, nil)

	msg := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	result, cmd := m.Update(msg)

	rm := result.(model)
	if !rm.quitting {
		t.Error("expected model to be in quitting state after ctrl+c")
	}

	// Ctrl+C should produce a tea.Quit command
	if cmd == nil {
		t.Error("expected a quit command after ctrl+c")
	}
}
