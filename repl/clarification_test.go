package repl

import (
	"strings"
	"testing"
)

func TestParseClarificationFromResponse(t *testing.T) {
	args := map[string]any{
		"context": "I need more information about your deployment",
		"questions": []any{
			map[string]any{
				"question": "Which namespace?",
				"options":  []any{"default", "kube-system", "production"},
			},
			map[string]any{
				"question": "How many replicas?",
			},
		},
	}

	c := ParseClarificationFromResponse(args)
	if c == nil {
		t.Fatal("expected non-nil clarification")
	}
	if c.Context != "I need more information about your deployment" {
		t.Errorf("unexpected context: %s", c.Context)
	}
	if len(c.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(c.Questions))
	}
	if c.Questions[0].Question != "Which namespace?" {
		t.Errorf("unexpected question: %s", c.Questions[0].Question)
	}
	if len(c.Questions[0].Options) != 3 {
		t.Errorf("expected 3 options, got %d", len(c.Questions[0].Options))
	}
	if c.Questions[0].Options[1] != "kube-system" {
		t.Errorf("unexpected option: %s", c.Questions[0].Options[1])
	}
	// Second question has no options
	if len(c.Questions[1].Options) != 0 {
		t.Errorf("expected 0 options for second question, got %d", len(c.Questions[1].Options))
	}
}

func TestParseClarificationFromResponseNoQuestions(t *testing.T) {
	args := map[string]any{
		"context": "test",
	}
	c := ParseClarificationFromResponse(args)
	if c != nil {
		t.Error("expected nil when no questions key")
	}
}

func TestParseClarificationFromResponseEmptyQuestions(t *testing.T) {
	args := map[string]any{
		"context":   "test",
		"questions": []any{},
	}
	c := ParseClarificationFromResponse(args)
	if c != nil {
		t.Error("expected nil for empty questions list")
	}
}

func TestParseClarificationFromResponseInvalidQuestion(t *testing.T) {
	args := map[string]any{
		"context": "test",
		"questions": []any{
			"not a map", // invalid
			map[string]any{
				"question": "valid question",
			},
		},
	}
	c := ParseClarificationFromResponse(args)
	if c == nil {
		t.Fatal("expected non-nil clarification")
	}
	if len(c.Questions) != 1 {
		t.Errorf("expected 1 valid question, got %d", len(c.Questions))
	}
}

func TestBuildClarificationMarkdown(t *testing.T) {
	c := &Clarification{
		Context: "Deployment details needed",
		Questions: []ClarificationQuestion{
			{
				Question: "Which namespace?",
				Options:  []string{"default", "production"},
			},
			{
				Question: "What image tag?",
			},
		},
	}

	md := buildClarificationMarkdown(c)

	if !strings.Contains(md, "# Clarification Needed") {
		t.Error("expected heading")
	}
	if !strings.Contains(md, "Deployment details needed") {
		t.Error("expected context")
	}
	if !strings.Contains(md, "Which namespace?") {
		t.Error("expected first question")
	}
	if !strings.Contains(md, "a) default") {
		t.Error("expected option a)")
	}
	if !strings.Contains(md, "b) production") {
		t.Error("expected option b)")
	}
	if !strings.Contains(md, "What image tag?") {
		t.Error("expected second question")
	}
}

func TestFormatClarificationAnswers(t *testing.T) {
	c := &Clarification{
		Questions: []ClarificationQuestion{
			{Question: "Which namespace?"},
			{Question: "How many replicas?"},
		},
	}
	answers := []string{"default", "3"}

	result := formatClarificationAnswers(c, answers)
	if !strings.Contains(result, "1. Which namespace?: default") {
		t.Errorf("expected formatted answer for Q1, got: %s", result)
	}
	if !strings.Contains(result, "2. How many replicas?: 3") {
		t.Errorf("expected formatted answer for Q2, got: %s", result)
	}
}

func TestFormatClarificationAnswersPartial(t *testing.T) {
	c := &Clarification{
		Questions: []ClarificationQuestion{
			{Question: "Q1"},
			{Question: "Q2"},
			{Question: "Q3"},
		},
	}
	// Fewer answers than questions
	answers := []string{"a1"}

	result := formatClarificationAnswers(c, answers)
	if !strings.Contains(result, "1. Q1: a1") {
		t.Errorf("expected first answer, got: %s", result)
	}
	// Q2 and Q3 should not appear since there are no answers for them
	if strings.Contains(result, "Q2") {
		t.Error("did not expect Q2 in output")
	}
}

func TestRenderClarificationNil(t *testing.T) {
	out := RenderClarification(nil)
	if out != "" {
		t.Errorf("expected empty string for nil clarification, got %q", out)
	}
}
