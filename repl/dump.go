package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/adk/session"
	"k8s.io/client-go/util/homedir"
)

// dumpSession writes the full ADK session and REPL state to a JSON file in ~/.kasa/.
// Returns the file path, event count, and any error.
func dumpSession(ctx context.Context, ss session.Service, sessionID string, state *SessionState) (string, int, error) {
	resp, err := ss.Get(ctx, &session.GetRequest{
		AppName:   "kasa",
		UserID:    "user1",
		SessionID: sessionID,
	})
	if err != nil {
		return "", 0, fmt.Errorf("getting session: %w", err)
	}

	sess := resp.Session

	// Collect events
	var events []*session.Event
	for evt := range sess.Events().All() {
		events = append(events, evt)
	}

	// Collect ADK session state
	sessionState := make(map[string]any)
	for k, v := range sess.State().All() {
		sessionState[k] = v
	}

	dump := map[string]any{
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
		"session_id":    sessionID,
		"event_count":   len(events),
		"repl_state":    state,
		"session_state": sessionState,
		"events":        events,
	}

	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return "", 0, fmt.Errorf("marshaling: %w", err)
	}

	dir := filepath.Join(homedir.HomeDir(), ".kasa")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", 0, fmt.Errorf("creating directory: %w", err)
	}

	filename := fmt.Sprintf("dump-%d.json", time.Now().Unix())
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", 0, fmt.Errorf("writing: %w", err)
	}

	return path, len(events), nil
}
