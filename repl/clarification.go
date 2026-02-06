package repl

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
)

// RenderClarification renders a clarification to a string using glamour markdown rendering.
// Returns the rendered string, or plain markdown if rendering fails.
func RenderClarification(c *Clarification) string {
	if c == nil {
		return ""
	}

	md := buildClarificationMarkdown(c)

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		return md
	}

	out, err := renderer.Render(md)
	if err != nil {
		return md
	}
	return out
}

// DisplayClarification formats and prints clarification questions for the user.
func DisplayClarification(c *Clarification) {
	fmt.Print(RenderClarification(c))
}

// buildClarificationMarkdown builds the markdown string for a clarification.
func buildClarificationMarkdown(c *Clarification) string {
	var md strings.Builder
	md.WriteString("# Clarification Needed\n\n")
	md.WriteString(c.Context)
	md.WriteString("\n\n")

	for i, q := range c.Questions {
		md.WriteString(fmt.Sprintf("**%d. %s**\n\n", i+1, q.Question))

		if len(q.Options) > 0 {
			for j, opt := range q.Options {
				md.WriteString(fmt.Sprintf("  %c) %s\n", 'a'+rune(j), opt))
			}
			md.WriteString("\n")
		}
	}

	md.WriteString("---\n\n")
	md.WriteString("Type your answers to continue.\n")
	return md.String()
}

// ParseClarificationFromResponse extracts a Clarification from the ask_clarification tool args.
func ParseClarificationFromResponse(args map[string]any) *Clarification {
	contextStr, _ := args["context"].(string)

	questionsRaw, ok := args["questions"].([]any)
	if !ok {
		return nil
	}

	questions := make([]ClarificationQuestion, 0, len(questionsRaw))
	for _, qRaw := range questionsRaw {
		qMap, ok := qRaw.(map[string]any)
		if !ok {
			continue
		}

		q := ClarificationQuestion{
			Question: getString(qMap, "question"),
		}

		if optionsRaw, ok := qMap["options"].([]any); ok {
			for _, optRaw := range optionsRaw {
				if opt, ok := optRaw.(string); ok {
					q.Options = append(q.Options, opt)
				}
			}
		}

		questions = append(questions, q)
	}

	if len(questions) == 0 {
		return nil
	}

	return &Clarification{
		Context:   contextStr,
		Questions: questions,
	}
}
