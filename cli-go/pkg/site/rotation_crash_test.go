package site

import (
	"context"
	"errors"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// keyedSite is a site whose keys on disk are kp and whose .well-known/polis
// publishes kp with a genesis key history.
func keyedSite(t *testing.T, kp keypair) storage.Site {
	t.Helper()
	dir := writeSite(t, kp.pub, "2026-01-01T00:00:00Z")
	s := storage.At(dir)
	ctx := context.Background()
	if err := s.Store().Put(ctx, PrivateKeyKey, kp.priv, storage.WithMode(0o600)); err != nil {
		t.Fatal(err)
	}
	if err := s.Store().Put(ctx, PublicKeyKey, []byte(kp.pub+"\n"), storage.WithMode(0o644)); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteGenesisKeyHistory(s); err != nil {
		t.Fatal(err)
	}
	return s
}

// finish signs the handover with keys.OldPriv and records it, as every
// rotation path does after the install.
func finish(t *testing.T, s storage.Site, keys *RotationKeys, ts string) {
	t.Helper()
	rotate(t, s.Dir(), "a.example", keypair{priv: keys.OldPriv, pub: keys.OldPub},
		keypair{pub: keys.NewPub}, ts)
	if err := FinishRotation(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func chainVerifies(t *testing.T, s storage.Site) {
	t.Helper()
	block, err := LoadKeyHistory(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyChain(block, "a.example"); err != nil {
		t.Fatalf("⛔ the published key history no longer verifies: %v", err)
	}
}

// ⛔⛔ THE CRASH THIS EXISTS FOR: the new key installed, the process dies before
// the key history is written. The resume must sign the handover with the
// RETIRED key — still on disk at PreviousKeyKey — and the chain must verify,
// through this rotation and the next one.
func TestARotationCrashedBetweenInstallAndHistoryWriteResumesAndTheChainVerifies(t *testing.T) {
	ctx := context.Background()
	genesis := newKeypair(t)
	s := keyedSite(t, genesis)

	keys, err := PrepareRotation(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := StageRotationKey(ctx, s, keys.NewPriv); err != nil {
		t.Fatal(err)
	}
	if err := InstallRotationKey(ctx, s, keys.NewPriv, keys.NewPubSSH); err != nil {
		t.Fatal(err)
	}
	// ✂ crash: RecordKeyRotation never runs.

	resumed, err := PrepareRotation(ctx, s)
	if err != nil {
		t.Fatalf("the interrupted rotation could not be resumed: %v", err)
	}
	if !resumed.Resuming || resumed.NewPub != keys.NewPub {
		t.Fatalf("resumed = %+v; want a resume of the staged key", resumed)
	}
	if string(resumed.OldPriv) != string(genesis.priv) || !SameSSHKey(resumed.OldPub, genesis.pub) {
		t.Fatal("⛔ the resume would sign the handover with a key that is not the published one")
	}
	if err := InstallRotationKey(ctx, s, resumed.NewPriv, resumed.NewPubSSH); err != nil {
		t.Fatal(err)
	}
	finish(t, s, resumed, "2026-02-01T00:00:00Z")
	chainVerifies(t, s)
	for _, k := range []string{StagedKeyKey, PreviousKeyKey} {
		if ok, _ := s.Store().Exists(ctx, k); ok {
			t.Errorf("%s was left after the resumed rotation finished", k)
		}
	}

	// And the NEXT rotation extends a chain that still verifies.
	next, err := PrepareRotation(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if next.Resuming || !SameSSHKey(next.OldPub, keys.NewPub) {
		t.Fatalf("next = %+v; want a fresh rotation from the key just recorded", next)
	}
	finish(t, s, next, "2026-03-01T00:00:00Z")
	chainVerifies(t, s)
}

// ⛔⛔ A crash INSIDE the install: the new private key written, id_ed25519.pub
// not. Judged by the .pub, the rotation looked not yet installed and the
// handover was signed with the private key on disk, which is the NEW key: an
// entry no chain can verify, published for good.
func TestARotationCrashedBetweenThePrivateAndPublicKeyWritesSignsWithTheRetiredKey(t *testing.T) {
	ctx := context.Background()
	genesis := newKeypair(t)
	s := keyedSite(t, genesis)
	keys, err := PrepareRotation(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := StageRotationKey(ctx, s, keys.NewPriv); err != nil {
		t.Fatal(err)
	}
	// The first two of InstallRotationKey's three writes.
	if err := s.Store().Put(ctx, PreviousKeyKey, genesis.priv, storage.WithMode(0o600)); err != nil {
		t.Fatal(err)
	}
	if err := s.Store().Put(ctx, PrivateKeyKey, keys.NewPriv, storage.WithMode(0o600)); err != nil {
		t.Fatal(err)
	}
	// ✂ crash: id_ed25519.pub still holds the genesis key.

	resumed, err := PrepareRotation(ctx, s)
	if err != nil {
		t.Fatalf("the interrupted rotation could not be resumed: %v", err)
	}
	if string(resumed.OldPriv) != string(genesis.priv) {
		t.Fatal("⛔ the resume would sign the handover with the new key, which is not the chain head")
	}
	if err := InstallRotationKey(ctx, s, resumed.NewPriv, resumed.NewPubSSH); err != nil {
		t.Fatal(err)
	}
	if prev, _ := s.Store().Get(ctx, PreviousKeyKey); prev != nil && string(prev) != string(genesis.priv) {
		t.Error("⛔ the retired key was overwritten by the resume")
	}
	if pub, _ := s.Store().Get(ctx, PublicKeyKey); !SameSSHKey(string(pub), keys.NewPub) {
		t.Error("the resume did not finish writing id_ed25519.pub")
	}
	finish(t, s, resumed, "2026-02-01T00:00:00Z")
	chainVerifies(t, s)
}

// ⛔ A private key that is not the published one is refused even when
// id_ed25519.pub still agrees with .well-known/polis.
func TestARotationRefusesAPrivateKeyThatIsNotThePublishedOne(t *testing.T) {
	ctx := context.Background()
	s := keyedSite(t, newKeypair(t))
	_ = s.Store().Put(ctx, PrivateKeyKey, newKeypair(t).priv, storage.WithMode(0o600))
	_, err := PrepareRotation(ctx, s)
	if !errors.Is(err, ErrKeyNotPublished) {
		t.Fatalf("err = %v; want ErrKeyNotPublished", err)
	}
}

// ⛔ The state the old code left behind — installed, the staged copy deleted,
// nothing recorded — must refuse, never sign a handover with the middle key.
func TestARotationRefusesWhenTheKeyOnDiskIsNotThePublishedOne(t *testing.T) {
	ctx := context.Background()
	s := keyedSite(t, newKeypair(t))
	other := newKeypair(t)
	_ = s.Store().Put(ctx, PrivateKeyKey, other.priv, storage.WithMode(0o600))
	_ = s.Store().Put(ctx, PublicKeyKey, []byte(other.pub+"\n"), storage.WithMode(0o644))

	_, err := PrepareRotation(ctx, s)
	if !errors.Is(err, ErrKeyNotPublished) {
		t.Fatalf("err = %v; want ErrKeyNotPublished", err)
	}
	if ok, _ := s.Store().Exists(ctx, StagedKeyKey); ok {
		t.Error("a refused rotation staged a key")
	}
}

// A staged key equal to the installed key, with no retired key kept (an
// interruption under a build from before it was kept), cannot be finished.
func TestAnInstalledUnrecordedRotationWithoutTheRetiredKeyIsUnfinishable(t *testing.T) {
	ctx := context.Background()
	s := keyedSite(t, newKeypair(t))
	nk := newKeypair(t)
	_ = s.Store().Put(ctx, StagedKeyKey, nk.priv, storage.WithMode(0o600))
	_ = s.Store().Put(ctx, PrivateKeyKey, nk.priv, storage.WithMode(0o600))
	_ = s.Store().Put(ctx, PublicKeyKey, []byte(nk.pub+"\n"), storage.WithMode(0o644))

	if _, err := PrepareRotation(ctx, s); !errors.Is(err, ErrRotationUnfinishable) {
		t.Fatalf("err = %v; want ErrRotationUnfinishable", err)
	}
	if ok, _ := s.Store().Exists(ctx, StagedKeyKey); !ok {
		t.Error("the staged copy was removed; it is evidence for whoever looks")
	}
}

// Recorded but the clean-up was lost: the leftovers go and a fresh rotation
// starts from the published key.
func TestARecordedRotationsLeftoversAreRemoved(t *testing.T) {
	ctx := context.Background()
	genesis := newKeypair(t)
	s := keyedSite(t, genesis)
	keys, _ := PrepareRotation(ctx, s)
	_ = StageRotationKey(ctx, s, keys.NewPriv)
	_ = InstallRotationKey(ctx, s, keys.NewPriv, keys.NewPubSSH)
	rotate(t, s.Dir(), "a.example", genesis, keypair{pub: keys.NewPub}, "2026-02-01T00:00:00Z")
	// ✂ crash before FinishRotation.

	next, err := PrepareRotation(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if next.Resuming || !SameSSHKey(next.OldPub, keys.NewPub) {
		t.Fatalf("next = %+v; want a fresh rotation from the recorded key", next)
	}
	for _, k := range []string{StagedKeyKey, PreviousKeyKey} {
		if ok, _ := s.Store().Exists(ctx, k); ok {
			t.Errorf("%s was left behind", k)
		}
	}
}
