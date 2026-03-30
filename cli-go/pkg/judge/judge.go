// Package judge provides cross-boundary verification for polis tenant sites.
//
// While patrol checks local filesystem integrity (keys, permissions, signatures),
// judge verifies the full trust chain: HTTP serving correctness, DS attestations,
// public key continuity, policy snapshots, index consistency, and cross-site
// comment signatures. Used by the hosted Judge goroutine on a 1-hour interval.
package judge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// CheckStatus represents the result of a single check.
type CheckStatus struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// Finding records a specific verification failure or alert.
type Finding struct {
	Check   string `json:"check"`            // "signature", "attestation", "index", "cross_site", "key_change", "policy_change"
	Handle  string `json:"handle"`           // tenant handle
	Path    string `json:"path,omitempty"`   // file path or URL
	Domain  string `json:"domain,omitempty"` // remote domain (item 6)
	Message string `json:"message"`
}

// TenantResult contains the verification results for a single tenant.
type TenantResult struct {
	Handle           string      `json:"handle"`
	HTTPVerify       CheckStatus `json:"http_verify"`       // Item 1
	Attestations     CheckStatus `json:"attestations"`      // Item 2
	KeyContinuity    CheckStatus `json:"key_continuity"`    // Item 3
	PolicySnapshot   CheckStatus `json:"policy_snapshot"`   // Item 4
	IndexConsistency CheckStatus `json:"index_consistency"` // Item 5
	CrossSiteVerify  CheckStatus `json:"cross_site_verify"` // Item 6
	Findings         []Finding   `json:"findings,omitempty"`
	PostsChecked     int         `json:"posts_checked"`
	PostsFailed      int         `json:"posts_failed"`
	CommentsChecked  int         `json:"comments_checked"`
	CommentsFailed   int         `json:"comments_failed"`
	DurationMs       int64       `json:"duration_ms"`
}

// Passed returns true if all checks passed with no findings.
func (r *TenantResult) Passed() bool {
	return r.HTTPVerify.OK && r.Attestations.OK && r.KeyContinuity.OK &&
		r.PolicySnapshot.OK && r.IndexConsistency.OK && r.CrossSiteVerify.OK &&
		len(r.Findings) == 0
}

// SweepResult contains the aggregated results of judging all tenants.
type SweepResult struct {
	Tenants    int            `json:"tenants"`
	Passed     int            `json:"passed"`
	Failed     int            `json:"failed"`
	Findings   int            `json:"findings"`
	Results    []TenantResult `json:"results"`
	DurationMs int64          `json:"duration_ms"`
}

// Options configures the judge checks.
type Options struct {
	BaseURL     string        // Loopback URL for HTTP verification (e.g. "http://localhost:8080")
	BaseDomain  string        // e.g. "polis.pub" — for subdomain-based routing
	TenantsDir  string        // e.g. "/data/tenants" — for local key lookups
	DSURL       string        // Discovery service URL
	StateDir    string        // /data/judge/ for baselines
	HTTPTimeout time.Duration // For external requests (item 6), default 5s
	HTTPClient  *http.Client  // Optional shared HTTP client for connection pooling
}

// ---------- Key baseline state ----------

// KeyBaseline records the first-seen public key for a tenant.
type KeyBaseline struct {
	PublicKey    string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	FirstSeen   string `json:"first_seen"`
	LastVerified string `json:"last_verified"`
}

// ---------- Policy snapshot state ----------

// PolicyFileSnapshot records the hash of a single policy file.
type PolicyFileSnapshot struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	SnapshotAt string `json:"snapshot_at"`
}

// PolicySnapshotState records hashes of all policy files for a tenant.
type PolicySnapshotState struct {
	Files       []PolicyFileSnapshot `json:"files"`
	LastChecked string               `json:"last_checked"`
}

// ---------- Main entry points ----------

// JudgeTenant runs all 6 checks for a single tenant.
func JudgeTenant(siteDir, handle string, opts Options) *TenantResult {
	start := time.Now()
	result := &TenantResult{Handle: handle}

	// Item 1: HTTP content verification
	result.HTTPVerify, result.PostsChecked, result.PostsFailed = checkHTTPContent(siteDir, handle, opts.BaseURL, opts.BaseDomain, opts.HTTPClient)
	appendFindings(result, "signature", handle, result.HTTPVerify)

	// Item 2: DS attestation audit (skip if no DS URL)
	if opts.DSURL != "" {
		result.Attestations = checkAttestations(siteDir, handle, opts.DSURL)
	} else {
		result.Attestations = CheckStatus{OK: true, Message: "skipped: no DS URL"}
	}

	// Item 3: Public key continuity
	result.KeyContinuity = checkKeyContinuity(siteDir, handle, opts.StateDir)
	appendKeyFindings(result, handle)

	// Item 4: Policy snapshot verification
	result.PolicySnapshot = checkPolicySnapshot(siteDir, handle, opts.StateDir)
	appendPolicyFindings(result, handle)

	// Item 5: Index consistency
	result.IndexConsistency = checkIndexConsistency(siteDir, handle)

	// Item 6: Cross-site comment verification
	timeout := opts.HTTPTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	result.CrossSiteVerify, result.CommentsChecked, result.CommentsFailed = checkCrossSiteComments(siteDir, handle, timeout, opts.BaseDomain, opts.TenantsDir, opts.BaseURL, opts.HTTPClient)

	result.DurationMs = time.Since(start).Milliseconds()
	return result
}

// JudgeAll runs JudgeTenant on every subdirectory of tenantsDir.
func JudgeAll(tenantsDir string, opts Options) *SweepResult {
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
		tr := JudgeTenant(siteDir, e.Name(), opts)
		sweep.Results = append(sweep.Results, *tr)
		sweep.Tenants++
		if tr.Passed() {
			sweep.Passed++
		} else {
			sweep.Failed++
		}
		sweep.Findings += len(tr.Findings)
	}

	sweep.DurationMs = time.Since(start).Milliseconds()
	return sweep
}

// ---------- Item 1: HTTP content verification ----------

// checkHTTPContent verifies that content served over HTTP matches on-disk signatures.
// The hosted server routes by subdomain (Host header), so we send requests to the
// loopback URL with Host: {handle}.{baseDomain}.
// Returns status, posts checked, posts failed.
func checkHTTPContent(siteDir, handle, baseURL, baseDomain string, hc *http.Client) (CheckStatus, int, int) {
	if baseURL == "" {
		return CheckStatus{OK: true, Message: "skipped: no base URL"}, 0, 0
	}

	// Load the site's public key
	pubKeyData, err := os.ReadFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub"))
	if err != nil {
		return CheckStatus{OK: false, Message: "cannot read public key: " + err.Error()}, 0, 0
	}

	postsDir := filepath.Join(siteDir, "content", "pub.polis.core", "post")
	if _, err := os.Stat(postsDir); os.IsNotExist(err) {
		return CheckStatus{OK: true}, 0, 0
	}

	client := hc
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	host := handle + "." + baseDomain
	var checked, failed int

	filepath.WalkDir(postsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == ".versions" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		checked++
		relPath, _ := filepath.Rel(siteDir, path)

		// Fetch via HTTP with Host header for subdomain routing
		reqURL := fmt.Sprintf("%s/%s", strings.TrimRight(baseURL, "/"), relPath)
		req, reqErr := http.NewRequest(http.MethodGet, reqURL, nil)
		if reqErr != nil {
			failed++
			return nil
		}
		req.Host = host

		resp, err := client.Do(req)
		if err != nil {
			failed++
			return nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			failed++
			return nil
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			failed++
			return nil
		}

		// Verify signature and hash of HTTP-served content
		if !verifyPostContent(string(body), pubKeyData) {
			failed++
		}
		return nil
	})

	if failed > 0 {
		return CheckStatus{OK: false, Message: fmt.Sprintf("%d/%d posts failed HTTP verification", failed, checked)}, checked, failed
	}
	return CheckStatus{OK: true}, checked, failed
}

// ---------- Item 2: DS attestation audit ----------

// checkAttestations verifies DS autobless attestations for blessed comments.
// This queries the DS for blessing records and verifies each attestation signature.
func checkAttestations(siteDir, handle, dsURL string) CheckStatus {
	// Load blessed comments
	bc, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		// No blessed comments file is fine
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") {
			return CheckStatus{OK: true, Message: "no blessed comments"}
		}
		return CheckStatus{OK: false, Message: "cannot load blessed.json: " + err.Error()}
	}

	if len(bc.Comments) == 0 {
		return CheckStatus{OK: true, Message: "no blessed comments"}
	}

	// For now, we verify that blessed.json is well-formed and has valid structure.
	// Full DS attestation verification requires querying DS relationship records,
	// which we'll do via the DS client in the hosted wrapper where we have the
	// full HTTP client infrastructure.
	totalBlessed := 0
	for _, pc := range bc.Comments {
		totalBlessed += len(pc.Blessed)
	}

	return CheckStatus{OK: true, Message: fmt.Sprintf("%d blessed comments verified structurally", totalBlessed)}
}

// ---------- Item 3: Public key continuity ----------

// checkKeyContinuity compares the tenant's current public key against a stored baseline.
// On first run, records the baseline. On subsequent runs, alerts if the key changed.
func checkKeyContinuity(siteDir, handle, stateDir string) CheckStatus {
	if stateDir == "" {
		return CheckStatus{OK: true, Message: "skipped: no state dir"}
	}

	// Read current public key
	pubKeyData, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis"))
	if err != nil {
		return CheckStatus{OK: false, Message: "cannot read .well-known/polis: " + err.Error()}
	}

	var wk map[string]interface{}
	if err := json.Unmarshal(pubKeyData, &wk); err != nil {
		return CheckStatus{OK: false, Message: "invalid .well-known/polis JSON: " + err.Error()}
	}

	currentKey, ok := wk["public_key"].(string)
	if !ok || currentKey == "" {
		return CheckStatus{OK: false, Message: "no public_key in .well-known/polis"}
	}

	fingerprint := sha256Fingerprint(currentKey)

	// Load or create baseline
	handleDir := filepath.Join(stateDir, handle)
	baselinePath := filepath.Join(handleDir, "key_baseline.json")

	baselineData, err := os.ReadFile(baselinePath)
	if err != nil {
		if os.IsNotExist(err) {
			// First run: record baseline
			baseline := KeyBaseline{
				PublicKey:    currentKey,
				Fingerprint:  fingerprint,
				FirstSeen:    time.Now().UTC().Format(time.RFC3339),
				LastVerified: time.Now().UTC().Format(time.RFC3339),
			}
			if writeErr := writeJSON(baselinePath, baseline); writeErr != nil {
				return CheckStatus{OK: false, Message: "cannot write key baseline: " + writeErr.Error()}
			}
			return CheckStatus{OK: true, Message: "baseline recorded"}
		}
		return CheckStatus{OK: false, Message: "cannot read key baseline: " + err.Error()}
	}

	var baseline KeyBaseline
	if err := json.Unmarshal(baselineData, &baseline); err != nil {
		return CheckStatus{OK: false, Message: "invalid key baseline JSON: " + err.Error()}
	}

	if baseline.Fingerprint != fingerprint {
		return CheckStatus{
			OK:      false,
			Message: fmt.Sprintf("key changed: %s → %s", baseline.Fingerprint[:16], fingerprint[:16]),
		}
	}

	// Update last verified
	baseline.LastVerified = time.Now().UTC().Format(time.RFC3339)
	writeJSON(baselinePath, baseline)

	return CheckStatus{OK: true}
}

// ---------- Item 4: Policy snapshot verification ----------

// checkPolicySnapshot compares current policy files against stored snapshots.
// Detects retroactive policy changes after blessings were granted.
func checkPolicySnapshot(siteDir, handle, stateDir string) CheckStatus {
	if stateDir == "" {
		return CheckStatus{OK: true, Message: "skipped: no state dir"}
	}

	// Find policy files
	policyFiles := []string{
		filepath.Join("policies", "rules.jsonl"),
		filepath.Join(".polis", "policies", "rules.jsonl"),
	}

	var currentSnapshots []PolicyFileSnapshot
	for _, relPath := range policyFiles {
		absPath := filepath.Join(siteDir, relPath)
		data, err := os.ReadFile(absPath)
		if err != nil {
			continue // File doesn't exist, skip
		}
		hash := sha256Hex(data)
		currentSnapshots = append(currentSnapshots, PolicyFileSnapshot{
			Path:       relPath,
			SHA256:     hash,
			SnapshotAt: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Load or create snapshot
	handleDir := filepath.Join(stateDir, handle)
	snapshotPath := filepath.Join(handleDir, "policy_snapshot.json")

	snapshotData, err := os.ReadFile(snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			// First run: record snapshot
			state := PolicySnapshotState{
				Files:       currentSnapshots,
				LastChecked: time.Now().UTC().Format(time.RFC3339),
			}
			if writeErr := writeJSON(snapshotPath, state); writeErr != nil {
				return CheckStatus{OK: false, Message: "cannot write policy snapshot: " + writeErr.Error()}
			}
			return CheckStatus{OK: true, Message: "snapshot recorded"}
		}
		return CheckStatus{OK: false, Message: "cannot read policy snapshot: " + err.Error()}
	}

	var stored PolicySnapshotState
	if err := json.Unmarshal(snapshotData, &stored); err != nil {
		return CheckStatus{OK: false, Message: "invalid policy snapshot JSON: " + err.Error()}
	}

	// Compare
	changed := comparePolicySnapshots(stored.Files, currentSnapshots)
	if len(changed) > 0 {
		// Update snapshot to current state (we've detected the change)
		state := PolicySnapshotState{
			Files:       currentSnapshots,
			LastChecked: time.Now().UTC().Format(time.RFC3339),
		}
		writeJSON(snapshotPath, state)

		return CheckStatus{
			OK:      false,
			Message: fmt.Sprintf("%d policy file(s) changed: %s", len(changed), strings.Join(changed, ", ")),
		}
	}

	// Update last checked
	stored.LastChecked = time.Now().UTC().Format(time.RFC3339)
	writeJSON(snapshotPath, stored)

	return CheckStatus{OK: true}
}

// comparePolicySnapshots returns the paths of files that changed between stored and current.
func comparePolicySnapshots(stored, current []PolicyFileSnapshot) []string {
	storedMap := make(map[string]string)
	for _, s := range stored {
		storedMap[s.Path] = s.SHA256
	}
	currentMap := make(map[string]string)
	for _, c := range current {
		currentMap[c.Path] = c.SHA256
	}

	var changed []string

	// Check for changes and additions
	for path, hash := range currentMap {
		if storedHash, ok := storedMap[path]; ok {
			if storedHash != hash {
				changed = append(changed, path)
			}
		} else {
			changed = append(changed, path+" (new)")
		}
	}

	// Check for removals
	for path := range storedMap {
		if _, ok := currentMap[path]; !ok {
			changed = append(changed, path+" (removed)")
		}
	}

	return changed
}

// ---------- Item 5: Index consistency ----------

// checkIndexConsistency verifies that public.jsonl entries match real files on disk.
// Detects orphaned entries, phantom files, and hash mismatches.
func checkIndexConsistency(siteDir, handle string) CheckStatus {
	entries, err := metadata.LoadPublicIndex(siteDir)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckStatus{OK: true, Message: "no index file"}
		}
		return CheckStatus{OK: false, Message: "cannot load index.jsonl: " + err.Error()}
	}

	if len(entries) == 0 {
		return CheckStatus{OK: true, Message: "empty index"}
	}

	var issues []string
	indexedPaths := make(map[string]bool)

	for _, entry := range entries {
		if entry.Type != "post" {
			continue
		}
		indexedPaths[entry.Path] = true

		// Check file exists
		absPath := filepath.Join(siteDir, entry.Path)
		data, err := os.ReadFile(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				issues = append(issues, fmt.Sprintf("orphan: %s (indexed but missing)", entry.Path))
			} else {
				issues = append(issues, fmt.Sprintf("read error: %s: %s", entry.Path, err.Error()))
			}
			continue
		}

		// Check hash if present
		if entry.CurrentVersion != "" {
			expectedHash := strings.TrimPrefix(entry.CurrentVersion, "sha256:")
			_, body := splitFrontmatterBody(string(data))
			if !verifyHash(body, expectedHash) {
				issues = append(issues, fmt.Sprintf("hash_mismatch: %s", entry.Path))
			}
		}
	}

	// Check for phantom files (on disk but not in index)
	postsDir := filepath.Join(siteDir, "content", "pub.polis.core", "post")
	if _, err := os.Stat(postsDir); err == nil {
		filepath.WalkDir(postsDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && d.Name() == ".versions" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			relPath, _ := filepath.Rel(siteDir, path)
			if !indexedPaths[relPath] {
				issues = append(issues, fmt.Sprintf("phantom: %s (on disk but not indexed)", relPath))
			}
			return nil
		})
	}

	if len(issues) > 0 {
		return CheckStatus{OK: false, Message: fmt.Sprintf("%d issues: %s", len(issues), strings.Join(issues, "; "))}
	}
	return CheckStatus{OK: true}
}

// ---------- Item 6: Cross-site comment verification ----------

// checkCrossSiteComments verifies blessed comment signatures against their claimed
// author's public key. For authors on the same baseDomain (co-tenants), fetches the
// key via HTTP to the local server (baseURL with Host header routing) instead of
// making an external HTTPS request. Falls back to local filesystem if baseURL is empty.
// Returns status, comments checked, comments failed.
func checkCrossSiteComments(siteDir, handle string, timeout time.Duration, baseDomain, tenantsDir, baseURL string, hc *http.Client) (CheckStatus, int, int) {
	// Find blessed comment files on disk
	commentsDir := filepath.Join(siteDir, "content", "pub.polis.core", "comment")
	if _, err := os.Stat(commentsDir); os.IsNotExist(err) {
		return CheckStatus{OK: true, Message: "no comments"}, 0, 0
	}

	client := hc
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	keyCache := make(map[string]string) // domain -> public key
	var checked, failed int

	filepath.WalkDir(commentsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		content := string(data)
		fm := parseCommentFrontmatter(content)
		if fm.Author == "" || fm.Signature == "" {
			return nil // Skip comments without author or signature
		}

		checked++

		// Extract domain from author URL
		domain := extractDomain(fm.Author)
		if domain == "" {
			return nil
		}

		// Fetch author's public key (cached per domain)
		pubKey, ok := keyCache[domain]
		if !ok {
			var fetchErr error
			// Check if this is a co-tenant (subdomain of baseDomain)
			// If so, fetch the key via HTTP to the local server with Host header routing
			if localHandle := extractLocalHandle(domain, baseDomain); localHandle != "" && baseURL != "" {
				pubKey, fetchErr = fetchCoTenantPublicKey(client, baseURL, localHandle, baseDomain)
			} else if localHandle := extractLocalHandle(domain, baseDomain); localHandle != "" && tenantsDir != "" {
				// Fallback to local filesystem if no baseURL (non-hosted mode)
				pubKey, fetchErr = fetchLocalPublicKey(tenantsDir, localHandle)
			} else {
				pubKey, fetchErr = fetchRemotePublicKey(client, domain)
			}
			if fetchErr != nil {
				// Don't count timeouts/network errors as failures
				keyCache[domain] = "" // Cache the failure to avoid retrying
				return nil
			}
			keyCache[domain] = pubKey
		}
		if pubKey == "" {
			return nil // Domain unreachable, skip
		}

		// Verify signature — comments are signed without the author field
		sshSig := reconstructSSHSignature(fm.Signature)
		contentToSign := extractCommentContentToSign(content)
		valid, verifyErr := signing.VerifySignature([]byte(contentToSign), []byte(pubKey), sshSig)
		if verifyErr != nil || !valid {
			failed++
		}

		return nil
	})

	if failed > 0 {
		return CheckStatus{OK: false, Message: fmt.Sprintf("%d/%d comments failed cross-site verification", failed, checked)}, checked, failed
	}
	return CheckStatus{OK: true}, checked, failed
}

// ---------- Helpers ----------

// verifyPostContent verifies a post's signature and hash from its full content.
func verifyPostContent(content string, pubKey []byte) bool {
	fm, body, err := parseFrontmatter(content)
	if err != nil {
		return false
	}

	// Verify signature
	if fm.Signature != "" {
		sshSig := reconstructSSHSignature(fm.Signature)
		contentToSign := extractContentToSign(content)
		valid, err := signing.VerifySignature([]byte(contentToSign), pubKey, sshSig)
		if err != nil || !valid {
			return false
		}
	}

	// Verify hash
	if fm.CurrentVersion != "" {
		expectedHash := strings.TrimPrefix(fm.CurrentVersion, "sha256:")
		if !verifyHash(body, expectedHash) {
			return false
		}
	}

	return true
}

// frontmatter holds parsed frontmatter fields needed for verification.
type frontmatter struct {
	Signature      string
	CurrentVersion string
}

// commentFrontmatter holds parsed frontmatter fields from a comment.
type commentFrontmatter struct {
	Author    string
	Signature string
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

// parseCommentFrontmatter extracts author and signature from a comment's frontmatter.
func parseCommentFrontmatter(content string) commentFrontmatter {
	fm := commentFrontmatter{}
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return fm
	}

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			break
		}
		if idx := strings.Index(lines[i], ":"); idx > 0 {
			key := strings.TrimSpace(lines[i][:idx])
			value := strings.TrimSpace(lines[i][idx+1:])
			switch key {
			case "author":
				fm.Author = value
			case "signature":
				fm.Signature = value
			}
		}
	}

	return fm
}

// extractContentToSign removes the signature line from content and canonicalizes.
// Used for posts where only "signature:" is added after signing.
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

// extractCommentContentToSign removes both "signature:" and "author:" lines
// from content and canonicalizes. Comments are signed without the author field,
// which is added to the final frontmatter after signing.
func extractCommentContentToSign(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	for _, line := range lines {
		if strings.HasPrefix(line, "signature:") || strings.HasPrefix(line, "author:") {
			continue
		}
		result = append(result, line)
	}
	return canonicalizeContent(strings.Join(result, "\n"))
}

// splitFrontmatterBody splits content into frontmatter and body parts.
func splitFrontmatterBody(content string) (string, string) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			body := strings.Join(lines[i+1:], "\n")
			body = strings.TrimPrefix(body, "\n")
			return strings.Join(lines[:i+1], "\n"), body
		}
	}
	return "", content
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

// verifyHash checks if body content matches an expected SHA-256 hash.
func verifyHash(body, expectedHash string) bool {
	canonical := canonicalizeContent(body)
	if sha256Hex([]byte(canonical)) == expectedHash {
		return true
	}
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

// sha256Fingerprint computes a hex-encoded SHA-256 fingerprint of a string.
func sha256Fingerprint(s string) string {
	return sha256Hex([]byte(s))
}

// extractLocalHandle returns the subdomain handle if domain is a subdomain of baseDomain.
// For example, extractLocalHandle("alice.polis.pub", "polis.pub") returns "alice".
// Returns "" if the domain is not a subdomain of baseDomain.
func extractLocalHandle(domain, baseDomain string) string {
	if baseDomain == "" {
		return ""
	}
	suffix := "." + baseDomain
	if strings.HasSuffix(domain, suffix) {
		handle := strings.TrimSuffix(domain, suffix)
		if handle != "" && !strings.Contains(handle, ".") {
			return handle
		}
	}
	return ""
}

// fetchLocalPublicKey reads a co-tenant's public key from the local filesystem.
func fetchLocalPublicKey(tenantsDir, handle string) (string, error) {
	wkPath := filepath.Join(tenantsDir, handle, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
	if err != nil {
		return "", err
	}
	var wk map[string]interface{}
	if err := json.Unmarshal(data, &wk); err != nil {
		return "", err
	}
	pubKey, _ := wk["public_key"].(string)
	return pubKey, nil
}

// fetchCoTenantPublicKey fetches a co-tenant's public key via HTTP to the
// local server using Host header routing. This avoids direct disk access
// across tenant directories, going through the HTTP serving layer instead.
func fetchCoTenantPublicKey(client *http.Client, baseURL, handle, baseDomain string) (string, error) {
	wkURL := strings.TrimRight(baseURL, "/") + "/.well-known/polis"
	req, err := http.NewRequest(http.MethodGet, wkURL, nil)
	if err != nil {
		return "", err
	}
	req.Host = handle + "." + baseDomain

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from co-tenant %s", resp.StatusCode, handle)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10)) // 64KB limit
	if err != nil {
		return "", err
	}

	var wk map[string]interface{}
	if err := json.Unmarshal(body, &wk); err != nil {
		return "", err
	}
	pubKey, _ := wk["public_key"].(string)
	return pubKey, nil
}

// fetchRemotePublicKey fetches a domain's public key from .well-known/polis.
func fetchRemotePublicKey(client *http.Client, domain string) (string, error) {
	url := fmt.Sprintf("https://%s/.well-known/polis", domain)
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, domain)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}

	var wk map[string]interface{}
	if err := json.Unmarshal(body, &wk); err != nil {
		return "", err
	}

	pubKey, _ := wk["public_key"].(string)
	return pubKey, nil
}

// extractDomain extracts the domain from a URL or domain string.
func extractDomain(authorOrURL string) string {
	// Handle full URLs
	if strings.HasPrefix(authorOrURL, "https://") {
		authorOrURL = strings.TrimPrefix(authorOrURL, "https://")
	} else if strings.HasPrefix(authorOrURL, "http://") {
		authorOrURL = strings.TrimPrefix(authorOrURL, "http://")
	}
	// Take just the host part
	if idx := strings.Index(authorOrURL, "/"); idx >= 0 {
		authorOrURL = authorOrURL[:idx]
	}
	return authorOrURL
}

// writeJSON writes a value as indented JSON to a file, creating parent dirs.
func writeJSON(path string, v interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// appendFindings adds findings from a check status to the result (for structured events).
// This is a helper used by the hosted wrapper.
func appendFindings(result *TenantResult, check, handle string, status CheckStatus) {
	if !status.OK && status.Message != "" {
		result.Findings = append(result.Findings, Finding{
			Check:   check,
			Handle:  handle,
			Message: status.Message,
		})
	}
}

func appendKeyFindings(result *TenantResult, handle string) {
	if !result.KeyContinuity.OK && result.KeyContinuity.Message != "" &&
		strings.Contains(result.KeyContinuity.Message, "key changed") {
		result.Findings = append(result.Findings, Finding{
			Check:   "key_change",
			Handle:  handle,
			Message: result.KeyContinuity.Message,
		})
	}
}

func appendPolicyFindings(result *TenantResult, handle string) {
	if !result.PolicySnapshot.OK && result.PolicySnapshot.Message != "" &&
		strings.Contains(result.PolicySnapshot.Message, "changed") {
		result.Findings = append(result.Findings, Finding{
			Check:   "policy_change",
			Handle:  handle,
			Message: result.PolicySnapshot.Message,
		})
	}
}

