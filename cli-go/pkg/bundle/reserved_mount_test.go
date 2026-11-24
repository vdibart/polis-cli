package bundle

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ No content type may be mounted where the owner's interface lives (/_ and
// everything under it) or where the site's own pages are: reading a bundle
// refuses one, and so does every writer of bundle.json.
func TestAMountUnderTheOwnersInterfaceIsRefused(t *testing.T) {
	for _, m := range []string{"_", "/_", "/_/", "/_/x", "_/notes", "/pql", "/about"} {
		if !ReservedMount(m) {
			t.Errorf("ReservedMount(%q) = false", m)
		}
	}
	for _, m := range []string{"/posts", "/_x", "/notes/_", "/aboutme"} {
		if ReservedMount(m) {
			t.Errorf("ReservedMount(%q) = true", m)
		}
	}

	b := DefaultCoreBundle()
	ct := b.Types["pub.polis.post"]
	ct.Mount = "/_/posts"
	b.Types["pub.polis.post"] = ct
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("Validate accepted a mount under /_: %v", err)
	}
	site := storage.At(t.TempDir())
	if err := SaveBundleAt(site, "content/pub.polis.core/bundle.json", b); err == nil {
		t.Error("SaveBundleAt wrote a bundle with a mount under /_")
	}
	if err := SaveBundle(t.TempDir()+"/bundle.json", b); err == nil {
		t.Error("SaveBundle wrote a bundle with a mount under /_")
	}
}
