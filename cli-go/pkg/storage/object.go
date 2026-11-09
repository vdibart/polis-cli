package storage

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// ObjectStore is a Store with OBJECT-STORE semantics: a flat keyspace, no
// directories, no file modes, no filesystem path.
//
// ⭐⭐⭐ IT EXISTS TO PROVE THE SEAM AGAINST A NON-POSIX BACKEND TODAY. The
// detach was justified on the claim that a second backend drops in without
// touching callers, and until now there was only one implementation, so the
// claim was untested. This is the second: it models exactly what Vercel Blob
// and S3 do, it declares `Modes: false`, `ModTimes: true`, `RealPath: false`,
// and it passes the same contract suite the POSIX store does.
//
// ⚠️ IT IS NOT A MOCK. The semantics it implements are the real constraints of
// a prefix store, and they are the ones that bite:
//
//   - ⛔ A DELIBERATELY EMPTY DIRECTORY CANNOT BE EXPRESSED. `Put` of a key
//     creates no parent objects, so listing a prefix with no keys under it
//     yields nothing — there is no object meaning "this folder exists and is
//     empty". See EmptyPrefixPolicy for what this package does about it.
//   - ⛔ NO RENAME, and none is needed: a single-object PUT is already atomic,
//     which is why `Rename` is deliberately absent from the Store interface.
//   - ⛔ NO MODES. A backend that cannot carry modes needs a different answer
//     to "`.polis` must not be world-readable" than a shrug; see the comment on
//     Capabilities below.
//
// ⭐ The Vercel Blob store is this plus HTTP: same key mapping, same
// capabilities, same empty-prefix answer. Keeping the semantics here means they
// are testable without a network or a token, and the HTTP layer has nothing to
// decide.
type ObjectStore struct {
	mu      sync.RWMutex
	objects map[string]objectEntry
	prefix  string
}

type objectEntry struct {
	data     []byte
	modified time.Time
}

// NewObjectStore returns an in-process object store.
//
// ⭐ `prefix` namespaces a tenant inside one bucket, the way a hosted
// deployment would. An empty prefix is the whole keyspace.
func NewObjectStore(prefix string) *ObjectStore {
	return &ObjectStore{objects: map[string]objectEntry{}, prefix: cleanPrefix(prefix)}
}

func cleanPrefix(p string) string {
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "." {
		return ""
	}
	return p
}

// objectKey maps a store key to its full object name, refusing a key that
// escapes the store.
//
// ⛔ THE REFUSAL IS THE SECURITY BOUNDARY, and on a shared bucket it is the
// only one. A POSIX store is also bounded by the filesystem; an object store
// has nothing but this check between one tenant's prefix and another's.
func (o *ObjectStore) objectKey(key string) (string, error) {
	// ⛔ THE SAME RULES AS EVERY OTHER BACKEND: CleanKey. This used to apply its
	// own, which silently turned an absolute path into a key
	// ("/home/me/site/a.md" → "home/me/site/a.md", exactly what CleanKey
	// refuses) and accepted NUL and whitespace.
	cleaned, err := CleanKey(key)
	if err != nil {
		return "", err
	}
	if cleaned == "" {
		if o.prefix == "" {
			return "", nil
		}
		return o.prefix, nil
	}
	if o.prefix == "" {
		return cleaned, nil
	}
	return o.prefix + "/" + cleaned, nil
}

func (o *ObjectStore) Get(ctx context.Context, key string) ([]byte, error) {
	ok, err := o.objectKey(key)
	if err != nil {
		return nil, err
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	e, present := o.objects[ok]
	if !present {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	out := make([]byte, len(e.data))
	copy(out, e.data)
	return out, nil
}

func (o *ObjectStore) Put(ctx context.Context, key string, data []byte, opts ...PutOption) error {
	ok, err := o.objectKey(key)
	if err != nil {
		return err
	}
	if ok == "" || ok == o.prefix {
		return fmt.Errorf("storage: cannot write the store root as an object")
	}
	// ⚠️ WithMode is accepted and IGNORED, because Capabilities().Modes is
	// false. ⛔ Silently ignoring it is correct here and only because the
	// capability says so — a caller that needs modes must check, which is what
	// the contract's realpath/modes checks enforce.
	stored := make([]byte, len(data))
	copy(stored, data)
	o.mu.Lock()
	defer o.mu.Unlock()
	if resolvePut(opts).IfAbsent {
		if _, present := o.objects[ok]; present {
			return ErrExists
		}
	}
	o.objects[ok] = objectEntry{data: stored, modified: time.Now().UTC()}
	return nil
}

func (o *ObjectStore) Exists(ctx context.Context, key string) (bool, error) {
	ok, err := o.objectKey(key)
	if err != nil {
		return false, err
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	_, present := o.objects[ok]
	return present, nil
}

func (o *ObjectStore) Stat(ctx context.Context, key string) (Info, error) {
	ok, err := o.objectKey(key)
	if err != nil {
		return Info{}, err
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	e, present := o.objects[ok]
	if !present {
		return Info{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	// ⚠️ Mode is left zero rather than invented. Capabilities().Modes is
	// false, so a caller reading it is a caller that skipped the check.
	return Info{Size: int64(len(e.data)), ModTime: e.modified}, nil
}

func (o *ObjectStore) List(ctx context.Context, prefix string, opts ...ListOption) ([]Entry, error) {
	res := resolveList(opts)
	base, err := o.objectKey(prefix)
	if err != nil {
		return nil, err
	}
	scan := base
	if scan != "" {
		scan += "/"
	}

	o.mu.RLock()
	defer o.mu.RUnlock()

	seenDir := map[string]bool{}
	var out []Entry
	for k := range o.objects {
		if scan != "" && !strings.HasPrefix(k, scan) {
			continue
		}
		rel := strings.TrimPrefix(k, scan)
		if rel == "" {
			continue
		}
		storeKey := k
		if o.prefix != "" {
			storeKey = strings.TrimPrefix(strings.TrimPrefix(k, o.prefix), "/")
		}
		if res.Recursive {
			e := o.objects[k]
			out = append(out, Entry{Key: storeKey, Size: int64(len(e.data)), ModTime: e.modified})
			continue
		}
		// ⭐ Shallow: a key with a separator below this prefix is reported as a
		// DIRECTORY entry, synthesised from the key name. That is what every
		// object store's "delimiter" listing does, and it is the only sense in
		// which an object store has directories at all.
		if i := strings.Index(rel, "/"); i >= 0 {
			dirKey := storeKey[:len(storeKey)-len(rel)+i]
			if !seenDir[dirKey] {
				seenDir[dirKey] = true
				out = append(out, Entry{Key: dirKey, IsDir: true})
			}
			continue
		}
		e := o.objects[k]
		out = append(out, Entry{Key: storeKey, Size: int64(len(e.data)), ModTime: e.modified})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (o *ObjectStore) Delete(ctx context.Context, key string, opts ...DeleteOption) error {
	res := resolveDelete(opts)
	ok, err := o.objectKey(key)
	if err != nil {
		return err
	}
	// ⛔ Never the whole store: a recursive delete of the root would wipe
	// .polis/keys. POSIX refuses it, and so does every backend.
	if ok == "" || ok == o.prefix {
		return fmt.Errorf("%w: refusing to delete the store root", ErrInvalidKey)
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	if res.Recursive {
		scan := ok
		if scan != "" {
			scan += "/"
		}
		removed := 0
		for k := range o.objects {
			if scan == "" || strings.HasPrefix(k, scan) {
				delete(o.objects, k)
				removed++
			}
		}
		if _, present := o.objects[ok]; present {
			delete(o.objects, ok)
			removed++
		}
		if removed == 0 && !res.IgnoreMissing {
			return fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return nil
	}

	if _, present := o.objects[ok]; !present {
		if res.IgnoreMissing {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	delete(o.objects, ok)
	return nil
}

// RealPath always refuses.
//
// ⛔ IT MUST RETURN ErrUnsupported RATHER THAN A PLAUSIBLE STRING. A caller
// that ignores Capabilities().RealPath and uses the result would otherwise get
// a path that does not exist, and find out at the open() rather than here.
// ✅ Verified 2026-10-04: nothing outside pkg/storage calls RealPath except the
// archive path, which keeps a POSIX store and declares it.
func (o *ObjectStore) RealPath(key string) (string, error) {
	return "", fmt.Errorf("%w: an object store has no filesystem path (key %q)", ErrUnsupported, key)
}

// Capabilities declares what this backend genuinely does.
//
// ⛔⛔ Modes IS FALSE, AND THAT IS THE LOAD-BEARING ANSWER TO "WHAT
// BECOMES OF FILE MODES?" On POSIX, "`.polis` must not be world-readable" is enforced by mode
// 0700 on the directory. An object store has no modes and no directories, so
// that mechanism does not merely degrade — it does not exist.
//
// ⭐ The answer is that the property is enforced by a DIFFERENT MECHANISM on a
// blob backend, not by a weaker version of the same one: nothing under `.polis`
// is ever served. On POSIX that is belt and braces — the serve handler blocks
// dot-paths AND the directory is 0700. On blob the handler is the whole of it,
// which is why the handler, not the mode, is the invariant worth testing.
//
// ⚠️ ModTimes is true because the store records one. A real blob backend
// returns the upload timestamp, which is equally meaningful for Patrol's
// "unchanged since last sweep" check.
func (o *ObjectStore) Capabilities() Capabilities {
	return Capabilities{Modes: false, ModTimes: true, RealPath: false}
}

// EmptyPrefixPolicy is this package's answer to the first question an object backend raises: what
// becomes of a deliberately empty directory.
//
// ⭐⭐⭐ THE ANSWER IS THAT A SITE DOES NOT NEED THEM, AND NOTHING SHOULD START
// RELYING ON THEM. `pkg/clone` creates `content/pub.polis.core/comment` and
// `.../follow` so an empty site's tree LOOKS like a populated one. That is
// cosmetic: no reader of a site fetches a directory, because nothing is served
// from one — a consumer walks `index.jsonl` and the pointers in
// `.well-known/polis`.
//
// ⛔ SO AN EXPORT THAT ROUND-TRIPS THROUGH BLOB LOSES THEM, AND THAT IS
// ACCEPTED RATHER THAN HIDDEN. The alternative — writing a marker object per
// empty prefix — invents a file that a POSIX site does not have, which breaks
// the byte-identical export comparison in the other direction and leaves litter
// a user would ask about.
//
// ⚠️ WHAT THIS COSTS: a `polis validate` or a human `ls` on a blob-backed site
// sees fewer directories than on a POSIX one. ⭐ The export assertion must
// therefore compare FILES, not the directory shape, and say so.
const EmptyPrefixPolicy = "not represented: an object store has no empty prefixes, and no reader of a site fetches a directory"
