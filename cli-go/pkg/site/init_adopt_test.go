package site

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const adoptBaseURL = "https://alice.example.com"

// keyedObjectSite is an object-store site holding only an identity key, as an
// instance's store is after its first start.
func keyedObjectSite(t *testing.T) (storage.Site, IdentityKey) {
	t.Helper()
	s := storage.On("/site", storage.NewObjectStore(""))
	k, err := EnsureIdentityKey(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return s, k
}

// ⭐ An instance's store holds only its key; the adopting init writes the site
// around that key and publishes it, with nothing stated on the owner's behalf.
func TestInitAdoptingAKeyWritesTheSiteAroundIt(t *testing.T) {
	ctx := context.Background()
	s, k := keyedObjectSite(t)

	res, err := Init(s, InitOptions{AdoptExistingKey: true, BaseURL: adoptBaseURL})
	if err != nil {
		t.Fatal(err)
	}
	if res.PublicKey != strings.TrimSpace(string(k.Public)) {
		t.Errorf("result public key = %q, want the adopted key", res.PublicKey)
	}
	priv, err := s.Store().Get(ctx, privateKeyKey)
	if err != nil || !bytes.Equal(priv, k.Private) {
		t.Fatalf("the private key changed under an adopting init (%v)", err)
	}

	wk, err := LoadWellKnown(s)
	if err != nil {
		t.Fatal(err)
	}
	if wk.PublicKey != strings.TrimSpace(string(k.Public)) {
		t.Errorf(".well-known/polis public_key = %q, want the stored key", wk.PublicKey)
	}
	if wk.AuthorName != "" {
		t.Errorf("author_name = %q; an instance has no author to state", wk.AuthorName)
	}
	if wk.SiteTitle != "alice.example.com" {
		t.Errorf("site_title = %q, want the domain", wk.SiteTitle)
	}
	block, err := LoadKeyHistory(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyChain(block, "alice.example.com"); err != nil {
		t.Errorf("the genesis entry does not verify: %v", err)
	}
	for _, key := range []string{"content/pub.polis.core/bundle.json", bundle.RegistryKey, ".well-known/did.json"} {
		if ok, err := s.Store().Exists(ctx, key); err != nil || !ok {
			t.Errorf("%s not written (%v)", key, err)
		}
	}
	// ⛔ Nothing stated on the owner's behalf, and no public .gitignore.
	for _, key := range []string{"content/pub.polis.core/license/license.json", ".gitignore"} {
		if ok, _ := s.Store().Exists(ctx, key); ok {
			t.Errorf("%s written by an adopting init", key)
		}
	}
	if res.License != "" {
		t.Errorf("licence stated: %q", res.License)
	}
}

// ⛔ The CLI path is unchanged: a site with a key is refused, and the key is
// left as it was.
func TestInitWithoutAdoptStillRefusesAnExistingKey(t *testing.T) {
	ctx := context.Background()
	s, k := keyedObjectSite(t)

	_, err := Init(s, InitOptions{BaseURL: adoptBaseURL})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("Init over an existing key: err = %v, want the refusal", err)
	}
	priv, _ := s.Store().Get(ctx, privateKeyKey)
	if !bytes.Equal(priv, k.Private) {
		t.Error("a refused init changed the key")
	}
	if ok, _ := s.Store().Exists(ctx, WellKnownKey); ok {
		t.Error("a refused init wrote .well-known/polis")
	}
}

// ⛔⛔ Racing starts: exactly one initialises; the rest write nothing and say so.
func TestTwoAdoptingInitsRaceAndOneWins(t *testing.T) {
	s, _ := keyedObjectSite(t)
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = Init(s, InitOptions{AdoptExistingKey: true, BaseURL: adoptBaseURL})
		}(i)
	}
	wg.Wait()
	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrAlreadyInitialised):
		default:
			t.Errorf("init %d: %v, want success or ErrAlreadyInitialised", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d inits succeeded; exactly one may", won)
	}
	if _, err := LoadWellKnown(s); err != nil {
		t.Fatalf("the winner's .well-known/polis does not load: %v", err)
	}
}

// AC: a second start finds the site and changes nothing.
func TestAdoptOnAnInitialisedSiteChangesNothing(t *testing.T) {
	ctx := context.Background()
	s, _ := keyedObjectSite(t)
	if _, err := Init(s, InitOptions{AdoptExistingKey: true, BaseURL: adoptBaseURL}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Store().Get(ctx, WellKnownKey)
	if _, err := Init(s, InitOptions{AdoptExistingKey: true, BaseURL: adoptBaseURL}); !errors.Is(err, ErrAlreadyInitialised) {
		t.Fatalf("second init: %v, want ErrAlreadyInitialised", err)
	}
	after, _ := s.Store().Get(ctx, WellKnownKey)
	if !bytes.Equal(before, after) {
		t.Error("a second start rewrote .well-known/polis")
	}
}

// AC: a key and part of a site, with no .well-known/polis, is not
// re-initialised: nothing is written.
func TestAdoptOnAPartialSiteWritesNothing(t *testing.T) {
	ctx := context.Background()
	s, _ := keyedObjectSite(t)
	planted := []byte(`{"partial":true}`)
	if err := s.Store().Put(ctx, "content/pub.polis.core/bundle.json", planted); err != nil {
		t.Fatal(err)
	}
	before, err := s.Store().List(ctx, "", storage.Recursive())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Init(s, InitOptions{AdoptExistingKey: true, BaseURL: adoptBaseURL}); !errors.Is(err, ErrPartialSite) {
		t.Fatalf("init on a partial site: %v, want ErrPartialSite", err)
	}
	after, _ := s.Store().List(ctx, "", storage.Recursive())
	if len(after) != len(before) {
		t.Errorf("store went from %d to %d keys; a refused init writes nothing", len(before), len(after))
	}
	got, _ := s.Store().Get(ctx, "content/pub.polis.core/bundle.json")
	if !bytes.Equal(got, planted) {
		t.Error("the partial site's bundle.json was changed")
	}
}

// ⛔ No domain, no site; no key, no generation.
func TestAdoptRefusesWithoutADomainOrAKey(t *testing.T) {
	ctx := context.Background()
	s, _ := keyedObjectSite(t)
	if _, err := Init(s, InitOptions{AdoptExistingKey: true}); err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Errorf("init with no base URL: %v, want a refusal naming it", err)
	}
	if ok, _ := s.Store().Exists(ctx, WellKnownKey); ok {
		t.Error("init with no base URL wrote .well-known/polis")
	}

	empty := storage.On("/site", storage.NewObjectStore(""))
	if _, err := Init(empty, InitOptions{AdoptExistingKey: true, BaseURL: adoptBaseURL}); err == nil {
		t.Fatal("an adopting init with no key succeeded")
	}
	if ok, _ := empty.Store().Exists(ctx, privateKeyKey); ok {
		t.Error("an adopting init generated a key")
	}
}
