// Package workspace exposes a local directory to the agent as a read-only
// context source — typically the directory kasa was launched from. The agent
// uses it to pick up task documentation, notes, draft YAML, etc.
//
// The workspace is a soft default, not a sandbox. Absolute paths passed to
// Read are honoured as-is; the size and skip-list defaults exist for token
// hygiene (avoid blowing the context window on a stray binary or node_modules
// tree), not security. There is no privilege boundary between the user and
// the kasa process — locking down file reads would be theatre.
package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// DefaultMaxFileBytes caps a single Read at 256 KiB.
	DefaultMaxFileBytes = 256 * 1024
	// DefaultMaxListEntries caps a single List at 500 entries.
	DefaultMaxListEntries = 500
)

// DefaultSkipDirs are directory names skipped during List. Token hygiene only.
var DefaultSkipDirs = map[string]bool{
	".git":         true,
	".hg":          true,
	".svn":         true,
	"node_modules": true,
	"vendor":       true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".next":        true,
	".cache":       true,
	".idea":        true,
	".vscode":      true,
}

// DefaultSkipExts are file extensions skipped during List (binaries, archives,
// media). Read still serves them if asked by name.
var DefaultSkipExts = map[string]bool{
	".exe": true, ".bin": true, ".so": true, ".dylib": true, ".a": true, ".o": true,
	".zip": true, ".gz": true, ".tar": true, ".tgz": true, ".bz2": true, ".xz": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true,
	".mp3": true, ".mp4": true, ".mov": true, ".avi": true, ".webm": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".pdf": true,
}

// Workspace exposes a directory to the agent.
type Workspace struct {
	root           string
	maxFileBytes   int64
	maxListEntries int
	skipDirs       map[string]bool
	skipExts       map[string]bool
}

// Entry is a single file or directory in a List result.
type Entry struct {
	Path  string // forward-slashed, relative to workspace root
	Size  int64  // 0 for directories
	IsDir bool
}

// New constructs a Workspace rooted at root. Root must exist and be a directory.
func New(root string) (*Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving workspace path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace %q is not a directory", abs)
	}
	return &Workspace{
		root:           abs,
		maxFileBytes:   DefaultMaxFileBytes,
		maxListEntries: DefaultMaxListEntries,
		skipDirs:       DefaultSkipDirs,
		skipExts:       DefaultSkipExts,
	}, nil
}

// Root returns the absolute workspace root.
func (w *Workspace) Root() string { return w.root }

// resolve turns p into an absolute path. Absolute inputs pass through; relative
// inputs are joined with the workspace root.
func (w *Workspace) resolve(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(w.root, p)
}

// List walks the workspace under prefix (relative to root, "" or "." for the
// whole tree) up to maxDepth levels. maxDepth == 0 means unlimited. Returns
// entries sorted by path, plus a truncated flag when the entry cap was hit.
//
// Skips directories and extensions in the default skip lists, and skips
// symlinks that resolve outside the workspace root — both for token hygiene.
func (w *Workspace) List(prefix string, maxDepth int) ([]Entry, bool, error) {
	if prefix == "" {
		prefix = "."
	}
	base := w.resolve(prefix)
	info, err := os.Lstat(base)
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		rel, _ := filepath.Rel(w.root, base)
		return []Entry{{
			Path:  filepath.ToSlash(rel),
			Size:  info.Size(),
			IsDir: false,
		}}, false, nil
	}

	var entries []Entry
	truncated := false
	baseDepth := strings.Count(base, string(filepath.Separator))
	sep := string(filepath.Separator)

	err = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == base {
			return nil
		}
		if truncated {
			return filepath.SkipAll
		}

		if maxDepth > 0 {
			depth := strings.Count(path, sep) - baseDepth
			if depth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		name := d.Name()
		isDir := d.IsDir()
		if isDir {
			if w.skipDirs[name] {
				return filepath.SkipDir
			}
		} else {
			ext := strings.ToLower(filepath.Ext(name))
			if w.skipExts[ext] {
				return nil
			}
		}

		// Skip symlinks pointing outside the workspace root. d.Type() is cheap
		// (no extra syscall); we only resolve the chain when we actually see one.
		if d.Type()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}
			if target != w.root && !strings.HasPrefix(target, w.root+sep) {
				return nil
			}
		}

		rel, _ := filepath.Rel(w.root, path)
		e := Entry{
			Path:  filepath.ToSlash(rel),
			IsDir: isDir,
		}
		if !isDir {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			e.Size = info.Size()
		}
		entries = append(entries, e)

		if len(entries) >= w.maxListEntries {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, truncated, nil
}

// Read returns up to maxFileBytes of the file at p. truncated is true when the
// file was larger than the cap.
func (w *Workspace) Read(p string) (content []byte, truncated bool, err error) {
	f, err := os.Open(w.resolve(p))
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if info.IsDir() {
		return nil, false, fmt.Errorf("%q is a directory", p)
	}
	buf, err := io.ReadAll(io.LimitReader(f, w.maxFileBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(buf)) > w.maxFileBytes {
		return buf[:w.maxFileBytes], true, nil
	}
	return buf, false, nil
}
