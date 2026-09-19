package sitecheck

import (
	"os"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// An erased chain FAILS, locally, with no DS
// call — and absence alone still does not.

func eraseChain(t *testing.T, dir string) {
	t.Helper()
	raw, err := site.LoadWellKnownRaw(dir)
	if err != nil {
		t.Fatal(err)
	}
	delete(raw, "public_key_history")
	if err := site.SaveWellKnownRaw(dir, raw, site.ChangeKeyHistory); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFailsARotatedSiteWhoseChainWasErased(t *testing.T) {
	k0, k1 := genKey(t), genKey(t)
	dir := khSite(t, k0, true)
	khRotate(t, dir, "alice.polis.pub", k0, k1, "2026-09-15T10:22:03Z")
	if err := site.PublishDIDDocument(dir, "alice.polis.pub"); err != nil {
		t.Fatal(err)
	}
	didDoc, _ := os.ReadFile(site.DIDDocumentPath(dir))
	if n := site.DIDDocumentKeyCount(didDoc); n != 2 {
		t.Fatalf("fixture: did.json names %d keys, want 2", n)
	}
	eraseChain(t, dir)

	r := RunLocalWith(dir, nil)
	if c := findCheck(t, r, "identity.key_history"); c.Outcome != OutcomeFailed {
		t.Fatalf("an erased chain on a rotated site must FAIL, got %+v", c)
	}
	if st := ChainErasure(nil, didDoc); st.OK {
		t.Fatal("the shared predicate passed an erasure")
	}
}

func TestValidateStillTreatsANeverRotatedSiteWithNoChainAsUnchecked(t *testing.T) {
	for name, withDID := range map[string]bool{"with did.json": true, "without did.json": false} {
		t.Run(name, func(t *testing.T) {
			dir := khSite(t, genKey(t), withDID)
			eraseChain(t, dir)
			r := RunLocalWith(dir, nil)
			if c := findCheck(t, r, "identity.key_history"); c.Outcome != OutcomeNotApplicable {
				t.Fatalf("absence on a never-rotated site must not become an alarm, got %+v", c)
			}
		})
	}
}

func TestValidateChecksTheMessagesKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	if c := findCheck(t, RunLocalWith(dir, nil), "identity.messages_key"); c.Outcome != OutcomePassed {
		t.Fatalf("a site init just provisioned must pass, got %+v", c)
	}

	// A swapped key under the old signature is a forgery.
	raw, _ := site.LoadWellKnownRaw(dir)
	block := raw["public_key_messages"].(map[string]interface{})
	block["current"].(map[string]interface{})["key"] = "c3dhcHBlZC1rZXktc3dhcHBlZC1rZXktc3dhcHBlZA=="
	if err := site.SaveWellKnownRaw(dir, raw, site.ChangeMessagesKey); err != nil {
		t.Fatal(err)
	}
	if c := findCheck(t, RunLocalWith(dir, nil), "identity.messages_key"); c.Outcome != OutcomeFailed {
		t.Fatalf("a swapped messages key must FAIL, got %+v", c)
	}

	delete(raw, "public_key_messages")
	if err := site.SaveWellKnownRaw(dir, raw, site.ChangeMessagesKey); err != nil {
		t.Fatal(err)
	}
	c := findCheck(t, RunLocalWith(dir, nil), "identity.messages_key")
	if c.Outcome != OutcomeNotApplicable || c.Reason == "" {
		t.Fatalf("an absent messages key is reported, not failed, and says why: %+v", c)
	}
}
