package actor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
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
func ProjectKey(siteDir, secret string) (ProjectionOutcome, error) {
	keyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519")

	if _, err := os.Stat(keyPath); err == nil {
		// ⛔ NEVER OVERWRITE. See the package comment — the intuitive
		// alternative destroys identities.
		return ProjectionPresent, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("cannot stat the actor key at %s: %w", keyPath, err)
	}

	if strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("actor key secret is absent or empty and the key file at %s does not exist: "+
			"refusing to generate a new identity — restore the secret", keyPath)
	}

	privKey := []byte(secret)
	if !strings.HasSuffix(secret, "\n") {
		// A secret store round-trips values without a trailing newline more
		// often than not, and the OpenSSH PEM parser wants one.
		privKey = []byte(secret + "\n")
	}
	if err := signing.ValidatePrivateKey(privKey); err != nil {
		return "", fmt.Errorf("actor key secret is not a valid private key: %w", err)
	}

	pubKey, err := signing.PublicKeyFor(privKey)
	if err != nil {
		return "", fmt.Errorf("derive public key from the secret: %w", err)
	}

	// ⛔ THE STALE-SECRET CHECK, AND IT HAS TO BE HERE.
	//
	// A secret that was not updated after a rotation holds a RETIRED key. The
	// plan assumed the key-history chain would catch that, and IT DOES NOT:
	// Judge's chain-head check compares public_key_history's head against
	// .well-known/polis's public_key — two fields in the SAME published
	// document, neither of which is the key file. Both stay correct while the
	// projected private key is wrong.
	//
	// What would eventually catch it is a signature failing to verify, i.e.
	// AFTER the actor has already published something under a key nobody
	// accepts. So the comparison is made HERE, against what the site itself
	// publishes, before the key is written at all.
	if published, err := site.LoadWellKnown(siteDir); err == nil && published != nil && published.PublicKey != "" {
		if !sameSSHKey(published.PublicKey, string(pubKey)) {
			return "", fmt.Errorf("the actor key secret does not match the key this site publishes: " +
				"the secret is stale (a rotation was recorded and the secret was not updated) or it belongs to another site. " +
				"Refusing to project a retired identity")
		}
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return "", fmt.Errorf("create key directory: %w", err)
	}
	// ⛔ 0600, AND THE MODE IS LOAD-BEARING. Patrol checks key-file permissions
	// on every sweep; a projection writing 0644 turns the fleet red on the
	// first sweep — and would be right to.
	if err := atomicfile.WriteFile(keyPath, privKey, 0600); err != nil {
		return "", fmt.Errorf("write projected key: %w", err)
	}
	// The public half is written too, so a projected site is byte-identical to
	// a generated one and Patrol's key-match check has something to compare.
	if err := atomicfile.WriteFile(keyPath+".pub", pubKey, 0644); err != nil {
		return "", fmt.Errorf("write projected public key: %w", err)
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
