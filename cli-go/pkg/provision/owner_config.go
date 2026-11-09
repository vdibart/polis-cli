package provision

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ErrNoOwnerKey means the site carries no recovery public key, so an instance
// started from it could be claimed by whoever reached it first.
var ErrNoOwnerKey = errors.New("provision: the site carries no recovery public key (" + owner.RecoveryPublicKeyKey + ")")

// OwnerConfig is the configuration a seeded site needs set on its instance
// before the instance first starts: the owner's recovery PUBLIC key as
// POLIS_RECOVERY_KEY, read from the site (`polis branch` writes it there).
//
// ⭐ The deploy flow merges this into its SetConfig call, which comes before
// the first deployment, so only the holder of the private half can claim the
// new instance.
//
// ⚠️ A PUBLIC KEY IS CONFIG, NOT A SECRET: it is returned as Plain, and does
// not go through the secret seam. ⛔ Absent or malformed REFUSES rather than
// returning an empty config: an instance started without it is open to
// trust-on-first-use. The error names the file, never the value.
func OwnerConfig(site storage.Site) (Config, error) {
	data, err := site.Store().Get(context.Background(), owner.RecoveryPublicKeyKey)
	if errors.Is(err, storage.ErrNotFound) {
		return Config{}, ErrNoOwnerKey
	}
	if err != nil {
		return Config{}, fmt.Errorf("provision: read %s: %w", owner.RecoveryPublicKeyKey, err)
	}
	key := strings.TrimSpace(string(data))
	if strings.ContainsAny(key, "\n\r") {
		return Config{}, fmt.Errorf("provision: %s holds more than one line: it must be one OpenSSH public key", owner.RecoveryPublicKeyKey)
	}
	if err := signing.ValidatePublicKey([]byte(key)); err != nil {
		return Config{}, fmt.Errorf("provision: %s is not a valid OpenSSH Ed25519 public key", owner.RecoveryPublicKeyKey)
	}
	return Config{
		Values: map[string]string{owner.RecoveryKeyEnv: key},
		Plain:  []string{owner.RecoveryKeyEnv},
	}, nil
}
