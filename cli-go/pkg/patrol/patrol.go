// Package patrol provides tenant data integrity verification for polis sites.
//
// It checks key validity, signature verification, hash integrity, private key
// leak detection, directory structure, metadata file validation, file
// permissions, and suspicious file scanning. Used by the hosted Patrol
// goroutine and the standalone patrol binary for backup verification.
package patrol

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
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

// SuspiciousFile records a file that shouldn't exist in a tenant directory.
type SuspiciousFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// PolicyWarning records a syntax error in a policy rule.
type PolicyWarning struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Rule  string `json:"rule"`
	Error string `json:"error"`
}

// MtimeAlert records a modification timestamp change for a tracked file.
type MtimeAlert struct {
	File     string `json:"file"`
	Severity string `json:"severity"` // "HIGH", "MEDIUM", "LOW"
	PrevMtime string `json:"prev_mtime"`
	CurrMtime string `json:"curr_mtime"`
}

// FileCreationAlert records an unexpected new file in a monitored directory.
type FileCreationAlert struct {
	Dir      string `json:"dir"`
	File     string `json:"file"`
	Severity string `json:"severity"` // "HIGH" or "MEDIUM"
}

// CheckOptions configures optional checks that require external context.
type CheckOptions struct {
	SnapshotDir string // "/data/patrol" — empty = skip snapshot checks
	BaseDomain  string // "polis.pub" — empty = skip domain validation
}

// CheckResult contains the full verification results for a single tenant.
type CheckResult struct {
	Handle string `json:"handle"`

	// Key and identity checks
	WellKnown  CheckStatus `json:"well_known"`
	PublicKey  CheckStatus `json:"public_key"`
	PrivateKey CheckStatus `json:"private_key"`
	KeyMatch   CheckStatus `json:"key_match"`
	KeyPerms   CheckStatus `json:"key_perms"`
	KeyLeak    CheckStatus `json:"key_leak"`

	// Structure and metadata checks
	Structure     CheckStatus `json:"structure"`
	DirExposure   CheckStatus `json:"dir_exposure"`
	BundleJSON    CheckStatus `json:"bundle_json"`
	IndexJSONL    CheckStatus `json:"index_jsonl"`
	BlessedJSON   CheckStatus `json:"blessed_json"`
	FollowingJSON CheckStatus `json:"following_json"`

	// Provisioning checks (missing expected files for tenant upgrades)
	PublicPolicies  CheckStatus `json:"public_policies"`
	PrivatePolicies CheckStatus `json:"private_policies"`
	DMPolicies      CheckStatus `json:"dm_policies"`
	StorageSalt     CheckStatus `json:"storage_salt"`
	DMDirectories   CheckStatus `json:"dm_directories"`
	BundleTypes       CheckStatus `json:"bundle_types"`
	DMDomainCase          CheckStatus `json:"dm_domain_case"`
	DMPreviewEncryption   CheckStatus `json:"dm_preview_encryption"`
	PolicyDenyAll     CheckStatus `json:"policy_deny_all"`
	PolicyFeedSelfOmit CheckStatus `json:"policy_feed_self_omit"`
	TagDirectory       CheckStatus `json:"tag_directory"`
	BaseTheme          CheckStatus `json:"base_theme"`
	ThemeStaleFiles    CheckStatus `json:"theme_stale_files"`
	Avatar             CheckStatus `json:"avatar"`
	WebappViewMode     CheckStatus `json:"webapp_view_mode"`

	// Post verification
	PostCount     int         `json:"post_count"`
	PostsVerified int         `json:"posts_verified"`
	PostsFailed   int         `json:"posts_failed"`
	PostErrors    []PostError `json:"post_errors,omitempty"`

	// Security scan (warnings — do not affect Passed)
	Suspicious []SuspiciousFile `json:"suspicious,omitempty"`

	// Snapshot-based checks (do not affect Passed — reported as separate alerts)
	SaltIntegrity      CheckStatus         `json:"salt_integrity,omitempty"`
	WellKnownFields    CheckStatus         `json:"well_known_fields,omitempty"`
	PolicyWarnings     []PolicyWarning     `json:"policy_warnings,omitempty"`
	MtimeAlerts        []MtimeAlert        `json:"mtime_alerts,omitempty"`
	FileCreationAlerts []FileCreationAlert `json:"file_creation_alerts,omitempty"`

	DurationMs int64 `json:"duration_ms"`
}

// Passed returns true if all checks passed: integrity, metadata, and security.
func (r *CheckResult) Passed() bool {
	return r.WellKnown.OK && r.PublicKey.OK && r.PrivateKey.OK &&
		r.KeyMatch.OK && r.KeyPerms.OK && r.KeyLeak.OK &&
		r.Structure.OK && r.DirExposure.OK &&
		r.BundleJSON.OK && r.IndexJSONL.OK &&
		r.BlessedJSON.OK && r.FollowingJSON.OK &&
		r.PostsFailed == 0 && len(r.Suspicious) == 0
}

// SweepResult contains the aggregated results of checking all tenants.
type SweepResult struct {
	Tenants        int              `json:"tenants"`
	Passed         int              `json:"passed"`
	Failed         int              `json:"failed"`
	Errors         int              `json:"errors"`
	Warnings       int              `json:"warnings"`
	Results        []CheckResult    `json:"results"`
	RootSuspicious []SuspiciousFile `json:"root_suspicious,omitempty"`
	DurationMs     int64            `json:"duration_ms"`
}

// maxFileSize is the threshold above which a file is flagged as suspicious (10MB).
const maxFileSize = 10 * 1024 * 1024

// suspiciousExtensions are file extensions that should not appear in tenant data.
var suspiciousExtensions = map[string]bool{
	// Shell scripts
	".sh": true, ".bash": true, ".zsh": true, ".fish": true,
	// Scripting languages
	".py": true, ".rb": true, ".pl": true, ".php": true, ".cgi": true,
	// Compiled binaries / libraries
	".exe": true, ".bin": true, ".so": true, ".dll": true, ".dylib": true,
	// Windows scripting
	".bat": true, ".cmd": true, ".ps1": true,
	// Java
	".jar": true, ".war": true, ".class": true,
}

// CheckTenant runs all integrity checks on a single tenant site directory.
// Pass CheckOptions{} to get the original behavior without snapshot or domain checks.
func CheckTenant(siteDir string, opts CheckOptions) *CheckResult {
	start := time.Now()
	handle := filepath.Base(siteDir)
	result := &CheckResult{Handle: handle}

	// Phase 1: Independent checks (always run regardless of .well-known status)
	result.Structure = checkStructure(siteDir)
	result.DirExposure = checkDirExposure(siteDir)
	result.KeyPerms = checkKeyPerms(siteDir)
	result.BundleJSON = checkBundleJSON(siteDir)
	result.IndexJSONL = checkIndexJSONL(siteDir)
	result.BlessedJSON = checkBlessedJSON(siteDir)
	result.FollowingJSON = checkFollowingJSON(siteDir)
	result.Suspicious = scanSuspicious(siteDir, handle)

	// Provisioning checks (missing expected files)
	result.PublicPolicies = checkPublicPolicies(siteDir)
	result.PrivatePolicies = checkPrivatePolicies(siteDir)
	result.DMPolicies = checkDMPolicies(siteDir)
	result.StorageSalt = checkStorageSalt(siteDir)
	result.DMDirectories = checkDMDirectories(siteDir, result)
	result.BundleTypes = checkBundleTypes(siteDir)
	result.DMDomainCase = checkDMDomainCase(siteDir)
	result.DMPreviewEncryption = checkDMPreviewEncryption(siteDir)
	result.PolicyDenyAll = checkPolicyDenyAll(siteDir)
	result.PolicyFeedSelfOmit = checkPolicyFeedSelfOmit(siteDir)
	result.TagDirectory = checkTagDirectory(siteDir)
	result.BaseTheme = checkBaseTheme(siteDir)
	result.ThemeStaleFiles = checkThemeStaleFiles(siteDir)
	result.Avatar = checkAvatar(siteDir)
	result.WebappViewMode = checkWebappViewMode(siteDir)

	// Policy syntax validation (Item 2)
	result.PolicyWarnings = checkPolicySyntax(siteDir)

	// Snapshot-based checks (Items 1, 3, 5 — only if SnapshotDir is set)
	if opts.SnapshotDir != "" {
		snapshotPath := filepath.Join(opts.SnapshotDir, handle, "snapshot.json")
		prev, _ := LoadSnapshot(snapshotPath)

		// Item 1: Salt integrity
		result.SaltIntegrity = checkSaltIntegrity(siteDir, prev)

		// Item 3: Mtime tracking
		result.MtimeAlerts = checkMtimeChanges(siteDir, prev)

		// Item 5: File creation detection
		result.FileCreationAlerts = checkFileCreation(siteDir, prev)

		// Save updated snapshot
		snap := buildSnapshot(siteDir)
		SaveSnapshot(snapshotPath, snap)
	}

	// Phase 2: .well-known dependent checks

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

	// Item 9: .well-known field validation
	result.WellKnownFields = checkWellKnownFields(wk, handle, opts.BaseDomain)

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
func CheckAllTenants(tenantsDir string, opts CheckOptions) *SweepResult {
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
		cr := CheckTenant(siteDir, opts)
		sweep.Results = append(sweep.Results, *cr)
		sweep.Tenants++
		if cr.Passed() {
			sweep.Passed++
		} else {
			sweep.Failed++
		}
		sweep.Errors += cr.PostsFailed
		sweep.Warnings += len(cr.Suspicious)
	}

	sweep.DurationMs = time.Since(start).Milliseconds()
	return sweep
}

// ---------- Provisioning checks ----------

func checkPublicPolicies(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, "policies", "rules.jsonl")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return CheckStatus{OK: false, Message: "missing"}
	}
	return CheckStatus{OK: true}
}

func checkPrivatePolicies(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, ".polis", "policies", "rules.jsonl")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return CheckStatus{OK: false, Message: "missing"}
	}
	return CheckStatus{OK: true}
}

func checkDMPolicies(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, ".polis", "policies", "rules.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		// If file doesn't exist, PrivatePolicies handles that
		return CheckStatus{OK: true}
	}
	if !strings.Contains(string(data), "pub.polis.dm") {
		return CheckStatus{OK: false, Message: "missing DM rules"}
	}
	return CheckStatus{OK: true}
}

// checkPolicyDenyAll verifies that the private policy file ends with a
// "deny all from all" catch-all rule. This ensures unknown content types
// are denied by default.
func checkPolicyDenyAll(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, ".polis", "policies", "rules.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		// If file doesn't exist, PrivatePolicies handles that
		return CheckStatus{OK: true}
	}
	policies, _ := policy.LoadPolicies(path, "")
	// Check if any active rule is "deny all from all"
	for _, p := range policies {
		if p.Active && p.Rule == "deny all from all" {
			return CheckStatus{OK: true}
		}
	}
	_ = data // used above via LoadPolicies
	return CheckStatus{OK: false, Message: "missing default-deny catch-all"}
}

// checkPolicyFeedSelfOmit verifies that the private policy file contains an
// "omit pub.polis.feed from self" rule to suppress self-authored feed events.
func checkPolicyFeedSelfOmit(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, ".polis", "policies", "rules.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		// If file doesn't exist, PrivatePolicies handles that
		return CheckStatus{OK: true}
	}
	policies, _ := policy.LoadPolicies(path, "")
	for _, p := range policies {
		if p.Active && p.Rule == "omit pub.polis.feed from self" {
			return CheckStatus{OK: true}
		}
	}
	_ = data
	return CheckStatus{OK: false, Message: "missing feed self-omit rule"}
}

func checkStorageSalt(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, ".polis", "storage-salt")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return CheckStatus{OK: false, Message: "missing"}
	}
	return CheckStatus{OK: true}
}

func checkDMDirectories(siteDir string, result *CheckResult) CheckStatus {
	path := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return CheckStatus{OK: false, Message: "missing"}
	}
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("cannot access: %v", err)}
	}
	// Item 4: verify DM directory permissions
	mode := info.Mode().Perm()
	if mode != 0700 {
		return CheckStatus{OK: false, Message: fmt.Sprintf("unsafe permissions: %04o", mode)}
	}
	return CheckStatus{OK: true}
}

// checkDMDomainCase scans DM conversation files for non-lowercase peer_domain
// or from/to fields. Mixed-case domains cause conversation ID mismatches and
// delivery failures since DNS hostnames are case-insensitive (RFC 1123).
func checkDMDomainCase(siteDir string) CheckStatus {
	convDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		// No DM directory is fine — other checks handle that
		return CheckStatus{OK: true}
	}

	var badFiles []string
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") || entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(convDir, entry.Name()))
		if err != nil {
			continue
		}
		var conv struct {
			PeerDomain string `json:"peer_domain"`
			Messages   []struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}
		if conv.PeerDomain != strings.ToLower(conv.PeerDomain) {
			badFiles = append(badFiles, entry.Name())
			continue
		}
		for _, msg := range conv.Messages {
			if msg.From != strings.ToLower(msg.From) || msg.To != strings.ToLower(msg.To) {
				badFiles = append(badFiles, entry.Name())
				break
			}
		}
	}

	if len(badFiles) > 0 {
		return CheckStatus{
			OK:      false,
			Message: fmt.Sprintf("mixed-case domains in %d conversation(s): %s", len(badFiles), strings.Join(badFiles, ", ")),
		}
	}
	return CheckStatus{OK: true}
}

// checkDMPreviewEncryption verifies that all DM conversation previews in the
// index are encrypted at rest. Plaintext previews leak message content to anyone
// with filesystem access.
func checkDMPreviewEncryption(siteDir string) CheckStatus {
	idxPath := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conversations.json")
	data, err := os.ReadFile(idxPath)
	if err != nil {
		// No conversations file — nothing to check
		return CheckStatus{OK: true}
	}

	var idx struct {
		Conversations []struct {
			LastPreview string `json:"last_preview"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return CheckStatus{OK: false, Message: "corrupt conversations.json"}
	}

	var plainCount int
	for _, c := range idx.Conversations {
		if !dm.IsEncryptedPreview(c.LastPreview) {
			plainCount++
		}
	}
	if plainCount > 0 {
		return CheckStatus{OK: false, Message: fmt.Sprintf("%d plaintext preview(s)", plainCount)}
	}
	return CheckStatus{OK: true}
}

func checkTagDirectory(siteDir string) CheckStatus {
	tagDir := filepath.Join(siteDir, "content", "pub.polis.core", "tag")
	info, err := os.Stat(tagDir)
	if err != nil {
		return CheckStatus{OK: false, Message: "missing"}
	}
	if !info.IsDir() {
		return CheckStatus{OK: false, Message: "not a directory"}
	}
	return CheckStatus{OK: true}
}

// ---------- Theme checks ----------

// checkBaseTheme verifies that the _base theme directory exists.
func checkBaseTheme(siteDir string) CheckStatus {
	baseDir := filepath.Join(siteDir, "site", "themes", "_base")
	info, err := os.Stat(baseDir)
	if err != nil {
		return CheckStatus{OK: false, Message: "missing"}
	}
	if !info.IsDir() {
		return CheckStatus{OK: false, Message: "not a directory"}
	}
	return CheckStatus{OK: true}
}

func checkAvatar(siteDir string) CheckStatus {
	wk, err := site.LoadWellKnown(siteDir)
	if err != nil {
		// If well-known is missing, that's caught by the WellKnown check
		return CheckStatus{OK: true}
	}
	if wk.Avatar == nil {
		return CheckStatus{OK: false, Message: "missing"}
	}
	if wk.Avatar.BG == "" || wk.Avatar.FG == "" {
		return CheckStatus{OK: false, Message: "incomplete (missing bg or fg)"}
	}
	return CheckStatus{OK: true}
}

// checkWebappViewMode flags the deprecated view_mode key in webapp config.
func checkWebappViewMode(siteDir string) CheckStatus {
	configPath := filepath.Join(siteDir, ".polis", "webapp", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return CheckStatus{OK: true} // no config file is fine
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return CheckStatus{OK: true} // malformed config caught elsewhere
	}
	if _, exists := obj["view_mode"]; exists {
		return CheckStatus{OK: false, Message: "deprecated view_mode key present"}
	}
	return CheckStatus{OK: true}
}

// baseTemplateFiles are the template files that _base provides.
var baseTemplateFiles = []string{
	"index.html", "post.html", "posts.html",
	"comment.html", "comment-inline.html",
	"tag.html", "tag-index.html",
}

// baseSnippetFiles are the snippet files that _base provides.
var baseSnippetFiles = []string{
	"about.html", "also-reading.html", "blessed-comment.html",
	"comment-item.html", "polis-widget.html", "post-item.html",
}

// oldCanonicalHashes maps filenames to SHA-256 hashes of known old system
// templates (with the theme-name HTML comment stripped). These are the templates
// that shipped with themes before the _base consolidation. Files matching these
// hashes are safe to remove — they're system files, not user customizations.
var oldCanonicalHashes = map[string]string{
	// Templates (hashed after stripping leading <!-- ... --> comment block)
	"index.html":          "5bf8b5c285685cca977f5435a9c3d4c253995d01c9350c505c70f847ee4afddd",
	"post.html":           "255c510add9de95ae69314d2d27f721447cf51e8fe4b974446f3604fb50c605f",
	"posts.html":          "9258ca3af95ba623b0755dbbbf86043ba15481dfee2857c02646d7f7302f7560",
	"comment.html":        "c8c0a6d52f1e2d0ea27e35aedb0f2313e469af193ba203de6f3253fffc6ada87",
	"comment-inline.html": "e3d7e9fc4dbd868b6c560f876bb40e978972cc7156c77c37545410482ff3c86b",
	"tag.html":            "be8e267db3bbadeafea035b1a720ace517a03fd7953c3ca2ee85456cbc76ad3d",
	"tag-index.html":      "1f49619cd6884ed45dbf0a8c09cfcb641b007dbd466f4f03addd30f102178368",
	// Snippets (hashed as-is, no comment to strip)
	"snippets/about.html":           "c4ce7e30ab58b1c4771089996677cf555c46706271763be793440d62d968ba28",
	"snippets/also-reading.html":    "fd16d02be6e72f8d0b82c8f93fb05db69c1175a09dfdebe325963dc4ad823ee7",
	"snippets/blessed-comment.html": "235d0f3ae0ff54425a323ca9ccf8bdeef8437255fd036135a54b8ea8ef1eabad",
	"snippets/comment-item.html":    "9f0641c8dc67fa527e537cd8b3bdb6e2809f511f92973df4e1c29947a9f4754b",
	"snippets/polis-widget.html":    "002a69a2c4445fb68e6a2e6f0c03d699099339cdbca63d5d67411d135f07b52f",
	"snippets/post-item.html":       "22962e9390beabf63a6eed8a8ec224ac42aa5a2e63eb4742b863098f7ccb2388",
}

// htmlCommentRe matches an HTML comment block at the start of a file.
var htmlCommentRe = regexp.MustCompile(`(?s)\A\s*<!--.*?-->\s*\n`)

// normalizeForHash strips the leading HTML comment block (theme-name comment)
// from template content before hashing, so all per-theme variants hash the same.
func normalizeForHash(content string) string {
	return htmlCommentRe.ReplaceAllString(content, "")
}

// isStaleFile returns true if the file at themePath is a known old system template
// (matches a canonical hash) OR matches the current _base file exactly.
func isStaleFile(themePath, basePath, relPath string) bool {
	themeData, err := os.ReadFile(themePath)
	if err != nil {
		return false
	}

	// Check 1: exact match against _base (handles future _base updates)
	if baseData, err := os.ReadFile(basePath); err == nil {
		if string(themeData) == string(baseData) {
			return true
		}
	}

	// Check 2: canonical hash match (handles old pre-consolidation templates)
	expectedHash, ok := oldCanonicalHashes[relPath]
	if !ok {
		return false
	}

	// For template files (not snippets), strip the comment before hashing
	content := string(themeData)
	if !strings.HasPrefix(relPath, "snippets/") {
		content = normalizeForHash(content)
	}

	h := sha256.Sum256([]byte(content))
	actualHash := fmt.Sprintf("%x", h)
	return actualHash == expectedHash
}

// checkThemeStaleFiles detects HTML templates and shared snippets in tenant
// theme directories that are old system files and should be removed.
func checkThemeStaleFiles(siteDir string) CheckStatus {
	baseDir := filepath.Join(siteDir, "site", "themes", "_base")
	themesDir := filepath.Join(siteDir, "site", "themes")

	entries, err := os.ReadDir(themesDir)
	if err != nil {
		return CheckStatus{OK: true} // no themes dir
	}

	totalStale := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "_base" {
			continue
		}
		themeDir := filepath.Join(themesDir, entry.Name())
		totalStale += countStaleFiles(themeDir, baseDir)
	}

	if totalStale > 0 {
		return CheckStatus{
			OK:      false,
			Message: fmt.Sprintf("%d stale theme file(s) should be removed", totalStale),
		}
	}
	return CheckStatus{OK: true}
}

// countStaleFiles counts files in themeDir that are stale system templates.
func countStaleFiles(themeDir, baseDir string) int {
	count := 0

	for _, file := range baseTemplateFiles {
		if isStaleFile(filepath.Join(themeDir, file), filepath.Join(baseDir, file), file) {
			count++
		}
	}

	for _, file := range baseSnippetFiles {
		relPath := filepath.Join("snippets", file)
		if isStaleFile(filepath.Join(themeDir, relPath), filepath.Join(baseDir, relPath), relPath) {
			count++
		}
	}

	return count
}

// StaleThemeFiles returns the list of stale system files in a theme directory.
// A file is stale if it matches _base exactly or matches a known old canonical hash.
// Exported for use by Medic and Tailor.
func StaleThemeFiles(themeDir, baseDir string) []string {
	var stale []string

	for _, file := range baseTemplateFiles {
		if isStaleFile(filepath.Join(themeDir, file), filepath.Join(baseDir, file), file) {
			stale = append(stale, file)
		}
	}

	for _, file := range baseSnippetFiles {
		relPath := filepath.Join("snippets", file)
		if isStaleFile(filepath.Join(themeDir, relPath), filepath.Join(baseDir, relPath), relPath) {
			stale = append(stale, relPath)
		}
	}

	return stale
}

// ---------- Structure check ----------

// requiredDirs are directories that must exist in every valid tenant.
var requiredDirs = []string{
	".well-known",
	".polis",
	filepath.Join(".polis", "keys"),
}

func checkStructure(siteDir string) CheckStatus {
	for _, dir := range requiredDirs {
		p := filepath.Join(siteDir, dir)
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			return CheckStatus{OK: false, Message: fmt.Sprintf("missing required directory: %s", dir)}
		}
		if err != nil {
			return CheckStatus{OK: false, Message: fmt.Sprintf("cannot access %s: %v", dir, err)}
		}
		if !info.IsDir() {
			return CheckStatus{OK: false, Message: fmt.Sprintf("%s exists but is not a directory", dir)}
		}
	}
	return CheckStatus{OK: true}
}

// ---------- Sensitive directory exposure check ----------

// sensitiveDirs are directories that must not be world-readable.
var sensitiveDirs = []string{".polis", ".git"}

func checkDirExposure(siteDir string) CheckStatus {
	for _, dir := range sensitiveDirs {
		p := filepath.Join(siteDir, dir)
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue // .git may not exist, that's fine
		}
		if err != nil {
			continue
		}
		if !info.IsDir() {
			continue
		}
		mode := info.Mode().Perm()
		// Fail if world-readable (others have read bit)
		if mode&0007 != 0 {
			return CheckStatus{OK: false, Message: fmt.Sprintf("%s is world-accessible (permissions: %04o, expected 0700 or 0750)", dir, mode)}
		}
	}
	return CheckStatus{OK: true}
}

// ---------- Key permissions check ----------

func checkKeyPerms(siteDir string) CheckStatus {
	privKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519")
	info, err := os.Stat(privKeyPath)
	if os.IsNotExist(err) {
		return CheckStatus{OK: false, Message: "private key not found"}
	}
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("failed to stat: %v", err)}
	}
	mode := info.Mode().Perm()
	if mode != 0600 && mode != 0400 {
		return CheckStatus{OK: false, Message: fmt.Sprintf("unsafe permissions %04o (expected 0600 or 0400)", mode)}
	}
	return CheckStatus{OK: true}
}

// ---------- Metadata file validation ----------

func checkBundleJSON(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return CheckStatus{OK: false, Message: "missing"}
	}
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("failed to read: %v", err)}
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("invalid JSON: %v", err)}
	}
	if s, _ := raw["name"].(string); s == "" {
		return CheckStatus{OK: false, Message: "missing required field: name"}
	}
	if s, _ := raw["version"].(string); s == "" {
		return CheckStatus{OK: false, Message: "missing required field: version"}
	}
	return CheckStatus{OK: true}
}

func checkBundleTypes(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	b, err := bundle.LoadBundle(path)
	if err != nil {
		// If bundle is missing/corrupt, BundleJSON check already covers that
		return CheckStatus{OK: true}
	}
	defaults := bundle.DefaultCoreBundle()
	var missing []string
	for name := range defaults.Types {
		if _, ok := b.Types[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return CheckStatus{OK: false, Message: fmt.Sprintf("missing types: %s", strings.Join(missing, ", "))}
	}
	return CheckStatus{OK: true}
}

func checkIndexJSONL(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return CheckStatus{OK: true} // optional file
	}
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("failed to open: %v", err)}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return CheckStatus{OK: false, Message: fmt.Sprintf("invalid JSON on line %d: %v", lineNum, err)}
		}
	}
	if err := scanner.Err(); err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("read error: %v", err)}
	}
	return CheckStatus{OK: true}
}

func checkBlessedJSON(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, "content", "pub.polis.core", "comment", "blessed.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return CheckStatus{OK: true} // optional file
	}
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("failed to read: %v", err)}
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("invalid JSON: %v", err)}
	}
	// Verify expected structure: must have "comments" array
	if _, ok := raw["comments"]; !ok {
		return CheckStatus{OK: false, Message: "missing required field: comments"}
	}
	return CheckStatus{OK: true}
}

func checkFollowingJSON(siteDir string) CheckStatus {
	path := filepath.Join(siteDir, "content", "pub.polis.core", "follow", "following.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return CheckStatus{OK: true} // optional file
	}
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("failed to read: %v", err)}
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("invalid JSON: %v", err)}
	}
	// Verify expected structure: must have "following" array
	if _, ok := raw["following"]; !ok {
		return CheckStatus{OK: false, Message: "missing required field: following"}
	}
	return CheckStatus{OK: true}
}

// ---------- Suspicious file scan ----------

// sshArtifacts are files that should not exist in tenant or data directories.
var sshArtifacts = []string{
	".bash_history", ".ssh", ".profile", ".bashrc", ".vimrc",
	".wget-hstory", ".python_history", ".node_repl_history",
}

func scanSuspicious(siteDir string, handle string) []SuspiciousFile {
	var results []SuspiciousFile
	const maxResults = 100

	// Item 11: Check for SSH/shell artifacts in tenant root
	for _, artifact := range sshArtifacts {
		p := filepath.Join(siteDir, artifact)
		if _, err := os.Lstat(p); err == nil {
			results = append(results, SuspiciousFile{
				Path:   artifact,
				Reason: "shell/SSH artifact (HIGH)",
			})
		}
	}

	// Get expected owner UID from the site directory itself
	expectedUID, uidAvailable := getFileUID(siteDir)

	filepath.WalkDir(siteDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(results) >= maxResults {
			return filepath.SkipAll
		}

		rel, _ := filepath.Rel(siteDir, path)
		if rel == "." {
			return nil
		}

		// Skip private state and git directories
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".polis" {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		// Check for symlinks (use Lstat, not the info from WalkDir which follows symlinks)
		linfo, err := os.Lstat(path)
		if err == nil && linfo.Mode()&os.ModeSymlink != 0 {
			results = append(results, SuspiciousFile{Path: rel, Reason: "symlink"})
			return nil
		}

		// Check suspicious file extensions (skip for discover.polis.pub which may have scripts)
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if suspiciousExtensions[ext] && handle != "discover.polis.pub" {
			results = append(results, SuspiciousFile{Path: rel, Reason: fmt.Sprintf("suspicious extension: %s", ext)})
		}

		// Check execute bit on regular files
		if info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			results = append(results, SuspiciousFile{Path: rel, Reason: fmt.Sprintf("executable permissions: %04o", info.Mode().Perm())})
		}

		// Check file size
		if info.Size() > maxFileSize {
			results = append(results, SuspiciousFile{Path: rel, Reason: fmt.Sprintf("oversized: %d bytes (limit: %d)", info.Size(), maxFileSize)})
		}

		// Check ownership (only on platforms where syscall.Stat_t is available)
		if uidAvailable {
			fileUID, ok := getFileUID(path)
			if ok && fileUID != expectedUID && fileUID != 0 {
				results = append(results, SuspiciousFile{Path: rel, Reason: fmt.Sprintf("unexpected owner uid=%d (expected %d or root)", fileUID, expectedUID)})
			}
		}

		return nil
	})

	return results
}

// getFileUID returns the owning UID of a file. Returns (uid, true) on Linux,
// (0, false) on platforms where syscall.Stat_t is not available.
func getFileUID(path string) (uint32, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		return sys.Uid, true
	}
	return 0, false
}

// ScanRootArtifacts checks the data root directory for SSH/shell artifacts
// that may indicate a container breakout. Unlike scanSuspicious, this does
// not recurse — it only checks the known artifact list against the root.
func ScanRootArtifacts(dataDir string) []SuspiciousFile {
	var results []SuspiciousFile
	for _, artifact := range sshArtifacts {
		p := filepath.Join(dataDir, artifact)
		if _, err := os.Lstat(p); err == nil {
			results = append(results, SuspiciousFile{
				Path:   filepath.Join(dataDir, artifact),
				Reason: "shell/SSH artifact at data root (HIGH)",
			})
		}
	}
	return results
}

// ---------- Salt integrity check (Item 1) ----------

func checkSaltIntegrity(siteDir string, prev *Snapshot) CheckStatus {
	hash, mtime, err := ComputeSaltHash(siteDir)
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("error reading salt: %v", err)}
	}
	if hash == "" {
		// Salt doesn't exist yet — not a tamper signal
		return CheckStatus{OK: true}
	}
	if prev == nil || prev.SaltHash == "" {
		// First sweep — baseline, no alerts
		return CheckStatus{OK: true, Message: "baseline recorded"}
	}
	if hash != prev.SaltHash {
		return CheckStatus{OK: false, Message: "salt content TAMPERED"}
	}
	if !mtime.Equal(prev.SaltMtime) {
		return CheckStatus{OK: false, Message: "salt mtime changed"}
	}
	return CheckStatus{OK: true}
}

// ---------- Policy syntax validation (Item 2) ----------

func checkPolicySyntax(siteDir string) []PolicyWarning {
	var warnings []PolicyWarning
	policyFiles := []string{
		filepath.Join(siteDir, "policies", "rules.jsonl"),
		filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"),
	}
	for _, path := range policyFiles {
		relPath, _ := filepath.Rel(siteDir, path)
		ws := validatePolicyFile(path, relPath)
		warnings = append(warnings, ws...)
	}

	// Item 13: Semantic checks (duplicates and contradictions)
	warnings = append(warnings, checkPolicySemantics(siteDir)...)

	return warnings
}

func validatePolicyFile(path, relPath string) []PolicyWarning {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var warnings []PolicyWarning
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue // Not valid JSON, skip (IndexJSONL already validates)
		}
		// Skip metadata lines (version/generator headers)
		if _, hasVersion := raw["version"]; hasVersion {
			if _, hasPolicy := raw["policy"]; !hasPolicy {
				continue
			}
		}
		rule, ok := raw["policy"].(string)
		if !ok {
			continue
		}
		if _, err := policy.Parse(rule); err != nil {
			warnings = append(warnings, PolicyWarning{
				File:  relPath,
				Line:  lineNum,
				Rule:  rule,
				Error: err.Error(),
			})
		}
	}
	return warnings
}

// checkPolicySemantics detects duplicate rules and contradictions (Item 13).
func checkPolicySemantics(siteDir string) []PolicyWarning {
	var warnings []PolicyWarning
	policyFiles := []string{
		filepath.Join(siteDir, "policies", "rules.jsonl"),
		filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"),
	}

	type ruleEntry struct {
		rule string
		file string
		line int
	}
	var allRules []ruleEntry
	type ruleKey struct {
		typ    string
		source string
		action string
	}
	seen := make(map[string]ruleEntry) // exact dedup
	byKey := make(map[string]ruleEntry) // type+source → first action for contradiction detection

	for _, path := range policyFiles {
		relPath, _ := filepath.Rel(siteDir, path)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(line), &raw); err != nil {
				continue
			}
			rule, ok := raw["policy"].(string)
			if !ok {
				continue
			}
			entry := ruleEntry{rule: rule, file: relPath, line: lineNum}
			allRules = append(allRules, entry)

			// Check for exact duplicates
			if prev, exists := seen[rule]; exists {
				warnings = append(warnings, PolicyWarning{
					File:  relPath,
					Line:  lineNum,
					Rule:  rule,
					Error: fmt.Sprintf("duplicate of %s:%d", prev.file, prev.line),
				})
			} else {
				seen[rule] = entry
			}

			// Check for contradictions (same type+source, different allow/deny)
			parsed, err := policy.Parse(rule)
			if err != nil {
				continue
			}
			k := parsed.Type + "|" + parsed.Source
			if prev, exists := byKey[k]; exists {
				prevParsed, _ := policy.Parse(prev.rule)
				if prevParsed != nil && isContradiction(parsed.Action, prevParsed.Action) {
					warnings = append(warnings, PolicyWarning{
						File:  relPath,
						Line:  lineNum,
						Rule:  rule,
						Error: fmt.Sprintf("contradicts %s at %s:%d", prev.rule, prev.file, prev.line),
					})
				}
			} else {
				byKey[k] = entry
			}
		}
		f.Close()
	}
	_ = allRules
	return warnings
}

func isContradiction(a, b string) bool {
	return (a == "allow" && b == "deny") || (a == "deny" && b == "allow")
}

// ---------- Mtime tracking (Item 3) ----------

func checkMtimeChanges(siteDir string, prev *Snapshot) []MtimeAlert {
	if prev == nil || len(prev.FileMtimes) == 0 {
		return nil // First sweep — baseline
	}
	current := ComputeFileMtimes(siteDir)
	var alerts []MtimeAlert
	for file, prevMtime := range prev.FileMtimes {
		currMtime, exists := current[file]
		if !exists {
			continue // File was removed — other checks handle that
		}
		if !currMtime.Equal(prevMtime) {
			severity := fileSeverity(file)
			// Salt mtime already covered by Item 1
			if file == filepath.Join(".polis", "storage-salt") {
				continue
			}
			alerts = append(alerts, MtimeAlert{
				File:      file,
				Severity:  severity,
				PrevMtime: prevMtime.Format(time.RFC3339),
				CurrMtime: currMtime.Format(time.RFC3339),
			})
		}
	}
	return alerts
}

func fileSeverity(file string) string {
	switch file {
	case filepath.Join(".polis", "keys", "id_ed25519"):
		return "HIGH"
	case filepath.Join(".polis", "keys", "id_ed25519.pub"):
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// ---------- File creation detection (Item 5) ----------

func checkFileCreation(siteDir string, prev *Snapshot) []FileCreationAlert {
	if prev == nil {
		return nil // First sweep — baseline
	}
	var alerts []FileCreationAlert

	// Check .polis/keys/
	currentKeys := ComputeKeyDirInventory(siteDir)
	prevKeys := make(map[string]bool)
	for _, f := range prev.KeyDirFiles {
		prevKeys[f] = true
	}
	for _, f := range currentKeys {
		if !prevKeys[f] {
			alerts = append(alerts, FileCreationAlert{
				Dir:      filepath.Join(".polis", "keys"),
				File:     f,
				Severity: "HIGH",
			})
		}
	}

	// Check policy directories
	currentPolicies := ComputePolicyDirInventory(siteDir)
	for dir, files := range currentPolicies {
		prevFiles := make(map[string]bool)
		if prev.PolicyDirFiles != nil {
			for _, f := range prev.PolicyDirFiles[dir] {
				prevFiles[f] = true
			}
		}
		for _, f := range files {
			if !prevFiles[f] {
				alerts = append(alerts, FileCreationAlert{
					Dir:      dir,
					File:     f,
					Severity: "MEDIUM",
				})
			}
		}
	}

	return alerts
}

// ---------- .well-known field validation (Item 9) ----------

func checkWellKnownFields(wk *site.WellKnown, handle, baseDomain string) CheckStatus {
	// Validate public key decodes to a valid Ed25519 key
	if wk.PublicKey != "" {
		pubKeyStr := strings.TrimSpace(wk.PublicKey)
		if err := signing.ValidatePublicKey([]byte(pubKeyStr)); err != nil {
			return CheckStatus{OK: false, Message: fmt.Sprintf("invalid public_key: %v", err)}
		}
	}

	// Validate bundles has at least one entry
	if len(wk.Bundles) == 0 {
		return CheckStatus{OK: false, Message: "no bundles defined"}
	}

	// Validate handle matches baseDomain pattern
	if baseDomain != "" && handle != "" {
		expectedSuffix := "." + baseDomain
		if !strings.HasSuffix(handle, expectedSuffix) {
			return CheckStatus{OK: false, Message: fmt.Sprintf("handle %q is not a subdomain of %q", handle, baseDomain)}
		}
	}

	return CheckStatus{OK: true}
}

// ---------- Snapshot builder ----------

func buildSnapshot(siteDir string) *Snapshot {
	snap := &Snapshot{
		GeneratedAt: time.Now().UTC(),
	}
	hash, mtime, err := ComputeSaltHash(siteDir)
	if err == nil && hash != "" {
		snap.SaltHash = hash
		snap.SaltMtime = mtime
	}
	snap.FileMtimes = ComputeFileMtimes(siteDir)
	snap.KeyDirFiles = ComputeKeyDirInventory(siteDir)
	snap.PolicyDirFiles = ComputePolicyDirInventory(siteDir)
	return snap
}

// ---------- Stale archive detection (Item 12) ----------

// StaleArchive records an archive file that exceeds the age threshold.
type StaleArchive struct {
	Path    string `json:"path"`
	AgeDays int    `json:"age_days"`
	Size    int64  `json:"size_bytes"`
}

// CheckStaleArchives scans archiveDir for files older than maxAge.
func CheckStaleArchives(archiveDir string, maxAge time.Duration) []StaleArchive {
	var stale []StaleArchive
	now := time.Now()

	filepath.WalkDir(archiveDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		age := now.Sub(info.ModTime())
		if age > maxAge {
			rel, _ := filepath.Rel(archiveDir, path)
			stale = append(stale, StaleArchive{
				Path:    rel,
				AgeDays: int(age.Hours() / 24),
				Size:    info.Size(),
			})
		}
		return nil
	})
	return stale
}

// ---------- Private key leak scan ----------

// scanForKeyLeak scans public-facing directories for private key PEM headers.
// Returns the first file path containing the header, or empty string if clean.
func scanForKeyLeak(siteDir string) string {
	header := "-----BEGIN OPENSSH PRIVATE KEY-----"

	// Public-facing directories to scan
	scanDirs := []string{
		"content", "site", ".well-known", "policies",
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
	var found string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			if found != "" {
				return filepath.SkipAll
			}
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

// ---------- Post verification ----------

// verifyPosts walks the content post directory and verifies signatures and hashes.
func verifyPosts(siteDir string, pubKey []byte, result *CheckResult) {
	postsDir := filepath.Join(siteDir, "content", "pub.polis.core", "post")
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

// ---------- Frontmatter parsing ----------

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

// ---------- Signature / hash helpers ----------

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
