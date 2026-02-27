// Package patrol provides tenant data integrity verification for polis sites.
//
// It checks key validity, signature verification, hash integrity, and private
// key leak detection. Used by the hosted Patrol goroutine and the standalone
// patrol binary for backup verification.
package patrol

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// CheckStatus represents the result of a single check.
type CheckStatus struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// PostError records a verification failure for a specific post.
type PostError struct {
	Path    string `json:"path"`
	Type    string `json:"type"` // "signature" or "hash"
	Message string `json:"message"`
}

// CheckResult contains the full verification results for a single tenant.
type CheckResult struct {
	Handle        string      `json:"handle"`
	WellKnown     CheckStatus `json:"well_known"`
	PublicKey     CheckStatus `json:"public_key"`
	PrivateKey    CheckStatus `json:"private_key"`
	KeyMatch      CheckStatus `json:"key_match"`
	KeyLeak       CheckStatus `json:"key_leak"`
	PostCount     int         `json:"post_count"`
	PostsVerified int         `json:"posts_verified"`
	PostsFailed   int         `json:"posts_failed"`
	PostErrors    []PostError `json:"post_errors,omitempty"`
	DurationMs    int64       `json:"duration_ms"`
}

// Passed returns true if all checks passed and no posts failed verification.
func (r *CheckResult) Passed() bool {
	return r.WellKnown.OK && r.PublicKey.OK && r.PrivateKey.OK &&
		r.KeyMatch.OK && r.KeyLeak.OK && r.PostsFailed == 0
}

// SweepResult contains the aggregated results of checking all tenants.
type SweepResult struct {
	Tenants    int            `json:"tenants"`
	Passed     int            `json:"passed"`
	Failed     int            `json:"failed"`
	Errors     int            `json:"errors"`
	Results    []CheckResult  `json:"results"`
	DurationMs int64          `json:"duration_ms"`
}

// CheckTenant runs all integrity checks on a single tenant site directory.
func CheckTenant(siteDir string) *CheckResult {
	start := time.Now()
	handle := filepath.Base(siteDir)
	result := &CheckResult{Handle: handle}

	// 1. Load and validate .well-known/polis
	wk, err := site.LoadWellKnown(siteDir)
	if err != nil {
		result.WellKnown = CheckStatus{OK: false, Message: fmt.Sprintf("failed to load: %v", err)}
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	if wk.PublicKey == "" {
		result.WellKnown = CheckStatus{OK: false, Message: "missing public_key field"}
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	result.WellKnown = CheckStatus{OK: true}

	// 2. Read and validate public key file
	pubKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub")
	pubKeyData, err := os.ReadFile(pubKeyPath)
	if err != nil {
		result.PublicKey = CheckStatus{OK: false, Message: fmt.Sprintf("failed to read: %v", err)}
	} else {
		result.PublicKey = CheckStatus{OK: true}
	}

	// 3. Read and validate private key file
	privKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519")
	privKeyData, err := os.ReadFile(privKeyPath)
	if err != nil {
		result.PrivateKey = CheckStatus{OK: false, Message: fmt.Sprintf("failed to read: %v", err)}
	} else if err := signing.ValidatePrivateKey(privKeyData); err != nil {
		result.PrivateKey = CheckStatus{OK: false, Message: fmt.Sprintf("invalid key: %v", err)}
	} else {
		result.PrivateKey = CheckStatus{OK: true}
	}

	// 4. Key match: compare pub key file content to .well-known/polis public_key
	if result.PublicKey.OK {
		pubKeyStr := strings.TrimSpace(string(pubKeyData))
		wkPubKey := strings.TrimSpace(wk.PublicKey)
		if pubKeyStr == wkPubKey {
			result.KeyMatch = CheckStatus{OK: true}
		} else {
			result.KeyMatch = CheckStatus{OK: false, Message: "public key file does not match .well-known/polis"}
		}
	} else {
		result.KeyMatch = CheckStatus{OK: false, Message: "skipped: public key file not available"}
	}

	// 5. Private key leak scan
	leakFile := scanForKeyLeak(siteDir)
	if leakFile != "" {
		result.KeyLeak = CheckStatus{OK: false, Message: fmt.Sprintf("private key header found in: %s", leakFile)}
	} else {
		result.KeyLeak = CheckStatus{OK: true}
	}

	// 6. Verify all posts
	if result.PublicKey.OK {
		verifyPosts(siteDir, pubKeyData, result)
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result
}

// CheckAllTenants runs CheckTenant on every subdirectory of tenantsDir.
func CheckAllTenants(tenantsDir string) *SweepResult {
	start := time.Now()
	sweep := &SweepResult{}

	entries, err := os.ReadDir(tenantsDir)
	if err != nil {
		sweep.DurationMs = time.Since(start).Milliseconds()
		return sweep
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		siteDir := filepath.Join(tenantsDir, e.Name())
		cr := CheckTenant(siteDir)
		sweep.Results = append(sweep.Results, *cr)
		sweep.Tenants++
		if cr.Passed() {
			sweep.Passed++
		} else {
			sweep.Failed++
		}
		sweep.Errors += cr.PostsFailed
	}

	sweep.DurationMs = time.Since(start).Milliseconds()
	return sweep
}

// scanForKeyLeak scans public-facing directories for private key PEM headers.
// Returns the first file path containing the header, or empty string if clean.
func scanForKeyLeak(siteDir string) string {
	header := "-----BEGIN OPENSSH PRIVATE KEY-----"

	// Public-facing directories to scan
	scanDirs := []string{
		"posts", "comments", "snippets", ".well-known", "metadata",
	}

	for _, dir := range scanDirs {
		dirPath := filepath.Join(siteDir, dir)
		if found := scanDirForString(dirPath, header); found != "" {
			rel, _ := filepath.Rel(siteDir, found)
			return rel
		}
	}

	// Also scan root-level .md and .html files
	entries, err := os.ReadDir(siteDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".html") {
			filePath := filepath.Join(siteDir, name)
			if fileContainsString(filePath, header) {
				return name
			}
		}
	}

	return ""
}

// scanDirForString recursively scans a directory for files containing a string.
func scanDirForString(dir, needle string) string {
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fileContainsString(path, needle) {
			return filepath.SkipAll
		}
		return nil
	})

	// Walk again to actually return the path (Walk doesn't support returning values)
	var found string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil
		}
		if fileContainsString(path, needle) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// fileContainsString checks if a file contains a given string using line-by-line scanning.
func fileContainsString(path, needle string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), needle) {
			return true
		}
	}
	return false
}

// verifyPosts walks the posts/ directory and verifies signatures and hashes.
func verifyPosts(siteDir string, pubKey []byte, result *CheckResult) {
	postsDir := filepath.Join(siteDir, "posts")
	if _, err := os.Stat(postsDir); os.IsNotExist(err) {
		return
	}

	filepath.WalkDir(postsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		// Skip .versions directories
		if d.IsDir() && d.Name() == ".versions" {
			return filepath.SkipDir
		}

		// Only process .md files
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		result.PostCount++

		content, err := os.ReadFile(path)
		if err != nil {
			result.PostsFailed++
			relPath, _ := filepath.Rel(siteDir, path)
			result.PostErrors = append(result.PostErrors, PostError{
				Path:    relPath,
				Type:    "read",
				Message: err.Error(),
			})
			return nil
		}

		relPath, _ := filepath.Rel(siteDir, path)
		verifyPost(string(content), relPath, pubKey, result)
		return nil
	})
}

// verifyPost verifies the signature and hash of a single post.
func verifyPost(content, relPath string, pubKey []byte, result *CheckResult) {
	fm, body, err := parseFrontmatter(content)
	if err != nil {
		result.PostsFailed++
		result.PostErrors = append(result.PostErrors, PostError{
			Path:    relPath,
			Type:    "parse",
			Message: err.Error(),
		})
		return
	}

	failed := false

	// Verify signature
	if fm.Signature != "" {
		sshSig := reconstructSSHSignature(fm.Signature)
		contentToSign := extractContentToSign(content)
		valid, err := signing.VerifySignature([]byte(contentToSign), pubKey, sshSig)
		if err != nil || !valid {
			failed = true
			msg := "signature does not match"
			if err != nil {
				msg = err.Error()
			}
			result.PostErrors = append(result.PostErrors, PostError{
				Path:    relPath,
				Type:    "signature",
				Message: msg,
			})
		}
	}

	// Verify hash
	if fm.CurrentVersion != "" {
		expectedHash := strings.TrimPrefix(fm.CurrentVersion, "sha256:")
		if !verifyHash(body, expectedHash) {
			failed = true
			result.PostErrors = append(result.PostErrors, PostError{
				Path:    relPath,
				Type:    "hash",
				Message: "content hash does not match current-version",
			})
		}
	}

	if failed {
		result.PostsFailed++
	} else {
		result.PostsVerified++
	}
}

// frontmatter holds parsed frontmatter fields needed for verification.
type frontmatter struct {
	Signature      string
	CurrentVersion string
}

// parseFrontmatter extracts signature and current-version from YAML frontmatter.
func parseFrontmatter(content string) (*frontmatter, string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return nil, "", fmt.Errorf("no frontmatter found")
	}

	fm := &frontmatter{}
	var bodyStart int

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			bodyStart = i + 1
			break
		}
		if idx := strings.Index(lines[i], ":"); idx > 0 {
			key := strings.TrimSpace(lines[i][:idx])
			value := strings.TrimSpace(lines[i][idx+1:])
			switch key {
			case "signature":
				fm.Signature = value
			case "current-version":
				fm.CurrentVersion = value
			}
		}
	}

	var body string
	if bodyStart < len(lines) {
		body = strings.Join(lines[bodyStart:], "\n")
		body = strings.TrimPrefix(body, "\n")
	}

	return fm, body, nil
}

// extractContentToSign removes the signature line from the content and canonicalizes.
// This reconstructs what publish.go signed: the full file (frontmatter + body) without
// the signature field, canonicalized.
func extractContentToSign(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	for _, line := range lines {
		if strings.HasPrefix(line, "signature:") {
			continue
		}
		result = append(result, line)
	}
	return canonicalizeContent(strings.Join(result, "\n"))
}

// reconstructSSHSignature wraps a base64 signature body in SSH signature PEM headers.
func reconstructSSHSignature(base64Body string) string {
	return "-----BEGIN SSH SIGNATURE-----\n" + wrapBase64(base64Body, 76) + "\n-----END SSH SIGNATURE-----"
}

// wrapBase64 breaks a base64 string into lines of the specified width.
func wrapBase64(s string, width int) string {
	var lines []string
	for len(s) > width {
		lines = append(lines, s[:width])
		s = s[width:]
	}
	if len(s) > 0 {
		lines = append(lines, s)
	}
	return strings.Join(lines, "\n")
}

// verifyHash checks if the body content matches an expected SHA-256 hash.
// Tries both canonicalized and direct hashing for backwards compatibility.
func verifyHash(body, expectedHash string) bool {
	// Try canonicalized
	canonical := canonicalizeContent(body)
	if sha256Hex([]byte(canonical)) == expectedHash {
		return true
	}
	// Try direct
	if sha256Hex([]byte(body)) == expectedHash {
		return true
	}
	return false
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

// sha256Hex computes the hex-encoded SHA-256 hash of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
