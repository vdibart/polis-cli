package site

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// StagedKeyKey holds a new private key between being generated and being
// installed during a rotation.
//
// ⛔⛔ WHY A ROTATION STAGES THE KEY. The network is told about the new key
// before the site switches to it. If the key were written in place first and
// the announcement then failed, the site would sign with a key nobody knows and
// the old key — needed to sign a retry — would be gone. If it were written in
// place AFTER the announcement, a crash or full disk mid-write would leave an
// empty key while the network is already on the new one: the identity could
// not be recovered (.old backups are retired). Staging keeps both keys durably
// on disk at every step: stage, announce, install.
//
// ⭐ THE SAME NAME THE BASH CLI USES (cmd_rotate_key stages with ssh-keygen to
// id_ed25519.new), so each implementation resumes the other's interrupted
// rotation rather than starting a second one over it.
const StagedKeyKey = ".polis/keys/id_ed25519.new"

// stagedPublicKeyKey is the public half the bash CLI stages beside the private
// key (ssh-keygen writes both). The Go paths never write it; they remove it.
const stagedPublicKeyKey = StagedKeyKey + ".pub"

// PreviousKeyKey holds the private key a rotation is retiring, from the moment
// the new key is installed until the rotation is recorded in .well-known/polis.
//
// ⛔⛔ WHY THE OLD KEY OUTLIVES THE INSTALL. The handover entry appended to
// public_key_history carries a transition signature made by the OLD key. A crash
// after the install and before that write used to leave the old key gone and the
// staged copy deleted, so nothing could finish the rotation — and nothing even
// noticed: the next rotation read the installed key as "old", the DS accepted
// it, and the chain gained an entry signed by a key that was never its head,
// failing verification for ever (a chain is append-only and never rebuilt).
// Keeping the old key until RecordKeyRotation has run lets a resume sign the
// handover again.
//
// ⚠️ This is NOT the retired .old backup: it exists only between install and
// record, and FinishRotation removes it. It is a private key, so the reaper
// excludes it from a departing user's archive, as it does the staged key.
const PreviousKeyKey = ".polis/keys/id_ed25519.prev"

// ErrKeyNotPublished reports a site whose public key on disk is not the
// public_key its .well-known/polis publishes. A rotation from that state would
// append a handover signed by a key that is not the chain's head, so it refuses.
var ErrKeyNotPublished = errors.New("the site's key does not match the public_key .well-known/polis publishes")

// ErrRotationUnfinishable reports an earlier rotation that installed its key and
// stopped before recording it, with the old key no longer on disk (a run of a
// build from before PreviousKeyKey existed). The handover cannot be signed again.
var ErrRotationUnfinishable = errors.New("an earlier rotation installed the new key and stopped before publishing it, and the old key is gone")

// ErrRotationPending reports a rotation whose outcome at the discovery
// service is unknown, so its staged key is kept. ⭐ Running the rotation again
// resumes it: it asks the discovery service which key it holds, then finishes
// or retries with the same staged key.
//
// ⛔ The remedy is NEVER to move the staged key over the current one by hand.
// That skips the published key history, the re-signs and did.json, and
// destroys the old key the transition signature needs: the site would sign
// with a key it does not publish, and nothing would notice.
var ErrRotationPending = errors.New("a key rotation is pending: run the rotation again to finish it")

// ErrRotationDiverged reports a staged key while the discovery service holds
// neither the current key nor the staged one. Nothing is changed and the
// staged key is kept; a person has to look.
var ErrRotationDiverged = errors.New("the discovery service holds neither this site's current key nor its staged key")

// StagedRotationKey returns the private key an earlier rotation staged and did
// not install, or nil when there is none.
func StagedRotationKey(ctx context.Context, site storage.Site) ([]byte, error) {
	b, err := site.Store().Get(ctx, StagedKeyKey)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the staged key: %w", err)
	}
	return b, nil
}

// StageRotationKey durably writes the new private key beside the current one,
// 0600, before anything is announced. ⛔ It is a conditional create, so it
// never replaces a staged key: that could destroy the only copy of a key the
// network already knows. A caller resumes a staged key (StagedRotationKey)
// rather than staging a new one.
func StageRotationKey(ctx context.Context, site storage.Site, privPEM []byte) error {
	err := site.Store().Put(ctx, StagedKeyKey, privPEM, storage.IfAbsent(), storage.WithMode(0o600))
	if errors.Is(err, storage.ErrExists) {
		return fmt.Errorf("%w (%s exists)", ErrRotationPending, StagedKeyKey)
	}
	if err != nil {
		return fmt.Errorf("stage new private key: %w", err)
	}
	return nil
}

// AbandonRotationKey removes a staged key. ⛔ Only after the discovery service
// REFUSED it (a 4xx): the current key was never touched and the network never
// learned the new one.
func AbandonRotationKey(ctx context.Context, site storage.Site) error {
	return site.Store().Delete(ctx, StagedKeyKey, storage.IgnoreMissing())
}

// RotationKeys is what a rotation signs with and installs.
type RotationKeys struct {
	// OldPriv signs the handover; OldPub is the key the handover retires, and
	// always the key .well-known/polis publishes.
	OldPriv []byte
	OldPub  string
	// NewPriv and NewPubSSH are installed; NewPub is NewPubSSH trimmed.
	NewPriv   []byte
	NewPubSSH []byte
	NewPub    string
	// Resuming says an earlier run staged NewPriv and its outcome at the
	// discovery service is unknown. The caller does not stage it again.
	Resuming bool
}

// PrepareRotation decides what a rotation starts from, after reading the
// site's keys, its published key and any interrupted rotation. It changes
// nothing, except to remove the leftovers of a rotation that had in fact
// finished. Every rotation path calls it before signing anything.
//
//	nothing staged                      → a fresh keypair; the site's key must be the published one
//	staged, not yet installed           → resume with it; the site's key must be the published one
//	staged, installed, not recorded     → resume with it, signing with PreviousKeyKey
//	staged, installed AND recorded      → the leftovers are removed and a fresh rotation starts
//
// ⛔ The published key is the authority because it is the chain's head: the
// next handover must be signed by it, or the chain breaks for good.
//
// ⛔ "Installed" is decided by the PRIVATE key, never by id_ed25519.pub. The
// install writes the private key before the public one, so a crash between the
// two leaves the new private key beside the old .pub; judging by the .pub then
// signed the handover with the new key, an entry no chain can verify.
func PrepareRotation(ctx context.Context, site storage.Site) (*RotationKeys, error) {
	curPriv, err := site.Store().Get(ctx, PrivateKeyKey)
	if err != nil {
		return nil, fmt.Errorf("read the current private key: %w", err)
	}
	curPubBytes, err := site.Store().Get(ctx, PublicKeyKey)
	if err != nil {
		return nil, fmt.Errorf("read the current public key: %w", err)
	}
	pubFile := strings.TrimSpace(string(curPubBytes))
	curPubSSH, err := signing.PublicKeyFromPrivate(curPriv)
	if err != nil {
		return nil, fmt.Errorf("the current private key does not parse: %w", err)
	}
	curPub := strings.TrimSpace(string(curPubSSH))
	wk, err := LoadWellKnown(site)
	if err != nil {
		return nil, fmt.Errorf("read .well-known/polis: %w", err)
	}
	published := strings.TrimSpace(wk.PublicKey)
	if published == "" {
		return nil, fmt.Errorf("%w: .well-known/polis publishes no public_key", ErrKeyNotPublished)
	}
	notPublished := func() error {
		k, held := PrivateKeyKey, curPub
		if SameSSHKey(curPub, published) {
			k, held = PublicKeyKey, pubFile
		}
		return fmt.Errorf("%w: %s holds %s, .well-known/polis publishes %s. Nothing was changed; "+
			"run 'polis validate' and bring the two back into agreement before rotating",
			ErrKeyNotPublished, k, abbrevKey(held), abbrevKey(published))
	}
	// The site's key, both halves, is the published one.
	agrees := func() bool { return SameSSHKey(curPub, published) && SameSSHKey(pubFile, published) }

	staged, err := StagedRotationKey(ctx, site)
	if err != nil {
		return nil, err
	}
	if staged != nil {
		stagedPubSSH, err := signing.PublicKeyFromPrivate(staged)
		if err != nil {
			return nil, fmt.Errorf("%w: the staged key at %s does not parse: %v", ErrRotationPending, StagedKeyKey, err)
		}
		stagedPub := strings.TrimSpace(string(stagedPubSSH))
		switch {
		case !SameSSHKey(stagedPub, curPub):
			// Staged, not installed: the current key still signs the handover.
			if !agrees() {
				return nil, notPublished()
			}
			return &RotationKeys{OldPriv: curPriv, OldPub: pubFile, NewPriv: staged,
				NewPubSSH: stagedPubSSH, NewPub: stagedPub, Resuming: true}, nil
		case SameSSHKey(stagedPub, published):
			// Installed and recorded: only the clean-up was lost. A .pub the
			// install never reached is written now.
			if !SameSSHKey(pubFile, stagedPub) {
				if err := site.Store().Put(ctx, PublicKeyKey, stagedPubSSH, storage.WithMode(0o644)); err != nil {
					return nil, fmt.Errorf("install new public key: %w", err)
				}
			}
			if err := FinishRotation(ctx, site); err != nil {
				return nil, err
			}
		default:
			// Installed, not recorded: the retired key is kept for this.
			prev, err := site.Store().Get(ctx, PreviousKeyKey)
			if errors.Is(err, storage.ErrNotFound) {
				return nil, fmt.Errorf("%w. Run 'polis validate' to see what the site publishes; the staged copy at %s "+
					"is safe to delete once you have checked", ErrRotationUnfinishable, StagedKeyKey)
			}
			if err != nil {
				return nil, fmt.Errorf("read the retired key: %w", err)
			}
			prevPub, err := signing.PublicKeyFromPrivate(prev)
			if err != nil || !SameSSHKey(string(prevPub), published) {
				return nil, fmt.Errorf("%w: the retired key kept at %s is not the published key %s",
					ErrRotationUnfinishable, PreviousKeyKey, abbrevKey(published))
			}
			return &RotationKeys{OldPriv: prev, OldPub: published, NewPriv: staged,
				NewPubSSH: stagedPubSSH, NewPub: stagedPub, Resuming: true}, nil
		}
	}

	if !agrees() {
		return nil, notPublished()
	}
	newPriv, newPubSSH, err := signing.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("generate a new keypair: %w", err)
	}
	return &RotationKeys{OldPriv: curPriv, OldPub: pubFile, NewPriv: newPriv,
		NewPubSSH: newPubSSH, NewPub: strings.TrimSpace(string(newPubSSH))}, nil
}

// abbrevKey shortens an OpenSSH key for a message: type and the end of the
// key material, which is where two keys differ to the eye.
func abbrevKey(k string) string {
	f := strings.Fields(k)
	if len(f) < 2 {
		return fmt.Sprintf("%q", k)
	}
	m := f[1]
	if len(m) > 12 {
		m = "…" + m[len(m)-12:]
	}
	return f[0] + " " + m
}

// RotationDS is what a rotation needs from the discovery service.
type RotationDS interface {
	RotateKey(discovery.KeyRotationRequest) (*discovery.Witness, error)
	KeyHistory(domain string) (*discovery.KeyHistoryResponse, error)
}

// AnnounceRotation tells the discovery service about req, whose new private
// key is already staged. resuming says the key was staged by an earlier run
// whose outcome is unknown.
//
// ⛔⛔ A LOST ANSWER IS NOT A REFUSAL. The DS may write the new key and then
// fail to answer — a timeout, a dropped connection, a 5xx from a proxy. Taking
// that as a refusal and deleting the staged key would destroy the only copy of
// the key the network now holds. So:
//
//	accepted          → the witness (nil when unwitnessed); go on and install
//	refused (4xx)     → the staged key is removed and the error returned
//	anything else     → ErrRotationPending; the staged key is kept
//
// When resuming it first asks which key the DS holds: the staged one means the
// earlier run was accepted (already is true, and there is no witness to carry);
// the current one means it was not, and the request is sent; neither is
// ErrRotationDiverged.
func AnnounceRotation(ctx context.Context, site storage.Site, ds RotationDS, req discovery.KeyRotationRequest, resuming bool) (witness *discovery.Witness, already bool, err error) {
	if resuming {
		hist, err := ds.KeyHistory(req.Domain)
		if err != nil {
			return nil, false, fmt.Errorf("%w: could not ask the discovery service which key it holds: %v", ErrRotationPending, err)
		}
		current := dsCurrentKey(hist)
		switch {
		case SameSSHKey(current, req.NewKey):
			return nil, true, nil
		case SameSSHKey(current, req.OldKey):
		default:
			return nil, false, fmt.Errorf("%w; the staged key is kept at %s", ErrRotationDiverged, StagedKeyKey)
		}
	}
	witness, err = ds.RotateKey(req)
	if err == nil {
		return witness, false, nil
	}
	var refused *discovery.RotationRefusedError
	if errors.As(err, &refused) {
		if resuming {
			// ⛔ A refused RE-SEND is not proof the key is unknown. The first
			// run's request may have landed after the history check above (the
			// DS then answers 409: the old key is no longer current), so the
			// staged key may be the one the DS now holds. Ask again.
			hist, herr := ds.KeyHistory(req.Domain)
			if herr == nil && SameSSHKey(dsCurrentKey(hist), req.NewKey) {
				return nil, true, nil
			}
			return nil, false, fmt.Errorf("%w: the discovery service refused the re-sent rotation (%v) and the staged key "+
				"is kept at %s, because an earlier request for it may yet be applied. Run the rotation again to retry",
				ErrRotationPending, err, StagedKeyKey)
		}
		_ = AbandonRotationKey(ctx, site)
		return nil, false, err
	}
	return nil, false, fmt.Errorf("%w: the discovery service may have taken the new key, which is kept at %s: %v",
		ErrRotationPending, StagedKeyKey, err)
}

// DSCurrentKey is the key the DS's witnessed history says is current, read the
// way a rotation reads it. Empty means the DS holds no key for the domain.
func DSCurrentKey(h *discovery.KeyHistoryResponse) string { return dsCurrentKey(h) }

// dsCurrentKey is the key the DS's witnessed history says is current: the one
// with no valid_until, or failing that the latest.
func dsCurrentKey(h *discovery.KeyHistoryResponse) string {
	if h == nil || len(h.Keys) == 0 {
		return ""
	}
	for i := len(h.Keys) - 1; i >= 0; i-- {
		if h.Keys[i].ValidUntil == "" {
			return strings.TrimSpace(h.Keys[i].PublicKey)
		}
	}
	return strings.TrimSpace(h.Keys[len(h.Keys)-1].PublicKey)
}

// InstallRotationKey makes the staged key current, after the network accepted
// it. ⭐ Each file lands by rename (atomicfile), so a crash leaves either the
// old key or the new one, never a partial file — and the private key always
// ends 0600, whatever the mode of the file it replaces.
//
// ⛔ The key being replaced is first kept at PreviousKeyKey, and the staged
// copy is NOT removed: both stay until the rotation is recorded, when the
// caller calls FinishRotation. Until then a crash leaves a rotation a resume can
// finish (PrepareRotation). A resume that finds the new key already installed
// keeps the retired key it already has.
func InstallRotationKey(ctx context.Context, site storage.Site, privPEM, pubSSH []byte) error {
	cur, err := site.Store().Get(ctx, PrivateKeyKey)
	if err != nil {
		return fmt.Errorf("read the key being retired: %w", err)
	}
	if !bytes.Equal(cur, privPEM) {
		if err := atomicfile.Write(ctx, site, PreviousKeyKey, cur, 0o600); err != nil {
			return fmt.Errorf("keep the key being retired: %w", err)
		}
	}
	if err := atomicfile.Write(ctx, site, PrivateKeyKey, privPEM, 0o600); err != nil {
		return fmt.Errorf("install new private key: %w", err)
	}
	// ⭐ The public key through Put, which is also atomic and keeps the file's
	// existing mode, as rotation always did: forcing 0644 through atomicfile
	// let a restrictive umask turn it into 0600, a difference the
	// differential caught. Only the PRIVATE key's mode is forced.
	if err := site.Store().Put(ctx, PublicKeyKey, pubSSH, storage.WithMode(0o644)); err != nil {
		return fmt.Errorf("install new public key: %w", err)
	}
	return nil
}

// FinishRotation removes the retired key and the staged copy, once
// RecordKeyRotation has published the handover. ⛔ Never before: until then they
// are what lets a resume sign the handover again. The retired key goes first,
// so a crash in between leaves only a staged copy that equals the published
// key, which PrepareRotation recognises as finished.
func FinishRotation(ctx context.Context, site storage.Site) error {
	for _, k := range []string{PreviousKeyKey, StagedKeyKey, stagedPublicKeyKey} {
		if err := site.Store().Delete(ctx, k, storage.IgnoreMissing()); err != nil {
			return fmt.Errorf("remove %s after the rotation was recorded: %w", k, err)
		}
	}
	return nil
}

// SameSSHKey compares two OpenSSH public keys by type and key material,
// ignoring the comment: a key staged by ssh-keygen and the same key derived in
// Go carry different comments.
func SameSSHKey(a, b string) bool {
	fa, fb := strings.Fields(a), strings.Fields(b)
	return len(fa) >= 2 && len(fb) >= 2 && fa[0] == fb[0] && fa[1] == fb[1]
}
