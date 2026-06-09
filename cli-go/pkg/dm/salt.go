package dm

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
)

// storageSaltFile is the site-wide DM storage salt. The legacy seed-derived at-rest
// re-seal that consumed it is retired (DMs now store the end-to-end wire box via the
// mailbox), but the salt is retained as a Patrol canary: an unexpected change to it is a
// tamper signal. Keep creating it on init/heal so the canary baseline exists.
const storageSaltFile = "storage-salt"

// loadOrCreateSalt loads the site-wide storage salt, creating it if it doesn't exist.
func loadOrCreateSalt(siteDir string) ([]byte, error) {
	saltPath := filepath.Join(siteDir, ".polis", storageSaltFile)

	data, err := os.ReadFile(saltPath)
	if err == nil {
		salt, err := hex.DecodeString(string(data))
		if err != nil || len(salt) != 32 {
			return nil, fmt.Errorf("corrupt storage-salt file")
		}
		return salt, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	// Generate new 32-byte random salt
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(saltPath), 0700); err != nil {
		return nil, err
	}
	if err := atomicfile.WriteFile(saltPath, []byte(hex.EncodeToString(salt)), 0600); err != nil {
		return nil, fmt.Errorf("write salt: %w", err)
	}

	return salt, nil
}

// EnsureSalt ensures the site-wide storage salt exists, creating it if needed.
func EnsureSalt(siteDir string) error {
	_, err := loadOrCreateSalt(siteDir)
	return err
}
