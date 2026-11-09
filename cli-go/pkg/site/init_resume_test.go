package site

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// failingPut is a store whose Put of one key fails while fail is set: a Blob
// error or a function timeout partway through a first start.
type failingPut struct {
	storage.Store
	key  string
	fail atomic.Bool
}

func (f *failingPut) Put(ctx context.Context, key string, data []byte, opts ...storage.PutOption) error {
	if f.fail.Load() && key == f.key {
		return errors.New("injected failure")
	}
	return f.Store.Put(ctx, key, data, opts...)
}

// laterBy moves the init clock forward for the rest of the test.
func laterBy(t *testing.T, d time.Duration) {
	t.Helper()
	old := initNow
	initNow = func() time.Time { return old().Add(d) }
	t.Cleanup(func() { initNow = old })
}

// siteComplete fails the test unless every output of an adopting init exists
// and the published messages key matches the keyring.
func siteComplete(t *testing.T, s storage.Site) {
	t.Helper()
	ctx := context.Background()
	for _, key := range []string{"content/pub.polis.core/bundle.json", bundle.RegistryKey,
		"policies/rules.jsonl", ".polis/storage-salt", ".well-known/did.json"} {
		if ok, err := s.Store().Exists(ctx, key); err != nil || !ok {
			t.Errorf("%s not written (%v)", key, err)
		}
	}
	if ok, _ := s.Store().Exists(ctx, InitPendingKey); ok {
		t.Error("the pending marker was left on a complete site")
	}
	block, err := LoadKeyHistory(s)
	if err != nil || block == nil {
		t.Fatalf("no key history (%v)", err)
	}
	kr, err := dm.LoadKeyring(s)
	if err != nil {
		t.Fatalf("no keyring: %v", err)
	}
	raw, err := LoadWellKnownRaw(s)
	if err != nil {
		t.Fatal(err)
	}
	if raw["public_key_messages"] == nil {
		t.Fatal("no messages key published")
	}
	published, _ := json.Marshal(raw["public_key_messages"])
	for i, e := range kr.Epochs {
		if !strings.Contains(string(published), e.PublicKeyMessages) {
			t.Errorf("⛔ the published messages key does not name the keyring's epoch %d", i)
		}
	}
}

// ⛔⛔ A first start that fails AFTER the claim used to leave the site
// half-built for good: every later start found .well-known/polis and stopped.
// Now a start soon after says the site is being written, and one after
// resumeAfter finishes it.
func TestAFirstStartThatFailsAfterTheClaimIsFinishedByALaterStart(t *testing.T) {
	ctx := context.Background()
	base, _ := keyedObjectSite(t)
	fp := &failingPut{Store: base.Store(), key: "content/pub.polis.core/bundle.json"}
	fp.fail.Store(true)
	s := storage.On(base.Dir(), fp)
	opts := InitOptions{AdoptExistingKey: true, BaseURL: adoptBaseURL}

	if _, err := Init(s, opts); err == nil {
		t.Fatal("the injected failure did not stop the first start")
	}
	if ok, _ := s.Store().Exists(ctx, WellKnownKey); !ok {
		t.Fatal("the test needs the failure after the claim")
	}
	fp.fail.Store(false)
	before, _ := s.Store().Get(ctx, WellKnownKey)

	// Moments later: another start may still be writing it.
	if _, err := Init(s, opts); !errors.Is(err, ErrInitInProgress) || !errors.Is(err, ErrAlreadyInitialised) {
		t.Fatalf("err = %v; want ErrInitInProgress (an ErrAlreadyInitialised)", err)
	}

	laterBy(t, resumeAfter+time.Minute)
	res, err := Init(s, opts)
	if err != nil {
		t.Fatalf("the later start did not finish the site: %v", err)
	}
	if !res.Resumed {
		t.Error("the result does not say the site was resumed")
	}
	siteComplete(t, s)
	wk, _ := LoadWellKnown(s)
	var was WellKnown
	_ = json.Unmarshal(before, &was)
	if wk.Created != was.Created || wk.PublicKey != was.PublicKey {
		t.Error("⛔ the resume rewrote the claim's identity or creation time")
	}

	// And then the site is simply initialised.
	if _, err := Init(s, opts); !errors.Is(err, ErrAlreadyInitialised) || errors.Is(err, ErrInitInProgress) {
		t.Fatalf("err = %v; want ErrAlreadyInitialised once complete", err)
	}
}

// ⛔ Several starts finishing one site at once each write only what is the
// same whoever writes it: one salt, one keyring, and a published messages key
// that names it.
func TestConcurrentResumesLeaveOneConsistentSite(t *testing.T) {
	base, _ := keyedObjectSite(t)
	fp := &failingPut{Store: base.Store(), key: "content/pub.polis.core/bundle.json"}
	fp.fail.Store(true)
	s := storage.On(base.Dir(), fp)
	opts := InitOptions{AdoptExistingKey: true, BaseURL: adoptBaseURL}
	if _, err := Init(s, opts); err == nil {
		t.Fatal("the injected failure did not stop the first start")
	}
	fp.fail.Store(false)
	// The first start got as far as the salt and keyring; drop them, so the
	// resumes race to create them.
	ctx := context.Background()
	_ = s.Store().Delete(ctx, ".polis/storage-salt", storage.IgnoreMissing())
	_ = s.Store().Delete(ctx, dm.DMDirKey+"/keyring.json", storage.IgnoreMissing())

	laterBy(t, resumeAfter+time.Minute)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = Init(s, opts)
		}(i)
	}
	wg.Wait()
	ok := 0
	for i, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrAlreadyInitialised):
		default:
			// ⚠️ A resume may lose a read-modify-write of .well-known/polis to
			// another; the identity guard refuses the stale write. That is a
			// retry on the next start, never a corrupt site.
			t.Logf("resume %d: %v", i, err)
		}
	}
	if ok == 0 {
		t.Fatal("no resume finished the site")
	}
	siteComplete(t, s)
}
