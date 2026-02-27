package repl

import (
	"fmt"
	"strings"
)

// command describes a single REPL slash command.
type command struct {
	Name        string
	Description string
	Group       string // used for grouping in help output
}

// commands is the single source of truth for all REPL commands.
var commands = []command{
	{"/approve", "Approve the pending plan", "Plans"},
	{"/abort", "Reject the pending plan (alias: /reject)", "Plans"},
	{"/copy", "Copy plan YAML to clipboard", "Plans"},
	{"/plan", "Display the pending plan again", "Plans"},
	{"/commit", "Commit staged manifest changes", "Manifests"},
	{"/pull", "Pull manifest changes from remote", "Manifests"},
	{"/push", "Push manifest changes to remote", "Manifests"},
	{"/status", "Show manifest git status", "Manifests"},
	{"/drift", "Compare manifests against live cluster", "Manifests"},
	{"/context", "Switch Kubernetes cluster context", "Cluster"},
	{"/help", "Show this help message", "General"},
	{"/debug", "Toggle debug mode", "General"},
	{"/dump", "Dump session events to file", "General"},
	{"/clear", "Clear conversation history", "General"},
}

// commandSummary returns a single-line summary for the welcome screen.
func commandSummary() string {
	groups := []struct {
		name  string
		label string
	}{
		{"Plans", "plans"},
		{"Manifests", "manifests"},
		{"Cluster", "cluster"},
		{"General", ""},
	}

	var parts []string
	for _, g := range groups {
		var names []string
		for _, c := range commands {
			if c.Group == g.name {
				names = append(names, "**"+c.Name+"**")
			}
		}
		s := strings.Join(names, " ")
		if g.label != "" {
			s += " " + g.label
		}
		parts = append(parts, s)
	}
	return "Commands: " + strings.Join(parts, " · ") + " · **exit**"
}

// commandHelp returns the full help text with descriptions.
func commandHelp() string {
	var b strings.Builder
	currentGroup := ""
	for _, c := range commands {
		if c.Group != currentGroup {
			if currentGroup != "" {
				b.WriteString("\n")
			}
			b.WriteString(c.Group + ":\n")
			currentGroup = c.Group
		}
		b.WriteString(fmt.Sprintf("  %-12s %s\n", c.Name, c.Description))
	}
	b.WriteString(fmt.Sprintf("\n  %-12s %s\n", "exit", "Exit kasa"))
	return b.String()
}
