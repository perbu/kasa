package main

import (
	"fmt"
	"io"

	"github.com/perbu/kasa/manifest"
	"github.com/perbu/kasa/tools"
)

// pullManifestsOnStartup runs the same Git operation as /pull, before any
// manifest reads or drift scans. A failed pull is visible but leaves the REPL
// available so the user can recover and retry.
func pullManifestsOnStartup(mgr *manifest.Manager, cacheDir string, out io.Writer) {
	if !mgr.HasRemote() {
		return
	}

	fmt.Fprintln(out, "Pulling manifests from remote...")
	before, beforeErr := mgr.Revision()
	contexts := mgr.ListContexts()
	gitOut, err := mgr.Pull()
	if gitOut != "" {
		fmt.Fprintln(out, gitOut)
	}
	after, afterErr := mgr.Revision()
	if err != nil || beforeErr != nil || afterErr != nil || before != after {
		// Pull affects the whole repository, including other cluster contexts.
		// Include removed contexts and invalidate on failure too: a conflicted
		// rebase can have changed files even though it didn't finish.
		contexts = append(contexts, mgr.ListContexts()...)
		contexts = append(contexts, mgr.Context())
		seen := make(map[string]bool)
		for _, name := range contexts {
			if !seen[name] {
				tools.NewDriftCache(cacheDir, name).Invalidate()
				seen[name] = true
			}
		}
	}

	if err != nil {
		fmt.Fprintf(out, "Startup pull failed: %v. Resolve the Git issue, then use /pull to retry.\n", err)
		return
	}
	fmt.Fprintln(out, "Pulled from remote.")
}
