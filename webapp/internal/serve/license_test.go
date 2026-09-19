package serve

import (
	"net/http/httptest"
	"testing"
)

func TestHeadersComeFromTheResourceNotTheSite(t *testing.T) {
	// The header must reflect THIS document's terms. If it were resolved from
	// the site licence, a post that overrides the default would ship a header
	// contradicting its own signed frontmatter.
	md := "---\n" +
		"title: An open post\n" +
		"license:\n" +
		"  v: pub.polis.license.v1\n" +
		"  profile: pub.polis.license.open/1\n" +
		"  train-ai: y\n" +
		"  search: y\n" +
		"  terms: https://maya.example/license\n" +
		"signature: abc\n" +
		"---\n\nBody.\n"

	w := httptest.NewRecorder()
	setLicenseHeaders(w, ".md", []byte(md))

	if got, want := w.Header().Get("Content-Usage"), "train-ai=y, search=y"; got != want {
		t.Errorf("Content-Usage = %q, want %q", got, want)
	}
	if got, want := w.Header().Get("Link"), `<https://maya.example/license>; rel="license"`; got != want {
		t.Errorf("Link = %q, want %q", got, want)
	}
}

func TestHTMLHeadersAreReadBackFromWhatTheRendererEmitted(t *testing.T) {
	html := `<!DOCTYPE html><html><head>` +
		`<meta name="content-usage" content="train-ai=n, search=y">` +
		`<link rel="license" href="https://maya.example/license">` +
		`</head><body>Body</body></html>`

	w := httptest.NewRecorder()
	setLicenseHeaders(w, ".html", []byte(html))

	if got, want := w.Header().Get("Content-Usage"), "train-ai=n, search=y"; got != want {
		t.Errorf("Content-Usage = %q, want %q", got, want)
	}
	if got, want := w.Header().Get("Link"), `<https://maya.example/license>; rel="license"`; got != want {
		t.Errorf("Link = %q, want %q", got, want)
	}
}

func TestBodyContentCannotForgeALicenceHeader(t *testing.T) {
	// A post whose BODY quotes a licence meta tag must not have it promoted
	// into a response header — only the head is authoritative.
	html := `<!DOCTYPE html><html><head><title>t</title></head><body>` +
		`Here is an example: &lt;meta name="content-usage" content="train-ai=y"&gt;` +
		`<meta name="content-usage" content="train-ai=y">` +
		`</body></html>`

	w := httptest.NewRecorder()
	setLicenseHeaders(w, ".html", []byte(html))

	if got := w.Header().Get("Content-Usage"); got != "" {
		t.Errorf("Content-Usage = %q, want empty — body content must not reach headers", got)
	}
}

func TestNoHeadersWhenNothingIsStated(t *testing.T) {
	// An empty Content-Usage parses as a statement expressing no preference,
	// which looks like an assertion while saying nothing. Silence is better.
	for _, tc := range []struct {
		name string
		ext  string
		data string
	}{
		{"markdown with no licence", ".md", "---\ntitle: t\nsignature: abc\n---\n\nBody.\n"},
		{"html with no licence", ".html", `<html><head><title>t</title></head><body>x</body></html>`},
		{"a stylesheet", ".css", "body{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			setLicenseHeaders(w, tc.ext, []byte(tc.data))
			if got := w.Header().Get("Content-Usage"); got != "" {
				t.Errorf("Content-Usage = %q, want empty", got)
			}
			if got := w.Header().Get("Link"); got != "" {
				t.Errorf("Link = %q, want empty", got)
			}
		})
	}
}
