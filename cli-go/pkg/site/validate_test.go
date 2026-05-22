package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidate_ScrubsAbsolutePaths — R18-12 regression guard. The
// ValidationResult emitted by Validate must NOT contain absolute
// hosted filesystem paths in error Path fields. Pre-fix /api/status
// returned errors carrying `/data/tenants/<handle>/.well-known/polis`
// etc., revealing the hosted directory structure to any owner session
// (and any future XSS-style attack against the SPA).
//
// The scrub rewrites Path to be relative to siteDir. The Code +
// Message + Suggestion already identify the file; the absolute path
// added no value to legitimate callers.
func TestValidate_ScrubsAbsolutePaths(t *testing.T) {
	// Build a site dir that's INCOMPLETE — missing keys + well-known
	// — so multiple ValidationError entries get populated with file
	// paths we can inspect.
	tempDir := t.TempDir()
	// Need the dir itself to exist for the Validate happy-path entry,
	// but no .well-known or keys → drives error population.
	if err := os.MkdirAll(filepath.Join(tempDir, ".polis"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	result := Validate(tempDir)
	if result == nil {
		t.Fatal("Validate returned nil result")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected validation errors for incomplete site; got none")
	}

	// Every error Path must be relative — no absolute prefix.
	for i, e := range result.Errors {
		if e.Path == "" {
			continue // empty path is fine
		}
		if filepath.IsAbs(e.Path) {
			t.Errorf("errors[%d] (%s): Path is absolute (%q); R18-12 expects relative",
				i, e.Code, e.Path)
		}
		// Specifically the tempDir prefix must not appear.
		if strings.Contains(e.Path, tempDir) {
			t.Errorf("errors[%d] (%s): Path contains absolute siteDir prefix (%q); R18-12 regression",
				i, e.Code, e.Path)
		}
	}

	// JSON shape — the field that gets returned to clients must not
	// carry the absolute siteDir anywhere either.
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(jsonBytes), tempDir) {
		t.Errorf("R18-12: serialized ValidationResult still contains absolute tempDir path %q\nbody: %s", tempDir, jsonBytes)
	}
}

// TestValidate_PathRemainsUsableAfterScrub — the scrub must keep
// enough information for the caller to identify which file the error
// is about (relative paths like `.well-known/polis`,
// `.polis/keys/id_ed25519`).
func TestValidate_PathRemainsUsableAfterScrub(t *testing.T) {
	tempDir := t.TempDir()
	os.MkdirAll(filepath.Join(tempDir, ".polis"), 0700)
	result := Validate(tempDir)

	wantPaths := map[string]string{
		"PRIVATE_KEY_MISSING": filepath.Join(".polis", "keys", "id_ed25519"),
		"PUBLIC_KEY_MISSING":  filepath.Join(".polis", "keys", "id_ed25519.pub"),
		"WELLKNOWN_MISSING":   filepath.Join(".well-known", "polis"),
	}
	got := make(map[string]string)
	for _, e := range result.Errors {
		got[e.Code] = e.Path
	}
	for code, expected := range wantPaths {
		if got[code] != expected {
			t.Errorf("error %s: Path = %q, want %q", code, got[code], expected)
		}
	}
}
