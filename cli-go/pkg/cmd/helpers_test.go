package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
)

// Tests for pure helpers in the cmd package. The handlers themselves
// (handleFollow, handleUnfollow, etc.) call exitError → os.Exit when
// validation fails, which makes direct testing impractical without a
// refactor. These tests target the underlying helpers — same code
// paths, just isolated from the os.Exit failure mode.

// ============================================================================
// extractDomain (about.go) — used by several commands to derive
// `<domain>` from a `https://<domain>/<path>` URL string.
// ============================================================================

func TestExtractDomain_HTTPSWithPath(t *testing.T) {
	got := extractDomain("https://alice.polis.pub/posts/hello.md")
	if got != "alice.polis.pub" {
		t.Errorf("got %q, want alice.polis.pub", got)
	}
}

func TestExtractDomain_HTTPSDomainOnly(t *testing.T) {
	got := extractDomain("https://alice.polis.pub")
	if got != "alice.polis.pub" {
		t.Errorf("got %q, want alice.polis.pub", got)
	}
}

func TestExtractDomain_HTTP(t *testing.T) {
	got := extractDomain("http://alice.example/x")
	if got != "alice.example" {
		t.Errorf("got %q, want alice.example", got)
	}
}

func TestExtractDomain_NoProtocol(t *testing.T) {
	// No scheme — returns the input up to the first /
	got := extractDomain("alice.example/x")
	if got != "alice.example" {
		t.Errorf("got %q, want alice.example", got)
	}
}

func TestExtractDomain_NoPath(t *testing.T) {
	got := extractDomain("alice.example")
	if got != "alice.example" {
		t.Errorf("got %q, want alice.example", got)
	}
}

func TestExtractDomain_Empty(t *testing.T) {
	got := extractDomain("")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ============================================================================
// countFiles (about.go) — used by `polis about` for stats. Walks a
// directory tree counting files with a given extension, skipping
// .versions subdirs.
// ============================================================================

func TestCountFiles_CountsByExtension(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.md":               "",
		"b.md":               "",
		"c.html":             "",
		"sub/d.md":           "",
		"sub/deep/e.md":      "",
		"sub/deep/f.txt":     "",
	})

	if got := countFiles(dir, ".md"); got != 4 {
		t.Errorf(".md count = %d, want 4", got)
	}
	if got := countFiles(dir, ".html"); got != 1 {
		t.Errorf(".html count = %d, want 1", got)
	}
	if got := countFiles(dir, ".tsx"); got != 0 {
		t.Errorf(".tsx count = %d, want 0", got)
	}
}

func TestCountFiles_SkipsVersionsDir(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"current.md":             "",
		".versions/old1.md":      "",
		".versions/old2.md":      "",
		"sub/now.md":             "",
		"sub/.versions/older.md": "",
	})

	// Only `current.md` and `sub/now.md` should count. The .versions
	// subdir contents are excluded.
	if got := countFiles(dir, ".md"); got != 2 {
		t.Errorf(".md count = %d, want 2 (must skip .versions/)", got)
	}
}

func TestCountFiles_MissingDirReturnsZero(t *testing.T) {
	if got := countFiles("/no/such/path/anywhere", ".md"); got != 0 {
		t.Errorf("missing dir count = %d, want 0", got)
	}
}

// ============================================================================
// isPolisSite + getBaseURLFromSite (render.go)
// ============================================================================

func TestIsPolisSite_True(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	if !isPolisSite(dir) {
		t.Error("expected isPolisSite=true when .well-known/polis exists")
	}
}

func TestIsPolisSite_False(t *testing.T) {
	if isPolisSite(t.TempDir()) {
		t.Error("expected isPolisSite=false for empty dir")
	}
}

func TestGetBaseURLFromSite_Present(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), []byte(`{"base_url":"https://alice.example"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if got := getBaseURLFromSite(dir); got != "https://alice.example" {
		t.Errorf("got %q, want https://alice.example", got)
	}
}

func TestGetBaseURLFromSite_MissingFileReturnsEmpty(t *testing.T) {
	if got := getBaseURLFromSite(t.TempDir()); got != "" {
		t.Errorf("got %q, want empty for missing site", got)
	}
}

func TestGetBaseURLFromSite_MalformedJSONReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), []byte(`{garbage`), 0644); err != nil {
		t.Fatal(err)
	}
	if got := getBaseURLFromSite(dir); got != "" {
		t.Errorf("got %q, want empty for malformed JSON", got)
	}
}

// ============================================================================
// nilIfEmpty (preview.go) — JSON-output helper
// ============================================================================

func TestNilIfEmpty_EmptyReturnsNil(t *testing.T) {
	if v := nilIfEmpty(""); v != nil {
		t.Errorf("nilIfEmpty(\"\") = %v, want nil", v)
	}
}

func TestNilIfEmpty_NonEmptyReturnsString(t *testing.T) {
	if v := nilIfEmpty("hello"); v != "hello" {
		t.Errorf("nilIfEmpty(%q) = %v, want %q", "hello", v, "hello")
	}
}

// ============================================================================
// loadEnvFile — only the malformed-line case (the basic/quoted/no-
// override/missing cases are already covered in root_test.go)
// ============================================================================

func TestLoadEnvFile_MalformedLinesSkipped(t *testing.T) {
	const key = "POLIS_TEST_MALFORMED_KEY_XYZ"
	os.Unsetenv(key)
	defer os.Unsetenv(key)

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "not-a-kv-line\nalso bad\n" + key + "=good\nmore bad lines\n"
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	loadEnvFile(envPath)
	if got := os.Getenv(key); got != "good" {
		t.Errorf("expected loader to skip malformed lines and pick up the valid one; got %q", got)
	}
}

// ============================================================================
// filterDMPolicies + policySliceToMaps (dm.go)
// ============================================================================

func TestFilterDMPolicies_KeepsOnlyDMRules(t *testing.T) {
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "rules.jsonl")
	content := `{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":false,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"allow pub.polis.comment from following"}
`
	if err := os.WriteFile(rulesPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got := filterDMPolicies(rulesPath)
	if len(got) != 2 {
		t.Fatalf("DM policy count = %d, want 2", len(got))
	}
	// Order preserved
	if !strings.Contains(got[0].Rule, "allow pub.polis.dm") {
		t.Errorf("first DM rule unexpected: %q", got[0].Rule)
	}
	if !strings.Contains(got[1].Rule, "deny pub.polis.dm") {
		t.Errorf("second DM rule unexpected: %q", got[1].Rule)
	}
}

func TestFilterDMPolicies_MissingFileReturnsNil(t *testing.T) {
	if got := filterDMPolicies("/no/such/rules.jsonl"); got != nil {
		t.Errorf("expected nil for missing file, got %v", got)
	}
}

func TestFilterDMPolicies_MalformedLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "rules.jsonl")
	content := `{"active":true,"policy":"allow pub.polis.dm from following"}
not-valid-json
{"active":true,"policy":"deny pub.polis.dm from all"}
`
	if err := os.WriteFile(rulesPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got := filterDMPolicies(rulesPath)
	if len(got) != 2 {
		t.Errorf("malformed line should be skipped; got %d valid DM rules, want 2", len(got))
	}
}

func TestPolicySliceToMaps_PreservesActiveAndRule(t *testing.T) {
	in := []policy.Policy{
		{Active: true, Rule: "allow pub.polis.dm from following"},
		{Active: false, Rule: "deny pub.polis.dm from all"},
	}
	got := policySliceToMaps(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0]["active"] != true || got[0]["policy"] != "allow pub.polis.dm from following" {
		t.Errorf("first map wrong: %+v", got[0])
	}
	if got[1]["active"] != false || got[1]["policy"] != "deny pub.polis.dm from all" {
		t.Errorf("second map wrong: %+v", got[1])
	}
}

func TestPolicySliceToMaps_EmptyInput(t *testing.T) {
	got := policySliceToMaps(nil)
	if got == nil {
		t.Error("expected empty slice, not nil")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

// ============================================================================
// helpers
// ============================================================================

// writeFiles creates files relative to base. The values are file
// contents (empty string means a zero-byte file).
func writeFiles(t *testing.T, base string, files map[string]string) {
	t.Helper()
	for relPath, body := range files {
		full := filepath.Join(base, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
