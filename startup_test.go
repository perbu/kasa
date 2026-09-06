package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/perbu/kasa/manifest"
	"github.com/perbu/kasa/tools"
)

func TestPullManifestsOnStartup(t *testing.T) {
	// All repositories and remotes are temporary and local. Isolate Git from
	// user signing, identity, autostash and other configuration.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Kasa Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Kasa Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	for _, scenario := range []string{"no remote", "up to date", "remote changes", "local commits", "dirty worktree", "first pull"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			local := filepath.Join(root, "local")
			remote := filepath.Join(root, "remote.git")
			seed := filepath.Join(root, "seed")
			if scenario != "no remote" {
				startupGit(t, root, "init", "--bare", "--initial-branch=main", remote)
				startupGit(t, root, "init", "--initial-branch=main", seed)
				startupWrite(t, seed, "cluster-a/deployment.yaml", "original\n")
				startupWrite(t, seed, "cluster-b/deployment.yaml", "other cluster\n")
				startupGit(t, seed, "add", ".")
				startupGit(t, seed, "commit", "-m", "initial")
				startupGit(t, seed, "remote", "add", "origin", remote)
				startupGit(t, seed, "push", "-u", "origin", "main")
				if scenario != "first pull" {
					startupGit(t, root, "clone", remote, local)
				}
			}

			mgr, err := manifest.NewManager(local, "cluster-a")
			if err != nil {
				t.Fatal(err)
			}
			if err := mgr.EnsureGitInit(); err != nil {
				t.Fatal(err)
			}
			if scenario == "first pull" {
				if err := mgr.SetupRemote(remote); err != nil {
					t.Fatal(err)
				}
			}

			cacheDir := filepath.Join(root, "cache")
			caches := []*tools.DriftCache{
				tools.NewDriftCache(cacheDir, "cluster-a"),
				tools.NewDriftCache(cacheDir, "cluster-b"),
			}
			for _, cache := range caches {
				if err := cache.Save(&tools.DriftScanResults{}); err != nil {
					t.Fatal(err)
				}
			}

			expected := "original\n"
			if scenario == "remote changes" || scenario == "local commits" || scenario == "dirty worktree" {
				startupWrite(t, seed, "cluster-a/deployment.yaml", "remote update\n")
				startupGit(t, seed, "commit", "-am", "remote update")
				startupGit(t, seed, "push")
				expected = "remote update\n"
			}
			if scenario == "local commits" {
				startupWrite(t, local, "cluster-a/local.yaml", "local commit\n")
				startupGit(t, local, "add", ".")
				startupGit(t, local, "commit", "-m", "local work")
			}
			if scenario == "dirty worktree" {
				expected = "uncommitted work\n"
				startupWrite(t, local, "cluster-a/deployment.yaml", expected)
			}

			var out bytes.Buffer
			pullManifestsOnStartup(mgr, cacheDir, &out)
			if scenario == "no remote" {
				if out.Len() != 0 {
					t.Fatalf("unexpected output: %s", &out)
				}
			} else {
				got, err := os.ReadFile(filepath.Join(local, "cluster-a/deployment.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != expected {
					t.Fatalf("got %q, want %q", got, expected)
				}
				if scenario == "dirty worktree" {
					if !strings.Contains(out.String(), "Startup pull failed:") {
						t.Fatalf("missing failure: %s", &out)
					}
				} else if !strings.Contains(out.String(), "Pulled from remote.") {
					t.Fatalf("missing success: %s", &out)
				}
			}
			if scenario == "local commits" {
				startupGit(t, local, "merge-base", "--is-ancestor", "origin/main", "HEAD")
				got, err := os.ReadFile(filepath.Join(local, "cluster-a/local.yaml"))
				if err != nil || string(got) != "local commit\n" {
					t.Fatalf("local work lost: %q, %v", got, err)
				}
			}
			wantFresh := scenario == "no remote" || scenario == "up to date"
			for _, cache := range caches {
				if cache.IsFresh() != wantFresh {
					t.Errorf("cache freshness = %v, want %v", cache.IsFresh(), wantFresh)
				}
			}
		})
	}
}

func startupGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func startupWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
