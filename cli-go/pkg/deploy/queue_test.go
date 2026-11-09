package deploy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func isPublic(k string) bool { return k != "" && !strings.HasPrefix(k, ".polis") }

type rig struct {
	q       *Queue
	store   storage.Store // the tracked store handlers write through
	deploys atomic.Int32
	during  func() // runs inside a deploy, once
	fail    error
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{}
	raw := storage.NewObjectStore("site")
	r.q = &Queue{Store: raw, IsPublic: isPublic}
	r.q.Deployer = DeployFunc(func(ctx context.Context) error {
		r.deploys.Add(1)
		if f := r.during; f != nil {
			r.during = nil
			f()
		}
		return r.fail
	})
	r.store = r.q.Track(raw)
	return r
}

// request runs one request through the middleware whose handler writes keys.
func (r *rig) request(keys ...string) {
	h := r.q.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for _, k := range keys {
			_ = r.store.Put(req.Context(), k, []byte("x"))
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
}

func (r *rig) marks(t *testing.T) int {
	m, err := r.q.Dirty(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(m)
}

// ⭐⭐ A burst of writes in one request is one deployment.
func TestABurstOfWritesIsOneDeployment(t *testing.T) {
	r := newRig(t)
	var keys []string
	for i := 0; i < 50; i++ {
		keys = append(keys, "posts/p"+string(rune('a'+i%26))+string(rune('a'+i/26))+".html")
	}
	r.request(keys...)
	if n := r.deploys.Load(); n != 1 {
		t.Errorf("%d deployments for one request of 50 writes; want 1", n)
	}
	if r.marks(t) != 0 {
		t.Error("marks remain after a successful deploy")
	}
}

// ⛔ A request that writes nothing public deploys nothing.
func TestARequestWithNoPublicWriteDeploysNothing(t *testing.T) {
	r := newRig(t)
	r.request()
	r.request(".polis/state.json", ".polis/deploy/whatever")
	if n := r.deploys.Load(); n != 0 {
		t.Errorf("%d deployments; want none", n)
	}
}

// ⭐ A write that lands during a deploy gets exactly one follow-up.
func TestAWriteDuringADeployGetsOneFollowUp(t *testing.T) {
	r := newRig(t)
	r.during = func() {
		ctx := context.Background()
		_ = r.store.Put(ctx, "late.html", []byte("x"))
		_ = r.q.MarkDirty(ctx) // what the other request's middleware does
	}
	r.request("index.html")
	if n := r.deploys.Load(); n != 2 {
		t.Errorf("%d deployments; want 2 (the write, then its follow-up)", n)
	}
	if r.marks(t) != 0 {
		t.Error("the late write was left undeployed")
	}
}

// ⭐ Under a live lease, a second request leaves its mark and returns.
func TestASecondRequestUnderALiveLeaseDoesNotDeploy(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, held, err := r.q.acquire(ctx); err != nil || held {
		t.Fatal("could not take the lease", err)
	}
	r.request("index.html")
	if n := r.deploys.Load(); n != 0 {
		t.Errorf("%d deployments under another holder's lease", n)
	}
	if r.marks(t) != 1 {
		t.Error("the write left no mark for the holder")
	}
}

// ⭐ A holder that died is taken over once its lease expires.
func TestAnExpiredLeaseIsTakenOver(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	now := time.Now()
	r.q.Now = func() time.Time { return now }
	if _, _, err := r.q.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	r.q.Now = func() time.Time { return now.Add(3 * time.Minute) }
	r.request("index.html")
	if n := r.deploys.Load(); n != 1 {
		t.Errorf("%d deployments; want the expired lease taken over and 1", n)
	}
	if _, ok := r.q.LeaseExpiry(ctx); ok {
		t.Error("the lease was not released")
	}
}

// ⭐ A refused deploy loses nothing: the mark stays, and the next writing
// request carries it.
func TestARefusedDeployIsDelayedNeverLost(t *testing.T) {
	r := newRig(t)
	r.fail = errors.New("429")
	r.request("index.html")
	if r.marks(t) != 1 {
		t.Fatal("a refused deploy deleted its mark")
	}
	o := r.q.Run(context.Background())
	if !o.Behind || o.Err == nil {
		t.Errorf("outcome %+v: want behind, with the error", o)
	}
	r.fail = nil
	r.request("about.html")
	if r.marks(t) != 0 {
		t.Error("the next deploy did not carry the earlier write")
	}
	if _, ok := r.q.LeaseExpiry(context.Background()); ok {
		t.Error("a failed deploy kept the lease")
	}
}

// ⭐ Many concurrent writing requests make few deployments, and none is lost.
//
// ⚠️ The requests are concurrent BY CONSTRUCTION: each handler writes, then
// waits until all 20 have written, so every mark is down before any request
// reaches the deploy. Left to the scheduler they ran one after another on a
// single CPU or a loaded machine, and the test measured that instead.
func TestConcurrentRequestsCoalesce(t *testing.T) {
	r := newRig(t)
	const n = 20
	var wrote sync.WaitGroup
	wrote.Add(n)
	h := r.q.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = r.store.Put(req.Context(), req.URL.Query().Get("k"), []byte("x"))
		wrote.Done()
		wrote.Wait()
	}))
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := "p" + string(rune('a'+i)) + ".html"
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/?k="+k, nil))
		}(i)
	}
	wg.Wait()
	r.q.Run(context.Background())
	if r.marks(t) != 0 {
		t.Error("a write was left undeployed")
	}
	if n := r.deploys.Load(); n < 1 || n >= 20 {
		t.Errorf("%d deployments for 20 requests", n)
	}
}

// ⭐ One request makes at most MaxRounds deployments under a steady stream.
func TestOneRequestIsBoundedUnderAStream(t *testing.T) {
	r := newRig(t)
	r.q.Deployer = DeployFunc(func(ctx context.Context) error {
		r.deploys.Add(1)
		return r.q.MarkDirty(ctx) // another write lands every time
	})
	r.request("index.html")
	if n := r.deploys.Load(); n != 3 {
		t.Errorf("%d deployments; want MaxRounds (3)", n)
	}
	if r.marks(t) == 0 {
		t.Error("the stream's last write should still be waiting")
	}
}

// ⭐ A write made outside any request's middleware (an instance's
// first-request start) is carried by the next request.
func TestAWriteBeforeTheMiddlewareIsCarriedByTheNextRequest(t *testing.T) {
	r := newRig(t)
	_ = r.store.Put(context.Background(), "index.html", []byte("x"))
	r.request()
	if n := r.deploys.Load(); n != 1 {
		t.Errorf("%d deployments; want the earlier write deployed", n)
	}
	r.request()
	if n := r.deploys.Load(); n != 1 {
		t.Errorf("%d deployments after a request that wrote nothing", n)
	}
}

// ⭐ A change recorded without a write (a seeded site) is deployed by the next
// request, once.
func TestAChangeRecordedWithoutAWriteIsDeployed(t *testing.T) {
	r := newRig(t)
	r.q.Changed()
	r.request()
	if n := r.deploys.Load(); n != 1 {
		t.Errorf("%d deployments; want the recorded change deployed", n)
	}
	r.request()
	if n := r.deploys.Load(); n != 1 {
		t.Errorf("%d deployments after a request that changed nothing", n)
	}
}

// laggingStore lists keys for a while after they are deleted, as Vercel
// Blob's eventually consistent listing does.
type laggingStore struct {
	storage.Store
	mu     sync.Mutex
	ghosts map[string]int // deleted key → listings it still appears in
}

func (s *laggingStore) Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error {
	s.mu.Lock()
	s.ghosts[key] = 3
	s.mu.Unlock()
	return s.Store.Delete(ctx, key, opts...)
}

func (s *laggingStore) List(ctx context.Context, prefix string, opts ...storage.ListOption) ([]storage.Entry, error) {
	e, err := s.Store.List(ctx, prefix, opts...)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, n := range s.ghosts {
		if n > 0 && strings.HasPrefix(k, prefix) {
			e = append(e, storage.Entry{Key: k})
			s.ghosts[k] = n - 1
		}
	}
	return e, err
}

// ⭐ A listing that still shows the marks this queue just deleted does not
// make a second deployment. Seen live at a seeded instance's first start:
// two identical deployments a second apart.
func TestAStaleListingOfDeletedMarksDoesNotDeployAgain(t *testing.T) {
	r := &rig{}
	raw := &laggingStore{Store: storage.NewObjectStore("site"), ghosts: map[string]int{}}
	r.q = &Queue{Store: raw, IsPublic: isPublic}
	r.q.Deployer = DeployFunc(func(ctx context.Context) error {
		r.deploys.Add(1)
		return nil
	})
	r.store = r.q.Track(raw)

	r.request("posts/a.html")
	if n := r.deploys.Load(); n != 1 {
		t.Errorf("%d deployments for one write under a lagging listing; want 1", n)
	}
	if n := r.marks(t); n != 0 {
		t.Errorf("%d marks reported waiting after the deploy; want 0 (the listing's are ones this queue deleted)", n)
	}
}
