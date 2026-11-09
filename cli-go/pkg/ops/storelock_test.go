package ops

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Two holders that share only the store (no process lock between them) never
// lose each other's read-modify-write.
func TestStoreLockSerialisesWritersThatShareOnlyTheStore(t *testing.T) {
	site := storage.At(t.TempDir())
	ctx := context.Background()
	const key = "counter"
	_ = site.Store().Put(ctx, key, []byte("0"))
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := WithStoreLock(ctx, site, "counter.lock", time.Minute, 30*time.Second, func() error {
				b, _ := site.Store().Get(ctx, key)
				n, _ := strconv.Atoi(string(b))
				time.Sleep(time.Millisecond)
				return site.Store().Put(ctx, key, []byte(strconv.Itoa(n+1)))
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if b, _ := site.Store().Get(ctx, key); string(b) != "6" {
		t.Errorf("counter = %s, want 6: a write was lost", b)
	}
	if ok, _ := site.Store().Exists(ctx, "counter.lock"); ok {
		t.Error("the lock was left behind")
	}
}

// An abandoned lock is taken over once it is stale; a live one is waited on
// and, past the wait, refused.
func TestStoreLockTakesOverOnlyAStaleLock(t *testing.T) {
	site := storage.At(t.TempDir())
	ctx := context.Background()
	old, _ := json.Marshal(storeLock{Holder: "dead", At: time.Now().Add(-time.Hour)})
	_ = site.Store().Put(ctx, "l", old)
	ran := false
	if err := WithStoreLock(ctx, site, "l", time.Minute, time.Second, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("a stale lock was not taken over: %v", err)
	}
	live, _ := json.Marshal(storeLock{Holder: "alive", At: time.Now()})
	_ = site.Store().Put(ctx, "l", live)
	if err := WithStoreLock(ctx, site, "l", time.Minute, 200*time.Millisecond, func() error { return nil }); err == nil {
		t.Error("a live lock was overridden")
	}
	if b, _ := site.Store().Get(ctx, "l"); string(b) != string(live) {
		t.Error("another holder's lock was deleted")
	}
}
