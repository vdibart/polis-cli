package provision

import (
	"strings"
	"testing"
)

const sha40 = "0123456789abcdef0123456789abcdef01234567"

// ⭐ Public keys become static files under .vercel/output/static/, sorted.
func TestStaticDeploymentFilesMapsThePublicTree(t *testing.T) {
	got, err := staticDeploymentFiles([]StaticFile{
		{Key: "posts/2026/hello.html", SHA: sha40, Size: 10},
		{Key: ".well-known/polis", SHA: sha40, Size: 2},
		{Key: "index.html", SHA: sha40, Size: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".vercel/output/static/.well-known/polis", ".vercel/output/static/index.html",
		".vercel/output/static/posts/2026/hello.html"}
	if len(got) != len(want) {
		t.Fatalf("got %d files", len(got))
	}
	for i, f := range got {
		if f.File != want[i] || f.Mode != modeFile || f.SHA != sha40 {
			t.Errorf("file %d = %+v; want %s", i, f, want[i])
		}
	}
}

// ⛔⛔ No private name ever becomes a static file, and one in the list refuses
// the whole deployment rather than being skipped.
func TestStaticDeploymentFilesRefusesEveryPrivateName(t *testing.T) {
	for _, key := range []string{
		".polis/keys/id_ed25519", ".polis/keys/id_ed25519.pub", ".polis/storage-salt", ".polis/deploy/token",
		".env", "posts/.env", "site/prod.env", ".env.local", "content/.git/config", ".ssh/id_rsa",
		"site/id_rsa", "", "/index.html", "posts/../.polis/x", " index.html", "a\\b",
	} {
		_, err := staticDeploymentFiles([]StaticFile{{Key: "index.html", SHA: sha40}, {Key: key, SHA: sha40}})
		if err == nil {
			t.Errorf("%q was deployed as a static file", key)
			continue
		}
		if key != "" && strings.Contains(err.Error(), sha40) {
			t.Errorf("the error names a digest: %v", err)
		}
	}
	if IsPublicKey(".polis/keys/id_ed25519") || !IsPublicKey(".well-known/polis") || !IsPublicKey("index.html") {
		t.Error("IsPublicKey disagrees with the list")
	}
}

// A digest that is not one, or a key listed twice, is refused.
func TestStaticDeploymentFilesRefusesBadEntries(t *testing.T) {
	for name, files := range map[string][]StaticFile{
		"short digest": {{Key: "a.html", SHA: "abc"}},
		"upper digest": {{Key: "a.html", SHA: strings.ToUpper(sha40)}},
		"negative":     {{Key: "a.html", SHA: sha40, Size: -1}},
		"twice":        {{Key: "a.html", SHA: sha40}, {Key: "a.html", SHA: sha40}},
	} {
		if _, err := staticDeploymentFiles(files); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
