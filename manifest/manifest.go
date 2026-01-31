package manifest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manager handles manifest file storage and git operations.
type Manager struct {
	baseDir string
}

// NewManager creates a new Manager with the given base directory.
// The baseDir can contain ~ which will be expanded to the home directory.
func NewManager(baseDir string) (*Manager, error) {
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
	}

	// Ensure directory exists
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("creating base directory: %w", err)
	}

	return m, nil
}

// BaseDir returns the base directory for manifests.
func (m *Manager) BaseDir() string {
	return m.baseDir
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
// The file is saved to <baseDir>/<namespace>/<appName>/<resourceType>.yaml
// Returns the path to the saved file.
func (m *Manager) SaveManifest(namespace, appName, resourceType string, content []byte) (string, error) {
	// Create directory structure
	dir := filepath.Join(m.baseDir, namespace, appName)
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
