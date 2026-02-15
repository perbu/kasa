package manifest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manager handles manifest file storage and git operations.
// Files are stored under <baseDir>/<context>/<namespace>/<app>/<type>.yaml.
// Git operations use baseDir as the repo root; file operations use contextDir().
type Manager struct {
	baseDir string
	context string
}

// ManifestInfo contains metadata about a manifest file.
type ManifestInfo struct {
	Namespace string `json:"namespace"`
	App       string `json:"app"`
	Type      string `json:"type"` // "deployment", "service", etc.
	Path      string `json:"path"` // relative path from baseDir
}

// NewManager creates a new Manager with the given base directory and cluster context.
// The baseDir can contain ~ which will be expanded to the home directory.
// The context is used as a subdirectory under baseDir to scope manifests per cluster.
func NewManager(baseDir, context string) (*Manager, error) {
	// Expand ~ to home directory
	if strings.HasPrefix(baseDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("getting home directory: %w", err)
		}
		baseDir = filepath.Join(home, baseDir[1:])
	}

	// Clean the path
	baseDir = filepath.Clean(baseDir)

	m := &Manager{
		baseDir: baseDir,
		context: context,
	}

	// Ensure base directory exists (git repo root)
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("creating base directory: %w", err)
	}

	// Ensure context directory exists
	if err := os.MkdirAll(m.contextDir(), 0755); err != nil {
		return nil, fmt.Errorf("creating context directory: %w", err)
	}

	return m, nil
}

// BaseDir returns the base directory for manifests (git repo root).
func (m *Manager) BaseDir() string {
	return m.baseDir
}

// Context returns the cluster context name.
func (m *Manager) Context() string {
	return m.context
}

// ListContexts returns the names of all context subdirectories in baseDir.
func (m *Manager) ListContexts() []string {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return nil
	}
	var contexts []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".git" {
			contexts = append(contexts, e.Name())
		}
	}
	return contexts
}

// contextDir returns the context-scoped directory: baseDir/context.
// All file operations (save, read, list, delete) use this as their root.
func (m *Manager) contextDir() string {
	return filepath.Join(m.baseDir, m.context)
}

// EnsureGitInit ensures the base directory is a git repository.
// If .git/ doesn't exist, it runs git init.
func (m *Manager) EnsureGitInit() error {
	gitDir := filepath.Join(m.baseDir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		// .git already exists
		return nil
	}

	// Run git init
	cmd := exec.Command("git", "init")
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// SaveManifest saves a manifest file to the appropriate location.
// The file is saved to <baseDir>/<context>/<namespace>/<appName>/<resourceType>.yaml
// Returns the path to the saved file.
func (m *Manager) SaveManifest(namespace, appName, resourceType string, content []byte) (string, error) {
	// Create directory structure
	dir := filepath.Join(m.contextDir(), namespace, appName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating manifest directory: %w", err)
	}

	// Write the file
	filename := resourceType + ".yaml"
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, content, 0644); err != nil {
		return "", fmt.Errorf("writing manifest file: %w", err)
	}

	// Stage the file
	if err := m.stageFile(path); err != nil {
		return "", fmt.Errorf("staging manifest file: %w", err)
	}

	return path, nil
}

// stageFile stages a file for commit using git add.
func (m *Manager) stageFile(path string) error {
	// Make path relative to baseDir for git add
	relPath, err := filepath.Rel(m.baseDir, path)
	if err != nil {
		return fmt.Errorf("getting relative path: %w", err)
	}

	cmd := exec.Command("git", "add", relPath)
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git add failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// Commit creates a git commit with the given message.
// Only commits if there are staged changes.
func (m *Manager) Commit(message string) error {
	// Check if there are staged changes
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = m.baseDir
	if err := cmd.Run(); err == nil {
		// No staged changes (exit code 0 means no differences)
		return fmt.Errorf("no staged changes to commit")
	}

	// Create commit
	cmd = exec.Command("git", "commit", "-m", message)
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commit failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// GetStatus returns the git status of the manifest directory.
func (m *Manager) GetStatus() (string, error) {
	cmd := exec.Command("git", "status", "--short")
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git status failed: %w\nOutput: %s", err, string(output))
	}

	return string(output), nil
}

// ListManifests scans the directory structure and returns manifest metadata.
// If namespace is non-empty, filters to that namespace.
// If app is non-empty, filters to that app name.
func (m *Manager) ListManifests(namespace, app string) ([]ManifestInfo, error) {
	var manifests []ManifestInfo
	ctxDir := m.contextDir()

	err := filepath.Walk(ctxDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}

		// Skip .git directory
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}

		// Only process .yaml files
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".yaml") {
			return nil
		}

		// Get relative path from context dir
		relPath, err := filepath.Rel(ctxDir, path)
		if err != nil {
			return err
		}

		// Parse path as <namespace>/<app>/<type>.yaml
		parts := strings.Split(relPath, string(filepath.Separator))
		if len(parts) != 3 {
			// Skip files that don't match expected structure
			return nil
		}

		ns := parts[0]
		appName := parts[1]
		resourceType := strings.TrimSuffix(parts[2], ".yaml")

		// Apply filters
		if namespace != "" && ns != namespace {
			return nil
		}
		if app != "" && appName != app {
			return nil
		}

		manifests = append(manifests, ManifestInfo{
			Namespace: ns,
			App:       appName,
			Type:      resourceType,
			Path:      relPath,
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking manifest directory: %w", err)
	}

	return manifests, nil
}

// ReadManifest reads and returns the content of a manifest file.
func (m *Manager) ReadManifest(namespace, app, resourceType string) ([]byte, error) {
	path := filepath.Join(m.contextDir(), namespace, app, resourceType+".yaml")

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("manifest not found: %s/%s/%s.yaml", namespace, app, resourceType)
		}
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	return content, nil
}

// DeleteManifest deletes a manifest file and stages the deletion in git.
// If resourceType is empty, deletes all manifests for the app.
// Returns the list of deleted file paths (relative to context dir).
func (m *Manager) DeleteManifest(namespace, app, resourceType string) ([]string, error) {
	var deleted []string
	ctxDir := m.contextDir()

	if resourceType != "" {
		// Delete single manifest
		path := filepath.Join(ctxDir, namespace, app, resourceType+".yaml")
		relPath := filepath.Join(namespace, app, resourceType+".yaml")

		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("manifest not found: %s", relPath)
			}
			return nil, fmt.Errorf("deleting manifest: %w", err)
		}

		// Stage the deletion (git path is relative to baseDir)
		gitRelPath := filepath.Join(m.context, namespace, app, resourceType+".yaml")
		if err := m.stageDeletion(gitRelPath); err != nil {
			return nil, fmt.Errorf("staging deletion: %w", err)
		}

		deleted = append(deleted, relPath)
	} else {
		// Delete all manifests for the app
		appDir := filepath.Join(ctxDir, namespace, app)
		entries, err := os.ReadDir(appDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("app directory not found: %s/%s", namespace, app)
			}
			return nil, fmt.Errorf("reading app directory: %w", err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}

			path := filepath.Join(appDir, entry.Name())
			relPath := filepath.Join(namespace, app, entry.Name())

			if err := os.Remove(path); err != nil {
				return nil, fmt.Errorf("deleting manifest %s: %w", relPath, err)
			}

			gitRelPath := filepath.Join(m.context, namespace, app, entry.Name())
			if err := m.stageDeletion(gitRelPath); err != nil {
				return nil, fmt.Errorf("staging deletion of %s: %w", relPath, err)
			}

			deleted = append(deleted, relPath)
		}
	}

	// Clean up empty app directory
	appDir := filepath.Join(ctxDir, namespace, app)
	if isEmpty, _ := isDirEmpty(appDir); isEmpty {
		os.Remove(appDir)
	}

	// Clean up empty namespace directory
	nsDir := filepath.Join(ctxDir, namespace)
	if isEmpty, _ := isDirEmpty(nsDir); isEmpty {
		os.Remove(nsDir)
	}

	return deleted, nil
}

// stageDeletion stages a file deletion in git.
func (m *Manager) stageDeletion(relPath string) error {
	cmd := exec.Command("git", "add", relPath)
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git add failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}

// isDirEmpty checks if a directory is empty.
func isDirEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

// SetupRemote configures the git remote "origin" to the given URL.
// If origin already exists with the same URL, this is a no-op.
// If origin exists with a different URL, it updates the URL.
// If origin doesn't exist, it adds it.
func (m *Manager) SetupRemote(url string) error {
	// Check if remote "origin" already exists
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err == nil {
		// Remote exists — check if URL matches
		existing := strings.TrimSpace(string(output))
		if existing == url {
			return nil
		}
		// URL differs, update it
		cmd = exec.Command("git", "remote", "set-url", "origin", url)
		cmd.Dir = m.baseDir
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git remote set-url failed: %w\nOutput: %s", err, string(output))
		}
		return nil
	}

	// Remote doesn't exist, add it
	cmd = exec.Command("git", "remote", "add", "origin", url)
	cmd.Dir = m.baseDir
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git remote add failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}

// HasRemote returns true if a git remote "origin" is configured.
func (m *Manager) HasRemote() bool {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = m.baseDir
	return cmd.Run() == nil
}

// Pull fetches and fast-forward merges from the remote.
// If no remote is configured, this is a no-op.
// Returns an error if fast-forward is not possible (diverged history).
func (m *Manager) Pull() error {
	if !m.HasRemote() {
		return nil
	}

	cmd := exec.Command("git", "pull", "--ff-only", "origin", "HEAD")
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("remote has diverged from local — resolve manually in %s\ngit output: %s", m.baseDir, strings.TrimSpace(string(output)))
	}
	return nil
}

// Push pushes the current branch to the remote.
// If no remote is configured, this is a no-op.
func (m *Manager) Push() error {
	if !m.HasRemote() {
		return nil
	}

	cmd := exec.Command("git", "push", "origin", "HEAD")
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("push failed — remote may have new changes, pull first\ngit output: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// StagedChangeCount returns the number of staged files in the git index.
// Returns 0 if there are no staged changes or on error.
func (m *Manager) StagedChangeCount() int {
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = m.baseDir
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return 0
	}
	return len(strings.Split(text, "\n"))
}

// StagedDiff returns the full diff of staged changes.
func (m *Manager) StagedDiff() (string, error) {
	cmd := exec.Command("git", "diff", "--cached")
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff --cached failed: %w\nOutput: %s", err, string(output))
	}
	return string(output), nil
}

// ManifestExists checks if a manifest file already exists.
func (m *Manager) ManifestExists(namespace, app, resourceType string) bool {
	path := filepath.Join(m.contextDir(), namespace, app, resourceType+".yaml")
	_, err := os.Stat(path)
	return err == nil
}

// ReadNotes reads KASA.md files at up to four levels (top-level, context, namespace, app)
// and returns their concatenated content with level headers.
// Missing files are silently skipped. Returns empty string if no notes exist.
func (m *Manager) ReadNotes(namespace, app string) string {
	var sections []string
	ctxDir := m.contextDir()

	// Top-level notes (repo root)
	if content, err := os.ReadFile(filepath.Join(m.baseDir, "KASA.md")); err == nil {
		sections = append(sections, "## Deployment Notes (top-level)\n"+string(content))
	}

	// Context-level notes
	if content, err := os.ReadFile(filepath.Join(ctxDir, "KASA.md")); err == nil {
		sections = append(sections, fmt.Sprintf("## Deployment Notes (%s)\n%s", m.context, string(content)))
	}

	// Namespace-level notes
	if namespace != "" {
		if content, err := os.ReadFile(filepath.Join(ctxDir, namespace, "KASA.md")); err == nil {
			sections = append(sections, fmt.Sprintf("## Deployment Notes (%s)\n%s", namespace, string(content)))
		}
	}

	// App-level notes
	if namespace != "" && app != "" {
		if content, err := os.ReadFile(filepath.Join(ctxDir, namespace, app, "KASA.md")); err == nil {
			sections = append(sections, fmt.Sprintf("## Deployment Notes (%s/%s)\n%s", namespace, app, string(content)))
		}
	}

	return strings.Join(sections, "\n\n")
}

// SaveNotes writes a KASA.md file at the appropriate level and stages it with git.
// If both namespace and app are empty, writes context-level. If only app is empty, writes namespace-level.
// If both are set, writes app-level.
// Returns the file path.
func (m *Manager) SaveNotes(namespace, app, content string) (string, error) {
	ctxDir := m.contextDir()
	var dir string
	switch {
	case namespace == "" && app == "":
		dir = ctxDir
	case app == "":
		dir = filepath.Join(ctxDir, namespace)
	default:
		dir = filepath.Join(ctxDir, namespace, app)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating directory for notes: %w", err)
	}

	path := filepath.Join(dir, "KASA.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("writing notes file: %w", err)
	}

	if err := m.stageFile(path); err != nil {
		return "", fmt.Errorf("staging notes file: %w", err)
	}

	return path, nil
}

// DeleteNamespace deletes all manifests for a namespace and stages the deletions.
// Returns the list of deleted file paths (relative to context dir).
func (m *Manager) DeleteNamespace(namespace string) ([]string, error) {
	var deleted []string
	ctxDir := m.contextDir()

	nsDir := filepath.Join(ctxDir, namespace)

	// Check if namespace directory exists
	if _, err := os.Stat(nsDir); os.IsNotExist(err) {
		return nil, nil // No manifests to delete
	}

	// Walk the namespace directory and delete all yaml files
	err := filepath.Walk(nsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories (we'll clean them up at the end)
		if info.IsDir() {
			return nil
		}

		// Only process .yaml files
		if !strings.HasSuffix(info.Name(), ".yaml") {
			return nil
		}

		// Get relative path from baseDir (for git staging)
		gitRelPath, err := filepath.Rel(m.baseDir, path)
		if err != nil {
			return err
		}

		// Get relative path from context dir (for return value)
		relPath, err := filepath.Rel(ctxDir, path)
		if err != nil {
			return err
		}

		// Delete the file
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("deleting manifest %s: %w", relPath, err)
		}

		// Stage the deletion
		if err := m.stageDeletion(gitRelPath); err != nil {
			return fmt.Errorf("staging deletion of %s: %w", relPath, err)
		}

		deleted = append(deleted, relPath)
		return nil
	})

	if err != nil {
		return deleted, err
	}

	// Clean up empty directories
	filepath.Walk(nsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		if isEmpty, _ := isDirEmpty(path); isEmpty {
			os.Remove(path)
		}
		return nil
	})

	// Remove the namespace directory itself if empty
	if isEmpty, _ := isDirEmpty(nsDir); isEmpty {
		os.Remove(nsDir)
	}

	return deleted, nil
}
