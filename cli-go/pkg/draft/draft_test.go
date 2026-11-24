package draft

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// A draft is saved new, replaced by id, and discarded; an id is only ever a
// file name, and a draft over the limit is refused whole.
func TestADraftIsSavedReplacedAndDiscarded(t *testing.T) {
	site := storage.At(t.TempDir())
	id, err := Save(site, "", "first")
	if err != nil || id == "" {
		t.Fatalf("save = %q, %v", id, err)
	}
	if again, err := Save(site, id, "second"); err != nil || again != id {
		t.Fatalf("replace = %q, %v; want %q", again, err, id)
	}
	if got, _ := site.Store().Get(t.Context(), Key(id)); string(got) != "second" {
		t.Errorf("draft reads %q, want the replacement", got)
	}
	if err := Discard(site, id); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if err := Discard(site, id); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("discarding again = %v, want fs.ErrNotExist", err)
	}
	if k := Key("../../keys/id_ed25519"); strings.Contains(k, "..") || !strings.HasPrefix(k, Dir+"/") {
		t.Errorf("Key of a path = %q, want a name under %s", k, Dir)
	}
	if _, err := Save(site, "", strings.Repeat("x", MaxSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an oversize draft = %v, want ErrTooLarge", err)
	}
	if err := Discard(site, ""); err == nil {
		t.Error("discarding with no id succeeded")
	}
}
