package render

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// --- buildOGDescription ---

func TestBuildOGDescription_TruncatesAtWordBoundary(t *testing.T) {
	body := strings.Repeat("hello world ", 30) // ~360 chars, well over 150
	got := buildOGDescription(body)
	if len(got) > maxOGDescriptionLen+5 { // +5 for the ellipsis
		t.Errorf("description length = %d, want ≤ %d (incl ellipsis)", len(got), maxOGDescriptionLen+5)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected truncation marker; got %q", got)
	}
	// Truncation should be at a word boundary — last char before ellipsis
	// must not be in the middle of a word. Crude check: the char before
	// ellipsis is a letter and the next char (eaten by truncation) was a
	// space — which is the case for our "hello world hello world..." input
	// since the truncation lands on a space.
	if strings.Contains(strings.TrimSuffix(got, "…"), " hel") || strings.HasSuffix(strings.TrimSuffix(got, "…"), "wor") {
		// "wor" suffix would mean we cut "world" mid-word — not OK.
		// Allow "world" but not "wor".
		stripped := strings.TrimSuffix(got, "…")
		if strings.HasSuffix(stripped, "wor") {
			t.Errorf("truncation cut mid-word: %q", got)
		}
	}
}

func TestBuildOGDescription_EmptyBody(t *testing.T) {
	if got := buildOGDescription(""); got != "" {
		t.Errorf("empty body should yield empty description; got %q", got)
	}
}

func TestBuildOGDescription_StripMarkdown(t *testing.T) {
	body := "# Heading\n\nParagraph with **bold** and *italic* and [link](https://x.example).\n"
	got := buildOGDescription(body)
	// All markdown syntax should be stripped to plain text.
	for _, marker := range []string{"**", "*", "[", "](", "#"} {
		if strings.Contains(got, marker) {
			t.Errorf("expected markdown marker %q stripped; got %q", marker, got)
		}
	}
}

// --- attrEscape ---

func TestAttrEscape_HandlesSpecials(t *testing.T) {
	cases := map[string]string{
		`Hello "world"`:  `Hello &#34;world&#34;`,
		`a < b & c > d`:  `a &lt; b &amp; c &gt; d`,
		`<script>x</script>`: `&lt;script&gt;x&lt;/script&gt;`,
	}
	for in, want := range cases {
		if got := attrEscape(in); got != want {
			t.Errorf("attrEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- JSON-LD: validity + defensive escaping ---

func TestBuildBlogPostingJSONLD_ValidJSON(t *testing.T) {
	jsonLD, err := buildBlogPostingJSONLD(
		"My Post", "2026-04-23T12:00:00Z", "2026-04-23T12:00:00Z",
		"Alice", "https://alice.example", "https://alice.example/posts/20260423/my-post.html",
	)
	if err != nil {
		t.Fatalf("buildBlogPostingJSONLD: %v", err)
	}
	body := stripScriptTag(t, jsonLD)
	var probe map[string]interface{}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("JSON-LD body not valid JSON: %v\n%s", err, body)
	}
	if probe["@type"] != "BlogPosting" {
		t.Errorf("@type = %v, want BlogPosting", probe["@type"])
	}
	if probe["headline"] != "My Post" {
		t.Errorf("headline = %v", probe["headline"])
	}
}

func TestBuildBlogPostingJSONLD_DefensiveScriptEscape(t *testing.T) {
	// Adversarial title containing literal "</script>" must not appear in the
	// rendered script-tag's text content (an HTML parser would close the tag).
	jsonLD, err := buildBlogPostingJSONLD(
		`Title with </script> embedded`,
		"2026-04-23T12:00:00Z", "2026-04-23T12:00:00Z",
		"Alice", "https://alice.example", "https://alice.example/post.html",
	)
	if err != nil {
		t.Fatalf("buildBlogPostingJSONLD: %v", err)
	}
	if strings.Contains(jsonLD, `</script>`) && strings.Index(jsonLD, `</script>`) < strings.LastIndex(jsonLD, `</script>`) {
		// More than one </script> sequence — the first one (in body) would
		// close the tag prematurely.
		t.Errorf("unsafe </script> sequence in JSON-LD body. Output:\n%s", jsonLD)
	}
	// Confirm the body still parses as JSON (escaping must be JSON-spec valid).
	body := stripScriptTag(t, jsonLD)
	var probe map[string]interface{}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("JSON-LD body not valid JSON after defensive escape: %v\n%s", err, body)
	}
	headline, _ := probe["headline"].(string)
	if !strings.Contains(headline, "</script>") {
		// JSON parser unescapes <\/script back to </script — so the value
		// the parser sees is still the original. We just need the on-the-wire
		// representation to escape the slash.
		t.Errorf("JSON parser should still recover the literal </script> in headline; got %q", headline)
	}
}

func TestBuildWebSiteJSONLD_ValidJSON(t *testing.T) {
	jsonLD, err := buildWebSiteJSONLD("Alice's Site", "https://alice.example/", "Tagline here.")
	if err != nil {
		t.Fatalf("buildWebSiteJSONLD: %v", err)
	}
	body := stripScriptTag(t, jsonLD)
	var probe map[string]interface{}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("WebSite JSON-LD invalid: %v\n%s", err, body)
	}
	if probe["@type"] != "WebSite" {
		t.Errorf("@type = %v, want WebSite", probe["@type"])
	}
}

// --- formatISO ---

func TestFormatISO(t *testing.T) {
	cases := map[string]string{
		"2026-04-23T12:00:00Z":      "2026-04-23T12:00:00Z",
		"2026-04-23T12:00:00+00:00": "2026-04-23T12:00:00Z", // RFC3339 with TZ → UTC
		"":                          "",
		"not-a-time":                "",
	}
	for in, want := range cases {
		if got := formatISO(in); got != want {
			t.Errorf("formatISO(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- End-to-end: rendered HTML contains the head-block ---

// TestRenderAll_StreamMetadataHeadBlock verifies the full stream render produces the
// 3.d head-block on per-post pages and on /index.html.
func TestRenderAll_StreamMetadataHeadBlock(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	writeStreamPost(t, tempDir, "20260101", "alpha", "Alpha", "First post body content.")
	writeStreamPost(t, tempDir, "20260102", "bravo", "Bravo", "Second post body content.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// Per-post: alpha must have OG, Twitter, JSON-LD BlogPosting.
	alphaHTML := mustReadFile(t, filepath.Join(tempDir, "posts", "20260101", "alpha.html"))
	for _, want := range []string{
		`<meta property="og:title" content="Alpha">`,
		`<meta property="og:url" content="https://example.com/posts/20260101/alpha.html">`,
		`<meta property="og:type" content="article">`,
		`<meta property="og:image" content="https://example.com/favicon.svg">`,
		`<meta name="twitter:card" content="summary">`,
		`<meta name="twitter:description" content=`,
		`<script type="application/ld+json">`,
		`"@type": "BlogPosting"`,
		`"headline": "Alpha"`,
	} {
		if !strings.Contains(alphaHTML, want) {
			t.Errorf("alpha.html missing %q", want)
		}
	}
	// og:description should contain the body excerpt (or fall back to title).
	if !strings.Contains(alphaHTML, `og:description`) {
		t.Error("alpha.html missing og:description")
	}

	// Index: WebSite JSON-LD (not BlogPosting).
	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	if !strings.Contains(indexHTML, `"@type": "WebSite"`) {
		t.Errorf("index.html should use WebSite JSON-LD; not found")
	}
	if strings.Contains(indexHTML, `"@type": "BlogPosting"`) {
		t.Errorf("index.html should NOT use BlogPosting JSON-LD")
	}
}

func stripScriptTag(t *testing.T, scriptHTML string) []byte {
	t.Helper()
	const open = `<script type="application/ld+json">`
	const close = `</script>`
	start := strings.Index(scriptHTML, open)
	end := strings.LastIndex(scriptHTML, close)
	if start < 0 || end < 0 {
		t.Fatalf("not a script tag: %q", scriptHTML)
	}
	body := scriptHTML[start+len(open) : end]
	body = strings.TrimSpace(body)
	// Reverse the defensive escape so the JSON parser sees valid JSON (the
	// runtime browser/crawler does this automatically; we mimic it for the
	// test).
	body = strings.ReplaceAll(body, `<\/`, `</`)
	return []byte(body)
}
