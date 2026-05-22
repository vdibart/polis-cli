package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/webapp/internal/serve"
)

// newStaticServeTestDir builds a minimal data dir resembling a rendered
// polis tenant (index.html + a posts/ tree + .well-known/polis + a
// blocked .polis/keys file). Returns the dir path.
func newStaticServeTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// /index.html
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body>Index</body></html>"), 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	// /posts/2026/02/post.html
	postDir := filepath.Join(dir, "posts", "2026", "02")
	if err := os.MkdirAll(postDir, 0755); err != nil {
		t.Fatalf("mkdir post: %v", err)
	}
	if err := os.WriteFile(filepath.Join(postDir, "post.html"), []byte("<html><body>Post</body></html>"), 0644); err != nil {
		t.Fatalf("write post: %v", err)
	}

	// /.well-known/polis (allowed dot-path)
	wkDir := filepath.Join(dir, ".well-known")
	if err := os.MkdirAll(wkDir, 0755); err != nil {
		t.Fatalf("mkdir well-known: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wkDir, "polis"), []byte(`{"version":"1"}`), 0644); err != nil {
		t.Fatalf("write polis: %v", err)
	}

	// /.polis/keys/id_ed25519 (blocked dot-path — must 404)
	keysDir := filepath.Join(dir, ".polis", "keys")
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		t.Fatalf("mkdir keys: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, "id_ed25519"), []byte("SECRET"), 0644); err != nil {
		t.Fatalf("write key: %v", err)
	}

	// /stream.js (the v4 SHAPE controller; theme.CopyStreamController installs
	// this at the tenant root so v4 pages' <script src="/stream.js" defer>
	// resolves under --reader / --mirror / hosted modes alike).
	if err := os.WriteFile(filepath.Join(dir, "stream.js"), []byte("(function(){})();"), 0644); err != nil {
		t.Fatalf("write stream.js: %v", err)
	}

	return dir
}

// TestDataDirStorage_ServesIndex ensures GET / returns index.html via
// the shared serve.ServeTenantPublic handler. Smoke-tests the full
// DataDirStorage → serve.Storage → ServeTenantPublic chain.
func TestDataDirStorage_ServesIndex(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for /, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Index") {
		t.Errorf("expected index body, got %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected text/html content-type, got %q", ct)
	}
}

// TestDataDirStorage_ServesPost ensures GET /posts/2026/02/post.html
// returns the rendered post HTML.
func TestDataDirStorage_ServesPost(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodGet, "/posts/2026/02/post.html", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for post, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Post") {
		t.Errorf("expected post body, got %q", w.Body.String())
	}
	// Posts mount gets CORS for cross-origin fetchers (the stream's
	// lazy-fetch path depends on this).
	if ao := w.Header().Get("Access-Control-Allow-Origin"); ao != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got %q", ao)
	}
}

// TestDataDirStorage_ServesWellKnown ensures /.well-known/polis is allowed
// (dot-path exception per the protocol).
func TestDataDirStorage_ServesWellKnown(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/polis", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for .well-known/polis, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json content-type, got %q", ct)
	}
}

// TestDataDirStorage_BlocksPrivatePaths ensures /.polis/keys/id_ed25519
// (private data) returns 404 even though the file exists on disk.
func TestDataDirStorage_BlocksPrivatePaths(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodGet, "/.polis/keys/id_ed25519", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for private dot-path, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "SECRET") {
		t.Error("private file content leaked through static-serve")
	}
}

// TestDataDirStorage_BlocksTraversal ensures path traversal is blocked.
func TestDataDirStorage_BlocksTraversal(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodGet, "/../etc/passwd", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for traversal, got %d", w.Code)
	}
}

// TestDataDirStorage_404OnMissing ensures missing paths return 404.
func TestDataDirStorage_404OnMissing(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodGet, "/posts/does-not-exist.html", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing file, got %d", w.Code)
	}
}

// TestDataDirStorage_ExtensionlessFallsBackToHTML ensures /posts/.../foo
// (no extension) is served as foo.html.
func TestDataDirStorage_ExtensionlessFallsBackToHTML(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodGet, "/posts/2026/02/post", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for extensionless post, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Post") {
		t.Errorf("expected post body, got %q", w.Body.String())
	}
}

// TestDataDirStorage_OptionsPreflight ensures OPTIONS on a CORS-enabled
// path returns 200 with the allow headers (lazy-fetch preflight) and no
// resource body.
func TestDataDirStorage_OptionsPreflight(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodOptions, "/posts/2026/02/post.html", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for OPTIONS preflight, got %d", w.Code)
	}
	if ao := w.Header().Get("Access-Control-Allow-Origin"); ao != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got %q", ao)
	}
	if am := w.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(am, "GET") {
		t.Errorf("expected GET in Access-Control-Allow-Methods, got %q", am)
	}
	if body := w.Body.String(); body != "" {
		t.Errorf("OPTIONS response should not carry a body, got %q", body)
	}
}

// TestDataDirStorage_SourceRedirectHTML ensures GET on a content source
// path (e.g., /content/pub.polis.core/post/.../foo.html) returns 301 with
// a Location header pointing at the configured mount path. The behavior
// is exercised on the hosted side via TestPublicContentSourceRedirectHTML;
// this test closes the symmetry gap on the single-tenant adapter.
func TestDataDirStorage_SourceRedirectHTML(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodGet, "/content/pub.polis.core/post/20260302/slug.html", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/posts/20260302/slug.html" {
		t.Errorf("expected redirect to /posts/20260302/slug.html, got %q", loc)
	}
}

// TestDataDirStorage_OptionsNonCORS ensures OPTIONS on a non-CORS-prefixed
// path (e.g. an HTML or CSS file outside posts/comments/content/.well-known)
// also short-circuits at the top of the handler — no file read, no body.
// SA-1 fix: previously the OPTIONS branch was nested inside the CORS-prefix
// conditional, so OPTIONS on /index.html or /styles.css would fall through
// to write the resource body (protocol non-conformance).
func TestDataDirStorage_OptionsNonCORS(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodOptions, "/index.html", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for OPTIONS on non-CORS path, got %d", w.Code)
	}
	if allow := w.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("expected GET in Allow header, got %q", allow)
	}
	if ao := w.Header().Get("Access-Control-Allow-Origin"); ao != "" {
		t.Errorf("non-CORS path should not get Access-Control-Allow-Origin, got %q", ao)
	}
	if body := w.Body.String(); body != "" {
		t.Errorf("OPTIONS on non-CORS path should not write resource body, got %q", body)
	}
}

// TestDataDirStorage_ServesStreamJS is the regression test for the
// /stream.js MIME-type bug (caught 2026-04-28 during 5.i Phase B local
// testing): serve.ServeTenantPublic's allow-list of extensions was
// missing ".js", so /stream.js returned 404 with text/plain content-
// type, the browser refused to execute the response, and the v4
// stream controller never loaded — silently breaking filter dropdowns,
// the comment panel, lazy-fetch, and pagination. The bug shipped in
// 5.a (commit 579cf05) when the handler was extracted from hosted;
// .js wasn't in the original allow-list either, but no caller had
// actually exercised /stream.js end-to-end through this code path
// until reader-mode local testing.
//
// This test asserts both the 200 and the application/javascript
// content-type so a future allow-list edit doesn't silently regress.
func TestDataDirStorage_ServesStreamJS(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodGet, "/stream.js", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for /stream.js, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("expected application/javascript content-type, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "function") {
		t.Errorf("expected JS body, got %q", w.Body.String())
	}
}

// TestDataDirStorage_BlocksOtherJS_Extensions confirms the .js allow
// is conservative: the file extension itself is allowed, but the
// dot-path block + traversal block + storage's normal access rules
// still apply. This guards against "drop the whole allow-list" from
// being the future fix.
func TestDataDirStorage_StreamJS_Cached(t *testing.T) {
	dir := newStaticServeTestDir(t)
	storage := NewDataDirStorage(dir)

	req := httptest.NewRequest(http.MethodGet, "/stream.js", nil)
	w := httptest.NewRecorder()
	serve.ServeTenantPublic(w, req, storage, "", nil)

	// Cache-Control is no-cache for all tenant-static content (the
	// cache-bust query string `?v=X` on the script tag is what drives
	// browser revalidation).
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control: no-cache, got %q", cc)
	}
}
