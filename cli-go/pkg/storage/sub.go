package storage

import (
	"context"
	"fmt"
	"strings"
)

// Sub returns a Store that addresses keys relative to prefix inside parent.
//
// This is what makes a hosted tenant and a standalone instance the same thing
// seen from different roots. A tenant on a multi-tenant host lives under
// tenants/<handle>/; an instance that owns its whole site lives at the root.
// Sub hands the first one a Store whose keys are its own site paths — exactly
// the keys the second one uses — so code written for either works for both,
// and an export is a copy in both directions rather than a translation.
//
// It is a decorator rather than "just re-root the filesystem path" on purpose:
// that shortcut only works for a backend that has paths, and the point is that
// the property holds for every backend.
func Sub(parent Store, prefix string) (Store, error) {
	p, err := CleanKey(prefix)
	if err != nil {
		return nil, err
	}
	if p == "" {
		return parent, nil
	}
	return &subStore{parent: parent, prefix: p}, nil
}

type subStore struct {
	parent Store
	prefix string
}

// resolve maps a key in the sub-store's space to the parent's space.
func (s *subStore) resolve(key string) (string, error) {
	k, err := CleanKey(key)
	if err != nil {
		return "", err
	}
	if k == "" {
		return s.prefix, nil
	}
	return s.prefix + "/" + k, nil
}

// unresolve maps a parent key back into the sub-store's space. Listing returns
// full keys, and a caller of the sub-store must never see the prefix: it does
// not know it exists, which is the whole point.
func (s *subStore) unresolve(key string) string {
	return strings.TrimPrefix(strings.TrimPrefix(key, s.prefix), "/")
}

func (s *subStore) Get(ctx context.Context, key string) ([]byte, error) {
	k, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	return s.parent.Get(ctx, k)
}

func (s *subStore) Put(ctx context.Context, key string, data []byte, opts ...PutOption) error {
	k, err := s.resolve(key)
	if err != nil {
		return err
	}
	return s.parent.Put(ctx, k, data, opts...)
}

func (s *subStore) Exists(ctx context.Context, key string) (bool, error) {
	k, err := s.resolve(key)
	if err != nil {
		return false, err
	}
	return s.parent.Exists(ctx, k)
}

func (s *subStore) Stat(ctx context.Context, key string) (Info, error) {
	k, err := s.resolve(key)
	if err != nil {
		return Info{}, err
	}
	return s.parent.Stat(ctx, k)
}

func (s *subStore) List(ctx context.Context, prefix string, opts ...ListOption) ([]Entry, error) {
	k, err := s.resolve(prefix)
	if err != nil {
		return nil, err
	}
	entries, err := s.parent.List(ctx, k, opts...)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		e.Key = s.unresolve(e.Key)
		out = append(out, e)
	}
	return out, nil
}

func (s *subStore) Delete(ctx context.Context, key string, opts ...DeleteOption) error {
	k, err := s.resolve(key)
	if err != nil {
		return err
	}
	// Refusing to delete the sub-store's own root mirrors POSIX refusing to
	// delete its root: a caller asking to remove "" means "everything I can
	// see", which is never what a content path meant.
	if k == s.prefix {
		return fmt.Errorf("%w: refusing to delete the root", ErrInvalidKey)
	}
	return s.parent.Delete(ctx, k, opts...)
}

func (s *subStore) RealPath(key string) (string, error) {
	k, err := s.resolve(key)
	if err != nil {
		return "", err
	}
	return s.parent.RealPath(k)
}

func (s *subStore) Capabilities() Capabilities { return s.parent.Capabilities() }

var _ Store = (*subStore)(nil)
