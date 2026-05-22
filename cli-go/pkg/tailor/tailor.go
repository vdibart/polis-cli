// Package tailor diagnoses and auto-fixes polis sites to bring them up to current spec.
// It understands every historical layout and can upgrade from any version.
package tailor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// CheckStatus represents the outcome of a single check.
type CheckStatus string

const (
	StatusPass CheckStatus = "pass"
	StatusFail CheckStatus = "fail"
	StatusSkip CheckStatus = "skip"
)

// Action represents a single file operation performed or planned.
type Action struct {
	Op     string `json:"op"`               // "move", "create", "update", "remove"
	Path   string `json:"path"`             // file/dir affected (relative to site root)
	Dest   string `json:"dest,omitempty"`   // destination path for moves
	Detail string `json:"detail,omitempty"` // human-readable detail
}

// CheckResult is the outcome of a single named check.
type CheckResult struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`             // what happened / what would happen
	Reason  string      `json:"reason"`              // why — references the version that introduced the change
	Actions []Action    `json:"actions,omitempty"`    // specific file operations performed/planned
}

// Result is the aggregate output of a tailor run.
type Result struct {
	SiteDir   string        `json:"site_dir"`
	Mode      string        `json:"mode"` // "diagnose" or "apply"
	BackupDir string        `json:"backup_dir,omitempty"`
	Checks    []CheckResult `json:"checks"`
	Summary   Summary       `json:"summary"`
}

// Summary counts check outcomes.
type Summary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// runContext carries shared state extracted before checks modify the site.
type runContext struct {
	siteDir   string
	dryRun    bool
	baseURL   string // extracted from .well-known/polis or POLIS_BASE_URL before legacy cleanup
	generator string // e.g. "polis-cli-go/0.59.0"
}

// checkFunc is the signature for individual check functions.
// dryRun=true means diagnose only; dryRun=false means apply fixes.
type checkFunc func(ctx *runContext) CheckResult

// allChecks returns the ordered list of checks to run.
func allChecks() []checkFunc {
	return []checkFunc{
		// Phase 1: Well-known identity
		checkWellKnownVersion,
		checkAuthorFieldMigration,
		checkWellKnownLegacyConfig,
		checkWellKnownBundles,
		checkAvatarConfig,
		// Phase 2: Bundle definition
		checkBundleJSON,
		// Phase 3: Layout migration
		checkLayoutPosts,
		checkLayoutComments,
		checkLayoutFollowing,
		checkLayoutBlessed,
		// Phase 4: Cross-platform fixes
		checkPathSeparators,
		// Phase 4.5: step-01 bundle/registry/theme refactor migrations
		// (must run before Phase 5 — index rebuild + render read the new paths)
		checkBundleReferencePayload,
		checkLegacyContentPath,
		checkRegistryMigration,
		checkLegacyThemeLocations,
		checkBundleActiveFields,
		// Phase 4.7: post-step-01 migrations
		// (notification relocation, v3→v4 shape cutover, R17-2 archive cleanup)
		checkNotificationRulesMigration,
		checkActiveShapeUpgrade,
		checkV3LegacyArchives,
		// Phase 5: Derived data rebuild
		checkIndexRebuild,
		// Phase 5.5: Re-render site (produces HTML at mount points)
		checkRenderSite,
		// Phase 6: Provisioning
		checkPoliciesPublic,
		checkPoliciesPrivate,
		checkPolicyContentConverge,
		checkTagDirectory,
		checkStorageSalt,
		checkDMDirectories,
		checkDMDomainCase,
		checkDMPreviewEncryption,
		checkThemeConsolidation,
		// Phase 7: Config migration
		checkWebappConfig,
		checkWebappViewMode,
		// Phase 6.5: Content-aware integrity (F1–F9)
		checkReferencePayloadIntegrity,
		checkRegistryIntegrity,
		checkKeyConsistency,
		checkBundlePathIntegrity,
		checkBundleDeclarations,
		checkIndexEntries,
		checkBlessedFollowingStructure,
		// Phase 6.7: Tailor-only self-heal (defense-in-depth + schema-version
		// plumbing for future bumps)
		checkActiveThemeUnset,
		checkRegistrySchemaVersion,
		checkRegistryFQNSanity,
		// Phase 8: Cleanup
		checkManifestObsolete,
		checkEmptyMetadataDir,
		checkStaleScopedFeed,
		checkStaleFeedViewedAt,
		checkOrphanedThemeDirs,
		checkStudio13RenameMigration,
		// Phase 9: CLI binary (network, runs last)
		checkCLIUpdate,
	}
}

// extractBaseURL reads the base URL from .well-known/polis (legacy fields) or env.
// Called once before checks run, since legacy config check removes these fields.
func extractBaseURL(siteDir string) string {
	// Environment variable takes precedence
	if url := os.Getenv("POLIS_BASE_URL"); url != "" {
		return url
	}

	// Read from .well-known/polis before legacy cleanup removes it
	wkPath := filepath.Join(siteDir, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
	if err != nil {
		return ""
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ""
	}

	// Try base_url first, then construct from domain
	if baseURL, ok := raw["base_url"].(string); ok && baseURL != "" {
		return baseURL
	}
	if domain, ok := raw["domain"].(string); ok && domain != "" {
		return "https://" + domain
	}

	return ""
}

// Diagnose runs all checks in dry-run mode.
// Optional generator overrides the package-level default.
func Diagnose(siteDir string, generator ...string) *Result {
	gen := GetGenerator()
	if len(generator) > 0 && generator[0] != "" {
		gen = generator[0]
	}
	return run(siteDir, true, "", gen)
}

// Apply runs all checks and fixes issues, creating a backup first.
// Optional generator overrides the package-level default.
func Apply(siteDir string, generator ...string) *Result {
	gen := GetGenerator()
	if len(generator) > 0 && generator[0] != "" {
		gen = generator[0]
	}
	backupDir := createBackupDir(siteDir)
	return run(siteDir, false, backupDir, gen)
}

func run(siteDir string, dryRun bool, backupDir string, generator string) *Result {
	mode := "diagnose"
	if !dryRun {
		mode = "apply"
	}

	result := &Result{
		SiteDir:   siteDir,
		Mode:      mode,
		BackupDir: backupDir,
	}

	// Verify .well-known/polis exists — if not, most checks are meaningless
	wkPath := filepath.Join(siteDir, ".well-known", "polis")
	if _, err := os.Stat(wkPath); os.IsNotExist(err) {
		result.Checks = []CheckResult{{
			Name:    "wellknown-exists",
			Status:  StatusSkip,
			Message: ".well-known/polis not found — run 'polis init' first",
			Reason:  "A polis site requires a .well-known/polis identity document",
		}}
		result.Summary = Summary{Total: 1, Skipped: 1}
		return result
	}

	// Extract base URL before legacy config check removes it from .well-known/polis
	ctx := &runContext{
		siteDir:   siteDir,
		dryRun:    dryRun,
		baseURL:   extractBaseURL(siteDir),
		generator: generator,
	}

	checks := allChecks()
	for _, check := range checks {
		cr := check(ctx)
		result.Checks = append(result.Checks, cr)
	}

	// Compute summary
	for _, cr := range result.Checks {
		result.Summary.Total++
		switch cr.Status {
		case StatusPass:
			result.Summary.Passed++
		case StatusFail:
			result.Summary.Failed++
		case StatusSkip:
			result.Summary.Skipped++
		}
	}

	return result
}

// createBackupDir creates a timestamped backup directory under .polis/tailor-backup/.
func createBackupDir(siteDir string) string {
	ts := time.Now().Format("20060102-150405")
	dir := filepath.Join(siteDir, ".polis", "tailor-backup", ts)
	os.MkdirAll(dir, 0700)
	return dir
}

// backupFile copies a file to the backup directory, preserving its relative path.
func backupFile(siteDir, backupDir, relPath string) error {
	if backupDir == "" {
		return nil
	}
	src := filepath.Join(siteDir, relPath)
	data, err := os.ReadFile(src)
	if err != nil {
		return nil // File doesn't exist, nothing to back up
	}
	dst := filepath.Join(backupDir, relPath)
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// pass creates a passing CheckResult.
func pass(name, message string) CheckResult {
	return CheckResult{Name: name, Status: StatusPass, Message: message}
}

// fail creates a failing CheckResult with reason and actions.
func fail(name, message, reason string, actions []Action) CheckResult {
	return CheckResult{Name: name, Status: StatusFail, Message: message, Reason: reason, Actions: actions}
}

// skip creates a skipped CheckResult.
func skip(name, message, reason string) CheckResult {
	return CheckResult{Name: name, Status: StatusSkip, Message: message, Reason: reason}
}

// relPath returns a forward-slash relative path from siteDir.
func relPath(siteDir, path string) string {
	rel, err := filepath.Rel(siteDir, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// fileExists checks if a path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dirExists checks if a path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// moveFile moves src to dst, creating destination directory as needed.
// Returns an error if dst already exists.
func moveFile(src, dst string) error {
	if fileExists(dst) {
		return fmt.Errorf("destination already exists: %s", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// moveDir moves an entire directory from src to dst.
func moveDir(src, dst string) error {
	if fileExists(dst) {
		return fmt.Errorf("destination already exists: %s", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// isDirEmpty checks if a directory contains no entries.
func isDirEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) == 0
}
