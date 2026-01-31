package tools

import (
	"encoding/json"
	"fmt"

	"github.com/perbu/kasa/references"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// GetReferenceTool provides access to Kubernetes resource documentation.
type GetReferenceTool struct{}

// NewGetReferenceTool creates a new GetReferenceTool.
func NewGetReferenceTool() *GetReferenceTool {
	return &GetReferenceTool{}
}

// Name returns the tool name.
func (t *GetReferenceTool) Name() string {
	return "get_reference"
}

// Description returns the tool description.
func (t *GetReferenceTool) Description() string {
	return "Get reference documentation for Kubernetes resources. Call without a topic to list available references, or specify a topic to get detailed documentation."
}

// IsLongRunning returns false as this is a quick operation.
func (t *GetReferenceTool) IsLongRunning() bool {
	return false
}

// ProcessRequest adds this tool to the LLM request.
func (t *GetReferenceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return addFunctionTool(req, t)
}

// Declaration returns the function declaration for the tool.
func (t *GetReferenceTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "object",
			Properties: map[string]*genai.Schema{
				"topic": {
					Type:        "string",
					Description: "The topic to look up (e.g., 'deployment', 'service', 'secret'). Leave empty to list all available topics.",
				},
			},
		},
	}
}

// Run executes the tool.
func (t *GetReferenceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	// Parse arguments
	var topic string
	if args != nil {
		argsMap, ok := args.(map[string]any)
		if !ok {
			if argsStr, ok := args.(string); ok {
				if err := json.Unmarshal([]byte(argsStr), &argsMap); err != nil {
					return map[string]any{"error": "invalid arguments format"}, nil
				}
			}
		}
		if argsMap != nil {
			if t, ok := argsMap["topic"].(string); ok {
				topic = t
			}
		}
	}

	// If no topic specified, list available references
	if topic == "" {
		descriptions := references.ListWithDescriptions()
		topics := make([]map[string]string, 0, len(descriptions))
		for name, desc := range descriptions {
			topics = append(topics, map[string]string{
				"topic":       name,
				"description": desc,
			})
		}
		return map[string]any{
			"available_topics": topics,
			"count":            len(topics),
			"hint":             "Call get_reference with a specific topic to get detailed documentation",
		}, nil
	}

	// Look up the specific topic
	content, err := references.Lookup(topic)
	if err != nil {
		available := references.List()
		return map[string]any{
			"error":            fmt.Sprintf("Topic '%s' not found", topic),
			"available_topics": available,
		}, nil
	}

	return map[string]any{
		"topic":   topic,
		"content": content,
	}, nil
}
