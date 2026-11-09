package dm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// storageSaltFile is the site-wide DM storage salt. The legacy seed-derived at-rest
// re-seal that consumed it is retired (DMs now store the end-to-end wire box via the
// mailbox), but the salt is retained as a Patrol canary: an unexpected change to it is a
// tamper signal. Keep creating it on init/heal so the canary baseline exists.
const storageSaltFile = "storage-salt"

// loadOrCreateSalt loads the site-wide storage salt, creating it if it doesn't exist.
func loadOrCreateSalt(site storage.Site) ([]byte, error) {
	// ⛔ The read and the write share ONE key. If they disagreed on a blob
	// site, every call would find no salt and mint a new one — and the salt is
	// Patrol's tamper canary, so a salt that changes on every call is a tamper
	// alarm that cannot be told from a real one.
	saltKey := ".polis/" + storageSaltFile
	ctx := context.Background()

	data, err := site.Store().Get(ctx, saltKey)
	if err == nil {
		salt, err := hex.DecodeString(string(data))
		if err != nil || len(salt) != 32 {
			return nil, fmt.Errorf("corrupt storage-salt file")
		}
		return salt, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// Generate new 32-byte random salt
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	// ⛔ A CONDITIONAL create: two starts finishing one site at once must not
	// each mint a salt, or the canary changes under Patrol. The loser reads the
	// winner's. ⭐ The store creates the parent, and .polis is a declared private
	// prefix, so it comes out 0700.
	err = site.Store().Put(ctx, saltKey, []byte(hex.EncodeToString(salt)), storage.IfAbsent(), storage.WithMode(0o600))
	if errors.Is(err, storage.ErrExists) {
		return loadOrCreateSalt(site)
	}
	if err != nil {
		return nil, fmt.Errorf("write salt: %w", err)
	}

	return salt, nil
}

// EnsureSalt ensures the site-wide storage salt exists, creating it if needed.
func EnsureSalt(site storage.Site) error {
	_, err := loadOrCreateSalt(site)
	return err
}
