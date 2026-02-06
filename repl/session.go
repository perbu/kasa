package repl

// ExecutionMode represents the current mode of the session.
type ExecutionMode string

const (
	// ModePlanning indicates the agent is gathering info and proposing plans.
	ModePlanning ExecutionMode = "planning"
	// ModeExecuting indicates an approved plan is being executed.
	ModeExecuting ExecutionMode = "executing"
)

// PlannedAction represents a single action in a plan.
type PlannedAction struct {
	Tool       string         `json:"tool"`
	Parameters map[string]any `json:"parameters"`
	Reason     string         `json:"reason"`
}

// Plan represents a proposed set of actions awaiting approval.
type Plan struct {
	Description string          `json:"description"`
	Actions     []PlannedAction `json:"actions"`
}

// ClarificationQuestion represents a single question in a clarification request.
type ClarificationQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// Clarification represents a set of clarifying questions for the user.
type Clarification struct {
	Context   string                  `json:"context"`
	Questions []ClarificationQuestion `json:"questions"`
}

// SessionState tracks the execution state for plan/approval workflow.
type SessionState struct {
	Mode                 ExecutionMode
	PendingPlan          *Plan
	PendingClarification *Clarification
}

// NewSessionState creates a new session state in planning mode.
func NewSessionState() *SessionState {
	return &SessionState{
		Mode: ModePlanning,
	}
}

// SetPendingPlan sets a plan that is awaiting user approval.
func (s *SessionState) SetPendingPlan(plan *Plan) {
	s.PendingPlan = plan
	s.Mode = ModePlanning
}

// ApprovePlan approves the pending plan and switches to executing mode.
func (s *SessionState) ApprovePlan() *Plan {
	if s.PendingPlan == nil {
		return nil
	}
	approved := s.PendingPlan
	s.PendingPlan = nil
	s.Mode = ModeExecuting
	return approved
}

// RejectPlan rejects the pending plan.
func (s *SessionState) RejectPlan() {
	s.PendingPlan = nil
	s.Mode = ModePlanning
}

// HasPendingPlan returns true if there's a plan awaiting approval.
func (s *SessionState) HasPendingPlan() bool {
	return s.PendingPlan != nil
}

// Reset clears any pending plan and returns to planning mode.
func (s *SessionState) Reset() {
	s.PendingPlan = nil
	s.Mode = ModePlanning
}
