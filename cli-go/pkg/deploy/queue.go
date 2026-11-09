// Package deploy queues and coalesces the deploys that publish a remote
// instance's site, where publishing means making a new deployment rather than
// writing files somewhere that serves them.
//
// ⚠️ EXPERIMENTAL. Remote instances on Vercel are exploratory and not under
// active development. This package's API may change or be removed without
// notice.
//
// ⭐⭐ BATCH, NEVER DEPLOY PER WRITE. A write lands in the private store at
// once and leaves a DIRTY MARK; at the end of the request that wrote, one
// deploy carries every write marked so far. A burst of writes (a theme change
// re-rendering every page) is one request, so it is one deployment.
//
// ⭐ Platform-agnostic: what a deploy does is the Deployer's (Vercel's is in
// pkg/provision). This package decides only WHEN, and who.
//
// The protocol, in the store, so separate processes agree:
//
//   - A request that wrote a public file writes a NEW marker under
//     DirtyPrefix. ⭐ A new key per mark, never an overwritten one, so no
//     reader can be shown a stale cached copy of "the" mark.
//   - One request holds the LEASE (LeaseKey, a conditional create carrying
//     an expiry). It lists the marks, deploys, and deletes exactly the marks
//     it listed, then looks again, until none are left or it has gone round
//     MaxRounds times.
//   - ⭐ Correct because each marker is written AFTER its request's content,
//     and the holder lists marks BEFORE the deploy lists the site: every mark
//     it deletes belongs to a write the deploy carried. A mark written later
//     survives for the next round or the next request.
//   - A request that finds the lease held returns at once: its mark is
//     already down, and the holder will see it. After releasing, the holder
//     looks once more and tries again, so a writer that found the lease held
//     just before the release is not stranded.
//   - ⚠️ The lease is an economy, not the correctness: two holders (a
//     takeover racing a slow holder) make two deployments of current
//     content, and the last to finish re-checks the marks.
//
// ⛔ A failed deploy deletes no marks: the write stands in the source, and the
// next deploy carries it. A limit delays a publish, never loses it.

package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const (
	// LeaseKey is the deploy lease. ⛔ Under .polis: never a static file.
	LeaseKey = ".polis/deploy/lease"

	// DirtyPrefix holds one marker per writing request not yet deployed.
	DirtyPrefix = ".polis/deploy/dirty"
)

// Deployer makes one deployment of the site as the store holds it now.
type Deployer interface {
	Deploy(ctx context.Context) error
}

// DeployFunc adapts a function to Deployer.
type DeployFunc func(ctx context.Context) error

func (f DeployFunc) Deploy(ctx context.Context) error { return f(ctx) }

// Queue coalesces one site's deploys.
type Queue struct {
	// Store is the site's private store.
	Store storage.Store

	// Deployer makes the deployment.
	Deployer Deployer

	// IsPublic reports whether a write to a key changes what readers see.
	IsPublic func(key string) bool

	// LeaseTTL bounds how long a holder that died keeps the lease
	// (default 2 minutes).
	LeaseTTL time.Duration

	// MaxRounds bounds the deploys one request makes (default 3), so a
	// steady stream of writes cannot hold one request open forever.
	MaxRounds int

	// Now is the clock, for tests.
	Now func() time.Time

	writes atomic.Uint64 // public writes made through Track
	marked atomic.Uint64 // the count when a request last marked the site dirty

	// deleted remembers the marks this process deleted, so a listing that
	// still shows one is read as stale (see Dirty).
	deletedMu sync.Mutex
	deleted   map[string]struct{}
	deletedQ  []string
}

// maxRemembered bounds the deleted marks a queue remembers. ⭐ Far more than
// a listing lags by: marks are deleted a few per deploy.
const maxRemembered = 4096

// forget records marks this queue deleted.
func (q *Queue) forget(keys []string) {
	q.deletedMu.Lock()
	defer q.deletedMu.Unlock()
	if q.deleted == nil {
		q.deleted = map[string]struct{}{}
	}
	for _, k := range keys {
		if _, ok := q.deleted[k]; ok {
			continue
		}
		q.deleted[k] = struct{}{}
		q.deletedQ = append(q.deletedQ, k)
	}
	for len(q.deletedQ) > maxRemembered {
		delete(q.deleted, q.deletedQ[0])
		q.deletedQ = q.deletedQ[1:]
	}
}

func (q *Queue) wasDeleted(key string) bool {
	q.deletedMu.Lock()
	defer q.deletedMu.Unlock()
	_, ok := q.deleted[key]
	return ok
}

// Outcome says what Run did.
type Outcome struct {
	Deploys int  // deployments made
	Held    bool // another holder had the lease
	Behind  bool // marks remain: the site is behind its source
	Err     error
}

func (q *Queue) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

func (q *Queue) ttl() time.Duration {
	if q.LeaseTTL > 0 {
		return q.LeaseTTL
	}
	return 2 * time.Minute
}

func (q *Queue) rounds() int {
	if q.MaxRounds > 0 {
		return q.MaxRounds
	}
	return 3
}

// Track returns s with every public write counted, so the request that made
// one is known to have written.
//
// ⭐ A process-wide count rather than a per-request flag: it needs no context
// threaded through every handler, and a concurrent request that sees another's
// write only marks the site dirty twice, which costs nothing.
func (q *Queue) Track(s storage.Store) storage.Store {
	return &tracked{Store: s, q: q}
}

// Changed records that the site's public files differ from what is deployed
// although no write was made through Track, so the next request carries a
// deploy as if it had written one.
//
// ⭐ For a site whose files arrived some other way: a store seeded before the
// instance first started has never been deployed from, and nothing it does
// would otherwise deploy it until its owner publishes.
func (q *Queue) Changed() { q.writes.Add(1) }

type tracked struct {
	storage.Store
	q *Queue
}

func (t *tracked) Put(ctx context.Context, key string, data []byte, opts ...storage.PutOption) error {
	err := t.Store.Put(ctx, key, data, opts...)
	if err == nil && t.q.IsPublic(key) {
		t.q.writes.Add(1)
	}
	return err
}

func (t *tracked) Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error {
	err := t.Store.Delete(ctx, key, opts...)
	// ⚠️ Counted even on an error: a recursive delete can remove some keys
	// and then fail, and a needless deploy is cheaper than a missed one.
	if k, cerr := storage.CleanKey(key); cerr == nil && (k == "" || t.q.IsPublic(k)) {
		t.q.writes.Add(1)
	}
	return err
}

// Middleware runs the deploy at the end of every request that wrote a public
// file. ⚠️ The response waits for it: there is nothing on this runtime that
// may run after a response is sent.
func (q *Queue) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		// ⭐ Against the count last marked, not the count when this request
		// began: a write made before this middleware ran (an instance's
		// first-request start) is carried by the first request after it.
		cur := q.writes.Load()
		if cur == q.marked.Load() {
			return
		}
		// ⭐ Not cancelled with the request: a client that hangs up after a
		// publish still gets its site deployed.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 90*time.Second)
		defer cancel()
		if err := q.MarkDirty(ctx); err != nil {
			return
		}
		q.marked.Store(cur)
		q.Run(ctx)
	})
}

// MarkDirty records that the site has changed since its last deploy.
func (q *Queue) MarkDirty(ctx context.Context) error {
	return q.Store.Put(ctx, DirtyPrefix+"/"+q.markName(), []byte("{}"))
}

// markName orders marks by time, and a random suffix keeps two marks made in
// the same nanosecond apart.
func (q *Queue) markName() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%020d-%s", q.now().UnixNano(), hex.EncodeToString(b))
}

// MarkTime is when a mark was made, read from its name.
func MarkTime(key string) (time.Time, bool) {
	name := key[strings.LastIndex(key, "/")+1:]
	i := strings.IndexByte(name, '-')
	if i <= 0 {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(name[:i], 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	return time.Unix(0, n), true
}

// Dirty returns the marks not yet deployed.
//
// ⭐ A mark this process deleted is left out even when the listing still
// shows it. Mark names are unique (a time and a random suffix) and never
// rewritten, so a deleted name that comes back can only be a stale listing:
// Vercel Blob's listing is eventually consistent, and reading its lag as new
// writes made a seeded instance's first start deploy twice, a second apart.
// ⚠️ Another process's deletions are not known here; a holder that sees
// those marks makes one needless deploy, never a missed one.
func (q *Queue) Dirty(ctx context.Context) ([]string, error) {
	entries, err := q.Store.List(ctx, DirtyPrefix, storage.Recursive())
	var out []string
	for _, e := range entries {
		if !e.IsDir && !q.wasDeleted(e.Key) {
			out = append(out, e.Key)
		}
	}
	return out, err
}

// Run deploys until no marks are left, if it can take the lease.
func (q *Queue) Run(ctx context.Context) Outcome {
	var o Outcome
	for attempt := 0; attempt < 2; attempt++ {
		token, held, err := q.acquire(ctx)
		if err != nil {
			o.Err = err
			break
		}
		if held {
			o.Held = true
			break
		}
		err = q.drain(ctx, &o)
		q.release(ctx, token)
		if err != nil {
			o.Err = err
			break
		}
		// ⭐ Looked at once more after the release, for a writer that found
		// the lease held just before it went.
		marks, lerr := q.Dirty(ctx)
		if lerr != nil || len(marks) == 0 || o.Deploys >= q.rounds() {
			break
		}
	}
	marks, err := q.Dirty(ctx)
	o.Behind = err != nil || len(marks) > 0
	return o
}

func (q *Queue) drain(ctx context.Context, o *Outcome) error {
	for o.Deploys < q.rounds() {
		marks, err := q.Dirty(ctx)
		if err != nil {
			return fmt.Errorf("deploy: cannot list what is waiting: %w", err)
		}
		if len(marks) == 0 {
			return nil
		}
		log.Printf("[i] deploy queue: round %d, deploying for %d mark(s)", o.Deploys+1, len(marks))
		if err := q.Deployer.Deploy(ctx); err != nil {
			return err
		}
		o.Deploys++
		for _, m := range marks {
			// ⚠️ A mark that cannot be deleted costs a needless deploy
			// later, never a lost one.
			_ = q.Store.Delete(ctx, m, storage.IgnoreMissing())
		}
		// ⭐ Remembered whether or not the delete succeeded: the deploy
		// carried every write these marks stood for.
		q.forget(marks)
	}
	return nil
}

type lease struct {
	Holder  string    `json:"holder"`
	Expires time.Time `json:"expires"`
}

// acquire takes the lease, taking over an expired one. held reports that a
// live holder has it.
func (q *Queue) acquire(ctx context.Context) (token string, held bool, err error) {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	token = hex.EncodeToString(b)
	doc, _ := json.Marshal(lease{Holder: token, Expires: q.now().Add(q.ttl()).UTC()})
	for try := 0; try < 2; try++ {
		err = q.Store.Put(ctx, LeaseKey, doc, storage.IfAbsent())
		if err == nil {
			return token, false, nil
		}
		if !errors.Is(err, storage.ErrExists) {
			return "", false, fmt.Errorf("deploy: cannot take the lease: %w", err)
		}
		raw, gerr := q.Store.Get(ctx, LeaseKey)
		if errors.Is(gerr, storage.ErrNotFound) {
			continue // released between the two calls
		}
		var l lease
		if gerr == nil && json.Unmarshal(raw, &l) == nil && q.now().Before(l.Expires) {
			return "", true, nil
		}
		// ⭐ Expired or unreadable: its holder is gone. Taken over.
		if derr := q.Store.Delete(ctx, LeaseKey, storage.IgnoreMissing()); derr != nil {
			return "", false, fmt.Errorf("deploy: cannot clear an expired lease: %w", derr)
		}
	}
	return "", true, nil
}

// release gives the lease up if it is still this holder's.
func (q *Queue) release(ctx context.Context, token string) {
	raw, err := q.Store.Get(ctx, LeaseKey)
	if err != nil {
		return
	}
	var l lease
	if json.Unmarshal(raw, &l) == nil && l.Holder == token {
		_ = q.Store.Delete(ctx, LeaseKey, storage.IgnoreMissing())
	}
}

// LeaseExpiry reports when the current lease expires, for status.
func (q *Queue) LeaseExpiry(ctx context.Context) (time.Time, bool) {
	raw, err := q.Store.Get(ctx, LeaseKey)
	if err != nil {
		return time.Time{}, false
	}
	var l lease
	if json.Unmarshal(raw, &l) != nil {
		return time.Time{}, false
	}
	return l.Expires, true
}
