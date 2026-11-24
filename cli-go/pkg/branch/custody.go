package branch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Custody decides which private key the owner keeps when the branch is
// deployed, and so which public key the new instance holds as its root of
// authority (POLIS_RECOVERY_KEY).
//
// ⭐ ONE MECHANISM, TWO KEYS. The instance holds a public key and the owner
// proves ownership by signing a challenge; the custody only decides which
// private key that is:
//
//   - ServerSigned: the instance holds the identity key and signs content, so
//     the owner keeps a separate RECOVERY-ONLY key, made here.
//   - ClientSigned: the owner keeps the IDENTITY key, which signs content and
//     recovery both, and nothing secret is seeded.
type Custody string

const (
	// ServerSigned is the default: the new identity key stays in the output.
	ServerSigned Custody = "server"
	// ClientSigned keeps the new identity key out of the output. ⚠️ Designed
	// for, not built: Run refuses it.
	ClientSigned Custody = "client"
)

// ErrClientSignedNotBuilt is the refusal for ClientSigned.
var ErrClientSignedNotBuilt = errors.New("a client-signed branch (you keep the identity key) is not built yet: branch server-signed")

// ownerKeys is THE SEAM for which key is withheld from the output. Given the
// new identity key, it returns the private key the owner keeps (written outside
// the output, never inside it), and the public key the instance is configured
// with. withholdIdentity says whether the identity key itself is the one kept,
// in which case it must leave the output too.
//
// ⭐ Building client-signed means removing the refusal in Run and honouring
// withholdIdentity; nothing here changes.
func ownerKeys(c Custody, identityPriv []byte) (withheld, recoveryPublic []byte, withholdIdentity bool, err error) {
	switch c {
	case ServerSigned, "":
		priv, pub, err := signing.GenerateKeypair()
		if err != nil {
			return nil, nil, false, fmt.Errorf("make the recovery key: %w", err)
		}
		return priv, pub, false, nil
	case ClientSigned:
		pub, err := signing.PublicKeyFromPrivate(identityPriv)
		if err != nil {
			return nil, nil, false, fmt.Errorf("derive the identity key's public half: %w", err)
		}
		return append([]byte(nil), identityPriv...), pub, true, nil
	default:
		return nil, nil, false, fmt.Errorf("unknown custody %q: use %q or %q", c, ServerSigned, ClientSigned)
	}
}

// DefaultRecoveryKeyPath is where the owner's recovery private key for a
// domain goes when no path is named: ~/.polis/recovery/<domain>, outside any
// site directory.
func DefaultRecoveryKeyPath(domain string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("cannot find your home directory for the recovery key: name a path with --recovery-key")
	}
	return filepath.Join(home, ".polis", "recovery", domain), nil
}

// checkRecoveryKeyPath refuses a path that exists, or that lies inside the
// output or the input. ⛔ The output is what gets seeded into the instance, so
// a private key there would reach the server and defeat its purpose.
//
// NOT the storage interface: the recovery key lives on the owner's machine,
// outside every site, and is never a site key.
func checkRecoveryKeyPath(p string, forbidden ...string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	for _, dir := range forbidden {
		if dir != "" && within(abs, dir) {
			return "", fmt.Errorf("the recovery key %s would be inside %s: it must stay outside the branch's input and output", abs, dir)
		}
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", fmt.Errorf("a file already exists at %s: a recovery key is never overwritten. Name another path with --recovery-key", abs)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("check the recovery key path %s: %w", abs, err)
	}
	return abs, nil
}

// writeRecoveryKey writes the owner's private key at p, mode 0600, in a
// directory made 0700, and never over an existing file.
//
// NOT the storage interface: the key lives on the owner's machine, outside
// every site.
func writeRecoveryKey(p string, priv []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create the recovery key's directory: %w", err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: the recovery-key path the owner named, checked by the caller
	if err != nil {
		return fmt.Errorf("write the recovery key: %w", err)
	}
	_, werr := f.Write(priv)
	if err := f.Sync(); werr == nil {
		werr = err
	}
	if err := f.Close(); werr == nil {
		werr = err
	}
	if werr != nil {
		if rerr := os.Remove(p); rerr != nil {
			return fmt.Errorf("write the recovery key: %w (and the partial file %s could not be removed: %v)", werr, p, rerr)
		}
		return fmt.Errorf("write the recovery key: %w", werr)
	}
	return nil
}

// removeRecoveryKey removes a recovery key this branch just wrote, when the
// branch then failed.
//
// NOT the storage interface: the key lives on the owner's machine.
func removeRecoveryKey(p string) {
	_ = os.Remove(p)
}
