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

// ManifestInfo contains metadata about a manifest file.
type ManifestInfo struct {
	Namespace string `json:"namespace"`
	App       string `json:"app"`
	Type      string `json:"type"` // "deployment", "service", etc.
	Path      string `json:"path"` // relative path from baseDir
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

// ListManifests scans the directory structure and returns manifest metadata.
// If namespace is non-empty, filters to that namespace.
// If app is non-empty, filters to that app name.
func (m *Manager) ListManifests(namespace, app string) ([]ManifestInfo, error) {
	var manifests []ManifestInfo

	err := filepath.Walk(m.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
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

		// Get relative path from baseDir
		relPath, err := filepath.Rel(m.baseDir, path)
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
	path := filepath.Join(m.baseDir, namespace, app, resourceType+".yaml")

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
// Returns the list of deleted file paths.
func (m *Manager) DeleteManifest(namespace, app, resourceType string) ([]string, error) {
	var deleted []string

	if resourceType != "" {
		// Delete single manifest
		path := filepath.Join(m.baseDir, namespace, app, resourceType+".yaml")
		relPath := filepath.Join(namespace, app, resourceType+".yaml")

		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("manifest not found: %s", relPath)
			}
			return nil, fmt.Errorf("deleting manifest: %w", err)
		}

		// Stage the deletion
		if err := m.stageDeletion(relPath); err != nil {
			return nil, fmt.Errorf("staging deletion: %w", err)
		}

		deleted = append(deleted, relPath)
	} else {
		// Delete all manifests for the app
		appDir := filepath.Join(m.baseDir, namespace, app)
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

			if err := m.stageDeletion(relPath); err != nil {
				return nil, fmt.Errorf("staging deletion of %s: %w", relPath, err)
			}

			deleted = append(deleted, relPath)
		}
	}

	// Clean up empty app directory
	appDir := filepath.Join(m.baseDir, namespace, app)
	if isEmpty, _ := isDirEmpty(appDir); isEmpty {
		os.Remove(appDir)
	}

	// Clean up empty namespace directory
	nsDir := filepath.Join(m.baseDir, namespace)
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

// ManifestExists checks if a manifest file already exists.
func (m *Manager) ManifestExists(namespace, app, resourceType string) bool {
	path := filepath.Join(m.baseDir, namespace, app, resourceType+".yaml")
	_, err := os.Stat(path)
	return err == nil
}

// DeleteNamespace deletes all manifests for a namespace and stages the deletions.
// Returns the list of deleted file paths.
func (m *Manager) DeleteNamespace(namespace string) ([]string, error) {
	var deleted []string

	nsDir := filepath.Join(m.baseDir, namespace)

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

		// Get relative path from baseDir
		relPath, err := filepath.Rel(m.baseDir, path)
		if err != nil {
			return err
		}

		// Delete the file
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("deleting manifest %s: %w", relPath, err)
		}

		// Stage the deletion
		if err := m.stageDeletion(relPath); err != nil {
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
