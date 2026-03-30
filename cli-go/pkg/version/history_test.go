package version

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Helpers ---

// writeFile creates a file with the given content inside dir.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	return path
}

// Minimal unified diff: change line "Hello" → "Hello World"
const sampleDiff = `--- a
+++ b
@@ -1 +1 @@
-Hello
+Hello World
`

// A realistic 3-version history file:
//
//	v1 (base, hash=aaa): full content "Alpha\n"
//	v2 (hash=bbb, parent=aaa): change to "Bravo\n"
//	v3 (hash=ccc, parent=bbb): change to "Bravo\nCharlie\n"
//
// The diffs are crafted to work with GNU patch for both forward and reverse application.
var threeVersionHistory = "# VERSION_FILE_FORMAT=1\n" +
	"# CANONICAL_FILE=post.md\n" +
	"# CURRENT_HASH=ccc\n" +
	"[VERSION aaa]\n" +
	"TIMESTAMP=2025-01-01T00:00:00Z\n" +
	"PARENT=none\n" +
	"FULL_CONTENT_START\n" +
	"Alpha\n" +
	"FULL_CONTENT_END\n" +
	"[VERSION bbb]\n" +
	"TIMESTAMP=2025-01-02T00:00:00Z\n" +
	"PARENT=aaa\n" +
	"DIFF_START\n" +
	"--- a\n" +
	"+++ b\n" +
	"@@ -1 +1 @@\n" +
	"-Alpha\n" +
	"+Bravo\n" +
	"\n" +
	"DIFF_END\n" +
	"[VERSION ccc]\n" +
	"TIMESTAMP=2025-01-03T00:00:00Z\n" +
	"PARENT=bbb\n" +
	"DIFF_START\n" +
	"--- a\n" +
	"+++ b\n" +
	"@@ -1 +1,2 @@\n" +
	" Bravo\n" +
	"+Charlie\n" +
	"\n" +
	"DIFF_END\n"

// --- GetVersionsFilePath ---

func TestGetVersionsFilePath(t *testing.T) {
	tests := []struct {
		canonical   string
		versionsDir string
		want        string
	}{
		{"posts/my-post.md", ".versions", "posts/.versions/my-post.md"},
		{"blog/2025/article.md", "_versions", "blog/2025/_versions/article.md"},
		{"root.md", ".versions", ".versions/root.md"},
	}
	for _, tt := range tests {
		got := GetVersionsFilePath(tt.canonical, tt.versionsDir)
		if got != tt.want {
			t.Errorf("GetVersionsFilePath(%q, %q) = %q, want %q", tt.canonical, tt.versionsDir, got, tt.want)
		}
	}
}

// --- ParseHistoryFile ---

func TestParseHistoryFile_Basic(t *testing.T) {
	tmp := t.TempDir()
	path := writeFile(t, tmp, "post.md", threeVersionHistory)

	h, err := ParseHistoryFile(path)
	if err != nil {
		t.Fatalf("ParseHistoryFile failed: %v", err)
	}

	if h.FormatVersion != "1" {
		t.Errorf("FormatVersion = %q, want %q", h.FormatVersion, "1")
	}
	if h.CanonicalFile != "post.md" {
		t.Errorf("CanonicalFile = %q, want %q", h.CanonicalFile, "post.md")
	}
	if h.CurrentHash != "ccc" {
		t.Errorf("CurrentHash = %q, want %q", h.CurrentHash, "ccc")
	}
	if len(h.Versions) != 3 {
		t.Fatalf("len(Versions) = %d, want 3", len(h.Versions))
	}

	// Base version
	v := h.Versions[0]
	if v.Hash != "aaa" {
		t.Errorf("v[0].Hash = %q, want %q", v.Hash, "aaa")
	}
	if v.Timestamp != "2025-01-01T00:00:00Z" {
		t.Errorf("v[0].Timestamp = %q", v.Timestamp)
	}
	if v.Parent != "none" {
		t.Errorf("v[0].Parent = %q, want %q", v.Parent, "none")
	}
	if v.FullContent != "Alpha" {
		t.Errorf("v[0].FullContent = %q, want %q", v.FullContent, "Alpha")
	}
	if v.Diff != "" {
		t.Errorf("v[0].Diff = %q, want empty", v.Diff)
	}

	// Second version (diff-based)
	v = h.Versions[1]
	if v.Hash != "bbb" {
		t.Errorf("v[1].Hash = %q, want %q", v.Hash, "bbb")
	}
	if v.Parent != "aaa" {
		t.Errorf("v[1].Parent = %q, want %q", v.Parent, "aaa")
	}
	if v.FullContent != "" {
		t.Errorf("v[1].FullContent = %q, want empty", v.FullContent)
	}
	if v.Diff == "" {
		t.Error("v[1].Diff is empty, want unified diff")
	}

	// Third version
	v = h.Versions[2]
	if v.Hash != "ccc" {
		t.Errorf("v[2].Hash = %q, want %q", v.Hash, "ccc")
	}
	if v.Parent != "bbb" {
		t.Errorf("v[2].Parent = %q, want %q", v.Parent, "bbb")
	}
}

func TestParseHistoryFile_SingleBaseVersion(t *testing.T) {
	content := `# VERSION_FILE_FORMAT=1
# CANONICAL_FILE=about.md
# CURRENT_HASH=abc123
[VERSION abc123]
TIMESTAMP=2025-06-15T12:00:00Z
PARENT=none
FULL_CONTENT_START
This is my about page.
It has multiple lines.
FULL_CONTENT_END
`
	tmp := t.TempDir()
	path := writeFile(t, tmp, "about.md", content)

	h, err := ParseHistoryFile(path)
	if err != nil {
		t.Fatalf("ParseHistoryFile failed: %v", err)
	}

	if len(h.Versions) != 1 {
		t.Fatalf("len(Versions) = %d, want 1", len(h.Versions))
	}
	if h.Versions[0].FullContent != "This is my about page.\nIt has multiple lines." {
		t.Errorf("FullContent = %q", h.Versions[0].FullContent)
	}
}

func TestParseHistoryFile_FileNotFound(t *testing.T) {
	_, err := ParseHistoryFile("/nonexistent/path/file")
	if err == nil {
		t.Error("Expected error for missing file, got nil")
	}
}

func TestParseHistoryFile_EmptyFile(t *testing.T) {
	tmp := t.TempDir()
	path := writeFile(t, tmp, "empty.md", "")

	h, err := ParseHistoryFile(path)
	if err != nil {
		t.Fatalf("ParseHistoryFile failed: %v", err)
	}
	if len(h.Versions) != 0 {
		t.Errorf("Expected 0 versions for empty file, got %d", len(h.Versions))
	}
	if h.FormatVersion != "" {
		t.Errorf("Expected empty FormatVersion, got %q", h.FormatVersion)
	}
}

func TestParseHistoryFile_NoHeaders(t *testing.T) {
	content := `[VERSION xyz]
TIMESTAMP=2025-01-01T00:00:00Z
PARENT=none
FULL_CONTENT_START
Some content
FULL_CONTENT_END
`
	tmp := t.TempDir()
	path := writeFile(t, tmp, "noheader.md", content)

	h, err := ParseHistoryFile(path)
	if err != nil {
		t.Fatalf("ParseHistoryFile failed: %v", err)
	}
	if h.FormatVersion != "" {
		t.Errorf("FormatVersion = %q, want empty", h.FormatVersion)
	}
	if h.CanonicalFile != "" {
		t.Errorf("CanonicalFile = %q, want empty", h.CanonicalFile)
	}
	if len(h.Versions) != 1 {
		t.Fatalf("len(Versions) = %d, want 1", len(h.Versions))
	}
	if h.Versions[0].FullContent != "Some content" {
		t.Errorf("FullContent = %q, want %q", h.Versions[0].FullContent, "Some content")
	}
}

// --- GetVersion ---

func TestGetVersion(t *testing.T) {
	h := &HistoryFile{
		Versions: []VersionEntry{
			{Hash: "aaa", Timestamp: "t1"},
			{Hash: "bbb", Timestamp: "t2"},
			{Hash: "ccc", Timestamp: "t3"},
		},
	}

	v := h.GetVersion("bbb")
	if v == nil {
		t.Fatal("GetVersion returned nil for existing hash")
	}
	if v.Hash != "bbb" {
		t.Errorf("Hash = %q, want %q", v.Hash, "bbb")
	}

	v = h.GetVersion("nonexistent")
	if v != nil {
		t.Errorf("GetVersion returned non-nil for missing hash: %v", v)
	}
}

// --- extractBodyContent ---

func TestExtractBodyContent(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
	}{
		{
			name:  "no frontmatter",
			input: "Just some content\nWith lines",
			want:  "Just some content\nWith lines",
		},
		{
			name:  "with frontmatter",
			input: "---\ntitle: Hello\ndate: 2025-01-01\n---\nBody here\nSecond line",
			want:  "Body here\nSecond line",
		},
		{
			name:  "frontmatter only",
			input: "---\ntitle: Hello\n---",
			want:  "",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "triple dash inside body",
			input: "---\ntitle: Test\n---\nLine one\n---\nLine three",
			want:  "Line one\n---\nLine three",
		},
		{
			name:  "no closing frontmatter delimiter",
			input: "---\ntitle: Hello\nno closing dash",
			want:  "",
		},
		{
			name:  "frontmatter with leading whitespace on dashes",
			input: "  ---  \ntitle: Test\n  ---  \nBody",
			want:  "Body",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBodyContent(tt.input)
			if got != tt.want {
				t.Errorf("extractBodyContent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- canonicalizeContent ---

func TestCanonicalizeContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already canonical",
			input: "Hello\n",
			want:  "Hello\n",
		},
		{
			name:  "leading newlines stripped",
			input: "\n\n\nHello\n",
			want:  "Hello\n",
		},
		{
			name:  "trailing whitespace on lines stripped",
			input: "Hello   \nWorld\t\n",
			want:  "Hello\nWorld\n",
		},
		{
			name:  "trailing blank lines stripped",
			input: "Hello\n\n\n",
			want:  "Hello\n",
		},
		{
			name:  "no trailing newline added",
			input: "Hello",
			want:  "Hello\n",
		},
		{
			name:  "empty input",
			input: "",
			want:  "\n",
		},
		{
			name:  "only whitespace",
			input: "   \n\t\n  ",
			want:  "\n",
		},
		{
			name:  "combined leading/trailing/whitespace",
			input: "\n\nLine one  \nLine two\t\n\n\n",
			want:  "Line one\nLine two\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := canonicalizeContent(tt.input)
			if got != tt.want {
				t.Errorf("canonicalizeContent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- applyForwardPatch ---

func TestApplyForwardPatch(t *testing.T) {
	content := "Hello\n"
	diff := `--- a
+++ b
@@ -1 +1 @@
-Hello
+Hello World
`
	got, err := applyForwardPatch(content, diff)
	if err != nil {
		t.Fatalf("applyForwardPatch failed: %v", err)
	}
	if got != "Hello World\n" {
		t.Errorf("applyForwardPatch = %q, want %q", got, "Hello World\n")
	}
}

func TestApplyForwardPatch_EmptyDiff(t *testing.T) {
	content := "unchanged\n"
	got, err := applyForwardPatch(content, "")
	if err != nil {
		t.Fatalf("applyForwardPatch failed: %v", err)
	}
	if got != content {
		t.Errorf("applyForwardPatch = %q, want %q", got, content)
	}
}

func TestApplyForwardPatch_AddLine(t *testing.T) {
	content := "Line one\n"
	diff := `--- a
+++ b
@@ -1 +1,2 @@
 Line one
+Line two
`
	got, err := applyForwardPatch(content, diff)
	if err != nil {
		t.Fatalf("applyForwardPatch failed: %v", err)
	}
	want := "Line one\nLine two\n"
	if got != want {
		t.Errorf("applyForwardPatch = %q, want %q", got, want)
	}
}

func TestApplyForwardPatch_RemoveLine(t *testing.T) {
	content := "Line one\nLine two\n"
	diff := `--- a
+++ b
@@ -1,2 +1 @@
 Line one
-Line two
`
	got, err := applyForwardPatch(content, diff)
	if err != nil {
		t.Fatalf("applyForwardPatch failed: %v", err)
	}
	want := "Line one\n"
	if got != want {
		t.Errorf("applyForwardPatch = %q, want %q", got, want)
	}
}

func TestApplyForwardPatch_InvalidDiff(t *testing.T) {
	content := "Hello\n"
	diff := "this is not a valid diff"
	_, err := applyForwardPatch(content, diff)
	if err == nil {
		t.Error("Expected error for invalid diff, got nil")
	}
}

// --- applyReversePatch ---

func TestApplyReversePatch(t *testing.T) {
	content := "Hello World\n"
	diff := `--- a
+++ b
@@ -1 +1 @@
-Hello
+Hello World
`
	got, err := applyReversePatch(content, diff)
	if err != nil {
		t.Fatalf("applyReversePatch failed: %v", err)
	}
	if got != "Hello\n" {
		t.Errorf("applyReversePatch = %q, want %q", got, "Hello\n")
	}
}

func TestApplyReversePatch_EmptyDiff(t *testing.T) {
	content := "unchanged\n"
	got, err := applyReversePatch(content, "")
	if err != nil {
		t.Fatalf("applyReversePatch failed: %v", err)
	}
	if got != content {
		t.Errorf("applyReversePatch = %q, want %q", got, content)
	}
}

func TestApplyReversePatch_RoundTrip(t *testing.T) {
	original := "Line one\nLine two\n"
	diff := `--- a
+++ b
@@ -1,2 +1,2 @@
-Line one
-Line two
+Line ONE
+Line TWO
`
	// Forward: original → modified
	modified, err := applyForwardPatch(original, diff)
	if err != nil {
		t.Fatalf("forward patch failed: %v", err)
	}
	// Reverse: modified → original
	restored, err := applyReversePatch(modified, diff)
	if err != nil {
		t.Fatalf("reverse patch failed: %v", err)
	}
	if restored != original {
		t.Errorf("round trip failed: got %q, want %q", restored, original)
	}
}

// --- buildVersionPath ---

func TestBuildVersionPath(t *testing.T) {
	h := &HistoryFile{
		Versions: []VersionEntry{
			{Hash: "aaa", Parent: "none"},
			{Hash: "bbb", Parent: "aaa"},
			{Hash: "ccc", Parent: "bbb"},
		},
	}

	// Base to tip
	path := buildVersionPath("aaa", "ccc", h)
	if path == nil {
		t.Fatal("buildVersionPath returned nil")
	}
	if len(path) != 3 {
		t.Fatalf("len(path) = %d, want 3", len(path))
	}
	if path[0] != "aaa" || path[1] != "bbb" || path[2] != "ccc" {
		t.Errorf("path = %v, want [aaa bbb ccc]", path)
	}

	// Base to middle
	path = buildVersionPath("aaa", "bbb", h)
	if path == nil {
		t.Fatal("buildVersionPath returned nil for base to middle")
	}
	if len(path) != 2 {
		t.Fatalf("len(path) = %d, want 2", len(path))
	}
	if path[0] != "aaa" || path[1] != "bbb" {
		t.Errorf("path = %v, want [aaa bbb]", path)
	}

	// Start equals end
	path = buildVersionPath("bbb", "bbb", h)
	if path == nil {
		t.Fatal("buildVersionPath returned nil for same start/end")
	}
	if len(path) != 1 || path[0] != "bbb" {
		t.Errorf("path = %v, want [bbb]", path)
	}
}

func TestBuildVersionPath_NoPath(t *testing.T) {
	h := &HistoryFile{
		Versions: []VersionEntry{
			{Hash: "aaa", Parent: "none"},
			{Hash: "bbb", Parent: "aaa"},
		},
	}

	// Cannot walk backward (wrong direction)
	path := buildVersionPath("bbb", "aaa", h)
	if path != nil {
		t.Errorf("Expected nil for reverse direction, got %v", path)
	}

	// Nonexistent target
	path = buildVersionPath("aaa", "zzz", h)
	if path != nil {
		t.Errorf("Expected nil for nonexistent target, got %v", path)
	}
}

func TestBuildVersionPath_Branching(t *testing.T) {
	// Simulate a branch: both bbb and ddd have parent aaa
	h := &HistoryFile{
		Versions: []VersionEntry{
			{Hash: "aaa", Parent: "none"},
			{Hash: "bbb", Parent: "aaa"},
			{Hash: "ccc", Parent: "bbb"},
			{Hash: "ddd", Parent: "aaa"},
		},
	}

	// Path to branch 1
	path := buildVersionPath("aaa", "ccc", h)
	if path == nil {
		t.Fatal("buildVersionPath returned nil for branch path")
	}
	if len(path) != 3 || path[0] != "aaa" || path[1] != "bbb" || path[2] != "ccc" {
		t.Errorf("path = %v, want [aaa bbb ccc]", path)
	}

	// Path to branch 2
	path = buildVersionPath("aaa", "ddd", h)
	if path == nil {
		t.Fatal("buildVersionPath returned nil for branch 2")
	}
	if len(path) != 2 || path[0] != "aaa" || path[1] != "ddd" {
		t.Errorf("path = %v, want [aaa ddd]", path)
	}
}

// --- ReconstructVersion (integration) ---

func TestReconstructVersion_BaseVersion(t *testing.T) {
	tmp := t.TempDir()

	// Current canonical content matches v3 (ccc): "Bravo\nCharlie\n"
	writeFile(t, tmp, "post.md", "Bravo\nCharlie\n")
	writeFile(t, tmp, ".versions/post.md", threeVersionHistory)

	content, err := ReconstructVersion(filepath.Join(tmp, "post.md"), "aaa", ".versions")
	if err != nil {
		t.Fatalf("ReconstructVersion failed: %v", err)
	}
	if content != "Alpha" {
		t.Errorf("ReconstructVersion(base) = %q, want %q", content, "Alpha")
	}
}

func TestReconstructVersion_MiddleVersion(t *testing.T) {
	tmp := t.TempDir()

	// Current canonical content matches v3 (ccc): "Bravo\nCharlie\n"
	writeFile(t, tmp, "post.md", "Bravo\nCharlie\n")
	writeFile(t, tmp, ".versions/post.md", threeVersionHistory)

	content, err := ReconstructVersion(filepath.Join(tmp, "post.md"), "bbb", ".versions")
	if err != nil {
		t.Fatalf("ReconstructVersion failed: %v", err)
	}
	if content != "Bravo\n" {
		t.Errorf("ReconstructVersion(middle) = %q, want %q", content, "Bravo\n")
	}
}

func TestReconstructVersion_CurrentVersion(t *testing.T) {
	tmp := t.TempDir()

	writeFile(t, tmp, "post.md", "Bravo\nCharlie\n")
	writeFile(t, tmp, ".versions/post.md", threeVersionHistory)

	content, err := ReconstructVersion(filepath.Join(tmp, "post.md"), "ccc", ".versions")
	if err != nil {
		t.Fatalf("ReconstructVersion failed: %v", err)
	}
	// Current hash = target, so no patches applied; should return the canonical content
	if content != "Bravo\nCharlie\n" {
		t.Errorf("ReconstructVersion(current) = %q, want %q", content, "Bravo\nCharlie\n")
	}
}

func TestReconstructVersion_NotFound(t *testing.T) {
	tmp := t.TempDir()

	writeFile(t, tmp, "post.md", "Bravo\nCharlie\n")
	writeFile(t, tmp, ".versions/post.md", threeVersionHistory)

	_, err := ReconstructVersion(filepath.Join(tmp, "post.md"), "nonexistent", ".versions")
	if err == nil {
		t.Error("Expected error for nonexistent version, got nil")
	}
}

func TestReconstructVersion_MissingVersionsFile(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "post.md", "content\n")

	_, err := ReconstructVersion(filepath.Join(tmp, "post.md"), "aaa", ".versions")
	if err == nil {
		t.Error("Expected error for missing versions file, got nil")
	}
}

func TestReconstructVersion_WithFrontmatter(t *testing.T) {
	tmp := t.TempDir()

	// Canonical file has frontmatter; extractBodyContent should strip it
	canonicalContent := "---\ntitle: My Post\ndate: 2025-01-03\n---\nBravo\nCharlie\n"
	writeFile(t, tmp, "post.md", canonicalContent)
	writeFile(t, tmp, ".versions/post.md", threeVersionHistory)

	// Reconstruct v2 (bbb) - "Bravo"
	content, err := ReconstructVersion(filepath.Join(tmp, "post.md"), "bbb", ".versions")
	if err != nil {
		t.Fatalf("ReconstructVersion failed: %v", err)
	}
	if content != "Bravo\n" {
		t.Errorf("ReconstructVersion with frontmatter = %q, want %q", content, "Bravo\n")
	}
}

// --- reconstructForward ---

func TestReconstructForward_NoBaseVersion(t *testing.T) {
	h := &HistoryFile{
		Versions: []VersionEntry{
			{Hash: "bbb", Parent: "aaa", Diff: "some diff"},
		},
	}
	_, err := reconstructForward("bbb", h)
	if err == nil {
		t.Error("Expected error when no base version exists")
	}
}

func TestReconstructForward_NoPathToTarget(t *testing.T) {
	h := &HistoryFile{
		Versions: []VersionEntry{
			{Hash: "aaa", Parent: "none", FullContent: "Hello\n"},
			// zzz has no parent chain from aaa
		},
	}
	_, err := reconstructForward("zzz", h)
	if err == nil {
		t.Error("Expected error when no path to target")
	}
}

// --- ParseHistoryFile edge cases ---

func TestParseHistoryFile_VersionWithoutEndMarkers(t *testing.T) {
	// If DIFF_END or FULL_CONTENT_END is missing, content should still be captured
	// at the end of the file (the "save last version" logic)
	content := `# VERSION_FILE_FORMAT=1
# CURRENT_HASH=aaa
[VERSION aaa]
TIMESTAMP=2025-01-01T00:00:00Z
PARENT=none
FULL_CONTENT_START
Unterminated content here
`
	tmp := t.TempDir()
	path := writeFile(t, tmp, "test.md", content)

	h, err := ParseHistoryFile(path)
	if err != nil {
		t.Fatalf("ParseHistoryFile failed: %v", err)
	}
	if len(h.Versions) != 1 {
		t.Fatalf("len(Versions) = %d, want 1", len(h.Versions))
	}
	if h.Versions[0].FullContent != "Unterminated content here" {
		t.Errorf("FullContent = %q, want %q", h.Versions[0].FullContent, "Unterminated content here")
	}
}

func TestParseHistoryFile_UnterminatedDiff(t *testing.T) {
	content := `# CURRENT_HASH=bbb
[VERSION bbb]
TIMESTAMP=2025-01-02T00:00:00Z
PARENT=aaa
DIFF_START
--- a
+++ b
@@ -1 +1 @@
-old
+new
`
	tmp := t.TempDir()
	path := writeFile(t, tmp, "test.md", content)

	h, err := ParseHistoryFile(path)
	if err != nil {
		t.Fatalf("ParseHistoryFile failed: %v", err)
	}
	if len(h.Versions) != 1 {
		t.Fatalf("len(Versions) = %d, want 1", len(h.Versions))
	}
	if h.Versions[0].Diff == "" {
		t.Error("Expected non-empty diff for unterminated DIFF_START")
	}
}

func TestParseHistoryFile_MultipleVersionsLastUnterminated(t *testing.T) {
	content := `# CURRENT_HASH=bbb
[VERSION aaa]
TIMESTAMP=2025-01-01T00:00:00Z
PARENT=none
FULL_CONTENT_START
Base content
FULL_CONTENT_END
[VERSION bbb]
TIMESTAMP=2025-01-02T00:00:00Z
PARENT=aaa
DIFF_START
some diff data
`
	tmp := t.TempDir()
	path := writeFile(t, tmp, "test.md", content)

	h, err := ParseHistoryFile(path)
	if err != nil {
		t.Fatalf("ParseHistoryFile failed: %v", err)
	}
	if len(h.Versions) != 2 {
		t.Fatalf("len(Versions) = %d, want 2", len(h.Versions))
	}
	if h.Versions[0].FullContent != "Base content" {
		t.Errorf("v[0].FullContent = %q", h.Versions[0].FullContent)
	}
	if h.Versions[1].Diff != "some diff data" {
		t.Errorf("v[1].Diff = %q", h.Versions[1].Diff)
	}
}

func TestParseHistoryFile_MultilineFullContent(t *testing.T) {
	content := `[VERSION abc]
TIMESTAMP=2025-01-01T00:00:00Z
PARENT=none
FULL_CONTENT_START
# Title

First paragraph.

Second paragraph with **bold**.

- List item one
- List item two
FULL_CONTENT_END
`
	tmp := t.TempDir()
	path := writeFile(t, tmp, "test.md", content)

	h, err := ParseHistoryFile(path)
	if err != nil {
		t.Fatalf("ParseHistoryFile failed: %v", err)
	}
	if len(h.Versions) != 1 {
		t.Fatalf("len(Versions) = %d, want 1", len(h.Versions))
	}

	want := "# Title\n\nFirst paragraph.\n\nSecond paragraph with **bold**.\n\n- List item one\n- List item two"
	if h.Versions[0].FullContent != want {
		t.Errorf("FullContent = %q, want %q", h.Versions[0].FullContent, want)
	}
}

// --- Integration: forward reconstruction with realistic patches ---

func TestReconstructForward_FullChain(t *testing.T) {
	// Build a realistic history and test forward reconstruction
	h := &HistoryFile{
		CurrentHash: "ccc",
		Versions: []VersionEntry{
			{
				Hash:        "aaa",
				Parent:      "none",
				FullContent: "Line one\n",
			},
			{
				Hash:   "bbb",
				Parent: "aaa",
				Diff: `--- a
+++ b
@@ -1 +1,2 @@
 Line one
+Line two
`,
			},
			{
				Hash:   "ccc",
				Parent: "bbb",
				Diff: `--- a
+++ b
@@ -1,2 +1,3 @@
 Line one
 Line two
+Line three
`,
			},
		},
	}

	// Forward reconstruct to v3
	content, err := reconstructForward("ccc", h)
	if err != nil {
		t.Fatalf("reconstructForward failed: %v", err)
	}
	want := "Line one\nLine two\nLine three\n"
	if content != want {
		t.Errorf("reconstructForward = %q, want %q", content, want)
	}

	// Forward reconstruct to v2
	content, err = reconstructForward("bbb", h)
	if err != nil {
		t.Fatalf("reconstructForward failed: %v", err)
	}
	want = "Line one\nLine two\n"
	if content != want {
		t.Errorf("reconstructForward = %q, want %q", content, want)
	}

	// Forward reconstruct to base
	content, err = reconstructForward("aaa", h)
	if err != nil {
		t.Fatalf("reconstructForward failed: %v", err)
	}
	want = "Line one\n"
	if content != want {
		t.Errorf("reconstructForward = %q, want %q", content, want)
	}
}
