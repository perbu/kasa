package tools

import "testing"

func TestToolCallCounter(t *testing.T) {
	c := NewToolCallCounter(3)

	// First two calls: no warning expected
	if n := c.Increment("list_pods"); n != 1 {
		t.Errorf("expected count 1, got %d", n)
	}
	if n := c.Increment("list_pods"); n != 2 {
		t.Errorf("expected count 2, got %d", n)
	}

	// Third call: hits threshold
	if n := c.Increment("list_pods"); n != 3 {
		t.Errorf("expected count 3, got %d", n)
	}

	// Different tool has its own count
	if n := c.Increment("get_logs"); n != 1 {
		t.Errorf("expected count 1 for get_logs, got %d", n)
	}

	// Total across all tools
	if total := c.Total(); total != 4 {
		t.Errorf("expected total 4, got %d", total)
	}

	// Reset clears everything
	c.Reset()
	if n := c.Increment("list_pods"); n != 1 {
		t.Errorf("expected count 1 after reset, got %d", n)
	}
	if total := c.Total(); total != 1 {
		t.Errorf("expected total 1 after reset, got %d", total)
	}
}

func TestToolCallCounterDisabled(t *testing.T) {
	c := NewToolCallCounter(0) // disabled

	for i := 0; i < 10; i++ {
		c.Increment("list_pods")
	}
	// threshold=0 means warnings are disabled; counter still counts
	if total := c.Total(); total != 10 {
		t.Errorf("expected total 10, got %d", total)
	}
}
