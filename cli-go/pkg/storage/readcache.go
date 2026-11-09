package storage

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// ReadCache returns s with its reads remembered, for ONE bounded operation such
// as a render: each key is read from s once, a key that is absent is
// remembered as absent, and a listing is made once per prefix.
//
// ⭐ WHY: a render reads the same templates, snippets and theme files for every
// page it writes. On a POSIX site that costs nothing; on a remote store every
// read is a network round trip, and one publish on a three-post site made 224
// sequential calls, 8.6 s of a 12.6 s publish (measured 2026-10-17).
//
// ⛔ Writes go straight to s, and a write or delete drops what the cache held
// for that key and every remembered listing, so the operation always reads
// back what it wrote. ⛔ Never keep one across requests: another process's
// writes are invisible to it, which is harmless inside one render and wrong
// beyond it.
func ReadCache(s Store) Store {
	return &readCache{Store: s, gets: map[string]cachedGet{}, lists: map[string]cachedList{}, stats: map[string]cachedStat{}}
}

type cachedGet struct {
	data []byte
	err  error
}

type cachedList struct {
	entries []Entry
	err     error
}

type cachedStat struct {
	info Info
	err  error
}

type readCache struct {
	Store
	mu    sync.Mutex
	gets  map[string]cachedGet
	lists map[string]cachedList
	stats map[string]cachedStat
}

// cacheable is an error worth remembering: absence. Any other failure may be
// transient and is retried on the next read.
func cacheable(err error) bool { return err == nil || errors.Is(err, ErrNotFound) }

func (c *readCache) Get(ctx context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	g, ok := c.gets[key]
	c.mu.Unlock()
	if ok {
		if g.err != nil {
			return nil, g.err
		}
		return append([]byte(nil), g.data...), nil
	}
	data, err := c.Store.Get(ctx, key)
	if cacheable(err) {
		c.mu.Lock()
		c.gets[key] = cachedGet{data: append([]byte(nil), data...), err: err}
		c.mu.Unlock()
	}
	return data, err
}

func (c *readCache) Exists(ctx context.Context, key string) (bool, error) {
	c.mu.Lock()
	g, ok := c.gets[key]
	c.mu.Unlock()
	if ok {
		return g.err == nil, nil
	}
	return c.Store.Exists(ctx, key)
}

func (c *readCache) Stat(ctx context.Context, key string) (Info, error) {
	c.mu.Lock()
	st, ok := c.stats[key]
	c.mu.Unlock()
	if ok {
		return st.info, st.err
	}
	info, err := c.Store.Stat(ctx, key)
	if cacheable(err) {
		c.mu.Lock()
		c.stats[key] = cachedStat{info: info, err: err}
		c.mu.Unlock()
	}
	return info, err
}

func (c *readCache) List(ctx context.Context, prefix string, opts ...ListOption) ([]Entry, error) {
	var o ListOptions
	for _, f := range opts {
		f(&o)
	}
	k := prefix
	if o.Recursive {
		k = "R\x00" + prefix
	}
	c.mu.Lock()
	l, ok := c.lists[k]
	c.mu.Unlock()
	if ok {
		return append([]Entry(nil), l.entries...), l.err
	}
	entries, err := c.Store.List(ctx, prefix, opts...)
	// ⚠️ A partial listing (entries AND an error) is not remembered: the next
	// caller deserves its own attempt.
	if err == nil {
		c.mu.Lock()
		c.lists[k] = cachedList{entries: append([]Entry(nil), entries...)}
		c.mu.Unlock()
	}
	return entries, err
}

func (c *readCache) Put(ctx context.Context, key string, data []byte, opts ...PutOption) error {
	err := c.Store.Put(ctx, key, data, opts...)
	c.invalidate(key)
	return err
}

func (c *readCache) Delete(ctx context.Context, key string, opts ...DeleteOption) error {
	err := c.Store.Delete(ctx, key, opts...)
	c.invalidate(key)
	return err
}

// invalidate drops what the cache knows about key, everything beneath it (a
// recursive delete), and every listing that could include it: one whose
// prefix is an ancestor of key, or lies beneath key.
func (c *readCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.gets {
		if k == key || strings.HasPrefix(k, key+"/") {
			delete(c.gets, k)
		}
	}
	for k := range c.stats {
		if k == key || strings.HasPrefix(k, key+"/") {
			delete(c.stats, k)
		}
	}
	for k := range c.lists {
		prefix := strings.TrimPrefix(k, "R\x00")
		p := strings.TrimSuffix(prefix, "/")
		if p == "" || key == p || strings.HasPrefix(key, p+"/") || strings.HasPrefix(p, key+"/") {
			delete(c.lists, k)
		}
	}
}
