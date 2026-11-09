package owner

import (
	"sync"
	"time"
)

// Cache is one process's memory of what the store said, so a busy instance
// does not ask again on every request.
//
// ⛔⛔ IT HOLDS COPIES OF THE STORE, NEVER STATE OF ITS OWN. Everything here can
// be thrown away at any moment — a restart, a recycle, another process serving
// the next request — and the answer must come out the same, because on a
// serverless platform that is exactly what happens between "begin" and
// "complete". Ceremony state rides in the client's MAC'd cookie; the store holds
// the MAC key, the generations and the single-use markers. ⚠️ A field added
// here that is not a copy of something in the store is the bug this file exists
// to prevent: sign-in would work on a laptop and fail at random on Vercel.
//
// ⭐ IT HANGS OFF THE INSTANCE, NOT OFF THE PACKAGE, for the same reason
// enrolment does: hosted runs many tenants in one process, and a package-level
// cache would be one more place a tenant's state could leak into another's.
// The server that owns a site owns its cache. A nil Cache is legitimate and
// means "read the store every time".
type Cache struct {
	mu sync.Mutex

	// ttl bounds how stale a cached generation or enrolment record may be.
	// ⚠️ This is the one cost of caching, and it is stated rather than hidden:
	// "sign out everywhere" reaches another process within ttl, not at once.
	ttl time.Duration

	// now is the clock, overridable in tests.
	now func() time.Time

	keys  map[string][]byte
	gens  map[string]cachedGenerations
	enrol map[string]cachedBlob
	swept map[string]string
}

// cacheTTL is how long a process trusts what it last read.
const cacheTTL = 30 * time.Second

type cachedGenerations struct {
	g  generations
	at time.Time
}

type cachedBlob struct {
	blob []byte
	err  error
	at   time.Time
}

// NewCache returns an empty cache for one server's site.
func NewCache() *Cache {
	return &Cache{
		ttl:   cacheTTL,
		now:   time.Now,
		keys:  map[string][]byte{},
		gens:  map[string]cachedGenerations{},
		enrol: map[string]cachedBlob{},
		swept: map[string]string{},
	}
}

// SetTTL changes how long this cache trusts a read. ⭐ For tests that need two
// processes to agree at once; production uses the default.
func (c *Cache) SetTTL(d time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.ttl = d
	c.mu.Unlock()
}

// clock returns the instance's clock: the cache's when there is one.
func clock(inst Instance) time.Time {
	if inst.Cache != nil && inst.Cache.now != nil {
		return inst.Cache.now()
	}
	return time.Now()
}
