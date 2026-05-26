// Package version provides version history parsing and reconstruction.
package version

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VersionEntry represents a version in the history file.
type VersionEntry struct {
	Hash        string
	Timestamp   string
	Parent      string
	FullContent string // Only for base version
	Diff        string // Only for non-base versions
}

// HistoryFile represents a parsed .versions file.
type HistoryFile struct {
	FormatVersion string
	CanonicalFile string
	CurrentHash   string
	Versions      []VersionEntry
}

// GetVersionsFilePath returns the path to the versions file for a given canonical file.
func GetVersionsFilePath(canonicalFile, versionsDir string) string {
	dir := filepath.Dir(canonicalFile)
	filename := filepath.Base(canonicalFile)
	return filepath.Join(dir, versionsDir, filename)
}

// ParseHistoryFile parses a versions file.
func ParseHistoryFile(path string) (*HistoryFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open versions file: %w", err)
	}
	defer file.Close()

	h := &HistoryFile{}
	var currentVersion *VersionEntry
	var inDiff, inFullContent bool
	var contentLines []string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Parse header lines
		if strings.HasPrefix(line, "# VERSION_FILE_FORMAT=") {
			h.FormatVersion = strings.TrimPrefix(line, "# VERSION_FILE_FORMAT=")
			continue
		}
		if strings.HasPrefix(line, "# CANONICAL_FILE=") {
			h.CanonicalFile = strings.TrimPrefix(line, "# CANONICAL_FILE=")
			continue
		}
		if strings.HasPrefix(line, "# CURRENT_HASH=") {
			h.CurrentHash = strings.TrimPrefix(line, "# CURRENT_HASH=")
			continue
		}

		// Parse version headers
		if strings.HasPrefix(line, "[VERSION ") && strings.HasSuffix(line, "]") {
			// Save previous version if any
			if currentVersion != nil {
				if inDiff {
					currentVersion.Diff = strings.Join(contentLines, "\n")
				} else if inFullContent {
					currentVersion.FullContent = strings.Join(contentLines, "\n")
				}
				h.Versions = append(h.Versions, *currentVersion)
			}

			hash := strings.TrimSuffix(strings.TrimPrefix(line, "[VERSION "), "]")
			currentVersion = &VersionEntry{Hash: hash}
			inDiff = false
			inFullContent = false
			contentLines = nil
			continue
		}

		if currentVersion != nil {
			if strings.HasPrefix(line, "TIMESTAMP=") {
				currentVersion.Timestamp = strings.TrimPrefix(line, "TIMESTAMP=")
				continue
			}
			if strings.HasPrefix(line, "PARENT=") {
				currentVersion.Parent = strings.TrimPrefix(line, "PARENT=")
				continue
			}
			if line == "DIFF_START" {
				inDiff = true
				contentLines = nil
				continue
			}
			if line == "DIFF_END" {
				currentVersion.Diff = strings.Join(contentLines, "\n")
				inDiff = false
				continue
			}
			if line == "FULL_CONTENT_START" {
				inFullContent = true
				contentLines = nil
				continue
			}
			if line == "FULL_CONTENT_END" {
				currentVersion.FullContent = strings.Join(contentLines, "\n")
				inFullContent = false
				continue
			}
			if inDiff || inFullContent {
				contentLines = append(contentLines, line)
			}
		}
	}

	// Save last version
	if currentVersion != nil {
		if inDiff {
			currentVersion.Diff = strings.Join(contentLines, "\n")
		} else if inFullContent {
			currentVersion.FullContent = strings.Join(contentLines, "\n")
		}
		h.Versions = append(h.Versions, *currentVersion)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read versions file: %w", err)
	}

	return h, nil
}

// GetVersion retrieves a specific version entry by hash.
func (h *HistoryFile) GetVersion(hash string) *VersionEntry {
	for i := range h.Versions {
		if h.Versions[i].Hash == hash {
			return &h.Versions[i]
		}
	}
	return nil
}

// ReconstructVersion reconstructs the content of a specific version.
func ReconstructVersion(canonicalFile, targetHash, versionsDir string) (string, error) {
	versionsPath := GetVersionsFilePath(canonicalFile, versionsDir)

	history, err := ParseHistoryFile(versionsPath)
	if err != nil {
		return "", err
	}

	version := history.GetVersion(targetHash)
	if version == nil {
		return "", fmt.Errorf("version %s not found in history", targetHash)
	}

	// If it's the base version, return full content
	if version.Parent == "none" && version.FullContent != "" {
		return version.FullContent, nil
	}

	// Try backward reconstruction from current canonical file
	content, err := reconstructBackward(canonicalFile, targetHash, history)
	if err == nil {
		return content, nil
	}

	// Fallback to forward reconstruction from base
	return reconstructForward(targetHash, history)
}

// reconstructBackward reconstructs by applying reverse patches from current.
func reconstructBackward(canonicalFile, targetHash string, history *HistoryFile) (string, error) {
	// Read current canonical file content (without frontmatter)
	data, err := os.ReadFile(canonicalFile)
	if err != nil {
		return "", err
	}

	content := extractBodyContent(string(data))
	content = canonicalizeContent(content)

	// Walk backward through versions
	currentHash := history.CurrentHash
	for currentHash != targetHash {
		version := history.GetVersion(currentHash)
		if version == nil {
			return "", fmt.Errorf("version %s not found", currentHash)
		}

		if version.Parent == "none" {
			return "", fmt.Errorf("reached base without finding target")
		}

		// Apply reverse patch
		content, err = applyReversePatch(content, version.Diff)
		if err != nil {
			return "", err
		}

		currentHash = version.Parent
	}

	return content, nil
}

// reconstructForward reconstructs from the base version forward.
func reconstructForward(targetHash string, history *HistoryFile) (string, error) {
	// Find base version
	var baseVersion *VersionEntry
	for i := range history.Versions {
		if history.Versions[i].Parent == "none" {
			baseVersion = &history.Versions[i]
			break
		}
	}

	if baseVersion == nil {
		return "", fmt.Errorf("no base version found")
	}

	if baseVersion.Hash == targetHash {
		return baseVersion.FullContent, nil
	}

	// Build path from base to target
	path := buildVersionPath(baseVersion.Hash, targetHash, history)
	if path == nil {
		return "", fmt.Errorf("no path from base to target")
	}

	content := baseVersion.FullContent
	for i := 1; i < len(path); i++ {
		version := history.GetVersion(path[i])
		if version == nil {
			return "", fmt.Errorf("version %s not found", path[i])
		}

		// Apply forward patch
		var err error
		content, err = applyForwardPatch(content, version.Diff)
		if err != nil {
			return "", err
		}
	}

	return content, nil
}

// buildVersionPath builds a path from start to end hash.
func buildVersionPath(start, end string, history *HistoryFile) []string {
	// Build child map
	children := make(map[string][]string)
	for _, v := range history.Versions {
		if v.Parent != "none" {
			children[v.Parent] = append(children[v.Parent], v.Hash)
		}
	}

	// BFS from start to end
	queue := [][]string{{start}}
	visited := make(map[string]bool)

	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]

		current := path[len(path)-1]
		if current == end {
			return path
		}

		if visited[current] {
			continue
		}
		visited[current] = true

		for _, child := range children[current] {
			newPath := make([]string, len(path)+1)
			copy(newPath, path)
			newPath[len(path)] = child
			queue = append(queue, newPath)
		}
	}

	return nil
}

// applyReversePatch applies a unified diff in reverse.
func applyReversePatch(content, diff string) (string, error) {
	if diff == "" {
		return content, nil
	}

	// Write content to temp file
	tempContent, err := os.CreateTemp("", "polis-content-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tempContent.Name())

	if _, err := tempContent.WriteString(content); err != nil {
		tempContent.Close()
		return "", err
	}
	tempContent.Close()

	// Write diff to temp file
	tempDiff, err := os.CreateTemp("", "polis-diff-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tempDiff.Name())

	if _, err := tempDiff.WriteString(diff); err != nil {
		tempDiff.Close()
		return "", err
	}
	tempDiff.Close()

	// Apply reverse patch
	tempOutput, err := os.CreateTemp("", "polis-output-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tempOutput.Name())
	tempOutput.Close()

	cmd := exec.Command("patch", "-R", "-s", "-o", tempOutput.Name(), tempContent.Name(), tempDiff.Name())
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to apply reverse patch: %w", err)
	}

	result, err := os.ReadFile(tempOutput.Name())
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// applyForwardPatch applies a unified diff forward.
func applyForwardPatch(content, diff string) (string, error) {
	if diff == "" {
		return content, nil
	}

	// Write content to temp file
	tempContent, err := os.CreateTemp("", "polis-content-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tempContent.Name())

	if _, err := tempContent.WriteString(content); err != nil {
		tempContent.Close()
		return "", err
	}
	tempContent.Close()

	// Write diff to temp file
	tempDiff, err := os.CreateTemp("", "polis-diff-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tempDiff.Name())

	if _, err := tempDiff.WriteString(diff); err != nil {
		tempDiff.Close()
		return "", err
	}
	tempDiff.Close()

	// Apply forward patch
	tempOutput, err := os.CreateTemp("", "polis-output-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tempOutput.Name())
	tempOutput.Close()

	cmd := exec.Command("patch", "-s", "-o", tempOutput.Name(), tempContent.Name(), tempDiff.Name())
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to apply forward patch: %w", err)
	}

	result, err := os.ReadFile(tempOutput.Name())
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// extractBodyContent extracts content without frontmatter.
func extractBodyContent(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return content
	}

	inFrontmatter := true
	dashCount := 1
	var bodyLines []string

	for i := 1; i < len(lines); i++ {
		if inFrontmatter && strings.TrimSpace(lines[i]) == "---" {
			dashCount++
			if dashCount == 2 {
				inFrontmatter = false
				continue
			}
		}
		if !inFrontmatter {
			bodyLines = append(bodyLines, lines[i])
		}
	}

	return strings.Join(bodyLines, "\n")
}

// canonicalizeContent normalizes content for consistent hashing.
func canonicalizeContent(content string) string {
	content = strings.TrimLeft(content, "\n")
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n") + "\n"
}

// InitializeHistory writes the initial version history side-car at versionsPath,
// recording the base (parent=none) version as full content. canonicalPath is the
// site-relative path stored in the header; body is the content without frontmatter;
// hash is the sha256 hex (no "sha256:" prefix); timestamp is RFC3339. The parent
// directory (e.g. ".versions/") is created if needed.
//
// This is content-type agnostic: posts and comments both call it with the
// versions path derived via GetVersionsFilePath(canonicalFile, ".versions").
func InitializeHistory(versionsPath, canonicalPath, body, hash, timestamp string) error {
	if err := os.MkdirAll(filepath.Dir(versionsPath), 0755); err != nil {
		return err
	}

	content := fmt.Sprintf(`# VERSION_FILE_FORMAT=1.0
# CANONICAL_FILE=%s
# CURRENT_HASH=sha256:%s

[VERSION sha256:%s]
TIMESTAMP=%s
PARENT=none
FULL_CONTENT_START
%sFULL_CONTENT_END

`, canonicalPath, hash, hash, timestamp, body)

	return os.WriteFile(versionsPath, []byte(content), 0644)
}

// AppendHistory appends a new version entry — a unified diff of newBody against
// oldBody — to the side-car at versionsPath and advances the CURRENT_HASH header.
// previousHash and newHash are sha256 hex (no prefix). If the side-car does not
// exist yet, it is created with a header (but no base version); callers that need
// the prior content to be reconstructable should InitializeHistory with the old
// content first (see comment republish lazy-init).
func AppendHistory(versionsPath, canonicalPath, previousHash, newHash, timestamp, oldBody, newBody string) error {
	if err := os.MkdirAll(filepath.Dir(versionsPath), 0755); err != nil {
		return err
	}

	if _, statErr := os.Stat(versionsPath); errors.Is(statErr, os.ErrNotExist) {
		header := fmt.Sprintf(`# VERSION_FILE_FORMAT=1.0
# CANONICAL_FILE=%s
# CURRENT_HASH=sha256:%s

`, canonicalPath, newHash)
		if err := os.WriteFile(versionsPath, []byte(header), 0644); err != nil {
			return err
		}
	} else if statErr != nil {
		return statErr
	} else if err := updateCurrentHash(versionsPath, newHash); err != nil {
		return err
	}

	diffContent, err := computeUnifiedDiff(oldBody, newBody)
	if err != nil {
		return fmt.Errorf("failed to compute diff: %w", err)
	}

	entry := fmt.Sprintf(`[VERSION sha256:%s]
TIMESTAMP=%s
PARENT=sha256:%s
DIFF_START
%sDIFF_END

`, newHash, timestamp, previousHash, diffContent)

	f, err := os.OpenFile(versionsPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(entry)
	return err
}

// updateCurrentHash rewrites the CURRENT_HASH header line in a side-car file.
func updateCurrentHash(versionsPath, newHash string) error {
	content, err := os.ReadFile(versionsPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "# CURRENT_HASH=") {
			lines[i] = "# CURRENT_HASH=sha256:" + newHash
			break
		}
	}

	return os.WriteFile(versionsPath, []byte(strings.Join(lines, "\n")), 0644)
}

// computeUnifiedDiff computes a unified diff between old and new content via the
// system `diff -u`. Returns the diff output, or empty string if identical.
func computeUnifiedDiff(oldContent, newContent string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "polis-diff-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	os.Chmod(tmpDir, 0700)

	oldFile, err := os.CreateTemp(tmpDir, "old-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file for old content: %w", err)
	}
	defer oldFile.Close()

	newFile, err := os.CreateTemp(tmpDir, "new-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file for new content: %w", err)
	}
	defer newFile.Close()

	if _, err := oldFile.WriteString(oldContent); err != nil {
		return "", fmt.Errorf("failed to write old content: %w", err)
	}
	if err := oldFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close old content file: %w", err)
	}

	if _, err := newFile.WriteString(newContent); err != nil {
		return "", fmt.Errorf("failed to write new content: %w", err)
	}
	if err := newFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close new content file: %w", err)
	}

	cmd := exec.Command("diff", "-u", oldFile.Name(), newFile.Name())
	output, err := cmd.Output()

	// diff returns exit code 1 when files differ, which is expected.
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return string(output), nil
			}
		}
		return "", fmt.Errorf("diff command failed: %w", err)
	}

	return string(output), nil
}
