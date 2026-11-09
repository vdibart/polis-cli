package dm

import (
	"errors"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"io/fs"
)

// ErrRevisionConflict is returned by SaveCAS when the on-disk keyring has advanced
// past the revision the caller read before mutating — i.e. a concurrent write happened
// (another device/session). The caller must reload, reapply its mutation, and retry.
// This is what prevents concurrent keyring writes from silently dropping a wrap.
var ErrRevisionConflict = errors.New("dm: keyring revision conflict (concurrent write; reload and retry)")

// SaveCAS persists the keyring only if the on-disk revision still equals
// expectedOnDiskRevision — the value the caller read *before* applying its mutation.
// If the on-disk keyring has moved on, it returns ErrRevisionConflict and writes
// nothing. This is the compare-and-swap guard against multi-device keyring writes.
//
// NOTE: there is a TOCTOU between the read-back and the write. The server MUST serialize
// keyring mutations per tenant (e.g. a per-tenant mutex) so the load → mutate → SaveCAS
// sequence is atomic; SaveCAS is the on-disk half of that contract, not a replacement
// for the lock.
func (k *Keyring) SaveCAS(site storage.Site, expectedOnDiskRevision int) error {
	// ⭐ Held under the keyring's per-key lock, so the compare and the swap
	// cannot interleave with another writer IN THIS PROCESS. ⚠️ Before this the
	// load-compare-save ran unlocked — a compare-and-swap only in name within a
	// process, and on a blob backend there is no filesystem lock to fall back
	// on. Cross-process exclusion is unchanged: that is still the revision check.
	return storage.WithKeyLock(site.Store(), keyringKey, func() error {
		return k.saveCASLocked(site, expectedOnDiskRevision)
	})
}

func (k *Keyring) saveCASLocked(site storage.Site, expectedOnDiskRevision int) error {
	onDisk, err := LoadKeyring(site)
	switch {
	case err == nil:
		if onDisk.Revision != expectedOnDiskRevision {
			return ErrRevisionConflict
		}
	case errors.Is(err, fs.ErrNotExist):
		// No keyring yet: only valid as a first write (expected revision 0).
		if expectedOnDiskRevision != 0 {
			return ErrRevisionConflict
		}
	default:
		return err
	}
	return k.Save(site)
}
