// Package tailor diagnoses and auto-fixes polis sites to bring them up to current spec.
// It understands every historical layout and can upgrade from any version.
package tailor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
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
	Message string      `json:"message"`           // what happened / what would happen
	Reason  string      `json:"reason"`            // why — references the version that introduced the change
	Actions []Action    `json:"actions,omitempty"` // specific file operations performed/planned

	// Outcome is set in apply mode on a check that found something: "fixed"
	// (a dry run afterwards finds nothing left to apply), "failed" (it still
	// does — the apply did not take), or "reported" (nothing to apply: the
	// check only reports). ⭐ It is decided by re-checking, not by the check's
	// own word, so no check can claim a fix that did not happen.
	Outcome string `json:"outcome,omitempty"`
}

// Outcomes of an applied check.
const (
	OutcomeFixed    = "fixed"
	OutcomeFailed   = "failed"
	OutcomeReported = "reported"
)

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

	// Apply mode only: how the failed checks came out.
	Fixed      int `json:"fixed,omitempty"`
	NotApplied int `json:"not_applied,omitempty"`
	Reported   int `json:"reported,omitempty"`
}

// runContext carries shared state extracted before checks modify the site.
type runContext struct {
	siteDir   string
	dryRun    bool
	baseURL   string // extracted from .well-known/polis or POLIS_BASE_URL before legacy cleanup
	generator string // e.g. "polis-cli-go/0.59.0"
	backupDir string // Apply mode: where the run backs up files before a check changes them ("" in Diagnose); created on first use

	backedUp map[string]bool // site-relative paths already copied in this run
	manifest Manifest        // what this apply run did, written into backupDir
}

// checkFunc is the signature for individual check functions.
// dryRun=true means diagnose only; dryRun=false means apply fixes.
type checkFunc func(ctx *runContext) CheckResult

// extractBaseURL reads the base URL from .well-known/polis (legacy fields) or env.
// Called once before checks run, since legacy config check removes these fields.
func extractBaseURL(siteDir string) string {
	// Environment variable takes precedence
	if url := os.Getenv("POLIS_BASE_URL"); url != "" {
		return url
	}

	// Read from .well-known/polis before legacy cleanup removes it
	wkPath := filepath.Join(siteDir, ".well-known", "polis")
	data, err := storage.ReadAtPath(wkPath)
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
	return run(siteDir, true, "", gen, nil)
}

// Apply runs all checks and fixes issues, creating a backup first.
// Optional generator overrides the package-level default.
func Apply(siteDir string, generator ...string) *Result {
	gen := GetGenerator()
	if len(generator) > 0 && generator[0] != "" {
		gen = generator[0]
	}
	return run(siteDir, false, newBackupDir(siteDir), gen, nil)
}

// Options selects which checks a run takes. The zero value is every check.
type Options struct {
	Only []string // run only these ids (in run order)
	Skip []string // run every check except these
}

// SelectionError is a selection that names an unknown check, uses both lists,
// or leaves out a check another selected check needs. Nothing was changed.
type SelectionError struct{ Msg string }

func (e *SelectionError) Error() string { return e.Msg }

// selector returns which check ids the options select, or an error naming an
// unknown id or a contradictory selection.
func (o Options) selector() (map[string]bool, error) {
	if len(o.Only) > 0 && len(o.Skip) > 0 {
		return nil, &SelectionError{"--only and --skip cannot be used together"}
	}
	known := map[string]bool{}
	for _, d := range checkDefs() {
		known[d.ID] = true
	}
	var unknown []string
	for _, id := range append(append([]string{}, o.Only...), o.Skip...) {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return nil, &SelectionError{"unknown check id(s): " + strings.Join(unknown, ", ") + " (tailor --list shows them)"}
	}
	sel := map[string]bool{}
	for id := range known {
		sel[id] = len(o.Only) == 0
	}
	for _, id := range o.Only {
		sel[id] = true
	}
	for _, id := range o.Skip {
		sel[id] = false
	}
	return sel, nil
}

// DiagnoseWith is Diagnose limited to the selected checks.
func DiagnoseWith(siteDir string, o Options) (*Result, error) {
	sel, err := o.selector()
	if err != nil {
		return nil, err
	}
	return run(siteDir, true, "", GetGenerator(), sel), nil
}

// ApplyWith applies the selected checks. ⛔ It refuses, changing nothing, when
// a selected check depends on one left out that does not already pass on this
// site: applying a check whose prerequisite has not happened is how a
// cherry-picked upgrade reaches a state no full run could produce.
func ApplyWith(siteDir string, o Options) (*Result, error) {
	sel, err := o.selector()
	if err != nil {
		return nil, err
	}
	if err := unmetDependencies(siteDir, sel); err != nil {
		return nil, err
	}
	return run(siteDir, false, newBackupDir(siteDir), GetGenerator(), sel), nil
}

// unmetDependencies names every left-out prerequisite of a selected check that
// a dry run says has not happened on this site.
func unmetDependencies(siteDir string, sel map[string]bool) error {
	defs := checkDefs()
	byID := map[string]CheckDef{}
	for _, d := range defs {
		byID[d.ID] = d
	}
	ctx := &runContext{siteDir: siteDir, dryRun: true, baseURL: extractBaseURL(siteDir), generator: GetGenerator()}
	status := map[string]CheckStatus{}
	var missing []string
	for _, d := range defs {
		if !sel[d.ID] {
			continue
		}
		for _, dep := range d.DependsOn {
			if sel[dep] {
				continue
			}
			st, seen := status[dep]
			if !seen {
				st = byID[dep].fn(ctx).Status
				status[dep] = st
			}
			// A skip means the prerequisite could not be evaluated (a read
			// failed, say), which is not evidence that it happened.
			if st != StatusPass {
				missing = append(missing, d.ID+" needs "+dep)
			}
		}
	}
	if len(missing) > 0 {
		return &SelectionError{"refused, nothing changed: a selected check needs one you left out that has not been applied yet: " +
			strings.Join(missing, "; ") + ". Add it to --only (or drop it from --skip), or apply it first."}
	}
	return nil
}

func run(siteDir string, dryRun bool, backupDir string, generator string, sel map[string]bool) *Result {
	mode := "diagnose"
	if !dryRun {
		mode = "apply"
	}

	result := &Result{
		SiteDir: siteDir,
		Mode:    mode,
	}

	// Verify .well-known/polis exists — if not, most checks are meaningless
	wkPath := filepath.Join(siteDir, ".well-known", "polis")
	if _, err := storage.StatAtPath(wkPath); errors.Is(err, fs.ErrNotExist) {
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
		backupDir: backupDir,
		manifest:  Manifest{Generator: generator, SiteDir: siteDir, Started: time.Now().UTC().Format(time.RFC3339)},
	}

	for _, def := range checkDefs() {
		if sel != nil && !sel[def.ID] {
			continue
		}
		check := def.fn
		if dryRun {
			result.Checks = append(result.Checks, check(ctx))
			continue
		}
		result.Checks = append(result.Checks, applyWithBackup(ctx, check))
	}

	if !dryRun {
		exists, err := ctx.writeManifest()
		if exists {
			result.BackupDir = backupDir
		}
		if err != nil {
			result.Checks = append(result.Checks, CheckResult{
				Name: "backup-manifest", Status: StatusFail,
				Message: fmt.Sprintf("could not write the backup manifest: %v", err),
				Reason:  "the manifest records every change this run made, so it can be undone by hand",
			})
		}
	}

	// Compute summary
	for _, cr := range result.Checks {
		result.Summary.Total++
		switch cr.Status {
		case StatusPass:
			result.Summary.Passed++
		case StatusFail:
			result.Summary.Failed++
			switch cr.Outcome {
			case OutcomeFixed:
				result.Summary.Fixed++
			case OutcomeFailed:
				result.Summary.NotApplied++
			case OutcomeReported:
				result.Summary.Reported++
			}
		case StatusSkip:
			result.Summary.Skipped++
		}
	}

	return result
}

// applyWithBackup applies one check after backing up everything its plan would
// rewrite or remove. ⛔ If the backup fails, the check does not apply at all.
func applyWithBackup(ctx *runContext, check checkFunc) CheckResult {
	probe := *ctx
	probe.dryRun = true
	plan := check(&probe)

	paths := backupPathsFor(ctx.siteDir, plan)
	for _, p := range paths {
		if err := ctx.backupPath(p); err != nil {
			return CheckResult{
				Name:    plan.Name,
				Status:  StatusFail,
				Message: fmt.Sprintf("not applied: could not back up %s first: %v", p, err),
				Reason:  plan.Reason,
				Actions: plan.Actions,
				Outcome: OutcomeFailed,
			}
		}
	}
	cr := check(ctx)
	ctx.recordApplied(cr, paths)
	if cr.Status == StatusFail {
		cr.Outcome = outcomeOf(plan, check(&probe))
	}
	return cr
}

// outcomeOf decides what an apply achieved from the plan before it and a dry
// run after it.
func outcomeOf(before, after CheckResult) string {
	changes := func(r CheckResult) bool {
		for _, a := range r.Actions {
			if a.Op != "flag" {
				return true
			}
		}
		return false
	}
	switch {
	case after.Status != StatusFail:
		return OutcomeFixed
	case changes(after):
		return OutcomeFailed
	case changes(before):
		return OutcomeFixed // what could be applied was; what is left only reports
	default:
		return OutcomeReported
	}
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
	_, err := storage.StatAtPath(path)
	return err == nil
}

// dirExists checks if a path exists and is a directory.
func dirExists(path string) bool {
	info, err := storage.StatAtPath(path)
	return err == nil && info.IsDir
}

// moveFile moves src to dst, creating destination directory as needed.
// Returns an error if dst already exists.
func moveFile(src, dst string) error {
	if fileExists(dst) {
		return fmt.Errorf("destination already exists: %s", dst)
	}
	// Stays: paired with the os.Rename below. The interface has no rename, because a
	// copy-then-delete is not the same operation.
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil { //nolint:gosec // G301: public tree only; .polis moves go through movePrivateFile at 0700
		return err
	}
	return os.Rename(src, dst)
}

// movePrivateFile moves src to dst under .polis, creating missing parents at
// 0700 (moveFile's 0755 would widen a private tree). Refuses an existing dst.
func movePrivateFile(src, dst string) error {
	if fileExists(dst) {
		return fmt.Errorf("destination already exists: %s", dst)
	}
	// Stays: paired with the os.Rename below, as in moveFile.
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// moveDir moves an entire directory from src to dst.
func moveDir(src, dst string) error {
	if fileExists(dst) {
		return fmt.Errorf("destination already exists: %s", dst)
	}
	// Stays: paired with the os.Rename below. The interface has no rename, because a
	// copy-then-delete is not the same operation.
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil { //nolint:gosec // G301: public tree only; .polis moves go through movePrivateFile at 0700
		return err
	}
	return os.Rename(src, dst)
}

// isDirEmpty checks if a directory contains no entries.
func isDirEmpty(path string) bool {
	entries, err := storage.ListDir(path)
	if err != nil {
		return false
	}
	return len(entries) == 0
}
