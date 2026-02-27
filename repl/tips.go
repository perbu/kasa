package repl

import (
	"math/rand/v2"
)

// tips is the pool of startup usage tips.
var tips = []string{
	"you can list and switch Kubernetes contexts with the **/context** command?",
	"you can scan for drift between your manifests and the live cluster with **/drift**?",
	"you can copy a pending plan's YAML to clipboard with **/copy**?",
	"you can use Kasa as a team by setting `deployments.remote` in config and syncing with **/pull** and **/push**?",
	"you can check the git status of your manifests with **/status**?",
	"you can commit manifest changes with **/commit**? It auto-generates a commit message.",
	"you can run Kasa non-interactively with `kasa -prompt \"list pods\"`?",
	"you can clear the conversation context and start fresh with **/clear**?",
	"you can run `kasa init` to create or update your configuration?",
	"mutating operations require approval? The agent proposes a plan and you **/approve** or **/abort** it.",
	"you can toggle debug mode at any time with **/debug**?",
	"you can dump the full session for troubleshooting with **/dump**?",
}

// randomTip returns a random "Did you know...?" tip formatted as markdown.
func randomTip() string {
	return "**Tip:** Did you know " + tips[rand.IntN(len(tips))]
}
