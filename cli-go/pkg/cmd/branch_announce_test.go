package cmd

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

func sigPtr(s string) *string { return &s }

// ⭐ A branched site's chain holds a handover signed under the ORIGIN's
// domain; that is what the announcement presents, with the predecessor's key
// as the old key.
func TestBranchHandoverFindsTheHandoverFromAnotherDomain(t *testing.T) {
	chain := &site.KeyHistoryBlock{
		History: []site.KeyHistoryEntry{{Epoch: 0, Key: "ssh-ed25519 K1", ValidFrom: "2026-03-01T00:00:00Z"}},
		Current: site.KeyHistoryEntry{Epoch: 1, Key: "ssh-ed25519 K2", ValidFrom: "2026-10-17T04:00:00Z",
			TransitionSig: sigPtr("SIG"), SigningDomain: "vdibart.polis.pub"},
	}
	source, h, ok := branchHandover(chain, "polis-branch.vercel.app")
	if !ok || source != "vdibart.polis.pub" {
		t.Fatalf("got %q, %v; want the origin", source, ok)
	}
	if h.OldKey != "ssh-ed25519 K1" || h.NewKey != "ssh-ed25519 K2" || h.Timestamp != "2026-10-17T04:00:00Z" || h.TransitionSig != "SIG" {
		t.Fatalf("handover %+v", h)
	}
}

func TestBranchHandoverIgnoresOrdinaryRotations(t *testing.T) {
	for name, chain := range map[string]*site.KeyHistoryBlock{
		"genesis only": {Current: site.KeyHistoryEntry{Key: "K1"}},
		"rotated here": {
			History: []site.KeyHistoryEntry{{Key: "K1"}},
			Current: site.KeyHistoryEntry{Key: "K2", TransitionSig: sigPtr("S")},
		},
		"rotated here, domain named": {
			History: []site.KeyHistoryEntry{{Key: "K1"}},
			Current: site.KeyHistoryEntry{Key: "K2", TransitionSig: sigPtr("S"), SigningDomain: "me.example"},
		},
	} {
		if _, _, ok := branchHandover(chain, "me.example"); ok {
			t.Errorf("%s: found a branch handover where there is none", name)
		}
	}
	if _, _, ok := branchHandover(nil, "me.example"); ok {
		t.Error("a nil chain gave a handover")
	}
}

// ⛔ The request bytes must equal the DS's buildSiteBranchedCanonicalJSON
// (discovery-service/core/handlers/branch.ts) byte for byte, or every
// announcement fails its signature check.
func TestTheBranchAnnouncementCanonicalMatchesTheDS(t *testing.T) {
	got, err := discovery.MakeSiteBranchedCanonicalJSON("b.example", "a.example", "-----BEGIN SSH SIGNATURE-----\nX\n-----END SSH SIGNATURE-----", "2026-11-08T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"action":"site-branched","domain":"b.example","source_domain":"a.example","timestamp":"2026-11-08T12:00:00Z","transition_sig":"-----BEGIN SSH SIGNATURE-----\nX\n-----END SSH SIGNATURE-----"}`
	if string(got) != want {
		t.Fatalf("canonical\n got %s\nwant %s", got, want)
	}
}

func TestBranchAnnounceHelpSaysItIsNotPromptedYet(t *testing.T) {
	for _, want := range []string{"Usage: polis branch announce", "Example:", "Refuses", "never that the original's owner agrees", "nothing runs this for you"} {
		if !strings.Contains(branchAnnounceUsage, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}
