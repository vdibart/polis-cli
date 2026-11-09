package actor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/secret"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Key projection — an actor's private key is held in a secret store and written
// onto the site's volume ONLY WHEN THE FILE IS ABSENT.
//
// # ⛔ THIS IS A BOOTSTRAP, NOT A SYNC, and the difference is not a preference
//
// The intuitive design is "write when the bytes differ", and it is actively
// dangerous. Three ways it breaks, all real:
//
//  1. ⛔ THE NATURAL GESTURE IS THE DESTRUCTIVE ONE. Setting a new secret
//     restarts the machine; a syncing projection would overwrite the private key
//     BEFORE any transition signature exists. Rotation signs the handover with
//     the OLD key before destroying it, and a secret store is write-only — the
//     old value is unrecoverable — so the chain could never be completed. And a
//     chain is append-only and is NEVER rebuilt, even when wrong. There is no
//     repair.
//  2. ⛔ A STALE SECRET WOULD REVERT A COMPLETED ROTATION. A restart after the
//     rotation was recorded but before the secret was updated would write the
//     OLD key back over the new one — the site then publishes a key it cannot
//     sign with, while its own history says the rotation happened.
//  3. ⚠️ A restart is not atomic with a rotation. The rotation path is one
//     process; splitting it across a machine restart is not.
//
// ⭐ Project-only-when-absent dissolves all three. It also means the key file is
// never rewritten, so its mtime is stable and Patrol's mtime alert never fires
// on it — and volume loss becomes fully recoverable rather than fatal, which is
// strictly better than snapshots alone.
//
// # ⛔ ABSENT MUST BE FATAL
//
// A missing or empty secret must fail the boot LOUDLY. It must NEVER generate a
// fresh keypair: that is the failure that silently replaces an identity, and
// everything the old key signed stops verifying against what the site publishes.

// ProjectionOutcome says what a projection did, which is what a caller logs.
type ProjectionOutcome string

const (
	// ProjectionPresent — the key file was already there. The normal case on
	// every restart after the first.
	ProjectionPresent ProjectionOutcome = "present"
	// ProjectionWritten — the key file was absent and has been restored.
	ProjectionWritten ProjectionOutcome = "written"
)

// ProjectKey restores an actor's private key from a secret when, and only when,
// the key file is absent.
//
// ⛔ It returns an error rather than generating anything in every failure mode.
// An actor that boots with the wrong identity is worse than one that does not
// boot.
func ProjectKey(siteDir, secretValue string) (ProjectionOutcome, error) {
	return ProjectKeyValue(siteDir, secret.New(secretValue))
}

// ProjectKeyValue is ProjectKey for a value already read through the secret
// seam (secret.Resolve), so it never passes through a plain string on the way.
func ProjectKeyValue(siteDir string, value secret.Value) (ProjectionOutcome, error) {
	keyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519")

	// ⭐ THE DISCIPLINE LIVES IN pkg/secret NOW, and this is a caller of it.
	//
	// Every rule below used to be written out here, and all of it still
	// applies — never overwrite, never generate, check staleness before
	// writing, fatal on either. What changed is WHERE it is written: in the
	// seam, so any later projected secret reuses it rather than reimplementing
	// reasoning that took a live incident to get right. (The remote identity
	// key, once planned as one, turned out not to be projected: it is
	// generated into the instance's own store.)
	//
	// ⛔ The three operations the seam cannot do itself are supplied here
	// because they are this tree's: real key parsing (pkg/signing), reading
	// what the site publishes (pkg/site), and the write (atomicfile, which
	// owns atomicity outside the storage interface and creates no parents).
	outcome, err := secret.Project(secret.Projection{
		Value:   value,
		KeyPath: keyPath,

		Absent: func(path string) (bool, error) {
			_, statErr := storage.StatAtPath(path)
			return secret.AbsentFromStat(statErr)
		},

		ValidateKey:  signing.ValidatePrivateKey,
		PublicKeyFor: signing.PublicKeyFor,

		// ⚠️ An unreadable or absent .well-known/polis yields "", which the
		// seam reads as "nothing to compare against" — the normal state of a
		// site being provisioned for the first time.
		PublishedKey: func() (string, error) {
			published, err := site.LoadWellKnown(storage.At(siteDir))
			if err != nil || published == nil {
				return "", err
			}
			return published.PublicKey, nil
		},

		SameKey: sameSSHKey,

		Write: func(priv, pub []byte) error {
			// Stays: atomicfile owns atomicity outside the storage interface
			// and creates no parents, so the directory is made here. ⛔ 0700,
			// because .polis must not be world-readable.
			if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
				return fmt.Errorf("create key directory: %w", err)
			}
			// ⛔ 0600, AND THE MODE IS LOAD-BEARING. Patrol checks key-file
			// permissions on every sweep; a projection writing 0644 turns the
			// fleet red on the first sweep — and would be right to.
			if err := atomicfile.WriteFile(keyPath, priv, 0600); err != nil {
				return err
			}
			// The public half is written too, so a projected site is
			// byte-identical to a generated one and Patrol's key-match check
			// has something to compare.
			return atomicfile.WriteFile(keyPath+".pub", pub, 0644)
		},
	})
	if err != nil {
		return "", err
	}
	if outcome == secret.Present {
		return ProjectionPresent, nil
	}
	return ProjectionWritten, nil
}

// sameSSHKey compares two OpenSSH public keys by TYPE AND BODY, ignoring the
// trailing comment. The comment is free-form and differs between tools; the
// key is the first two fields.
func sameSSHKey(a, b string) bool {
	fa := strings.Fields(strings.TrimSpace(a))
	fb := strings.Fields(strings.TrimSpace(b))
	if len(fa) < 2 || len(fb) < 2 {
		return false
	}
	return fa[0] == fb[0] && fa[1] == fb[1]
}
