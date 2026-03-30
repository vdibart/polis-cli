package snippet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- helpers ---

// setupDataDir creates a minimal polis data directory with .well-known/polis.
func setupDataDir(t *testing.T, activeTheme string) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".well-known"), 0755)
	os.WriteFile(filepath.Join(dir, ".well-known", "polis"),
		[]byte(`{"active_theme":"`+activeTheme+`"}`), 0644)
	return dir
}

// writeFile creates a file with intermediate directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

// --- validatePath ---

func TestValidatePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"empty path is valid", "", ""},
		{"simple filename", "about.html", ""},
		{"nested path", "widgets/header.html", ""},
		{"absolute path rejected", "/etc/passwd", "absolute paths not allowed"},
		{"traversal with dots rejected", "../secret", "path traversal not allowed"},
		{"traversal mid-path rejected", "foo/../../etc/passwd", "path traversal not allowed"},
		{"null byte rejected", "foo\x00bar", "null bytes not allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validatePath(%q) unexpected error: %v", tt.path, err)
				}
			} else {
				if err == nil {
					t.Errorf("validatePath(%q) expected error containing %q, got nil", tt.path, tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("validatePath(%q) error = %q, want containing %q", tt.path, err.Error(), tt.wantErr)
				}
			}
		})
	}
}

// --- GetActiveTheme ---

func TestGetActiveTheme_ReadsFromManifest(t *testing.T) {
	dir := setupDataDir(t, "turbo")
	theme, err := GetActiveTheme(dir)
	if err != nil {
		t.Fatalf("GetActiveTheme: %v", err)
	}
	if theme != "turbo" {
		t.Errorf("got %q, want %q", theme, "turbo")
	}
}

func TestGetActiveTheme_DefaultsToZane_MissingFile(t *testing.T) {
	dir := t.TempDir() // no .well-known
	theme, err := GetActiveTheme(dir)
	if err != nil {
		t.Fatalf("GetActiveTheme: %v", err)
	}
	if theme != "zane" {
		t.Errorf("got %q, want %q", theme, "zane")
	}
}

func TestGetActiveTheme_DefaultsToZane_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".well-known"), 0755)
	os.WriteFile(filepath.Join(dir, ".well-known", "polis"), []byte("not json"), 0644)

	theme, err := GetActiveTheme(dir)
	if err != nil {
		t.Fatalf("GetActiveTheme: %v", err)
	}
	if theme != "zane" {
		t.Errorf("got %q, want %q", theme, "zane")
	}
}

func TestGetActiveTheme_DefaultsToZane_EmptyTheme(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".well-known"), 0755)
	os.WriteFile(filepath.Join(dir, ".well-known", "polis"), []byte(`{"active_theme":""}`), 0644)

	theme, err := GetActiveTheme(dir)
	if err != nil {
		t.Fatalf("GetActiveTheme: %v", err)
	}
	if theme != "zane" {
		t.Errorf("got %q, want %q", theme, "zane")
	}
}

// --- resolveSnippetFile ---

func TestResolveSnippetFile_ExactHTML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "about.html"), "html content")

	got := resolveSnippetFile(dir, "about.html")
	if got != filepath.Join(dir, "about.html") {
		t.Errorf("got %q, want exact .html path", got)
	}
}

func TestResolveSnippetFile_ExactMD(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "about.md"), "md content")

	got := resolveSnippetFile(dir, "about.md")
	if got != filepath.Join(dir, "about.md") {
		t.Errorf("got %q, want exact .md path", got)
	}
}

func TestResolveSnippetFile_ExactExtension_Missing(t *testing.T) {
	dir := t.TempDir()
	got := resolveSnippetFile(dir, "about.html")
	if got != "" {
		t.Errorf("got %q, want empty for missing file", got)
	}
}

func TestResolveSnippetFile_NoExtension_PrefersMarkdown(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "about.md"), "md")
	writeFile(t, filepath.Join(dir, "about.html"), "html")

	got := resolveSnippetFile(dir, "about")
	// Per TEMPLATING.md: .md -> .html -> exact, so .md wins
	if got != filepath.Join(dir, "about.md") {
		t.Errorf("got %q, want .md (higher priority)", got)
	}
}

func TestResolveSnippetFile_NoExtension_FallsToHTML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "about.html"), "html")

	got := resolveSnippetFile(dir, "about")
	if got != filepath.Join(dir, "about.html") {
		t.Errorf("got %q, want .html fallback", got)
	}
}

func TestResolveSnippetFile_NoExtension_FallsToExact(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "about"), "exact")

	got := resolveSnippetFile(dir, "about")
	if got != filepath.Join(dir, "about") {
		t.Errorf("got %q, want exact path", got)
	}
}

func TestResolveSnippetFile_NoExtension_NotFound(t *testing.T) {
	dir := t.TempDir()
	got := resolveSnippetFile(dir, "nonexistent")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestResolveSnippetFile_NestedPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "widgets", "header.html"), "nested")

	got := resolveSnippetFile(dir, "widgets/header.html")
	if got != filepath.Join(dir, "widgets", "header.html") {
		t.Errorf("got %q, want nested path", got)
	}
}

// --- ListSnippets ---

func TestListSnippets_GlobalOnly(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "header.html"), "global header")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "footer.md"), "global footer")

	tree, err := ListSnippets(dataDir, "", "turbo", "", "global")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if len(tree.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(tree.Entries))
	}
	for _, e := range tree.Entries {
		if e.Type != "global" {
			t.Errorf("expected type global, got %q for %s", e.Type, e.Name)
		}
	}
}

func TestListSnippets_ThemeOnly(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "themes", "turbo", "snippets", "nav.html"), "theme nav")

	tree, err := ListSnippets(dataDir, "", "turbo", "", "theme")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if len(tree.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(tree.Entries))
	}
	if tree.Entries[0].Type != "theme" {
		t.Errorf("expected type theme, got %q", tree.Entries[0].Type)
	}
}

func TestListSnippets_MergedGlobalOverridesTheme(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "header.html"), "global header")
	writeFile(t, filepath.Join(dataDir, "site", "themes", "turbo", "snippets", "header.html"), "theme header")

	tree, err := ListSnippets(dataDir, "", "turbo", "", "all")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if len(tree.Entries) != 1 {
		t.Fatalf("expected 1 merged entry, got %d", len(tree.Entries))
	}
	e := tree.Entries[0]
	if e.Type != "global" {
		t.Errorf("expected global (override), got %q", e.Type)
	}
	if !e.HasOverride {
		t.Error("expected HasOverride=true when global overrides theme")
	}
}

func TestListSnippets_SkipsHiddenFiles(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", ".hidden.html"), "hidden")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "visible.html"), "visible")

	tree, err := ListSnippets(dataDir, "", "turbo", "", "global")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if len(tree.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(tree.Entries))
	}
	if tree.Entries[0].Name != "visible.html" {
		t.Errorf("expected visible.html, got %s", tree.Entries[0].Name)
	}
}

func TestListSnippets_SkipsNonHTMLMD(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "valid.html"), "ok")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "image.png"), "skip")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "data.json"), "skip")

	tree, err := ListSnippets(dataDir, "", "turbo", "", "global")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if len(tree.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(tree.Entries))
	}
}

func TestListSnippets_DirectoriesFirst(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "aaa.html"), "file")
	os.MkdirAll(filepath.Join(dataDir, "site", "snippets", "zzz"), 0755)

	tree, err := ListSnippets(dataDir, "", "turbo", "", "global")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if len(tree.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(tree.Entries))
	}
	if !tree.Entries[0].IsDir {
		t.Error("expected directory first in sort order")
	}
}

func TestListSnippets_SubDirectory(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "widgets", "header.html"), "nested")

	tree, err := ListSnippets(dataDir, "", "turbo", "widgets", "global")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if len(tree.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(tree.Entries))
	}
	if tree.Entries[0].Path != filepath.Join("widgets", "header.html") {
		t.Errorf("expected path widgets/header.html, got %s", tree.Entries[0].Path)
	}
	if tree.Parent != "" {
		t.Errorf("expected empty parent for single-level subdir, got %q", tree.Parent)
	}
}

func TestListSnippets_ParentPathCalculation(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	os.MkdirAll(filepath.Join(dataDir, "site", "snippets", "a", "b"), 0755)

	tree, err := ListSnippets(dataDir, "", "turbo", "a/b", "global")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}
	if tree.Parent != "a" {
		t.Errorf("expected parent 'a', got %q", tree.Parent)
	}
}

func TestListSnippets_ThemeFallbackToCLI(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	cliThemesDir := t.TempDir()
	writeFile(t, filepath.Join(cliThemesDir, "turbo", "snippets", "nav.html"), "cli theme nav")

	tree, err := ListSnippets(dataDir, cliThemesDir, "turbo", "", "theme")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if len(tree.Entries) != 1 {
		t.Fatalf("expected 1 entry from CLI fallback, got %d", len(tree.Entries))
	}
}

func TestListSnippets_EmptyDirectory(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	os.MkdirAll(filepath.Join(dataDir, "site", "snippets"), 0755)

	tree, err := ListSnippets(dataDir, "", "turbo", "", "global")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}
	if len(tree.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(tree.Entries))
	}
}

func TestListSnippets_PathTraversal(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	_, err := ListSnippets(dataDir, "", "turbo", "../secret", "global")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestListSnippets_DefaultFilter(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "global.html"), "g")
	writeFile(t, filepath.Join(dataDir, "site", "themes", "turbo", "snippets", "theme.html"), "t")

	// Empty filter should return both
	tree, err := ListSnippets(dataDir, "", "turbo", "", "")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}
	if len(tree.Entries) != 2 {
		t.Errorf("expected 2 merged entries, got %d", len(tree.Entries))
	}
}

func TestListSnippets_DefaultThemeFromManifest(t *testing.T) {
	dataDir := setupDataDir(t, "sols")
	writeFile(t, filepath.Join(dataDir, "site", "themes", "sols", "snippets", "nav.html"), "sols nav")

	// Pass empty activeTheme - should read from manifest
	tree, err := ListSnippets(dataDir, "", "", "", "theme")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}
	if tree.ActiveTheme != "sols" {
		t.Errorf("expected activeTheme 'sols', got %q", tree.ActiveTheme)
	}
	if len(tree.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(tree.Entries))
	}
}

// --- ReadSnippet ---

func TestReadSnippet_GlobalSource(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "header.html"), "<header>hello</header>")

	sc, err := ReadSnippet(dataDir, "", "turbo", "header.html", "global")
	if err != nil {
		t.Fatalf("ReadSnippet: %v", err)
	}
	if sc.Content != "<header>hello</header>" {
		t.Errorf("content = %q, want <header>hello</header>", sc.Content)
	}
	if sc.Source != "global" {
		t.Errorf("source = %q, want global", sc.Source)
	}
	if sc.ModTime == "" {
		t.Error("expected non-empty ModTime")
	}
}

func TestReadSnippet_ThemeSource(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "themes", "turbo", "snippets", "nav.html"), "<nav/>")

	sc, err := ReadSnippet(dataDir, "", "turbo", "nav.html", "theme")
	if err != nil {
		t.Fatalf("ReadSnippet: %v", err)
	}
	if sc.Content != "<nav/>" {
		t.Errorf("content = %q, want <nav/>", sc.Content)
	}
}

func TestReadSnippet_ThemeFallbackToCLI(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	cliThemesDir := t.TempDir()
	writeFile(t, filepath.Join(cliThemesDir, "turbo", "snippets", "footer.html"), "<footer/>")

	sc, err := ReadSnippet(dataDir, cliThemesDir, "turbo", "footer.html", "theme")
	if err != nil {
		t.Fatalf("ReadSnippet: %v", err)
	}
	if sc.Content != "<footer/>" {
		t.Errorf("content = %q, want <footer/>", sc.Content)
	}
}

func TestReadSnippet_ExtensionResolution(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "about.md"), "# About")

	// Request without extension - should resolve to .md
	sc, err := ReadSnippet(dataDir, "", "turbo", "about", "global")
	if err != nil {
		t.Fatalf("ReadSnippet: %v", err)
	}
	if sc.Content != "# About" {
		t.Errorf("content = %q, want '# About'", sc.Content)
	}
}

func TestReadSnippet_NotFound(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	_, err := ReadSnippet(dataDir, "", "turbo", "nonexistent.html", "global")
	if err == nil {
		t.Error("expected error for missing snippet")
	}
	if !strings.Contains(err.Error(), "snippet not found") {
		t.Errorf("error = %q, want 'snippet not found'", err.Error())
	}
}

func TestReadSnippet_InvalidSource(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	_, err := ReadSnippet(dataDir, "", "turbo", "header.html", "invalid")
	if err == nil {
		t.Error("expected error for invalid source")
	}
	if !strings.Contains(err.Error(), "invalid source") {
		t.Errorf("error = %q, want 'invalid source'", err.Error())
	}
}

func TestReadSnippet_PathTraversal(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	_, err := ReadSnippet(dataDir, "", "turbo", "../../../etc/passwd", "global")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestReadSnippet_DefaultThemeFromManifest(t *testing.T) {
	dataDir := setupDataDir(t, "sols")
	writeFile(t, filepath.Join(dataDir, "site", "themes", "sols", "snippets", "nav.html"), "sols nav")

	sc, err := ReadSnippet(dataDir, "", "", "nav.html", "theme")
	if err != nil {
		t.Fatalf("ReadSnippet: %v", err)
	}
	if sc.Content != "sols nav" {
		t.Errorf("content = %q, want 'sols nav'", sc.Content)
	}
}

// --- WriteSnippet ---

func TestWriteSnippet_GlobalNew(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	err := WriteSnippet(dataDir, "", "turbo", "header.html", "<header>new</header>", "global")
	if err != nil {
		t.Fatalf("WriteSnippet: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "site", "snippets", "header.html"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "<header>new</header>" {
		t.Errorf("content = %q, want <header>new</header>", string(data))
	}
}

func TestWriteSnippet_GlobalOverwrite(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "header.html"), "old")

	err := WriteSnippet(dataDir, "", "turbo", "header.html", "new", "global")
	if err != nil {
		t.Fatalf("WriteSnippet: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dataDir, "site", "snippets", "header.html"))
	if string(data) != "new" {
		t.Errorf("content = %q, want 'new'", string(data))
	}
}

func TestWriteSnippet_GlobalDefaultsToHTML(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	// Write without extension
	err := WriteSnippet(dataDir, "", "turbo", "header", "content", "global")
	if err != nil {
		t.Fatalf("WriteSnippet: %v", err)
	}

	// Should have created header.html
	if _, err := os.Stat(filepath.Join(dataDir, "site", "snippets", "header.html")); err != nil {
		t.Errorf("expected header.html to exist: %v", err)
	}
}

func TestWriteSnippet_GlobalResolvesExisting(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "about.md"), "old md")

	// Write without extension - should find existing .md file
	err := WriteSnippet(dataDir, "", "turbo", "about", "new md", "global")
	if err != nil {
		t.Fatalf("WriteSnippet: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dataDir, "site", "snippets", "about.md"))
	if string(data) != "new md" {
		t.Errorf("content = %q, want 'new md'", string(data))
	}
}

func TestWriteSnippet_ThemeWritesToLocalDir(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	err := WriteSnippet(dataDir, "", "turbo", "nav.html", "<nav/>", "theme")
	if err != nil {
		t.Fatalf("WriteSnippet: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "site", "themes", "turbo", "snippets", "nav.html"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "<nav/>" {
		t.Errorf("content = %q, want <nav/>", string(data))
	}
}

func TestWriteSnippet_ThemeResolvesExistingInCLI(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	cliThemesDir := t.TempDir()
	writeFile(t, filepath.Join(cliThemesDir, "turbo", "snippets", "footer.html"), "cli footer")

	err := WriteSnippet(dataDir, cliThemesDir, "turbo", "footer.html", "updated footer", "theme")
	if err != nil {
		t.Fatalf("WriteSnippet: %v", err)
	}

	// Should overwrite in CLI dir since that's where it was found
	data, _ := os.ReadFile(filepath.Join(cliThemesDir, "turbo", "snippets", "footer.html"))
	if string(data) != "updated footer" {
		t.Errorf("content = %q, want 'updated footer'", string(data))
	}
}

func TestWriteSnippet_InvalidSource(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	err := WriteSnippet(dataDir, "", "turbo", "header.html", "x", "invalid")
	if err == nil {
		t.Error("expected error for invalid source")
	}
}

func TestWriteSnippet_PathTraversal(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	err := WriteSnippet(dataDir, "", "turbo", "../../../etc/evil", "x", "global")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestWriteSnippet_NestedPath(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	err := WriteSnippet(dataDir, "", "turbo", "widgets/header.html", "<h1/>", "global")
	if err != nil {
		t.Fatalf("WriteSnippet: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dataDir, "site", "snippets", "widgets", "header.html"))
	if string(data) != "<h1/>" {
		t.Errorf("content = %q, want <h1/>", string(data))
	}
}

func TestWriteSnippet_DefaultThemeFromManifest(t *testing.T) {
	dataDir := setupDataDir(t, "sols")

	err := WriteSnippet(dataDir, "", "", "nav.html", "nav", "theme")
	if err != nil {
		t.Fatalf("WriteSnippet: %v", err)
	}

	// Should have written to sols theme dir
	if _, err := os.Stat(filepath.Join(dataDir, "site", "themes", "sols", "snippets", "nav.html")); err != nil {
		t.Errorf("expected file in sols theme dir: %v", err)
	}
}

// --- CreateSnippet ---

func TestCreateSnippet_Success(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	err := CreateSnippet(dataDir, "header.html", "<header/>")
	if err != nil {
		t.Fatalf("CreateSnippet: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "site", "snippets", "header.html"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "<header/>" {
		t.Errorf("content = %q, want <header/>", string(data))
	}
}

func TestCreateSnippet_Markdown(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	err := CreateSnippet(dataDir, "about.md", "# About")
	if err != nil {
		t.Fatalf("CreateSnippet: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dataDir, "site", "snippets", "about.md"))
	if string(data) != "# About" {
		t.Errorf("content = %q", string(data))
	}
}

func TestCreateSnippet_AlreadyExists(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "header.html"), "exists")

	err := CreateSnippet(dataDir, "header.html", "new content")
	if err == nil {
		t.Error("expected error when snippet already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want 'already exists'", err.Error())
	}
}

func TestCreateSnippet_InvalidExtension(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	err := CreateSnippet(dataDir, "data.json", "{}")
	if err == nil {
		t.Error("expected error for invalid extension")
	}
	if !strings.Contains(err.Error(), ".html or .md") {
		t.Errorf("error = %q, want mention of .html or .md", err.Error())
	}
}

func TestCreateSnippet_NoExtension(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	err := CreateSnippet(dataDir, "header", "content")
	if err == nil {
		t.Error("expected error for no extension")
	}
}

func TestCreateSnippet_PathTraversal(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	err := CreateSnippet(dataDir, "../evil.html", "x")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestCreateSnippet_NestedDirectory(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	err := CreateSnippet(dataDir, "widgets/sidebar.html", "<aside/>")
	if err != nil {
		t.Fatalf("CreateSnippet: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dataDir, "site", "snippets", "widgets", "sidebar.html"))
	if string(data) != "<aside/>" {
		t.Errorf("content = %q", string(data))
	}
}

func TestCreateSnippet_AbsolutePath(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	err := CreateSnippet(dataDir, "/etc/evil.html", "x")
	if err == nil {
		t.Error("expected error for absolute path")
	}
}

// --- DeleteSnippet ---

func TestDeleteSnippet_Success(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "header.html"), "content")

	err := DeleteSnippet(dataDir, "header.html")
	if err != nil {
		t.Fatalf("DeleteSnippet: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "site", "snippets", "header.html")); !os.IsNotExist(err) {
		t.Error("expected file to be deleted")
	}
}

func TestDeleteSnippet_NotFound(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	err := DeleteSnippet(dataDir, "nonexistent.html")
	if err == nil {
		t.Error("expected error for missing snippet")
	}
	if !strings.Contains(err.Error(), "snippet not found") {
		t.Errorf("error = %q, want 'snippet not found'", err.Error())
	}
}

func TestDeleteSnippet_CannotDeleteDirectory(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	os.MkdirAll(filepath.Join(dataDir, "site", "snippets", "widgets"), 0755)

	err := DeleteSnippet(dataDir, "widgets")
	if err == nil {
		t.Error("expected error when trying to delete directory")
	}
	if !strings.Contains(err.Error(), "cannot delete directory") {
		t.Errorf("error = %q, want 'cannot delete directory'", err.Error())
	}
}

func TestDeleteSnippet_PathTraversal(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	err := DeleteSnippet(dataDir, "../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestDeleteSnippet_AbsolutePath(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	err := DeleteSnippet(dataDir, "/etc/passwd")
	if err == nil {
		t.Error("expected error for absolute path")
	}
}

func TestDeleteSnippet_Nested(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "widgets", "old.html"), "old")

	err := DeleteSnippet(dataDir, "widgets/old.html")
	if err != nil {
		t.Fatalf("DeleteSnippet: %v", err)
	}
}

// --- Integration: round-trip CRUD ---

func TestCRUD_RoundTrip(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")

	// Create
	err := CreateSnippet(dataDir, "header.html", "<header>v1</header>")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Read
	sc, err := ReadSnippet(dataDir, "", "turbo", "header.html", "global")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if sc.Content != "<header>v1</header>" {
		t.Errorf("Read content = %q", sc.Content)
	}

	// List
	tree, err := ListSnippets(dataDir, "", "turbo", "", "global")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tree.Entries) != 1 {
		t.Fatalf("List entries = %d, want 1", len(tree.Entries))
	}

	// Update (Write)
	err = WriteSnippet(dataDir, "", "turbo", "header.html", "<header>v2</header>", "global")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	sc, err = ReadSnippet(dataDir, "", "turbo", "header.html", "global")
	if err != nil {
		t.Fatalf("Read after write: %v", err)
	}
	if sc.Content != "<header>v2</header>" {
		t.Errorf("Read after write content = %q", sc.Content)
	}

	// Delete
	err = DeleteSnippet(dataDir, "header.html")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify gone
	_, err = ReadSnippet(dataDir, "", "turbo", "header.html", "global")
	if err == nil {
		t.Error("expected error reading deleted snippet")
	}
}

// --- SnippetInfo fields ---

func TestListSnippets_EntryFields(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	writeFile(t, filepath.Join(dataDir, "site", "snippets", "header.html"), "hello world")

	tree, err := ListSnippets(dataDir, "", "turbo", "", "global")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if len(tree.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(tree.Entries))
	}

	e := tree.Entries[0]
	if e.Name != "header.html" {
		t.Errorf("Name = %q", e.Name)
	}
	if e.Path != "header.html" {
		t.Errorf("Path = %q", e.Path)
	}
	if e.Extension != ".html" {
		t.Errorf("Extension = %q", e.Extension)
	}
	if e.IsDir {
		t.Error("IsDir should be false for files")
	}
	if e.Size != int64(len("hello world")) {
		t.Errorf("Size = %d, want %d", e.Size, len("hello world"))
	}
	if e.ModTime == "" {
		t.Error("ModTime should not be empty")
	}
}

func TestListSnippets_DirectoryEntry(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	os.MkdirAll(filepath.Join(dataDir, "site", "snippets", "widgets"), 0755)

	tree, err := ListSnippets(dataDir, "", "turbo", "", "global")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if len(tree.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(tree.Entries))
	}

	e := tree.Entries[0]
	if !e.IsDir {
		t.Error("expected IsDir=true for directory")
	}
	if e.Path != "widgets" {
		t.Errorf("Path = %q, want 'widgets'", e.Path)
	}
}

// --- SnippetTree fields ---

func TestListSnippets_TreeFields(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	os.MkdirAll(filepath.Join(dataDir, "site", "snippets"), 0755)

	tree, err := ListSnippets(dataDir, "", "turbo", "", "all")
	if err != nil {
		t.Fatalf("ListSnippets: %v", err)
	}

	if tree.Path != "" {
		t.Errorf("Path = %q, want empty for root", tree.Path)
	}
	if tree.Parent != "" {
		t.Errorf("Parent = %q, want empty for root", tree.Parent)
	}
	if tree.ActiveTheme != "turbo" {
		t.Errorf("ActiveTheme = %q, want turbo", tree.ActiveTheme)
	}
}

// --- NullByte in path ---

func TestReadSnippet_NullByte(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	_, err := ReadSnippet(dataDir, "", "turbo", "file\x00.html", "global")
	if err == nil {
		t.Error("expected error for null byte in path")
	}
}

func TestWriteSnippet_NullByte(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	err := WriteSnippet(dataDir, "", "turbo", "file\x00.html", "x", "global")
	if err == nil {
		t.Error("expected error for null byte in path")
	}
}

func TestCreateSnippet_NullByte(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	err := CreateSnippet(dataDir, "file\x00.html", "x")
	if err == nil {
		t.Error("expected error for null byte in path")
	}
}

func TestDeleteSnippet_NullByte(t *testing.T) {
	dataDir := setupDataDir(t, "turbo")
	err := DeleteSnippet(dataDir, "file\x00.html")
	if err == nil {
		t.Error("expected error for null byte in path")
	}
}
