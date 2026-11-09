package publicfile

import "testing"

func TestServable(t *testing.T) {
	for key, want := range map[string]bool{
		"index.html": true, "posts/2026/a.html": true, "styles.css": true, "stream.js": true,
		".well-known/polis": true, "robots.txt": true, "rsl.xml": true,
		"content/pub.polis.core/post/20260101/a.md": true,
		// What the scan found a deploy published and serve never would.
		"notes.txt": false, "backup.tar.gz": false, "site.sqlite": false, "README": false,
		"deploy.toml": false, "content/x/secrets.yaml": false, "polis-export.zip": false,
		"posts/robots.txt": false,
		// Private names, whatever their extension.
		".polis/keys/id_ed25519": false, ".polis/webapp/config.json": false, ".env": false, ".git/HEAD": false,
	} {
		if got := Servable(key); got != want {
			t.Errorf("Servable(%q) = %v, want %v", key, got, want)
		}
	}
}
