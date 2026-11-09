package storage

import (
	"context"
	"errors"
	"testing"
)

type callCounter struct {
	Store
	gets, lists, stats int
}

func (c *callCounter) Get(ctx context.Context, key string) ([]byte, error) {
	c.gets++
	return c.Store.Get(ctx, key)
}

func (c *callCounter) List(ctx context.Context, prefix string, opts ...ListOption) ([]Entry, error) {
	c.lists++
	return c.Store.List(ctx, prefix, opts...)
}

func (c *callCounter) Stat(ctx context.Context, key string) (Info, error) {
	c.stats++
	return c.Store.Stat(ctx, key)
}

// ⭐ A read cache reads each key once, remembers an absent key as absent, and
// lists each prefix once.
func TestAReadCacheReadsEachKeyOnce(t *testing.T) {
	ctx := context.Background()
	under := &callCounter{Store: NewObjectStore("")}
	_ = under.Store.Put(ctx, "a/x.html", []byte("X"))
	c := ReadCache(under)

	for i := 0; i < 5; i++ {
		if b, err := c.Get(ctx, "a/x.html"); err != nil || string(b) != "X" {
			t.Fatalf("Get = %q, %v", b, err)
		}
		if _, err := c.Get(ctx, "a/missing.md"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing Get err = %v; want ErrNotFound", err)
		}
		if ok, _ := c.Exists(ctx, "a/x.html"); !ok {
			t.Fatal("Exists = false for a key the cache read")
		}
		if _, err := c.List(ctx, "a", Recursive()); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Stat(ctx, "a/x.html"); err != nil {
			t.Fatal(err)
		}
	}
	if under.gets != 2 || under.lists != 1 || under.stats != 1 {
		t.Errorf("underlying calls: %d gets, %d lists, %d stats; want 2, 1, 1", under.gets, under.lists, under.stats)
	}
}

// ⛔ The operation always reads back what it wrote: a write or delete drops the
// key, what is beneath it, and every remembered listing.
func TestAReadCacheReadsBackItsOwnWrites(t *testing.T) {
	ctx := context.Background()
	c := ReadCache(NewObjectStore(""))
	if _, err := c.Get(ctx, "p/new.html"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	before, _ := c.List(ctx, "p", Recursive())
	if err := c.Put(ctx, "p/new.html", []byte("NEW")); err != nil {
		t.Fatal(err)
	}
	if b, err := c.Get(ctx, "p/new.html"); err != nil || string(b) != "NEW" {
		t.Errorf("after Put, Get = %q, %v; want NEW", b, err)
	}
	after, _ := c.List(ctx, "p", Recursive())
	if len(after) != len(before)+1 {
		t.Errorf("listing after Put has %d entries; want %d", len(after), len(before)+1)
	}
	if err := c.Delete(ctx, "p", DeleteRecursive()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "p/new.html"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after a recursive Delete, Get err = %v; want ErrNotFound", err)
	}
}

// ⭐ A returned slice is the caller's: changing it does not change what the
// next caller reads.
func TestAReadCacheHandsOutCopies(t *testing.T) {
	ctx := context.Background()
	under := NewObjectStore("")
	_ = under.Put(ctx, "k", []byte("abc"))
	c := ReadCache(under)
	b, _ := c.Get(ctx, "k")
	b[0] = 'Z'
	if b2, _ := c.Get(ctx, "k"); string(b2) != "abc" {
		t.Errorf("second Get = %q; a caller's edit leaked into the cache", b2)
	}
}

// ⭐ A write drops only the listings it could change, so a render that writes
// its pages does not list its templates again.
func TestAWriteKeepsListingsItCannotChange(t *testing.T) {
	ctx := context.Background()
	under := &callCounter{Store: NewObjectStore("")}
	_ = under.Store.Put(ctx, "themes/t/a.css", []byte("A"))
	c := ReadCache(under)
	_, _ = c.List(ctx, "themes", Recursive())
	_ = c.Put(ctx, "posts/new.html", []byte("N"))
	_, _ = c.List(ctx, "themes", Recursive())
	if under.lists != 1 {
		t.Errorf("listing an unrelated prefix after a write: %d underlying lists; want 1", under.lists)
	}
	_ = c.Put(ctx, "themes/t/b.css", []byte("B"))
	got, _ := c.List(ctx, "themes", Recursive())
	if under.lists != 2 || len(got) != 2 {
		t.Errorf("after a write beneath the prefix: %d lists, %d entries; want a fresh listing with 2", under.lists, len(got))
	}
}

// WithStore keeps the site's identity and swaps only the store.
func TestWithStoreKeepsTheSite(t *testing.T) {
	s := At(t.TempDir())
	c := s.WithStore(ReadCache(s.Store()))
	if c.ID() != s.ID() || !c.Valid() {
		t.Errorf("WithStore changed the site: ID %q vs %q, valid %v", c.ID(), s.ID(), c.Valid())
	}
}
