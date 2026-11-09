package site

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func publish(t *testing.T, s storage.Site, pub []byte) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"public_key": string(pub)})
	if err := s.Store().Put(context.Background(), WellKnownKey, b); err != nil {
		t.Fatal(err)
	}
}

func bothBackends(t *testing.T) map[string]storage.Site {
	return map[string]storage.Site{
		"posix":  storage.At(t.TempDir()),
		"object": storage.On("/site", storage.NewObjectStore("")),
	}
}

// ⛔⛔ A PUBLISHED KEY WITH NO PRIVATE KEY BESIDE IT IS NEVER REPLACED BY A NEW
// ONE. Two reviewers proved the bootstrap used to mint a second identity here.
func TestTheBootstrapRefusesToMintASecondIdentity(t *testing.T) {
	for name, s := range bothBackends(t) {
		t.Run(name, func(t *testing.T) {
			_, pub, _ := signing.GenerateKeypair()
			publish(t, s, pub)
			if _, err := EnsureIdentityKey(context.Background(), s); err == nil {
				t.Fatal("generated a key beside an already-published identity")
			}
			if ok, _ := s.Store().Exists(context.Background(), PrivateKeyKey); ok {
				t.Error("⛔ a private key was written although a key is already published")
			}
		})
	}
}

// ⛔ A present private key must be the published one.
func TestAPrivateKeyThatIsNotThePublishedOneIsRefused(t *testing.T) {
	for name, s := range bothBackends(t) {
		t.Run(name, func(t *testing.T) {
			priv, _, _ := signing.GenerateKeypair()
			_, otherPub, _ := signing.GenerateKeypair()
			_ = s.Store().Put(context.Background(), PrivateKeyKey, priv)
			publish(t, s, otherPub)
			if _, err := EnsureIdentityKey(context.Background(), s); err == nil {
				t.Error("used a private key that does not match the published identity")
			}
		})
	}
}

// ⭐ The normal cases still work: the matching key is used, and a site that
// publishes nothing gets a key.
func TestTheMatchingKeyIsUsedAndAnUnpublishedSiteStillBootstraps(t *testing.T) {
	ctx := context.Background()
	s := storage.At(t.TempDir())
	priv, pub, _ := signing.GenerateKeypair()
	_ = s.Store().Put(ctx, PrivateKeyKey, priv)
	publish(t, s, pub)
	if k, err := EnsureIdentityKey(ctx, s); err != nil || k.Created {
		t.Fatalf("the matching key was refused or replaced: %+v %v", k, err)
	}
	fresh := storage.At(t.TempDir())
	_ = fresh.Store().Put(ctx, WellKnownKey, []byte(`{"version":"0.1"}`)) // published, but no key
	if k, err := EnsureIdentityKey(ctx, fresh); err != nil || !k.Created {
		t.Fatalf("a site publishing no key did not bootstrap: %v", err)
	}
}

// ⛔ "Could not tell" is not "nothing published".
func TestAnUnreadableWellKnownRefuses(t *testing.T) {
	s := storage.At(t.TempDir())
	_ = s.Store().Put(context.Background(), WellKnownKey, []byte("{not json"))
	if _, err := EnsureIdentityKey(context.Background(), s); err == nil {
		t.Error("generated a key although .well-known/polis could not be parsed")
	}
}

// ── THE LIVE IDENTITY, CHECKED BEFORE A PUBLISH ──────────────────────────────
//
// A store that is empty or replaced beside a live site mints a new identity on
// first start; the check stops it reaching readers.

func wellKnownDoc(t *testing.T, pub []byte, history *KeyHistoryBlock) []byte {
	t.Helper()
	doc := map[string]any{"version": "0.1", "public_key": string(pub)}
	if history != nil {
		doc["public_key_history"] = history
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestALiveSiteWithNoIdentityNeverBlocksAPublish(t *testing.T) {
	_, pub, _ := signing.GenerateKeypair()
	src := wellKnownDoc(t, pub, nil)
	for name, live := range map[string][]byte{
		"no live document":   nil,
		"a document, no key": []byte(`{"version":"0.1"}`),
	} {
		if err := CheckLiveIdentity(src, live); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTheStoresOwnKeyPublishes(t *testing.T) {
	_, pub, _ := signing.GenerateKeypair()
	doc := wellKnownDoc(t, pub, GenesisKeyHistory(string(pub), "2026-10-14T00:00:00Z"))
	if err := CheckLiveIdentity(doc, doc); err != nil {
		t.Fatal(err)
	}
}

// ⭐ A deliberate rotation publishes: the live key is the one the store's
// history retired.
func TestARotationPublishesOverTheKeyItRetired(t *testing.T) {
	_, old, _ := signing.GenerateKeypair()
	_, cur, _ := signing.GenerateKeypair()
	live := wellKnownDoc(t, old, GenesisKeyHistory(string(old), "2026-01-01T00:00:00Z"))
	src := wellKnownDoc(t, cur, &KeyHistoryBlock{
		Current: KeyHistoryEntry{Epoch: 1, Key: string(cur), ValidFrom: "2026-10-14T00:00:00Z"},
		History: []KeyHistoryEntry{{Epoch: 0, Key: string(old), ValidFrom: "2026-01-01T00:00:00Z", ValidUntil: "2026-10-14T00:00:00Z"}},
	})
	if err := CheckLiveIdentity(src, live); err != nil {
		t.Fatalf("⛔ a rotation was refused: %v", err)
	}
}

// ⛔⛔ The case the check exists for: an empty store beside a live site mints a
// new identity, and publishing it would replace the real one.
func TestAReplacedStoreNeverPublishesOverTheLiveIdentity(t *testing.T) {
	ctx := context.Background()
	_, real, _ := signing.GenerateKeypair()
	live := wellKnownDoc(t, real, GenesisKeyHistory(string(real), "2026-01-01T00:00:00Z"))

	fresh := storage.On("/site", storage.NewObjectStore(""))
	k, err := EnsureIdentityKey(ctx, fresh)
	if err != nil || !k.Created {
		t.Fatalf("an empty store did not bootstrap: %v", err)
	}
	src := wellKnownDoc(t, k.Public, GenesisKeyHistory(string(k.Public), "2026-10-14T00:00:00Z"))

	err = CheckLiveIdentity(src, live)
	if !errors.Is(err, ErrLiveIdentityConflict) {
		t.Fatalf("⛔ a replaced store would publish over the live identity: %v", err)
	}
	for _, key := range [][]byte{real, k.Public} {
		if strings.Contains(err.Error(), strings.Fields(string(key))[1]) {
			t.Error("⛔ the refusal quotes a key")
		}
	}
	if !strings.Contains(err.Error(), "restore") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
}

// ⛔ A store with no identity at all would publish a site without one.
func TestAStoreWithNoIdentityNeverPublishesOverOne(t *testing.T) {
	_, real, _ := signing.GenerateKeypair()
	live := wellKnownDoc(t, real, nil)
	for name, src := range map[string][]byte{
		"no document":        nil,
		"a document, no key": []byte(`{"version":"0.1"}`),
	} {
		if err := CheckLiveIdentity(src, live); !errors.Is(err, ErrLiveIdentityConflict) {
			t.Errorf("%s: %v, want a conflict", name, err)
		}
	}
}

// ⛔ "Could not tell" is not "nothing published", on either side.
func TestAnUnparseableDocumentRefusesWithoutClaimingAConflict(t *testing.T) {
	_, pub, _ := signing.GenerateKeypair()
	good := wellKnownDoc(t, pub, nil)
	for name, c := range map[string][2][]byte{
		"live":  {good, []byte("{not json")},
		"store": {[]byte("{not json"), good},
	} {
		err := CheckLiveIdentity(c[0], c[1])
		if err == nil || errors.Is(err, ErrLiveIdentityConflict) {
			t.Errorf("%s unparseable: %v, want a refusal that is not a conflict", name, err)
		}
	}
}
