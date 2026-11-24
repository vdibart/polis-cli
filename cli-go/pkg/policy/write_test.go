package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func decide(t *testing.T, site storage.Site, typ, source string) Decision {
	t.Helper()
	pols, err := LoadPolicies(site)
	if err != nil {
		t.Fatal(err)
	}
	return Evaluate(pols, Event{Type: typ, ActorDomain: source}, EvalContext{})
}

// ⛔ The first-match case behind the rule: an added deny decides first, so it
// tightens even above the owner's own bless; the owner's lines are untouched;
// only deny can be added; only what was added can be removed.
func TestTightenOnlyNeverLoosens(t *testing.T) {
	site := storage.At(t.TempDir())
	ctx := context.Background()
	owner := `{"version":2,"generator":"test"}
{"active":true,"policy":"bless pub.polis.comment from all"}
`
	if err := site.Store().Put(ctx, PrivateRulesKey, []byte(owner)); err != nil {
		t.Fatal(err)
	}
	before := decide(t, site, "pub.polis.comment", "spam.example")

	if err := TightenPrivate(site, "deny pub.polis.comment from all at spam.example", "api"); err != nil {
		t.Fatal(err)
	}
	after := decide(t, site, "pub.polis.comment", "spam.example")
	if after == before || after != Deny {
		t.Errorf("after adding a deny: %v (before %v), want deny", after, before)
	}
	data, _ := site.Store().Get(ctx, PrivateRulesKey)
	if !strings.HasPrefix(string(data), `{"version":2,"generator":"test"}`) || !strings.Contains(string(data), `{"active":true,"policy":"bless pub.polis.comment from all"}`) {
		t.Errorf("the owner's lines changed:\n%s", data)
	}

	// review is weaker than deny: on top, it would turn the owner's denials
	// into waiting comments, so it is refused too.
	for _, loosening := range []string{"allow pub.polis.dm from all", "bless pub.polis.comment from all", "review pub.polis.comment from all"} {
		if err := TightenPrivate(site, loosening, "api"); err != ErrNotTightening {
			t.Errorf("%q: err = %v, want ErrNotTightening", loosening, err)
		}
	}
	// The owner's own bless can't be removed this way; the added deny can.
	if removed, _ := RemoveAdded(site, "bless pub.polis.comment from all", "api"); removed {
		t.Error("removed the owner's own rule")
	}
	if removed, err := RemoveAdded(site, "deny pub.polis.comment from all at spam.example", "api"); err != nil || !removed {
		t.Errorf("remove the added rule: %v %v", removed, err)
	}
	if got := decide(t, site, "pub.polis.comment", "spam.example"); got != before {
		t.Errorf("after removing it: %v, want %v again", got, before)
	}
}

// A site with no private file gets one, header first.
func TestTightenCreatesThePrivateFile(t *testing.T) {
	site := storage.At(t.TempDir())
	if err := TightenPrivate(site, "deny pub.polis.comment from all at spam.example", "api"); err != nil {
		t.Fatal(err)
	}
	data, _ := site.Store().Get(context.Background(), PrivateRulesKey)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"version":2`) || !strings.Contains(lines[1], `"added_by":"api"`) {
		t.Errorf("file = %q", data)
	}
}

// A file with added rules still parses, so Tailor and Medic treat it as the
// site's own rules (left as they are), never as damage.
func TestTightenedFileStillParses(t *testing.T) {
	site := storage.At(t.TempDir())
	if err := TightenPrivate(site, "deny pub.polis.dm from all at spam.example", "api"); err != nil {
		t.Fatal(err)
	}
	data, _ := site.Store().Get(context.Background(), PrivateRulesKey)
	if !Parses(data) {
		t.Errorf("a tightened file does not parse:\n%s", data)
	}
}

// ⛔ The case review found: the owner denies a site's comments; review on top
// would have turned that deny into a waiting comment. It is refused.
func TestReviewCannotOverrideAnOwnersDeny(t *testing.T) {
	site := storage.At(t.TempDir())
	owner := `{"version":2,"generator":"test"}
{"active":true,"policy":"deny pub.polis.comment from all at troll.example"}
`
	if err := site.Store().Put(context.Background(), PrivateRulesKey, []byte(owner)); err != nil {
		t.Fatal(err)
	}
	if err := TightenPrivate(site, "review pub.polis.comment from all", "api"); err != ErrNotTightening {
		t.Fatalf("review on top: err = %v, want ErrNotTightening", err)
	}
	if got := decide(t, site, "pub.polis.comment", "troll.example"); got != Deny {
		t.Errorf("the owner's deny became %v", got)
	}
}
