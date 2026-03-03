package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func createTestTheme(t *testing.T, themeDir, themeName string) {
	t.Helper()
	dir := filepath.Join(themeDir, themeName)
	os.MkdirAll(dir, 0755)

	// Create required templates
	templates := map[string]string{
		"post.html":           "<html>{{title}}</html>",
		"comment.html":        "<html>{{title}}</html>",
		"comment-inline.html": "<div>{{content}}</div>",
		"index.html":          "<html>{{site_title}}</html>",
	}

	for name, content := range templates {
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
	}

	// Create CSS file
	os.WriteFile(filepath.Join(dir, themeName+".css"), []byte("body {}"), 0644)
}

func TestLoad(t *testing.T) {
	tempDir := t.TempDir()
	themesDir := filepath.Join(tempDir, "site", "themes")
	createTestTheme(t, themesDir, "turbo")

	templates, err := Load(tempDir, "", "turbo")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if templates.Post == "" {
		t.Error("Expected post template to be loaded")
	}

	if templates.Index == "" {
		t.Error("Expected index template to be loaded")
	}
}

func TestLoadFallbackToCLI(t *testing.T) {
	dataDir := t.TempDir()
	cliThemesDir := t.TempDir()

	// Only create theme in CLI dir (not local)
	createTestTheme(t, cliThemesDir, "sols")

	templates, err := Load(dataDir, cliThemesDir, "sols")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if templates.Post == "" {
		t.Error("Expected post template from CLI fallback")
	}
}

func TestLoadMissingTheme(t *testing.T) {
	tempDir := t.TempDir()

	_, err := Load(tempDir, "", "nonexistent")
	if err == nil {
		t.Error("Expected error for missing theme")
	}
}

func TestManifest(t *testing.T) {
	tempDir := t.TempDir()

	// Create .well-known directory and write polis config
	os.MkdirAll(filepath.Join(tempDir, ".well-known"), 0755)
	os.WriteFile(filepath.Join(tempDir, ".well-known", "polis"), []byte(`{"active_theme":"turbo","public_key":"test"}`), 0644)

	// Load manifest
	loaded, err := LoadManifest(tempDir)
	if err != nil {
		t.Fatalf("LoadManifest failed: %v", err)
	}

	if loaded.ActiveTheme != "turbo" {
		t.Errorf("Expected active_theme 'turbo', got '%s'", loaded.ActiveTheme)
	}
}

func TestGetActiveTheme(t *testing.T) {
	tempDir := t.TempDir()

	// Create .well-known directory and write initial polis config
	os.MkdirAll(filepath.Join(tempDir, ".well-known"), 0755)
	os.WriteFile(filepath.Join(tempDir, ".well-known", "polis"), []byte(`{"active_theme":"initial","public_key":"test"}`), 0644)

	// Set active theme
	err := SetActiveTheme(tempDir, "zane")
	if err != nil {
		t.Fatalf("SetActiveTheme failed: %v", err)
	}

	// Get active theme
	theme, err := GetActiveTheme(tempDir)
	if err != nil {
		t.Fatalf("GetActiveTheme failed: %v", err)
	}

	if theme != "zane" {
		t.Errorf("Expected 'zane', got '%s'", theme)
	}
}

func TestCopyCSS(t *testing.T) {
	tempDir := t.TempDir()
	themesDir := filepath.Join(tempDir, "site", "themes")
	createTestTheme(t, themesDir, "turbo")

	err := CopyCSS(tempDir, "", "turbo")
	if err != nil {
		t.Fatalf("CopyCSS failed: %v", err)
	}

	// Verify CSS was copied
	cssPath := filepath.Join(tempDir, "styles.css")
	if _, err := os.Stat(cssPath); err != nil {
		t.Errorf("Expected styles.css to exist: %v", err)
	}
}

func TestListThemes(t *testing.T) {
	dataDir := t.TempDir()
	cliThemesDir := t.TempDir()

	// Create local theme
	createTestTheme(t, filepath.Join(dataDir, "site", "themes"), "local-theme")

	// Create CLI themes
	createTestTheme(t, cliThemesDir, "cli-theme1")
	createTestTheme(t, cliThemesDir, "cli-theme2")

	themes, err := ListThemes(dataDir, cliThemesDir)
	if err != nil {
		t.Fatalf("ListThemes failed: %v", err)
	}

	if len(themes) != 3 {
		t.Errorf("Expected 3 themes, got %d: %v", len(themes), themes)
	}
}

func TestSelectRandomTheme_SavesChoice(t *testing.T) {
	dataDir := t.TempDir()
	cliThemesDir := t.TempDir()

	// Create two CLI themes
	createTestTheme(t, cliThemesDir, "sols")
	createTestTheme(t, cliThemesDir, "zane")

	selected, err := SelectRandomTheme(dataDir, cliThemesDir)
	if err != nil {
		t.Fatalf("SelectRandomTheme failed: %v", err)
	}

	// Verify returned theme is one of the available ones
	if selected != "sols" && selected != "zane" {
		t.Errorf("Expected 'sols' or 'zane', got '%s'", selected)
	}

	// Verify the choice was persisted in the manifest
	saved, err := GetActiveTheme(dataDir)
	if err != nil {
		t.Fatalf("GetActiveTheme failed after SelectRandomTheme: %v", err)
	}
	if saved != selected {
		t.Errorf("Persisted theme '%s' doesn't match selected '%s'", saved, selected)
	}
}

func TestSelectRandomTheme_NoThemes(t *testing.T) {
	dataDir := t.TempDir()
	cliThemesDir := t.TempDir()

	_, err := SelectRandomTheme(dataDir, cliThemesDir)
	if err == nil {
		t.Error("Expected error when no themes available")
	}
}

func TestSelectRandomTheme_SingleTheme(t *testing.T) {
	dataDir := t.TempDir()
	cliThemesDir := t.TempDir()

	createTestTheme(t, cliThemesDir, "turbo")

	// Run multiple times to verify single theme is always selected
	for i := 0; i < 10; i++ {
		selected, err := SelectRandomTheme(dataDir, cliThemesDir)
		if err != nil {
			t.Fatalf("SelectRandomTheme failed: %v", err)
		}
		if selected != "turbo" {
			t.Errorf("Expected 'turbo', got '%s'", selected)
		}
	}
}

func TestLoad_OptionalArchiveTemplate(t *testing.T) {
	tempDir := t.TempDir()
	themesDir := filepath.Join(tempDir, "site", "themes")
	createTestTheme(t, themesDir, "turbo")

	// Add posts.html to the theme
	os.WriteFile(filepath.Join(themesDir, "turbo", "posts.html"), []byte("<html>{{#posts}}{{title}}{{/posts}}</html>"), 0644)

	templates, err := Load(tempDir, "", "turbo")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if templates.Archive == "" {
		t.Error("Expected archive template to be loaded")
	}

	if templates.Archive != "<html>{{#posts}}{{title}}{{/posts}}</html>" {
		t.Errorf("Unexpected archive template content: %s", templates.Archive)
	}
}

func TestLoad_MissingArchiveTemplate(t *testing.T) {
	tempDir := t.TempDir()
	themesDir := filepath.Join(tempDir, "site", "themes")
	createTestTheme(t, themesDir, "turbo")

	// Do NOT add posts.html

	templates, err := Load(tempDir, "", "turbo")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if templates.Archive != "" {
		t.Errorf("Expected empty archive template, got: %s", templates.Archive)
	}
}

func TestCalculateCSSPath(t *testing.T) {
	tests := []struct {
		filePath string
		expected string
	}{
		{"index.html", "styles.css"},
		{"posts/2026/01/my-post.html", "../../../styles.css"},
		{"comments/2026/01/reply.html", "../../../styles.css"},
		{"about.html", "styles.css"},
	}

	for _, tc := range tests {
		result := CalculateCSSPath(tc.filePath)
		if result != tc.expected {
			t.Errorf("CalculateCSSPath(%q) = %q, want %q", tc.filePath, result, tc.expected)
		}
	}
}

func TestCalculateHomePath(t *testing.T) {
	tests := []struct {
		filePath string
		expected string
	}{
		{"index.html", "index.html"},
		{"posts/2026/01/my-post.html", "../../../index.html"},
		{"about.html", "index.html"},
	}

	for _, tc := range tests {
		result := CalculateHomePath(tc.filePath)
		if result != tc.expected {
			t.Errorf("CalculateHomePath(%q) = %q, want %q", tc.filePath, result, tc.expected)
		}
	}
}
