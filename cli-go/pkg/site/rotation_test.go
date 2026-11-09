package site

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐ The staging helpers both rotation paths share.
func TestRotationStagesAbandonsAndInstalls(t *testing.T) {
	ctx := context.Background()
	s := storage.At(t.TempDir())
	_ = s.Store().Put(ctx, PrivateKeyKey, []byte("OLD"), storage.WithMode(0o644))

	if err := StageRotationKey(ctx, s, []byte("NEW")); err != nil {
		t.Fatal(err)
	}
	// ⛔ A second rotation cannot overwrite a staged key.
	if err := StageRotationKey(ctx, s, []byte("OTHER")); !errors.Is(err, ErrRotationPending) {
		t.Fatalf("a second staging gave %v; want ErrRotationPending", err)
	}
	if b, _ := s.Store().Get(ctx, StagedKeyKey); string(b) != "NEW" {
		t.Fatal("the staged key was overwritten")
	}
	if fi, _ := os.Stat(s.Path(StagedKeyKey)); fi.Mode().Perm() != 0o600 {
		t.Errorf("staged key is %o; want 600", fi.Mode().Perm())
	}

	if err := AbandonRotationKey(ctx, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.Store().Get(ctx, PrivateKeyKey); string(b) != "OLD" {
		t.Fatal("abandoning touched the current key")
	}

	_ = StageRotationKey(ctx, s, []byte("NEW"))
	if err := InstallRotationKey(ctx, s, []byte("NEW"), []byte("PUB")); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.Store().Get(ctx, PrivateKeyKey); string(b) != "NEW" {
		t.Fatal("the new key was not installed")
	}
	if fi, _ := os.Stat(s.Path(PrivateKeyKey)); fi.Mode().Perm() != 0o600 {
		t.Errorf("installed key is %o over a 0644 file; want 600", fi.Mode().Perm())
	}
	// ⛔ Install keeps BOTH the staged copy and the retired key: until the
	// rotation is recorded they are what lets a resume sign the handover again.
	if ok, _ := s.Store().Exists(ctx, StagedKeyKey); !ok {
		t.Error("the staged key was removed at install, before the rotation was recorded")
	}
	if b, _ := s.Store().Get(ctx, PreviousKeyKey); string(b) != "OLD" {
		t.Errorf("the retired key was not kept at install: %q", b)
	}
	if fi, _ := os.Stat(s.Path(PreviousKeyKey)); fi.Mode().Perm() != 0o600 {
		t.Errorf("retired key is %o; want 600", fi.Mode().Perm())
	}
	// A resumed install of the same key keeps the retired key it already has.
	if err := InstallRotationKey(ctx, s, []byte("NEW"), []byte("PUB")); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.Store().Get(ctx, PreviousKeyKey); string(b) != "OLD" {
		t.Errorf("a resumed install replaced the retired key with %q", b)
	}

	if err := FinishRotation(ctx, s); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{StagedKeyKey, PreviousKeyKey} {
		if ok, _ := s.Store().Exists(ctx, k); ok {
			t.Errorf("%s was left after the rotation finished", k)
		}
	}
}

type fakeRotationDS struct {
	rotateErr error
	current   string
	histErr   error
	posted    int
	// landed, when set, is what the DS holds once a rotation has been posted:
	// an earlier run's request that arrived while this one was in flight.
	landed string
}

func (f *fakeRotationDS) RotateKey(discovery.KeyRotationRequest) (*discovery.Witness, error) {
	f.posted++
	if f.landed != "" {
		f.current = f.landed
	}
	return nil, f.rotateErr
}

func (f *fakeRotationDS) KeyHistory(string) (*discovery.KeyHistoryResponse, error) {
	if f.histErr != nil {
		return nil, f.histErr
	}
	return &discovery.KeyHistoryResponse{Keys: []discovery.KeyHistoryEntry{
		{PublicKey: "ssh-ed25519 AAAAgenesis polis-local", ValidUntil: "2026-01-01T00:00:00Z"},
		{PublicKey: f.current},
	}}, nil
}

// ⛔⛔ A lost or 5xx answer is not a refusal: the DS may have written the new
// key before failing to answer, so the staged key — then the only copy of the
// key the network holds — is kept. Only a 4xx removes it.
func TestAnnounceRotationKeepsTheStagedKeyUnlessRefused(t *testing.T) {
	ctx := context.Background()
	req := discovery.KeyRotationRequest{Domain: "a.example", OldKey: "ssh-ed25519 AAAAold polis-local", NewKey: "ssh-ed25519 AAAAnew polis-local"}
	cases := []struct {
		name     string
		ds       *fakeRotationDS
		resuming bool
		want     error
		refused  bool
		kept     bool
		posted   int
		already  bool
	}{
		{name: "no answer", ds: &fakeRotationDS{rotateErr: errors.New("request failed: context deadline exceeded")}, want: ErrRotationPending, kept: true, posted: 1},
		{name: "5xx", ds: &fakeRotationDS{rotateErr: errors.New("key rotation answered status 502")}, want: ErrRotationPending, kept: true, posted: 1},
		{name: "4xx refusal", ds: &fakeRotationDS{rotateErr: &discovery.RotationRefusedError{Status: 401}}, refused: true, kept: false, posted: 1},
		{name: "accepted", ds: &fakeRotationDS{}, kept: true, posted: 1},
		{name: "resume, DS already holds the new key", ds: &fakeRotationDS{current: "ssh-ed25519 AAAAnew polis@laptop"}, resuming: true, kept: true, posted: 0, already: true},
		{name: "resume, DS still holds the old key", ds: &fakeRotationDS{current: "ssh-ed25519 AAAAold polis-local"}, resuming: true, kept: true, posted: 1},
		{name: "resume, DS holds neither", ds: &fakeRotationDS{current: "ssh-ed25519 AAAAother x"}, resuming: true, want: ErrRotationDiverged, kept: true, posted: 0},
		// ⛔ A refused RE-SEND keeps the staged key: the earlier request may have
		// landed after the history check, and then the DS holds it.
		{name: "resume, re-send refused because the earlier request landed", ds: &fakeRotationDS{current: "ssh-ed25519 AAAAold polis-local", landed: "ssh-ed25519 AAAAnew x", rotateErr: &discovery.RotationRefusedError{Status: 409}}, resuming: true, kept: true, posted: 1, already: true},
		{name: "resume, re-send refused and the DS still holds the old key", ds: &fakeRotationDS{current: "ssh-ed25519 AAAAold polis-local", rotateErr: &discovery.RotationRefusedError{Status: 409}}, resuming: true, want: ErrRotationPending, kept: true, posted: 1},
		{name: "resume, DS unreachable", ds: &fakeRotationDS{histErr: errors.New("request failed")}, resuming: true, want: ErrRotationPending, kept: true, posted: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := storage.At(t.TempDir())
			if err := StageRotationKey(ctx, s, []byte("NEW")); err != nil {
				t.Fatal(err)
			}
			_, already, err := AnnounceRotation(ctx, s, c.ds, req, c.resuming)
			var refused *discovery.RotationRefusedError
			switch {
			case c.want != nil && !errors.Is(err, c.want):
				t.Errorf("err = %v; want %v", err, c.want)
			case c.refused && !errors.As(err, &refused):
				t.Errorf("err = %v; want the refusal", err)
			case c.want == nil && !c.refused && err != nil:
				t.Errorf("err = %v; want none", err)
			}
			if already != c.already {
				t.Errorf("already = %v; want %v", already, c.already)
			}
			if c.ds.posted != c.posted {
				t.Errorf("posted %d times; want %d", c.ds.posted, c.posted)
			}
			if ok, _ := s.Store().Exists(ctx, StagedKeyKey); ok != c.kept {
				t.Errorf("staged key kept = %v; want %v", ok, c.kept)
			}
		})
	}
}
