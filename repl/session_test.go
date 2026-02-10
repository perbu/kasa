package repl

import "testing"

func TestNewSessionState(t *testing.T) {
	s := NewSessionState()
	if s.Mode != ModePlanning {
		t.Errorf("expected ModePlanning, got %s", s.Mode)
	}
	if s.PendingPlan != nil {
		t.Error("expected nil PendingPlan")
	}
	if s.HasPendingPlan() {
		t.Error("expected HasPendingPlan() to be false")
	}
}

func TestSetPendingPlan(t *testing.T) {
	s := NewSessionState()
	plan := &Plan{
		Description: "deploy nginx",
		Actions: []PlannedAction{
			{Tool: "create_deployment", Reason: "create nginx deployment"},
		},
	}
	s.SetPendingPlan(plan)

	if !s.HasPendingPlan() {
		t.Error("expected HasPendingPlan() to be true")
	}
	if s.Mode != ModePlanning {
		t.Errorf("expected ModePlanning, got %s", s.Mode)
	}
	if s.PendingPlan.Description != "deploy nginx" {
		t.Errorf("unexpected description: %s", s.PendingPlan.Description)
	}
}

func TestApprovePlan(t *testing.T) {
	s := NewSessionState()
	plan := &Plan{Description: "deploy nginx"}
	s.SetPendingPlan(plan)

	approved := s.ApprovePlan()
	if approved == nil {
		t.Fatal("expected non-nil approved plan")
	}
	if approved.Description != "deploy nginx" {
		t.Errorf("unexpected description: %s", approved.Description)
	}
	if s.HasPendingPlan() {
		t.Error("expected HasPendingPlan() to be false after approval")
	}
	if s.Mode != ModeExecuting {
		t.Errorf("expected ModeExecuting, got %s", s.Mode)
	}
}

func TestApprovePlanNil(t *testing.T) {
	s := NewSessionState()
	approved := s.ApprovePlan()
	if approved != nil {
		t.Error("expected nil when approving with no pending plan")
	}
}

func TestRejectPlan(t *testing.T) {
	s := NewSessionState()
	s.SetPendingPlan(&Plan{Description: "bad plan"})
	s.RejectPlan()

	if s.HasPendingPlan() {
		t.Error("expected HasPendingPlan() to be false after rejection")
	}
	if s.Mode != ModePlanning {
		t.Errorf("expected ModePlanning, got %s", s.Mode)
	}
}

func TestReset(t *testing.T) {
	s := NewSessionState()
	s.SetPendingPlan(&Plan{Description: "a plan"})
	s.ApprovePlan() // mode is now ModeExecuting
	s.Reset()

	if s.HasPendingPlan() {
		t.Error("expected no pending plan after reset")
	}
	if s.Mode != ModePlanning {
		t.Errorf("expected ModePlanning after reset, got %s", s.Mode)
	}
}

func TestSetPendingClarification(t *testing.T) {
	s := NewSessionState()
	c := &Clarification{
		Context: "need more info",
		Questions: []ClarificationQuestion{
			{Question: "which namespace?", Options: []string{"default", "kube-system"}},
		},
	}
	s.PendingClarification = c

	if s.PendingClarification == nil {
		t.Error("expected non-nil PendingClarification")
	}
	if s.PendingClarification.Context != "need more info" {
		t.Errorf("unexpected context: %s", s.PendingClarification.Context)
	}
}
