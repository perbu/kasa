package tools

import (
	"encoding/json"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// AskClarificationTool asks the user clarifying questions before proposing a plan.
type AskClarificationTool struct{}

// NewAskClarificationTool creates a new AskClarificationTool.
func NewAskClarificationTool() *AskClarificationTool {
	return &AskClarificationTool{}
}

// Name returns the tool name.
func (t *AskClarificationTool) Name() string {
	return "ask_clarification"
}

// Description returns the tool description.
func (t *AskClarificationTool) Description() string {
	return "Ask the user clarifying questions before proposing a plan. Use this when a mutating request is ambiguous (e.g., namespace, replicas, image version, service type). Keep it focused: 1-3 questions for genuinely ambiguous choices."
}

// IsLongRunning returns false as this is a quick operation.
func (t *AskClarificationTool) IsLongRunning() bool {
	return false
}

// Category returns the tool category.
func (t *AskClarificationTool) Category() ToolCategory {
	return CategoryPlanning
}

// ProcessRequest adds this tool to the LLM request.
func (t *AskClarificationTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *AskClarificationTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"context": {
					Type:        "string",
					Description: "Brief explanation of what you are trying to do and why you need clarification",
				},
				"questions": {
					Type:        "array",
					Description: "List of questions to ask the user (1-3 questions)",
					Items: &genai.Schema{
						Type: "object",
						Properties: map[string]*genai.Schema{
							"question": {
								Type:        "string",
								Description: "The question text",
							},
							"options": {
								Type:        "array",
								Description: "Optional list of suggested answers for multiple-choice",
								Items: &genai.Schema{
									Type: "string",
								},
							},
						},
						Required: []string{"question"},
					},
				},
			},
			Required: []string{"context", "questions"},
		},
	}
}

// Run executes the tool. This tool does NOT block - it captures the questions
// for display and returns a status indicating answers are needed.
func (t *AskClarificationTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	argsMap, ok := args.(map[string]any)
	if !ok {
		if argsStr, ok := args.(string); ok {
			if err := json.Unmarshal([]byte(argsStr), &argsMap); err != nil {
				return map[string]any{"error": "invalid arguments format"}, nil
			}
		} else {
			return map[string]any{"error": "invalid arguments type"}, nil
		}
	}

	contextStr, _ := argsMap["context"].(string)
	if contextStr == "" {
		return map[string]any{"error": "context is required"}, nil
	}

	questions, ok := argsMap["questions"].([]any)
	if !ok || len(questions) == 0 {
		return map[string]any{"error": "at least one question is required"}, nil
	}

	// Validate questions have required fields
	for i, q := range questions {
		qMap, ok := q.(map[string]any)
		if !ok {
			return map[string]any{"error": "invalid question format", "index": i}, nil
		}
		if _, ok := qMap["question"].(string); !ok {
			return map[string]any{"error": "question missing text", "index": i}, nil
		}
	}

	return map[string]any{
		"status":    "awaiting_answers",
		"message":   "Questions displayed to user. Their response will follow.",
		"context":   contextStr,
		"questions": questions,
	}, nil
}
