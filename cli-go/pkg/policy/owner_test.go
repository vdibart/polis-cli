package policy

import (
	"errors"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐ The owner replaces a rule file with any valid rules; one that does not
// parse refuses the whole list, naming it, and changes nothing.
func TestTheOwnerReplacesTheirRulesWithValidOnes(t *testing.T) {
	site := storage.At(t.TempDir())
	if err := ResetDefaults(site); err != nil {
		t.Fatal(err)
	}
	none := []string{"allow pub.polis.dm from following", "deny pub.polis.dm from all", "bless pub.polis.comment from self", "review pub.polis.comment from all", "deny all from all"}
	if err := SetRules(site, PublicFile, none); err != nil {
		t.Fatal(err)
	}
	_, public, err := LoadPolicyFiles(site)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range public {
		if p.Rule != "" {
			got = append(got, p.Rule)
		}
	}
	if strings.Join(got, "|") != strings.Join(none, "|") {
		t.Errorf("public rules = %v, want %v", got, none)
	}

	err = SetRules(site, PublicFile, []string{"bless pub.polis.comment from all", "bless the moon from everyone", "", "permit pub.polis.dm from all"})
	var bad *InvalidRulesError
	if !errors.As(err, &bad) || len(bad.Problems) != 2 || bad.Problems[0].Index != 1 || bad.Problems[1].Rule != "permit pub.polis.dm from all" {
		t.Fatalf("invalid rules = %v, want both named, by place", err)
	}
	_, after, _ := LoadPolicyFiles(site)
	if len(after) != len(public) {
		t.Errorf("a refused list changed the rules: %d → %d", len(public), len(after))
	}
}

// ⭐ Restoring the defaults puts both files back, whatever was there, the
// rules an agent added included.
func TestRestoringTheDefaultsPutsBothFilesBack(t *testing.T) {
	site := storage.At(t.TempDir())
	_ = SetRules(site, PublicFile, []string{"bless pub.polis.comment from all"})
	_ = SetRules(site, PrivateFile, []string{"deny pub.polis.comment from all at spam.example"})
	if err := TightenPrivate(site, "deny pub.polis.dm from all at x.example", "api"); err != nil {
		t.Fatal(err)
	}
	if err := ResetDefaults(site); err != nil {
		t.Fatal(err)
	}
	for _, f := range []File{PublicFile, PrivateFile} {
		data, err := site.Store().Get(t.Context(), f.RelPath())
		if err != nil {
			t.Fatal(err)
		}
		if Classify(data, f) == Authored {
			t.Errorf("%s rules after a reset are not the defaults: %s", f, data)
		}
	}
}
