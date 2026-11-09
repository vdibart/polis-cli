package dm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ A peer must be a domain: convKey("..") was the DM directory itself, so
// DeleteConversation("..") would have deleted every conversation.
func TestAPeerThatIsNotADomainIsRefused(t *testing.T) {
	dir := t.TempDir()
	mb := NewMailbox(storage.At(dir))
	keep := filepath.Join(dir, filepath.FromSlash(DMDirKey), conversationsSubdir, "bob.example", "conversation.json")
	_ = os.MkdirAll(filepath.Dir(keep), 0o700)
	_ = os.WriteFile(keep, []byte("{}"), 0o600)

	for _, peer := range []string{"..", ".", "", "../..", "a/b", `a\b`, "bob.example/../..", ".hidden", "a..b"} {
		if _, err := mb.convKey(peer); err == nil {
			t.Errorf("convKey(%q) accepted", peer)
		}
		if err := mb.DeleteConversation(peer); err == nil {
			t.Errorf("DeleteConversation(%q) succeeded", peer)
		}
		if _, err := mb.LoadMeta(peer); err == nil {
			t.Errorf("LoadMeta(%q) succeeded", peer)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a refused peer removed another conversation: %v", err)
	}
	for _, peer := range []string{"bob.example", "Bob.Example", "localhost:8080", "a-b.polis.pub"} {
		if _, err := mb.convKey(peer); err != nil {
			t.Errorf("convKey(%q) refused a real peer: %v", peer, err)
		}
	}
}
