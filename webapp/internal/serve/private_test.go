package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
)

const plantedKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----\n"

// privateSite writes a site holding every private-looking file the shared
// handler was measured against, plus the public files that must still serve.
func privateSite(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		".polis/keys/id_ed25519":  plantedKey,
		".env":                    "POLIS_BASE_URL=https://x\n",
		".git/config":             "[core]\n",
		".ssh/id_ed25519":         plantedKey,
		"content/.git/config":     "[core]\n",
		"content/.git/HEAD":       "ref: refs/heads/main\n",
		"site/.ssh/id_ed25519":    plantedKey,
		"site/id_rsa":             plantedKey,
		"posts/.env":              "X=1\n",
		".DS_Store":               "x",
		"index.html.swp":          "x",
		"index.html.bak":          "x",
		"index.html~":             "x",
		"server.pem":              plantedKey,
		"backup.tar.gz":           "x",
		".htpasswd":               "u:x\n",
		"posts/.hidden.html":      "<p>hidden</p>",
		"site/themes/t/.git/HEAD": "ref: refs/heads/main\n",
		// public
		"index.html":        "<html>home</html>",
		".well-known/polis": `{"public_key":"ssh-ed25519 AAAA"}`,
		"content/pub.polis.core/post/20260101/.versions/hello.md": "---\nversion: 1\n---\n",
		"posts/20260101/hello.html":                               "<p>hello</p>",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func get(t *testing.T, root, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	ServeTenantPublic(w, req, dirStorage{root}, "", nil)
	return w
}

// Every path the handler was measured against, including the four it used to
// serve, is refused; version history and .well-known still serve.
func TestPrivateNamesAreNeverServed(t *testing.T) {
	root := privateSite(t)
	refused := []string{
		"/.polis/keys/id_ed25519", "/.env", "/.git/config", "/.ssh/id_ed25519",
		// the four the handler served before the declared list
		"/content/.git/config", "/content/.git/HEAD", "/site/.ssh/id_ed25519", "/site/id_rsa",
		"/posts/.env", "/.DS_Store", "/index.html.swp", "/index.html.bak", "/index.html~",
		"/server.pem", "/backup.tar.gz", "/.htpasswd", "/posts/.hidden.html", "/site/themes/t/.git/HEAD",
		// case and encoding
		"/CONTENT/.GIT/config", "/content/.Git/HEAD", "/site/ID_RSA", "/%2Epolis/keys/id_ed25519",
	}
	for _, p := range refused {
		if w := get(t, root, p); w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "PRIVATE KEY") {
			t.Errorf("GET %s = %d, want 404", p, w.Code)
		}
	}
	served := map[string]string{
		"/.well-known/polis": "public_key",
		"/content/pub.polis.core/post/20260101/.versions/hello.md": "version: 1",
		"/posts/20260101/hello.html":                               "hello",
		"/posts/20260101/hello":                                    "hello",
	}
	for p, want := range served {
		if w := get(t, root, p); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			t.Errorf("GET %s = %d %q, want 200 containing %q", p, w.Code, w.Body.String(), want)
		}
	}
}

// An extensionless file outside .well-known/ is not served even when no
// private name matches it: it falls through to the .html lookup.
func TestExtensionlessFilesServeOnlyUnderWellKnown(t *testing.T) {
	root := privateSite(t)
	for rel, body := range map[string]string{"posts/notes": "raw", "site/token": "secret"} {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	for _, p := range []string{"/posts/notes", "/site/token"} {
		if w := get(t, root, p); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, w.Code)
		}
	}
	// ...and a page beside a same-named extensionless file still resolves.
	os.WriteFile(filepath.Join(root, "posts/notes.html"), []byte("<p>notes</p>"), 0o644)
	if w := get(t, root, "/posts/notes"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "notes</p>") {
		t.Errorf("GET /posts/notes = %d %q, want the .html page", w.Code, w.Body.String())
	}
}

// ⭐ The fault injection: switch the handler's denial off and the outside-in
// probe, running against the real handler, reports the planted key. With the
// denial in place it reports nothing.
func TestTheProbeCatchesAHandlerRegression(t *testing.T) {
	root := privateSite(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeTenantPublic(w, r, dirStorage{root}, "", nil)
	}))
	defer ts.Close()

	if res := sitecheck.ProbePrivate(ts.URL, "", nil, sitecheck.PrivateProbeTargets()); len(res.Exposures) != 0 {
		t.Fatalf("with the denial in place the probe reported %+v", res.Exposures)
	}

	savedDenied, savedExt := denied, extensionlessServed
	denied = func(string) bool { return false }
	extensionlessServed = func(string) bool { return true }
	defer func() { denied, extensionlessServed = savedDenied, savedExt }()

	res := sitecheck.ProbePrivate(ts.URL, "", nil, sitecheck.PrivateProbeTargets())
	var key, git bool
	for _, e := range res.Exposures {
		if strings.HasSuffix(e.URL, "/.polis/keys/id_ed25519") && e.KeyHeader {
			key = true
		}
		if strings.HasSuffix(e.URL, "/content/.git/config") {
			git = true
		}
	}
	if !key || !git {
		t.Errorf("with the denial off the probe missed the planted key (%v) or nested .git (%v): %+v", key, git, res.Exposures)
	}
}

// Each layer holds on its own. With the name denial off, the extensionless rule
// still keeps the key and git internals in, and a private file WITH a served
// extension is exposed. With the extensionless rule off, the name denial keeps
// everything in.
func TestEachDenialLayerHoldsAlone(t *testing.T) {
	root := privateSite(t)
	os.WriteFile(filepath.Join(root, ".polis/webapp-config.json"), []byte(`{"secret":1}`), 0o644)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeTenantPublic(w, r, dirStorage{root}, "", nil)
	}))
	defer ts.Close()
	exposed := func() map[string]bool {
		out := map[string]bool{}
		for _, e := range sitecheck.ProbePrivate(ts.URL, "", nil, sitecheck.PrivateProbeTargets()).Exposures {
			out[strings.TrimPrefix(e.URL, ts.URL)] = true
		}
		return out
	}

	savedDenied, savedExt := denied, extensionlessServed
	defer func() { denied, extensionlessServed = savedDenied, savedExt }()

	denied = func(string) bool { return false }
	got := exposed()
	if got["/.polis/keys/id_ed25519"] || got["/content/.git/config"] {
		t.Errorf("name denial off: the extensionless rule let an extensionless private file out: %v", got)
	}
	if !got["/.polis/webapp-config.json"] {
		t.Errorf("name denial off: expected the .json under .polis to be exposed, proving the layer matters: %v", got)
	}

	denied, extensionlessServed = savedDenied, func(string) bool { return true }
	if got := exposed(); len(got) != 0 {
		t.Errorf("extensionless rule off: the name denial let out %v", got)
	}
}
