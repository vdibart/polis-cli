package site

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The identity key, relative to the site root.
//
// ⛔ Both are under .polis, which is never served: on POSIX it is a declared
// private prefix (0700), and the serving configurations refuse the whole tree.
const (
	PrivateKeyKey = ".polis/keys/id_ed25519"
	PublicKeyKey  = ".polis/keys/id_ed25519.pub"
)

// IdentityKey is the instance's key pair, and whether this call made it.
type IdentityKey struct {
	Private []byte // OpenSSH PEM
	Public  []byte // OpenSSH public key, derived from Private
	Created bool
}

// NeedsIdentityKey reports whether an instance should hold — and therefore
// bootstrap — its own identity key.
//
// ⭐ Only where the instance signs unattended AND there is no operator.
//   - remote, client-signed: the owner signs; the instance holds NO key, so it
//     generates nothing. Generating one would be a server key nobody asked for.
//   - local: `polis init` makes the key, in front of the person.
//   - custodied: the operator provisions each tenant's key at signup.
func NeedsIdentityKey(set capability.Set) bool {
	return set.InstanceCanSignUnattended && !set.HasOperator
}

// EnsureIdentityKey returns the site's identity key, generating it into the
// site's OWN STORE if there is none.
//
// ⭐⭐ WHY THE STORE AND NOT THE PLATFORM'S SECRETS. Writing a key into a
// project's environment variables needs a PROJECT-EDITING token, and parking
// that beside the signing key means one compromise yields both. The instance
// already holds a storage-scoped credential for its own store, so the key goes
// there and nothing secret is injected at creation.
//
// ⛔⛔ THE CREATE IS CONDITIONAL, NEVER A READ-THEN-WRITE. Two concurrent cold
// starts that both find no key must not both make one: that is two identities
// for one site, and the loser's signatures verify against nothing. So the
// private key is written with storage.IfAbsent, and the loser of the race
// ADOPTS the winner's key instead of its own.
//
// ⚠️ IT NEVER OVERWRITES. An existing private key is returned as found; the
// public half is re-derived from it, so a missing or stale .pub heals to the
// key that actually signs and can never disagree with it.
func EnsureIdentityKey(ctx context.Context, site storage.Site) (IdentityKey, error) {
	st := site.Store()

	priv, err := st.Get(ctx, PrivateKeyKey)
	switch {
	case err == nil:
		return existingIdentityKey(ctx, site, priv)
	case errors.Is(err, storage.ErrNotFound):
	default:
		// ⛔ "Could not look" is not "absent": generating here would mint a
		// second identity whenever the store hiccups.
		return IdentityKey{}, fmt.Errorf("read identity key %s: %w", PrivateKeyKey, err)
	}

	// ⛔⛔ WHAT THE SITE ALREADY PUBLISHES DECIDES. A key in .well-known/polis
	// with no private key beside it means the private key was lost or not
	// carried over — a move that arrived without it, a mis-set mode, a private
	// store that read empty. Generating would mint a SECOND IDENTITY beside the
	// published one: everything signed under it verifies against a key the
	// site's own history never learned about. secret.Project refuses exactly
	// this case, and so does this.
	published, err := publishedKey(ctx, site)
	if err != nil {
		return IdentityKey{}, err
	}
	if published != nil {
		return IdentityKey{}, fmt.Errorf("this site already publishes an identity key in %s but %s is missing; "+
			"refusing to generate a second identity — restore the private key, or rotate the key deliberately", WellKnownKey, PrivateKeyKey)
	}
	fresh, _, gerr := signing.GenerateKeypair()
	if gerr != nil {
		return IdentityKey{}, fmt.Errorf("generate identity key: %w", gerr)
	}
	created := false
	perr := st.Put(ctx, PrivateKeyKey, fresh, storage.IfAbsent(), storage.WithMode(0o600))
	switch {
	case perr == nil:
		priv, created = fresh, true
	case errors.Is(perr, storage.ErrExists):
		// ⭐ Lost the race: another start made the key first. Use ITS key.
		if priv, err = st.Get(ctx, PrivateKeyKey); err != nil {
			return IdentityKey{}, fmt.Errorf("read the identity key another start created: %w", err)
		}
	default:
		return IdentityKey{}, fmt.Errorf("store identity key %s: %w", PrivateKeyKey, perr)
	}
	k, err := keyPair(ctx, site, priv, nil)
	k.Created = created
	return k, err
}

// existingIdentityKey checks a private key the store already holds against
// what the site publishes, and returns it.
//
// ⭐ The store is the only copy read. A store that is empty or replaced
// beside a live site is caught where its key would reach readers: before a
// publish, against the live .well-known/polis (CheckLiveIdentity).
func existingIdentityKey(ctx context.Context, site storage.Site, priv []byte) (IdentityKey, error) {
	published, err := publishedKey(ctx, site)
	if err != nil {
		return IdentityKey{}, err
	}
	return keyPair(ctx, site, priv, published)
}

// keyPair derives the public half of priv, refuses it when published is set
// and differs, and heals a missing or stale .pub.
func keyPair(ctx context.Context, site storage.Site, priv []byte, published ed25519.PublicKey) (IdentityKey, error) {
	st := site.Store()
	pub, err := signing.PublicKeyFromPrivate(priv)
	if err != nil {
		// ⛔ Name the key, never its bytes.
		return IdentityKey{}, fmt.Errorf("identity key %s does not parse: %w", PrivateKeyKey, err)
	}
	// ⛔ A private key that is not the published one is a different identity
	// too: signing with it would produce signatures nothing published verifies.
	if published != nil {
		mine, perr := signing.ParsePublicKey(pub)
		if perr != nil || !bytes.Equal(mine, published) {
			return IdentityKey{}, fmt.Errorf("the private key in %s does not match the identity key %s publishes; "+
				"refusing to use it", PrivateKeyKey, WellKnownKey)
		}
	}
	// ⭐ Idempotent: every racer derives identical bytes from the one winning
	// key, so a plain Put here cannot disagree with another.
	have, gerr := st.Get(ctx, PublicKeyKey)
	if gerr != nil || string(have) != string(pub) {
		if err := st.Put(ctx, PublicKeyKey, pub, storage.WithMode(0o644)); err != nil {
			return IdentityKey{}, fmt.Errorf("store public key %s: %w", PublicKeyKey, err)
		}
	}
	return IdentityKey{Private: priv, Public: pub}, nil
}

// publishedKey returns the identity key .well-known/polis publishes, or nil if
// the site publishes none.
//
// ⛔ "Could not tell" is an ERROR, never "nothing published": a .well-known
// that cannot be read or parsed might be publishing a key, and treating it as
// absent would let the caller mint a second identity.
func publishedKey(ctx context.Context, site storage.Site) (ed25519.PublicKey, error) {
	return wellKnownKeyIn(ctx, site.Store(), "")
}

// wellKnownKeyIn reads the identity key one store's .well-known/polis
// publishes; nil when the document is absent or names none.
func wellKnownKeyIn(ctx context.Context, st storage.Store, which string) (ed25519.PublicKey, error) {
	data, err := st.Get(ctx, WellKnownKey)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s%s to check for a published identity: %w", which, WellKnownKey, err)
	}
	var wk WellKnown
	if err := json.Unmarshal(data, &wk); err != nil {
		return nil, fmt.Errorf("%s%s does not parse, so whether it publishes an identity is unknown: %w", which, WellKnownKey, err)
	}
	if strings.TrimSpace(wk.PublicKey) == "" {
		return nil, nil
	}
	k, err := signing.ParsePublicKey([]byte(wk.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("the public key %s%s publishes does not parse: %w", which, WellKnownKey, err)
	}
	return k, nil
}
