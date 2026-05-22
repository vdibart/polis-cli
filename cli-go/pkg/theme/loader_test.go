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

// createBaseTheme creates a _base theme directory with all templates.
// Pre-SHAPE-refactor tests used this; still useful for cliThemesDir-based tests.
func createBaseTheme(t *testing.T, themeDir string) {
	t.Helper()
	dir := filepath.Join(themeDir, "_base")
	os.MkdirAll(dir, 0755)

	templates := map[string]string{
		"post.html":           "<html>BASE POST</html>",
		"comment.html":        "<html>BASE COMMENT</html>",
		"comment-inline.html": "<div>BASE INLINE</div>",
		"index.html":          "<html>BASE INDEX</html>",
		"posts.html":          "<html>BASE ARCHIVE</html>",
		"tag.html":            "<html>BASE TAG</html>",
		"tag-index.html":      "<html>BASE TAG INDEX</html>",
	}

	for name, content := range templates {
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
	}
}

// installShapeFixture writes a minimal blog shape fixture into dataDir at
// .polis/bundles/pub.polis.core/shapes/v3/. Required by Load/LoadShape which
// now expect the shape dir to exist (post-SHAPE refactor).
func installShapeFixture(t *testing.T, dataDir string) {
	t.Helper()
	dir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "shapes", "v3")
	os.MkdirAll(dir, 0755)
	templates := map[string]string{
		"post.html":           "<html>SHAPE POST</html>",
		"comment.html":        "<html>SHAPE COMMENT</html>",
		"comment-inline.html": "<div>SHAPE INLINE</div>",
		"index.html":          "<html>SHAPE INDEX</html>",
		"posts.html":          "<html>SHAPE ARCHIVE</html>",
		"tag.html":            "<html>SHAPE TAG</html>",
		"tag-index.html":      "<html>SHAPE TAG INDEX</html>",
	}
	for name, content := range templates {
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
	}
}

// createCSSOnlyTheme creates a theme with just a CSS file (no HTML templates).
func createCSSOnlyTheme(t *testing.T, themeDir, themeName string) {
	t.Helper()
	dir := filepath.Join(themeDir, themeName)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, themeName+".css"), []byte("body { color: red; }"), 0644)
}

func TestLoad(t *testing.T) {
	tempDir := t.TempDir()
	installShapeFixture(t, tempDir)
	themesDir := filepath.Join(tempDir, "site", "themes")
	createTestTheme(t, themesDir, "turbo")

	templates, err := LoadShape(tempDir, "", "v3", "turbo")
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
	installShapeFixture(t, dataDir)
	cliThemesDir := t.TempDir()

	// Only create theme in CLI dir (not local)
	createTestTheme(t, cliThemesDir, "sols")

	templates, err := LoadShape(dataDir, cliThemesDir, "v3", "sols")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if templates.Post == "" {
		t.Error("Expected post template from CLI fallback")
	}
}

func TestLoadMissingTheme(t *testing.T) {
	tempDir := t.TempDir()

	_, err := LoadShape(tempDir, "", "v3", "nonexistent")
	if err == nil {
		t.Error("Expected error for missing theme")
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
	installShapeFixture(t, tempDir)
	themesDir := filepath.Join(tempDir, "site", "themes")
	createTestTheme(t, themesDir, "turbo")

	// Add posts.html to the theme (overrides shape)
	os.WriteFile(filepath.Join(themesDir, "turbo", "posts.html"), []byte("<html>{{#posts}}{{title}}{{/posts}}</html>"), 0644)

	templates, err := LoadShape(tempDir, "", "v3", "turbo")
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
	installShapeFixture(t, tempDir)
	themesDir := filepath.Join(tempDir, "site", "themes")
	createTestTheme(t, themesDir, "turbo")

	// Do NOT add posts.html — should fall through to shape's posts.html.
	templates, err := LoadShape(tempDir, "", "v3", "turbo")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if templates.Archive != "<html>SHAPE ARCHIVE</html>" {
		t.Errorf("Expected shape archive template fallback, got: %s", templates.Archive)
	}
}

// TestLoad_CSSOnlyThemeUsesShape verifies that a CSS-only theme (no HTML)
// loads all templates from the installed shape. Post-SHAPE refactor the shape
// replaces the old _base concept as the fallback source.
func TestLoad_CSSOnlyThemeUsesShape(t *testing.T) {
	dataDir := t.TempDir()
	installShapeFixture(t, dataDir)
	themesDir := filepath.Join(dataDir, "site", "themes")

	// CSS-only theme (no HTML templates).
	createCSSOnlyTheme(t, themesDir, "minimal")

	templates, err := LoadShape(dataDir, "", "v3", "minimal")
	if err != nil {
		t.Fatalf("Load failed for CSS-only theme: %v", err)
	}

	// All templates should come from the shape fixture.
	if templates.Post != "<html>SHAPE POST</html>" {
		t.Errorf("Expected shape post template, got: %s", templates.Post)
	}
	if templates.Index != "<html>SHAPE INDEX</html>" {
		t.Errorf("Expected shape index template, got: %s", templates.Index)
	}
	if templates.Archive != "<html>SHAPE ARCHIVE</html>" {
		t.Errorf("Expected shape archive template, got: %s", templates.Archive)
	}
	if templates.Tag != "<html>SHAPE TAG</html>" {
		t.Errorf("Expected shape tag template, got: %s", templates.Tag)
	}
}

// TestLoad_ThemeOverridesShape verifies that a theme's per-template overrides
// beat the shape fallback (the studio13 pattern).
func TestLoad_ThemeOverridesShape(t *testing.T) {
	dataDir := t.TempDir()
	installShapeFixture(t, dataDir)
	themesDir := filepath.Join(dataDir, "site", "themes")

	// Theme with only post.html override.
	themeDir := filepath.Join(themesDir, "custom")
	os.MkdirAll(themeDir, 0755)
	os.WriteFile(filepath.Join(themeDir, "custom.css"), []byte("body {}"), 0644)
	os.WriteFile(filepath.Join(themeDir, "post.html"), []byte("<html>CUSTOM POST</html>"), 0644)

	templates, err := LoadShape(dataDir, "", "v3", "custom")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// post.html should come from theme override.
	if templates.Post != "<html>CUSTOM POST</html>" {
		t.Errorf("Expected custom post template, got: %s", templates.Post)
	}
	// index.html should come from shape fallback.
	if templates.Index != "<html>SHAPE INDEX</html>" {
		t.Errorf("Expected shape index template, got: %s", templates.Index)
	}
}

func TestLoad_NoShapeInstalledFails(t *testing.T) {
	dataDir := t.TempDir()
	themesDir := filepath.Join(dataDir, "site", "themes")

	// Full theme, but no shape installed at .polis/bundles/...
	createTestTheme(t, themesDir, "turbo")

	_, err := LoadShape(dataDir, "", "v3", "turbo")
	if err == nil {
		t.Fatal("expected Load to fail when shape is not installed")
	}
}

func TestLoadShape_UnknownShapeFails(t *testing.T) {
	dataDir := t.TempDir()
	installShapeFixture(t, dataDir) // installs v3 only
	themesDir := filepath.Join(dataDir, "site", "themes")
	createTestTheme(t, themesDir, "turbo")

	_, err := LoadShape(dataDir, "", "v4", "turbo")
	if err == nil {
		t.Fatal("expected LoadShape to fail for undeclared shape v4")
	}
}

func TestListThemes_ExcludesBase(t *testing.T) {
	dataDir := t.TempDir()
	cliThemesDir := t.TempDir()

	// Create _base and a real theme
	createBaseTheme(t, filepath.Join(dataDir, "site", "themes"))
	createTestTheme(t, filepath.Join(dataDir, "site", "themes"), "turbo")

	themes, err := ListThemes(dataDir, cliThemesDir)
	if err != nil {
		t.Fatalf("ListThemes failed: %v", err)
	}

	for _, name := range themes {
		if name == "_base" {
			t.Error("_base should not appear in ListThemes output")
		}
	}

	if len(themes) != 1 {
		t.Errorf("Expected 1 theme (turbo), got %d: %v", len(themes), themes)
	}
}

func TestIsValidTheme_CSSOnly(t *testing.T) {
	dir := t.TempDir()
	themeDir := filepath.Join(dir, "mytheme")
	os.MkdirAll(themeDir, 0755)
	os.WriteFile(filepath.Join(themeDir, "mytheme.css"), []byte("body {}"), 0644)

	if !isValidTheme(themeDir) {
		t.Error("CSS-only theme should be valid")
	}
}

func TestIsValidTheme_BaseExcluded(t *testing.T) {
	dir := t.TempDir()
	baseDir := filepath.Join(dir, "_base")
	os.MkdirAll(baseDir, 0755)
	// Even with all templates, _base should not be valid
	os.WriteFile(filepath.Join(baseDir, "post.html"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(baseDir, "comment.html"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(baseDir, "comment-inline.html"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(baseDir, "index.html"), []byte("x"), 0644)

	if isValidTheme(baseDir) {
		t.Error("_base should not be a valid selectable theme")
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

// --- hotfix-studio13-cleanup: picker shape-filtering + DisplayName tests ---

// installBundleThemes writes minimal valid theme dirs (just a CSS file each)
// for the named themes into the bundle-installed location. Mirrors what
// EnsureReferencePayload would have done on a real tenant.
func installBundleThemes(t *testing.T, dataDir string, names ...string) {
	t.Helper()
	base := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "themes")
	for _, name := range names {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".css"), []byte(":root { --color-bg: #000; }"), 0644); err != nil {
			t.Fatalf("write css: %v", err)
		}
	}
}

func TestListUserSelectableThemes_FiltersByShape_Blog(t *testing.T) {
	dir := t.TempDir()
	// Install a cross-section of bundle-declared themes.
	installBundleThemes(t, dir, "vice", "studio13-nk")

	// V3 picker should include vice (v3+v4) and studio13-nk (blog-only).
	themes, err := ListUserSelectableThemes(dir, "", "v3")
	if err != nil {
		t.Fatalf("ListUserSelectableThemes(v3): %v", err)
	}
	names := map[string]bool{}
	for _, p := range themes {
		names[p.Name] = true
	}
	if !names["vice"] {
		t.Error("v3 picker should include vice")
	}
	if !names["studio13-nk"] {
		t.Error("v3 picker should include studio13-nk (blog-only theme)")
	}
}

func TestListUserSelectableThemes_EmptyShapeDefaultsToBlog(t *testing.T) {
	dir := t.TempDir()
	installBundleThemes(t, dir, "vice", "studio13-nk")

	// Empty active_shape → treated as v3 per bundle.GetActiveShapeName default.
	themes, err := ListUserSelectableThemes(dir, "", "")
	if err != nil {
		t.Fatalf("ListUserSelectableThemes(empty): %v", err)
	}
	if len(themes) == 0 {
		t.Error("expected v3 themes when active_shape empty; got none")
	}
}

func TestListUserSelectableThemes_ExcludesOrphansNotDeclared(t *testing.T) {
	dir := t.TempDir()
	// "junk" is installed on disk but NOT declared in DefaultCoreBundle.
	// Picker must not surface it — disk drift is handled by the orphan-dir
	// Patrol check, not by the picker.
	installBundleThemes(t, dir, "vice", "junk")

	themes, err := ListUserSelectableThemes(dir, "", "v3")
	if err != nil {
		t.Fatalf("ListUserSelectableThemes: %v", err)
	}
	for _, p := range themes {
		if p.Name == "junk" {
			t.Error("picker must not include undeclared theme 'junk'")
		}
	}
}

func TestListThemesWithPalettes_PopulatesDisplayName(t *testing.T) {
	dir := t.TempDir()
	// studio13-nk carries DisplayName "studio13" in DefaultCoreBundle.
	installBundleThemes(t, dir, "studio13-nk")

	themes, err := ListThemesWithPalettes(dir, "")
	if err != nil {
		t.Fatalf("ListThemesWithPalettes: %v", err)
	}
	found := false
	for _, p := range themes {
		if p.Name == "studio13-nk" {
			found = true
			if p.DisplayName != "studio13" {
				t.Errorf("studio13-nk DisplayName = %q, want %q", p.DisplayName, "studio13")
			}
		}
	}
	if !found {
		t.Error("studio13-nk not returned from ListThemesWithPalettes")
	}
}

func TestGetThemeDir_ChecksBundleLocation(t *testing.T) {
	dir := t.TempDir()
	installBundleThemes(t, dir, "vice")
	got := GetThemeDir(dir, "", "vice")
	want := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "themes", "vice")
	if got != want {
		t.Errorf("GetThemeDir = %q, want %q (bundle-installed path)", got, want)
	}
}

// --- step-02/2.b: stream shape loading tests ---

// installStreamShapeFixture writes a minimal stream shape fixture into dataDir at
// .polis/bundles/pub.polis.core/shapes/v4/. Mirrors installShapeFixture's
// pattern for v3. Only stream.html is required — partials are optional and
// individual tests write them as needed.
func installStreamShapeFixture(t *testing.T, dataDir string) {
	t.Helper()
	dir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "shapes", "v4")
	os.MkdirAll(filepath.Join(dir, "snippets"), 0755)
	os.WriteFile(filepath.Join(dir, "stream.html"), []byte("<html>V4 STREAM</html>"), 0644)
	os.WriteFile(filepath.Join(dir, "stream-post.html"), []byte("<a>V4 POST</a>"), 0644)
	os.WriteFile(filepath.Join(dir, "stream-comment.html"), []byte("<div>V4 COMMENT</div>"), 0644)
	os.WriteFile(filepath.Join(dir, "stream-profile.html"), []byte("<a>V4 PROFILE</a>"), 0644)
	os.WriteFile(filepath.Join(dir, "stream-mention.html"), []byte("<a>V4 MENTION</a>"), 0644)
}

func TestLoadShape_StreamLoadsTemplates(t *testing.T) {
	tempDir := t.TempDir()
	installStreamShapeFixture(t, tempDir)

	templates, err := LoadShape(tempDir, "", "v4", "vice")
	if err != nil {
		t.Fatalf("LoadShape(v4): %v", err)
	}
	if templates.Stream != "<html>V4 STREAM</html>" {
		t.Errorf("Stream = %q, want shape-dir content", templates.Stream)
	}
	if templates.StreamPost != "<a>V4 POST</a>" {
		t.Errorf("StreamPost = %q, want shape-dir content", templates.StreamPost)
	}
	if templates.StreamComment != "<div>V4 COMMENT</div>" {
		t.Errorf("StreamComment = %q, want shape-dir content", templates.StreamComment)
	}
	if templates.StreamProfile != "<a>V4 PROFILE</a>" {
		t.Errorf("StreamProfile = %q, want shape-dir content", templates.StreamProfile)
	}
	if templates.StreamMention != "<a>V4 MENTION</a>" {
		t.Errorf("StreamMention = %q, want shape-dir content", templates.StreamMention)
	}
	// v3 fields must NOT be populated when shape is v4.
	if templates.Post != "" || templates.Comment != "" || templates.Index != "" {
		t.Errorf("v4 LoadShape populated v3 fields (Post/Comment/Index) — want empty")
	}
}

func TestLoadShape_BlogDoesNotLoadStreamFields(t *testing.T) {
	tempDir := t.TempDir()
	installShapeFixture(t, tempDir)

	templates, err := LoadShape(tempDir, "", "v3", "vice")
	if err != nil {
		t.Fatalf("LoadShape(v3): %v", err)
	}
	// stream fields must stay empty on blog shape.
	if templates.Stream != "" || templates.StreamPost != "" || templates.StreamComment != "" ||
		templates.StreamProfile != "" || templates.StreamMention != "" {
		t.Errorf("v3 LoadShape populated stream fields — want empty; got Stream=%q", templates.Stream)
	}
	// v3 required fields populated.
	if templates.Post == "" || templates.Index == "" {
		t.Error("v3 LoadShape did not populate v3 required fields")
	}
}

func TestLoadShape_StreamMissingTemplatesErrors(t *testing.T) {
	tempDir := t.TempDir()
	// Install v4 dir but NO stream.html.
	dir := filepath.Join(tempDir, ".polis", "bundles", "pub.polis.core", "shapes", "v4")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "stream-post.html"), []byte("<a></a>"), 0644)

	_, err := LoadShape(tempDir, "", "v4", "vice")
	if err == nil {
		t.Error("LoadShape(v4) without stream.html should error")
	}
}

func TestLoadShape_UnknownShapeErrors(t *testing.T) {
	tempDir := t.TempDir()
	// Install shape dir but under an unknown name; dispatcher should reject.
	dir := filepath.Join(tempDir, ".polis", "bundles", "pub.polis.core", "shapes", "v99")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "anything.html"), []byte(""), 0644)

	_, err := LoadShape(tempDir, "", "v99", "vice")
	if err == nil {
		t.Error("LoadShape(v99) should error — unsupported shape")
	}
}

func TestLoadShape_StreamPartialsAreOptional(t *testing.T) {
	tempDir := t.TempDir()
	dir := filepath.Join(tempDir, ".polis", "bundles", "pub.polis.core", "shapes", "v4")
	os.MkdirAll(dir, 0755)
	// Only stream.html present — partials absent.
	os.WriteFile(filepath.Join(dir, "stream.html"), []byte("<html></html>"), 0644)

	templates, err := LoadShape(tempDir, "", "v4", "vice")
	if err != nil {
		t.Fatalf("LoadShape(v4) with only stream.html: %v", err)
	}
	if templates.Stream == "" {
		t.Error("Stream should be populated")
	}
	// Partials may be empty — no error expected.
}
