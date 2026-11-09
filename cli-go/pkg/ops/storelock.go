package ops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// WithStoreLock runs fn while holding a lock kept IN THE SITE'S STORE, so it
// holds across every instance that shares the store, not only this process.
//
// ⭐ The store has no compare-and-swap, only a conditional create, so the lock
// is an object created with IfAbsent and deleted when fn returns. A lock older
// than stale is treated as abandoned (its holder died) and taken over; stale
// must exceed the longest fn, or two holders can overlap.
//
// ⚠️ Use it for RARE writes only: each acquisition costs a create and a
// delete, which on a metered store (Vercel Blob) are the expensive kind.
func WithStoreLock(ctx context.Context, site storage.Site, key string, stale, wait time.Duration, fn func() error) error {
	tok := make([]byte, 8)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	body, _ := json.Marshal(storeLock{Holder: hex.EncodeToString(tok), At: time.Now().UTC()})
	store := site.Store()
	deadline := time.Now().Add(wait)
	backoff := 50 * time.Millisecond
	for {
		err := store.Put(ctx, key, body, storage.IfAbsent(), storage.WithMode(0o600))
		if err == nil {
			break
		}
		if !errors.Is(err, storage.ErrExists) {
			return fmt.Errorf("take the lock %s: %w", key, err)
		}
		if held, rerr := store.Get(ctx, key); rerr == nil {
			var l storeLock
			if json.Unmarshal(held, &l) != nil || time.Since(l.At) > stale {
				_ = store.Delete(ctx, key, storage.IgnoreMissing())
				continue
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another instance holds %s; try again", key)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < time.Second {
			backoff *= 2
		}
	}
	defer func() {
		// ⭐ Delete only our own lock: if we overran stale and another holder
		// took over, the lock is theirs now.
		if held, err := store.Get(context.Background(), key); err == nil && string(held) == string(body) {
			_ = store.Delete(context.Background(), key, storage.IgnoreMissing())
		}
	}()
	return fn()
}

type storeLock struct {
	Holder string    `json:"holder"`
	At     time.Time `json:"at"`
}
