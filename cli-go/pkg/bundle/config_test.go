package bundle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFQN_HappyCases(t *testing.T) {
	cases := []struct {
		input string
		want  FQN
	}{
		{"pub.polis.themes.vice", FQN{Namespace: "pub.polis", Kind: "themes", Name: "vice"}},
		{"pub.polis.shapes.v3", FQN{Namespace: "pub.polis", Kind: "shapes", Name: "v3"}},
		{"com.alice.themes.cool", FQN{Namespace: "com.alice", Kind: "themes", Name: "cool"}},
		{"com.alice.shapes.stream", FQN{Namespace: "com.alice", Kind: "shapes", Name: "stream"}},
	}
	for _, c := range cases {
		got, err := ParseFQN(c.input)
		if err != nil {
			t.Errorf("ParseFQN(%q): unexpected error %v", c.input, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseFQN(%q) = %+v, want %+v", c.input, got, c.want)
		}
	}
}

func TestParseFQN_RoundTrip(t *testing.T) {
	cases := []string{
		"pub.polis.themes.vice",
		"pub.polis.shapes.v3",
		"com.alice.bob.themes.cool", // multi-segment namespace
	}
	for _, c := range cases {
		fqn, err := ParseFQN(c)
		if err != nil {
			t.Errorf("ParseFQN(%q) failed: %v", c, err)
			continue
		}
		if fqn.String() != c {
			t.Errorf("FQN.String() = %q, want %q", fqn.String(), c)
		}
	}
}

func TestParseFQN_RejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"bare",
		"no.kind.here",
		"pub.polis.themes.", // empty name
		".themes.foo",       // empty namespace
	}
	for _, c := range cases {
		_, err := ParseFQN(c)
		if err == nil {
			t.Errorf("ParseFQN(%q): expected error, got nil", c)
		}
	}
}

func TestQualifyHelpers(t *testing.T) {
	if QualifyTheme("vice") != "pub.polis.themes.vice" {
		t.Error("QualifyTheme failed")
	}
	if QualifyShape("v3") != "pub.polis.shapes.v3" {
		t.Error("QualifyShape failed")
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	siteDir := t.TempDir()
	reg := DefaultRegistry()
	reg.ActiveTheme = "pub.polis.themes.vice"
	if err := SaveRegistry(siteDir, reg); err != nil {
		t.Fatalf("SaveRegistry: %v", err)
	}
	loaded, err := LoadRegistry(siteDir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if loaded.ActiveTheme != "pub.polis.themes.vice" {
		t.Errorf("ActiveTheme = %q, want pub.polis.themes.vice", loaded.ActiveTheme)
	}
	if loaded.ActiveShape != "pub.polis.shapes.v4" {
		t.Errorf("ActiveShape = %q, want pub.polis.shapes.v4", loaded.ActiveShape)
	}
	if len(loaded.InstalledBundles) != 1 || loaded.InstalledBundles[0].Name != "pub.polis.core" {
		t.Errorf("InstalledBundles unexpected: %+v", loaded.InstalledBundles)
	}
}

func TestRegistryAtomicWrite(t *testing.T) {
	siteDir := t.TempDir()
	reg := DefaultRegistry()
	if err := SaveRegistry(siteDir, reg); err != nil {
		t.Fatalf("SaveRegistry: %v", err)
	}
	// No .tmp file should remain after a successful save.
	tmp := RegistryPath(siteDir) + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("expected no .tmp file, got: %v", err)
	}
}

func TestLoadRegistryMissing(t *testing.T) {
	siteDir := t.TempDir()
	_, err := LoadRegistry(siteDir)
	if err == nil {
		t.Fatal("expected error when registry.json is missing")
	}
}

// LoadRegistryOrInit is the safe-fallback helper used by the four
// mutator call sites (EnsureReferencePayload, SetActiveThemeName,
// SetActiveShapeName, MigrateActiveThemeToRegistry) post-2026-04-29.
// Critical contract: fall back to DefaultRegistry ONLY for genuinely-
// missing files; surface any other read/parse error so the caller
// doesn't silently overwrite a present-but-broken registry. The
// discover.polis.pub regression (root-owned 0600 file → polis service
// got permission-denied → silent fallback wiped active_theme/
// active_shape) made this gap a high-priority fix.

func TestLoadRegistryOrInit_MissingFile(t *testing.T) {
	siteDir := t.TempDir()
	reg, err := LoadRegistryOrInit(siteDir)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if reg == nil || reg.ActiveShape != "pub.polis.shapes.v4" {
		t.Errorf("missing file should yield DefaultRegistry (v4 post-cutover); got %+v", reg)
	}
}

func TestLoadRegistryOrInit_MalformedJSON(t *testing.T) {
	siteDir := t.TempDir()
	regDir := filepath.Join(siteDir, ".polis", "bundles")
	if err := os.MkdirAll(regDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Trailing comma — json.Unmarshal will reject. Pre-fix, the silent
	// fallback would have returned DefaultRegistry and the caller
	// would have overwritten the user's broken edits with v3 + no theme.
	if err := os.WriteFile(filepath.Join(regDir, "registry.json"), []byte(`{"active_theme":"pub.polis.themes.sols",}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadRegistryOrInit(siteDir)
	if err == nil {
		t.Fatal("malformed JSON must propagate as error, not silently fall back to DefaultRegistry")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("error should NOT classify as ErrNotExist on malformed JSON: %v", err)
	}
}

func TestLoadRegistryOrInit_PermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks; skip in root-suid test environments")
	}
	siteDir := t.TempDir()
	regDir := filepath.Join(siteDir, ".polis", "bundles")
	if err := os.MkdirAll(regDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	regPath := filepath.Join(regDir, "registry.json")
	if err := os.WriteFile(regPath, []byte(`{"active_theme":"pub.polis.themes.sols"}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Strip read permission. This emulates the discover.polis.pub
	// regression: file present but unreadable for the polis service
	// user. The silent-fallback bug would have swallowed this and
	// overwritten the file with DefaultRegistry; the fix surfaces it
	// so the caller (Medic) emits the error in its medic.fix detail
	// instead of silently destroying state.
	if err := os.Chmod(regPath, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(regPath, 0644) // best-effort cleanup so t.TempDir teardown can rm

	_, err := LoadRegistryOrInit(siteDir)
	if err == nil {
		t.Fatal("permission-denied read must propagate, not silently fall back")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("permission error should NOT classify as ErrNotExist: %v", err)
	}
}

// --- Migration tests ---

func writeWellKnown(t *testing.T, siteDir string, fields map[string]interface{}) {
	t.Helper()
	wkDir := filepath.Join(siteDir, ".well-known")
	os.MkdirAll(wkDir, 0755)
	data, _ := json.MarshalIndent(fields, "", "  ")
	os.WriteFile(filepath.Join(wkDir, "polis"), data, 0644)
}

func readWellKnown(t *testing.T, siteDir string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis"))
	if err != nil {
		t.Fatalf("read well-known: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse well-known: %v", err)
	}
	return m
}

func TestMigrateActiveThemeToRegistry_HappyPath(t *testing.T) {
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, map[string]interface{}{
		"public_key":   "ssh-ed25519 AAAA...",
		"active_theme": "vice",
		"author":       "alice",
	})
	if err := MigrateActiveThemeToRegistry(siteDir); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	reg, err := LoadRegistry(siteDir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if reg.ActiveTheme != "pub.polis.themes.vice" {
		t.Errorf("ActiveTheme = %q, want pub.polis.themes.vice", reg.ActiveTheme)
	}
	if reg.ActiveShape != "pub.polis.shapes.v4" {
		t.Errorf("ActiveShape = %q, want pub.polis.shapes.v4 (post-cutover default)", reg.ActiveShape)
	}

	wk := readWellKnown(t, siteDir)
	if _, ok := wk["active_theme"]; ok {
		t.Error("expected active_theme stripped from well-known")
	}
	if wk["author"] != "alice" {
		t.Error("expected other well-known fields preserved")
	}
}

func TestMigrateActiveThemeToRegistry_Idempotent(t *testing.T) {
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, map[string]interface{}{
		"public_key":   "ssh-ed25519 AAAA...",
		"active_theme": "vice",
	})
	if err := MigrateActiveThemeToRegistry(siteDir); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Second run should be no-op (active_theme already gone, registry exists).
	if err := MigrateActiveThemeToRegistry(siteDir); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	reg, _ := LoadRegistry(siteDir)
	if reg.ActiveTheme != "pub.polis.themes.vice" {
		t.Errorf("ActiveTheme changed on second migration: %q", reg.ActiveTheme)
	}
}

func TestMigrateActiveThemeToRegistry_NoActiveTheme(t *testing.T) {
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, map[string]interface{}{
		"public_key": "ssh-ed25519 AAAA...",
	})
	if err := MigrateActiveThemeToRegistry(siteDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := LoadRegistry(siteDir); err == nil {
		t.Error("expected no registry created when there's nothing to migrate")
	}
}

func TestMigrateActiveThemeToRegistry_NoWellKnown(t *testing.T) {
	siteDir := t.TempDir()
	if err := MigrateActiveThemeToRegistry(siteDir); err != nil {
		t.Fatalf("migrate with no well-known: %v", err)
	}
}

func TestMigrateActiveThemeToRegistry_PreservesExistingRegistryTheme(t *testing.T) {
	siteDir := t.TempDir()
	// Pre-existing registry with a different active_theme.
	preExisting := DefaultRegistry()
	preExisting.ActiveTheme = "pub.polis.themes.especial-light"
	if err := SaveRegistry(siteDir, preExisting); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
	// Well-known has a stale legacy value.
	writeWellKnown(t, siteDir, map[string]interface{}{"active_theme": "vice"})

	if err := MigrateActiveThemeToRegistry(siteDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	reg, _ := LoadRegistry(siteDir)
	if reg.ActiveTheme != "pub.polis.themes.especial-light" {
		t.Errorf("registry's active_theme overwritten: %q", reg.ActiveTheme)
	}
	wk := readWellKnown(t, siteDir)
	if _, ok := wk["active_theme"]; ok {
		t.Error("legacy active_theme should still be stripped")
	}
}

// --- GetActiveThemeName fallback tests ---

func TestGetActiveThemeName_PrefersRegistry(t *testing.T) {
	siteDir := t.TempDir()
	reg := DefaultRegistry()
	reg.ActiveTheme = "pub.polis.themes.vice"
	SaveRegistry(siteDir, reg)
	writeWellKnown(t, siteDir, map[string]interface{}{"active_theme": "especial-light"})

	got, err := GetActiveThemeName(siteDir)
	if err != nil {
		t.Fatalf("GetActiveThemeName: %v", err)
	}
	if got != "vice" {
		t.Errorf("got %q, want vice (registry wins)", got)
	}
}

func TestGetActiveThemeName_LegacyFallback(t *testing.T) {
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, map[string]interface{}{"active_theme": "especial-light"})

	got, err := GetActiveThemeName(siteDir)
	if err != nil {
		t.Fatalf("GetActiveThemeName: %v", err)
	}
	if got != "especial-light" {
		t.Errorf("got %q, want especial-light (legacy fallback)", got)
	}
}

func TestGetActiveThemeName_EmptyWhenNeither(t *testing.T) {
	siteDir := t.TempDir()
	got, err := GetActiveThemeName(siteDir)
	if err != nil {
		t.Fatalf("GetActiveThemeName: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty when no source has active_theme", got)
	}
}

func TestSetActiveThemeName_WritesRegistry(t *testing.T) {
	siteDir := t.TempDir()
	if err := SetActiveThemeName(siteDir, "vice"); err != nil {
		t.Fatalf("SetActiveThemeName: %v", err)
	}
	reg, err := LoadRegistry(siteDir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if reg.ActiveTheme != "pub.polis.themes.vice" {
		t.Errorf("ActiveTheme = %q, want pub.polis.themes.vice", reg.ActiveTheme)
	}
}

func TestGetActiveShapeName_DefaultsToStream(t *testing.T) {
	siteDir := t.TempDir()
	got, err := GetActiveShapeName(siteDir)
	if err != nil {
		t.Fatalf("GetActiveShapeName: %v", err)
	}
	if got != "v4" {
		t.Errorf("got %q, want v4 (post-cutover default)", got)
	}
}

func TestGetActiveShapeName_FromRegistry(t *testing.T) {
	siteDir := t.TempDir()
	reg := DefaultRegistry()
	reg.ActiveShape = "pub.polis.shapes.v4"
	SaveRegistry(siteDir, reg)
	got, _ := GetActiveShapeName(siteDir)
	if got != "v4" {
		t.Errorf("got %q, want v4", got)
	}
}

// ============================================================================
// LoadRegistryRaw Tests (F10)
// ============================================================================

func TestLoadRegistryRaw_NotExist(t *testing.T) {
	siteDir := t.TempDir()
	raw, err := LoadRegistryRaw(siteDir)
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if raw != nil {
		t.Errorf("raw = %v, want nil for missing file", raw)
	}
}

func TestLoadRegistryRaw_ValidJSON(t *testing.T) {
	siteDir := t.TempDir()
	reg := DefaultRegistry()
	reg.ActiveTheme = "pub.polis.themes.vice"
	if err := SaveRegistry(siteDir, reg); err != nil {
		t.Fatalf("SaveRegistry: %v", err)
	}
	raw, err := LoadRegistryRaw(siteDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == nil {
		t.Fatal("raw should not be nil on valid JSON")
	}
	if raw["active_theme"] != "pub.polis.themes.vice" {
		t.Errorf("active_theme = %v, want pub.polis.themes.vice", raw["active_theme"])
	}
}

func TestLoadRegistryRaw_MalformedJSON(t *testing.T) {
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Dir(RegistryPath(siteDir)), 0700)
	os.WriteFile(RegistryPath(siteDir), []byte("not valid{"), 0600)

	raw, err := LoadRegistryRaw(siteDir)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if raw != nil {
		t.Errorf("raw = %v, want nil on parse error", raw)
	}
}

// ============================================================================
// SetActiveShapeName Tests (F2 prerequisite)
// ============================================================================

func TestSetActiveShapeName_WritesRegistry(t *testing.T) {
	siteDir := t.TempDir()
	if err := SetActiveShapeName(siteDir, "v3"); err != nil {
		t.Fatalf("SetActiveShapeName: %v", err)
	}
	reg, err := LoadRegistry(siteDir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if reg.ActiveShape != "pub.polis.shapes.v3" {
		t.Errorf("ActiveShape = %q, want pub.polis.shapes.v3", reg.ActiveShape)
	}
}

func TestSetActiveShapeName_PreservesOtherFields(t *testing.T) {
	siteDir := t.TempDir()
	reg := DefaultRegistry()
	reg.ActiveTheme = "pub.polis.themes.vice"
	if err := SaveRegistry(siteDir, reg); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SetActiveShapeName(siteDir, "v4"); err != nil {
		t.Fatalf("SetActiveShapeName: %v", err)
	}
	loaded, _ := LoadRegistry(siteDir)
	if loaded.ActiveTheme != "pub.polis.themes.vice" {
		t.Errorf("ActiveTheme changed: %q", loaded.ActiveTheme)
	}
	if loaded.ActiveShape != "pub.polis.shapes.v4" {
		t.Errorf("ActiveShape = %q", loaded.ActiveShape)
	}
}

func TestSetActiveShapeName_RejectsEmpty(t *testing.T) {
	siteDir := t.TempDir()
	if err := SetActiveShapeName(siteDir, ""); err == nil {
		t.Error("expected error for empty shape name")
	}
}

func TestParseFQN_BundleNameWithDots(t *testing.T) {
	// Multi-segment bundle namespace works as long as it doesn't contain
	// the reserved substrings.
	fqn, err := ParseFQN("com.alice.cool.themes.fancy")
	if err != nil {
		t.Fatalf("ParseFQN: %v", err)
	}
	if fqn.Namespace != "com.alice.cool" {
		t.Errorf("Namespace = %q", fqn.Namespace)
	}
	if !strings.HasSuffix(fqn.Name, "fancy") {
		t.Errorf("Name = %q", fqn.Name)
	}
}
