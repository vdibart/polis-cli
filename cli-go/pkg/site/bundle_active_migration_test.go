package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// helpers ─────────────────────────────────────────────────────────────────

func writeWK(t *testing.T, siteDir string, fields map[string]interface{}) {
	t.Helper()
	wkDir := filepath.Join(siteDir, ".well-known")
	os.MkdirAll(wkDir, 0755)
	data, _ := json.MarshalIndent(fields, "", "  ")
	os.WriteFile(filepath.Join(wkDir, "polis"), data, 0644)
}

func writeReg(t *testing.T, siteDir string, fields map[string]interface{}) {
	t.Helper()
	regDir := filepath.Join(siteDir, ".polis", "bundles")
	os.MkdirAll(regDir, 0700)
	data, _ := json.MarshalIndent(fields, "", "  ")
	os.WriteFile(filepath.Join(regDir, "registry.json"), data, 0600)
}

func readWK(t *testing.T, siteDir string) map[string]interface{} {
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

func readReg(t *testing.T, siteDir string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(siteDir, ".polis", "bundles", "registry.json"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse registry: %v", err)
	}
	return m
}

// ── Detection ──────────────────────────────────────────────────────────

func TestLegacyBundleActiveFieldsExist_NoFiles(t *testing.T) {
	siteDir := t.TempDir()
	if LegacyBundleActiveFieldsExist(siteDir) {
		t.Error("expected false on empty site")
	}
}

func TestLegacyBundleActiveFieldsExist_WellKnownOnly(t *testing.T) {
	siteDir := t.TempDir()
	writeWK(t, siteDir, map[string]interface{}{
		"public_key": "x",
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]interface{}{
				"active": true,
				"path":   "content/pub.polis.core/bundle.json",
			},
		},
	})
	if !LegacyBundleActiveFieldsExist(siteDir) {
		t.Error("expected true when well-known has bundles.<name>.active")
	}
}

func TestLegacyBundleActiveFieldsExist_RegistryOnly(t *testing.T) {
	siteDir := t.TempDir()
	writeReg(t, siteDir, map[string]interface{}{
		"active_theme": "pub.polis.themes.vice",
		"installed_bundles": []interface{}{
			map[string]interface{}{
				"name":   "pub.polis.core",
				"path":   ".polis/bundles/pub.polis.core",
				"active": true,
			},
		},
	})
	if !LegacyBundleActiveFieldsExist(siteDir) {
		t.Error("expected true when registry has installed_bundles[].active")
	}
}

func TestLegacyBundleActiveFieldsExist_AlreadyClean(t *testing.T) {
	siteDir := t.TempDir()
	writeWK(t, siteDir, map[string]interface{}{
		"public_key": "x",
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]interface{}{
				"path": "content/pub.polis.core/bundle.json",
			},
		},
	})
	writeReg(t, siteDir, map[string]interface{}{
		"installed_bundles": []interface{}{
			map[string]interface{}{
				"name": "pub.polis.core",
				"path": ".polis/bundles/pub.polis.core",
			},
		},
	})
	if LegacyBundleActiveFieldsExist(siteDir) {
		t.Error("expected false when neither file has the active field")
	}
}

// ── Strip helper ───────────────────────────────────────────────────────

func TestStripBundleActiveFields_StripsBoth(t *testing.T) {
	siteDir := t.TempDir()
	writeWK(t, siteDir, map[string]interface{}{
		"public_key": "x",
		"author":     "alice",
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]interface{}{
				"active": true,
				"path":   "content/pub.polis.core/bundle.json",
			},
		},
	})
	writeReg(t, siteDir, map[string]interface{}{
		"active_theme": "pub.polis.themes.vice",
		"installed_bundles": []interface{}{
			map[string]interface{}{
				"name":   "pub.polis.core",
				"path":   ".polis/bundles/pub.polis.core",
				"active": true,
			},
		},
	})

	if err := StripBundleActiveFields(siteDir); err != nil {
		t.Fatalf("Strip: %v", err)
	}

	wk := readWK(t, siteDir)
	core := wk["bundles"].(map[string]interface{})["pub.polis.core"].(map[string]interface{})
	if _, ok := core["active"]; ok {
		t.Error("well-known bundle 'active' should be stripped")
	}
	if core["path"] != "content/pub.polis.core/bundle.json" {
		t.Error("well-known bundle 'path' should be preserved")
	}
	if wk["author"] != "alice" {
		t.Error("other well-known fields should be preserved")
	}

	reg := readReg(t, siteDir)
	bundles := reg["installed_bundles"].([]interface{})
	entry := bundles[0].(map[string]interface{})
	if _, ok := entry["active"]; ok {
		t.Error("registry installed_bundle 'active' should be stripped")
	}
	if entry["name"] != "pub.polis.core" || entry["path"] != ".polis/bundles/pub.polis.core" {
		t.Errorf("registry entry siblings should be preserved, got %+v", entry)
	}
	if reg["active_theme"] != "pub.polis.themes.vice" {
		t.Error("registry active_theme should be preserved")
	}
}

func TestStripBundleActiveFields_OnlyWellKnown(t *testing.T) {
	siteDir := t.TempDir()
	writeWK(t, siteDir, map[string]interface{}{
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]interface{}{"active": true, "path": "x"},
		},
	})

	if err := StripBundleActiveFields(siteDir); err != nil {
		t.Fatalf("Strip: %v", err)
	}
	wk := readWK(t, siteDir)
	core := wk["bundles"].(map[string]interface{})["pub.polis.core"].(map[string]interface{})
	if _, ok := core["active"]; ok {
		t.Error("well-known bundle 'active' should be stripped")
	}
}

func TestStripBundleActiveFields_OnlyRegistry(t *testing.T) {
	siteDir := t.TempDir()
	writeReg(t, siteDir, map[string]interface{}{
		"installed_bundles": []interface{}{
			map[string]interface{}{"name": "pub.polis.core", "path": "p", "active": true},
		},
	})

	if err := StripBundleActiveFields(siteDir); err != nil {
		t.Fatalf("Strip: %v", err)
	}
	reg := readReg(t, siteDir)
	entry := reg["installed_bundles"].([]interface{})[0].(map[string]interface{})
	if _, ok := entry["active"]; ok {
		t.Error("registry installed_bundle 'active' should be stripped")
	}
}

func TestStripBundleActiveFields_Idempotent(t *testing.T) {
	siteDir := t.TempDir()
	writeWK(t, siteDir, map[string]interface{}{
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]interface{}{"active": true, "path": "x"},
		},
	})
	writeReg(t, siteDir, map[string]interface{}{
		"installed_bundles": []interface{}{
			map[string]interface{}{"name": "pub.polis.core", "path": "p", "active": true},
		},
	})

	if err := StripBundleActiveFields(siteDir); err != nil {
		t.Fatalf("first strip: %v", err)
	}
	// Capture mtimes after first strip.
	wkInfo1, _ := os.Stat(filepath.Join(siteDir, ".well-known", "polis"))
	regInfo1, _ := os.Stat(filepath.Join(siteDir, ".polis", "bundles", "registry.json"))

	// Second pass should not re-write either file (no-op).
	if err := StripBundleActiveFields(siteDir); err != nil {
		t.Fatalf("second strip: %v", err)
	}
	wkInfo2, _ := os.Stat(filepath.Join(siteDir, ".well-known", "polis"))
	regInfo2, _ := os.Stat(filepath.Join(siteDir, ".polis", "bundles", "registry.json"))

	if !wkInfo2.ModTime().Equal(wkInfo1.ModTime()) {
		t.Error("well-known mtime changed on no-op second strip")
	}
	if !regInfo2.ModTime().Equal(regInfo1.ModTime()) {
		t.Error("registry mtime changed on no-op second strip")
	}
}

// TestStripBundleActiveFields_PreservesUnknownFields verifies the avatar-block
// lesson from step 1: raw-map mutation must not lose fields the WellKnown
// struct doesn't know about.
func TestStripBundleActiveFields_PreservesUnknownFields(t *testing.T) {
	siteDir := t.TempDir()
	writeWK(t, siteDir, map[string]interface{}{
		"public_key":   "x",
		"author":       "alice",
		"some_future_field": "preserve_me",
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]interface{}{
				"active":        true,
				"path":          "content/pub.polis.core/bundle.json",
				"future_sibling": "preserve_me_too",
			},
		},
	})

	if err := StripBundleActiveFields(siteDir); err != nil {
		t.Fatalf("Strip: %v", err)
	}
	wk := readWK(t, siteDir)
	if wk["some_future_field"] != "preserve_me" {
		t.Error("top-level unknown field should be preserved")
	}
	core := wk["bundles"].(map[string]interface{})["pub.polis.core"].(map[string]interface{})
	if core["future_sibling"] != "preserve_me_too" {
		t.Error("bundle entry sibling field should be preserved")
	}
}

func TestStripBundleActiveFields_NoFiles(t *testing.T) {
	siteDir := t.TempDir()
	if err := StripBundleActiveFields(siteDir); err != nil {
		t.Fatalf("Strip on empty site: %v", err)
	}
}
