package repl

import "google.golang.org/genai"

// ToolCallInfo holds information about a function call in an event.
type ToolCallInfo struct {
	Name   string
	Reason string
	Args   map[string]any
}

// ToolResponseInfo holds a function response name and its data.
type ToolResponseInfo struct {
	Name     string
	Response map[string]any
}

// EventResult holds the extracted information from processing event content parts.
type EventResult struct {
	Plan          *Plan
	Clarification *Clarification
	TextParts     []string
	ToolCalls     []ToolCallInfo
	ToolResponses []ToolResponseInfo
}

// ProcessEventParts extracts plan, clarification, text, and tool call info from event content parts.
func ProcessEventParts(parts []*genai.Part) *EventResult {
	result := &EventResult{}

	for _, part := range parts {
		if part.FunctionCall != nil {
			if part.FunctionCall.Name == "propose_plan" && part.FunctionCall.Args != nil {
				plan := ParsePlanFromResponse(part.FunctionCall.Args)
				if plan != nil && plan.IsValid() {
					result.Plan = plan
				}
			}

			if part.FunctionCall.Name == "ask_clarification" && part.FunctionCall.Args != nil {
				clarification := ParseClarificationFromResponse(part.FunctionCall.Args)
				if clarification != nil {
					result.Clarification = clarification
				}
			}

			result.ToolCalls = append(result.ToolCalls, ToolCallInfo{
				Name:   part.FunctionCall.Name,
				Reason: extractToolReason(part.FunctionCall.Args),
				Args:   part.FunctionCall.Args,
			})
		}

		if part.FunctionResponse != nil {
			result.ToolResponses = append(result.ToolResponses, ToolResponseInfo{
				Name:     part.FunctionResponse.Name,
				Response: part.FunctionResponse.Response,
			})
		}

		if part.Text != "" {
			result.TextParts = append(result.TextParts, part.Text)
		}
	}

	return result
}

// extractToolReason gets the "reason" field from tool call args.
func extractToolReason(args map[string]any) string {
	if args == nil {
		return ""
	}
	reason, ok := args["reason"].(string)
	if !ok {
		return ""
	}
	return reason
}
