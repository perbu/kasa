package repl

import (
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/util/homedir"
)

// History manages command history with file persistence.
type History struct {
	entries []string
	cursor  int // points to current position; len(entries) means "new input"
	file    string
}

// NewHistory creates a History that loads/saves from ~/.kasa/history.
func NewHistory() *History {
	h := &History{}
	if home := homedir.HomeDir(); home != "" {
		kasaDir := filepath.Join(home, ".kasa")
		_ = os.MkdirAll(kasaDir, 0755)
		h.file = filepath.Join(kasaDir, "history")
	}
	h.Load()
	return h
}

// Load reads history entries from the file.
func (h *History) Load() {
	if h.file == "" {
		return
	}
	data, err := os.ReadFile(h.file)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	h.entries = nil
	for _, line := range lines {
		if line == "" {
			continue
		}
		// Unescape: literal \n → newline
		entry := strings.ReplaceAll(line, "\\n", "\n")
		h.entries = append(h.entries, entry)
	}
	h.cursor = len(h.entries)
}

// Save writes all history entries to the file.
func (h *History) Save() {
	if h.file == "" {
		return
	}
	var sb strings.Builder
	for _, entry := range h.entries {
		// Escape newlines for single-line storage
		escaped := strings.ReplaceAll(entry, "\n", "\\n")
		sb.WriteString(escaped)
		sb.WriteByte('\n')
	}
	_ = os.WriteFile(h.file, []byte(sb.String()), 0644)
}

// Add appends an entry, deduplicating consecutive entries.
func (h *History) Add(entry string) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return
	}
	// Deduplicate consecutive
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == entry {
		h.cursor = len(h.entries)
		return
	}
	h.entries = append(h.entries, entry)
	h.cursor = len(h.entries)
	h.Save()
}

// Previous returns the previous history entry, or "" if at the beginning.
// Returns (entry, ok) where ok is false if no more history.
func (h *History) Previous() (string, bool) {
	if h.cursor <= 0 {
		return "", false
	}
	h.cursor--
	return h.entries[h.cursor], true
}

// Next returns the next history entry, or "" if at the end (new input).
// Returns (entry, ok) where ok is false if past the end.
func (h *History) Next() (string, bool) {
	if h.cursor >= len(h.entries)-1 {
		h.cursor = len(h.entries)
		return "", false
	}
	h.cursor++
	return h.entries[h.cursor], true
}

// ResetCursor resets the cursor to the end (ready for new input).
func (h *History) ResetCursor() {
	h.cursor = len(h.entries)
}
