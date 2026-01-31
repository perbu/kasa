package references

import (
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		topic    string
		contains string
		wantErr  bool
	}{
		{"deployment", "apiVersion: apps/v1", false},
		{"service", "ClusterIP", false},
		{"httproute", "gateway.networking.k8s.io", false},
		{"postgres-cluster", "postgresql.cnpg.io", false},
		{"DEPLOYMENT", "apiVersion: apps/v1", false}, // case insensitive
		{"deployment.md", "apiVersion: apps/v1", false}, // with extension
		{"nonexistent", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.topic, func(t *testing.T) {
			got, err := Lookup(tt.topic)
			if (err != nil) != tt.wantErr {
				t.Errorf("Lookup(%q) error = %v, wantErr %v", tt.topic, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !strings.Contains(got, tt.contains) {
				t.Errorf("Lookup(%q) does not contain %q", tt.topic, tt.contains)
			}
		})
	}
}

func TestList(t *testing.T) {
	topics := List()
	if len(topics) == 0 {
		t.Error("List() returned empty slice")
	}

	// Verify all listed topics are actually available
	for _, topic := range topics {
		if _, err := Lookup(topic); err != nil {
			t.Errorf("Listed topic %q is not available: %v", topic, err)
		}
	}
}

func TestListWithDescriptions(t *testing.T) {
	descs := ListWithDescriptions()
	topics := List()

	for _, topic := range topics {
		if _, ok := descs[topic]; !ok {
			t.Errorf("Topic %q missing from ListWithDescriptions()", topic)
		}
	}
}

func TestWalk(t *testing.T) {
	count := 0
	err := Walk(func(topic string, content []byte) error {
		if len(content) == 0 {
			t.Errorf("Topic %q has empty content", topic)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}
	if count != len(List()) {
		t.Errorf("Walk() visited %d files, want %d", count, len(List()))
	}
}

func TestAll(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}

	// Should contain markers for each topic
	for _, topic := range List() {
		if !strings.Contains(all, "# Reference: "+topic) {
			t.Errorf("All() missing reference marker for %q", topic)
		}
	}
}
