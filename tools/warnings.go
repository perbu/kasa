package tools

import (
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

// WarningRelay captures Kubernetes API warning headers (deprecation notices,
// admission webhook warnings) and forwards them to the REPL for display. It
// implements rest.WarningHandler and is set on the rest.Config at client
// construction. Warnings are deduplicated per session and always logged via
// klog (which kasa redirects to a file). The channel send is non-blocking:
// if nothing is draining the channel (non-interactive mode, or a burst of
// warnings), the warning is dropped from the channel but kept in the log.
type WarningRelay struct {
	ch   chan string
	mu   sync.Mutex
	seen map[string]struct{}
}

// NewWarningRelay creates a relay with a buffered channel.
func NewWarningRelay() *WarningRelay {
	return &WarningRelay{
		ch:   make(chan string, 32),
		seen: make(map[string]struct{}),
	}
}

// Ch returns the channel the REPL reads warnings from.
func (w *WarningRelay) Ch() <-chan string { return w.ch }

// HandleWarningHeader implements rest.WarningHandler.
func (w *WarningRelay) HandleWarningHeader(code int, _ string, text string) {
	if code != 299 || text == "" {
		return
	}
	klog.Warning(text)

	// HTTP header values may legally contain horizontal tabs, which the
	// bubbletea renderer can't measure. Normalize before display.
	text = strings.ReplaceAll(text, "\t", " ")

	w.mu.Lock()
	_, dup := w.seen[text]
	if !dup {
		w.seen[text] = struct{}{}
	}
	w.mu.Unlock()
	if dup {
		return
	}

	select {
	case w.ch <- text:
	default:
	}
}
