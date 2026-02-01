package tools

import (
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// SleepTool provides the sleep tool for the agent.
type SleepTool struct{}

// NewSleepTool creates a new SleepTool.
func NewSleepTool() *SleepTool {
	return &SleepTool{}
}

// Name returns the tool name.
func (t *SleepTool) Name() string {
	return "sleep"
}

// Description returns the tool description.
func (t *SleepTool) Description() string {
	return "Wait for a specified duration in seconds. Useful for polling scenarios where you need to wait before checking status again."
}

// IsLongRunning returns false as the sleep duration is capped.
func (t *SleepTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *SleepTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *SleepTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"seconds": {
					Type:        "number",
					Description: "Duration to sleep in seconds (e.g., 1.5 for 1.5 seconds). Maximum 300 seconds (5 minutes).",
				},
			},
			Required: []string{"seconds"},
		},
	}
}

// Run executes the tool.
func (t *SleepTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, ok := args.(map[string]any)
	if !ok {
		return map[string]any{"error": "invalid arguments"}, nil
	}

	secondsRaw, ok := argsMap["seconds"]
	if !ok {
		return map[string]any{"error": "seconds parameter is required"}, nil
	}

	var seconds float64
	switch v := secondsRaw.(type) {
	case float64:
		seconds = v
	case int:
		seconds = float64(v)
	case int64:
		seconds = float64(v)
	default:
		return map[string]any{"error": "seconds must be a number"}, nil
	}

	if seconds < 0 {
		return map[string]any{"error": "seconds cannot be negative"}, nil
	}

	// Cap at 5 minutes to prevent excessively long waits
	const maxSeconds = 300.0
	if seconds > maxSeconds {
		seconds = maxSeconds
	}

	duration := time.Duration(seconds * float64(time.Second))
	start := time.Now()
	time.Sleep(duration)
	elapsed := time.Since(start)

	return map[string]any{
		"slept_seconds": elapsed.Seconds(),
		"message":       "Sleep completed",
	}, nil
}
