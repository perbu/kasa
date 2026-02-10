package repl

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestHistory creates a History with a temp file for testing.
func newTestHistory(t *testing.T) *History {
	t.Helper()
	dir := t.TempDir()
	return &History{
		file: filepath.Join(dir, "history"),
	}
}

func TestHistoryAddAndNavigate(t *testing.T) {
	h := newTestHistory(t)
	h.Add("first")
	h.Add("second")
	h.Add("third")

	// Navigate backwards
	entry, ok := h.Previous()
	if !ok || entry != "third" {
		t.Errorf("expected 'third', got %q (ok=%v)", entry, ok)
	}
	entry, ok = h.Previous()
	if !ok || entry != "second" {
		t.Errorf("expected 'second', got %q (ok=%v)", entry, ok)
	}
	entry, ok = h.Previous()
	if !ok || entry != "first" {
		t.Errorf("expected 'first', got %q (ok=%v)", entry, ok)
	}

	// At the beginning
	_, ok = h.Previous()
	if ok {
		t.Error("expected ok=false at the beginning of history")
	}

	// Navigate forward
	entry, ok = h.Next()
	if !ok || entry != "second" {
		t.Errorf("expected 'second', got %q (ok=%v)", entry, ok)
	}
	entry, ok = h.Next()
	if !ok || entry != "third" {
		t.Errorf("expected 'third', got %q (ok=%v)", entry, ok)
	}

	// Past the end
	_, ok = h.Next()
	if ok {
		t.Error("expected ok=false past end of history")
	}
}

func TestHistoryDeduplication(t *testing.T) {
	h := newTestHistory(t)
	h.Add("same")
	h.Add("same")
	h.Add("same")

	// Should only have one entry
	entry, ok := h.Previous()
	if !ok || entry != "same" {
		t.Errorf("expected 'same', got %q", entry)
	}
	_, ok = h.Previous()
	if ok {
		t.Error("expected only one entry after deduplication")
	}
}

func TestHistoryDeduplicationNonConsecutive(t *testing.T) {
	h := newTestHistory(t)
	h.Add("a")
	h.Add("b")
	h.Add("a") // not consecutive with first "a", should be added

	entry, ok := h.Previous()
	if !ok || entry != "a" {
		t.Errorf("expected 'a', got %q", entry)
	}
	entry, ok = h.Previous()
	if !ok || entry != "b" {
		t.Errorf("expected 'b', got %q", entry)
	}
	entry, ok = h.Previous()
	if !ok || entry != "a" {
		t.Errorf("expected 'a', got %q", entry)
	}
}

func TestHistoryEmpty(t *testing.T) {
	h := newTestHistory(t)
	h.Add("")
	h.Add("   ")

	_, ok := h.Previous()
	if ok {
		t.Error("expected empty entries to be ignored")
	}
}

func TestHistoryResetCursor(t *testing.T) {
	h := newTestHistory(t)
	h.Add("one")
	h.Add("two")

	h.Previous()
	h.Previous()
	h.ResetCursor()

	// After reset, Previous should return the last entry
	entry, ok := h.Previous()
	if !ok || entry != "two" {
		t.Errorf("expected 'two' after cursor reset, got %q", entry)
	}
}

func TestHistoryPersistence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "history")

	// Write history
	h1 := &History{file: file}
	h1.Add("alpha")
	h1.Add("beta")
	h1.Add("gamma")

	// Load in a new instance
	h2 := &History{file: file}
	h2.Load()

	entry, ok := h2.Previous()
	if !ok || entry != "gamma" {
		t.Errorf("expected 'gamma', got %q", entry)
	}
	entry, ok = h2.Previous()
	if !ok || entry != "beta" {
		t.Errorf("expected 'beta', got %q", entry)
	}
	entry, ok = h2.Previous()
	if !ok || entry != "alpha" {
		t.Errorf("expected 'alpha', got %q", entry)
	}
}

func TestHistoryMultilineEntries(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "history")

	h1 := &History{file: file}
	h1.Add("line1\nline2\nline3")
	h1.Add("single line")

	// Verify file format uses escaped newlines
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// Each entry should be on one line with \n escaped
	if got := len(splitNonEmpty(content, "\n")); got != 2 {
		t.Errorf("expected 2 lines in file, got %d", got)
	}

	// Load and verify round-trip
	h2 := &History{file: file}
	h2.Load()

	entry, ok := h2.Previous()
	if !ok || entry != "single line" {
		t.Errorf("expected 'single line', got %q", entry)
	}
	entry, ok = h2.Previous()
	if !ok || entry != "line1\nline2\nline3" {
		t.Errorf("expected multiline entry, got %q", entry)
	}
}

func TestHistoryLoadMissingFile(t *testing.T) {
	h := &History{file: "/nonexistent/path/history"}
	h.Load() // should not panic
	_, ok := h.Previous()
	if ok {
		t.Error("expected no entries from missing file")
	}
}

func TestHistoryNoFile(t *testing.T) {
	h := &History{} // no file set
	h.Add("test")
	h.Load() // should not panic
	h.Save() // should not panic
}

// splitNonEmpty splits a string and filters empty strings.
func splitNonEmpty(s, sep string) []string {
	parts := make([]string, 0)
	for _, p := range splitString(s, sep) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitString(s, sep string) []string {
	result := make([]string, 0)
	for len(s) > 0 {
		idx := indexOf(s, sep)
		if idx < 0 {
			result = append(result, s)
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	return result
}

func indexOf(s, sub string) int {
	for i := range len(s) - len(sub) + 1 {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
