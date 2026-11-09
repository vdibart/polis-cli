package provision

import "testing"

// ⛔ A deploy publishes only what the self-hosted serve handler would answer:
// the scan found notes, backups, databases and old exports in a site
// directory going public on Vercel.
func TestADeployPublishesOnlyWhatServeWouldAnswer(t *testing.T) {
	for key, want := range map[string]bool{
		"index.html": true, "posts/20261013/hello.html": true, ".well-known/polis": true,
		"robots.txt": true, "rsl.xml": true, "styles.css": true, "stream.js": true, "public.jsonl": true,
		"notes.txt": false, "backup.tar.gz": false, "site.sqlite": false, "README": false,
		"deploy.toml": false, "content/x/secrets.yaml": false, "polis-export-2026.zip": false,
		".polis/keys/id_ed25519": false, ".env.local": false,
	} {
		if got := IsPublicKey(key); got != want {
			t.Errorf("IsPublicKey(%q) = %v, want %v", key, got, want)
		}
	}
}
