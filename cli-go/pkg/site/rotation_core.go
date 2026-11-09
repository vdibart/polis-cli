package site

import (
	"context"
	"fmt"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The offline core of a key rotation, shared by every command that rotates a
// site's identity key on the owner's machine: `polis rotate-key`, which wraps
// it in a discovery-service announcement, and `polis branch`, which makes no
// network call at all.
//
// ⭐ ONE CORE, SO THE NEXT FIX TO ROTATION LANDS ONCE. The steps are the ones
// rotate-key has always taken, in the same order: sign, stage, (announce),
// install, record, finish, re-sign the messages-key block, re-sign the follow
// and blessing lists, republish did.json. Each step's reasoning is at the
// function it calls; this file only orders them.
//
// ⛔ PrepareRotation is NOT inside it. A caller prepares first, so it can say
// "resuming" and settle its own inputs (rotate-key reads its domain only after
// preparing) before anything is signed.

// RotationStage names the step a rotation failed at, so a caller can say what
// state the site was left in. The stages before StageInstall changed nothing
// but the staged key; from StageInstall on the new key may be in place.
type RotationStage string

const (
	StageCanonical   RotationStage = "canonical"
	StageSign        RotationStage = "sign"
	StageStage       RotationStage = "stage"
	StageInstall     RotationStage = "install"
	StageRecord      RotationStage = "record"
	StageMessagesKey RotationStage = "messages-key"
)

// RotationError is a rotation that stopped at Stage.
type RotationError struct {
	Stage RotationStage
	Err   error
}

func (e *RotationError) Error() string { return fmt.Sprintf("rotation %s: %v", e.Stage, e.Err) }
func (e *RotationError) Unwrap() error { return e.Err }

// RotationAnnouncer tells a discovery service about a rotation whose new key is
// staged, and returns its countersignatures. resuming is RotationKeys.Resuming.
// An error stops the rotation before the key is installed.
type RotationAnnouncer func(req discovery.KeyRotationRequest, resuming bool) ([]discovery.Witness, error)

// RotationOptions is what a rotation needs beyond the keys.
type RotationOptions struct {
	// SigningDomain goes into the canonical rotation message the old key signs,
	// and is recorded on the new entry as signing_domain. ⛔ For a branch it is
	// the ORIGIN's domain: the old key speaks for the old address.
	SigningDomain string

	// DIDHost is the host did.json is republished for. Empty means
	// SigningDomain — a rotation in place. A branch passes the NEW host.
	DIDHost string

	// Announce, when set, runs after the new key is staged and before it is
	// installed (strict ordering: the network learns the key before the site
	// switches to it). Nil makes no network call of any kind.
	Announce RotationAnnouncer

	// Timestamp is the rotation's valid_from, signed verbatim. Empty means now.
	Timestamp string
}

// RotationResult is what a completed rotation did.
type RotationResult struct {
	NewPublicKey string
	Timestamp    string
	// Epoch is the new entry's epoch in public_key_history.
	Epoch int

	// FinishErr is a failure to remove the retired and staged keys after the
	// rotation was recorded. Not fatal (the next rotation removes them), but a
	// private key is left on disk, so a caller says so.
	FinishErr error

	// MessagesKeyResigned says the site holds a DM keyring and its
	// public_key_messages block was re-signed under the new key.
	MessagesKeyResigned bool

	FollowResign  signing.ResignOutcome
	FollowErr     error
	BlessedResign signing.ResignOutcome
	BlessedErr    error

	// DIDPublished: did.json now states the new key. When republishing failed,
	// DIDErr says why and DIDRemoved says the stale document was deleted.
	DIDPublished bool
	DIDRemoved   bool
	DIDErr       error
}

// CompleteRotation carries a prepared rotation through to the end. keys comes
// from PrepareRotation on the same site.
//
// ⛔ It never calls a discovery service itself; only opts.Announce can.
func CompleteRotation(ctx context.Context, site storage.Site, keys *RotationKeys, opts RotationOptions) (*RotationResult, error) {
	timestamp := opts.Timestamp
	if timestamp == "" {
		timestamp = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	res := &RotationResult{NewPublicKey: keys.NewPub, Timestamp: timestamp}

	// Build canonical rotation JSON and sign with the OLD key.
	canonical, err := discovery.MakeKeyRotationCanonicalJSON(opts.SigningDomain, keys.OldPub, keys.NewPub, timestamp)
	if err != nil {
		return nil, &RotationError{StageCanonical, err}
	}
	transitionSig, err := signing.SignContent(canonical, keys.OldPriv)
	if err != nil {
		return nil, &RotationError{StageSign, err}
	}

	// ⛔ STAGE FIRST: the new key is durably on disk before anyone is told.
	if !keys.Resuming {
		if err := StageRotationKey(ctx, site, keys.NewPriv); err != nil {
			return nil, &RotationError{StageStage, err}
		}
	}

	var witnesses []discovery.Witness
	if opts.Announce != nil {
		w, err := opts.Announce(discovery.KeyRotationRequest{
			Domain:        opts.SigningDomain,
			OldKey:        keys.OldPub,
			NewKey:        keys.NewPub,
			TransitionSig: transitionSig,
			Timestamp:     timestamp,
		}, keys.Resuming)
		if err != nil {
			return nil, err
		}
		witnesses = w
	}

	if err := InstallRotationKey(ctx, site, keys.NewPriv, keys.NewPubSSH); err != nil {
		return nil, &RotationError{StageInstall, err}
	}

	// public_key and the appended history entry, in one write, through the
	// seam. timestamp is passed verbatim: it is the value the old key just
	// signed, and it becomes the new entry's valid_from.
	if err := RecordKeyRotation(site, keys.NewPub, transitionSig, timestamp, opts.SigningDomain, witnesses...); err != nil {
		return nil, &RotationError{StageRecord, err}
	}
	res.FinishErr = FinishRotation(ctx, site)
	if block, herr := LoadKeyHistory(site); herr == nil && block != nil {
		res.Epoch = block.Current.Epoch
	}

	// Re-sign the public_key_messages block under the NEW identity key. The DM
	// messages keys themselves don't change — only the signatures over them.
	// A site with no DM keyring has nothing to re-sign.
	keyringPresent, _ := site.Store().Exists(ctx, dm.DMDirKey+"/keyring.json")
	if keyringPresent {
		if err := PublishMessagesKey(site, keys.NewPriv); err != nil {
			return nil, &RotationError{StageMessagesKey, err}
		}
		res.MessagesKeyResigned = true
	}

	// Re-sign the follow file and the blessing list under the NEW key. Neither
	// carries a signing time, so each verifies against the current key only.
	// ⛔ Only a file that verifies under the OLD key is re-signed
	// (signing.ResignOutcome), so a rotation never launders a failing list.
	// Not fatal: a file that was not re-signed is the file it was before.
	res.FollowResign, res.FollowErr = following.ResignAfterRotation(site, []byte(keys.OldPub), keys.NewPriv)
	res.BlessedResign, res.BlessedErr = metadata.ResignBlessedAfterRotation(site, []byte(keys.OldPub), keys.NewPriv)

	// Republish the DID Document so it carries the new key and the retired
	// ones. ⚠️ It is rebuilt from .well-known/polis, so it must come AFTER
	// RecordKeyRotation. Not fatal: the rotation has happened. A document that
	// could not be republished names the RETIRED key, so it is removed rather
	// than left answering with a key the site no longer holds; it is derived,
	// and `polis did --write` rebuilds it.
	host := opts.DIDHost
	if host == "" {
		host = opts.SigningDomain
	}
	if err := PublishDIDDocument(site, host); err == nil {
		res.DIDPublished = true
	} else {
		res.DIDErr = err
		if rmErr := site.Store().Delete(ctx, DIDDocumentKey); rmErr == nil {
			res.DIDRemoved = true
		}
	}
	return res, nil
}
