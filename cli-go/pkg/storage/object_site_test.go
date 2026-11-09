package storage

import (
	"context"
	"testing"
)

// ⭐⭐⭐ THE CLAIM THE DETACH WAS JUSTIFIED ON, tested directly: a site handle
// backed by an object store behaves like one backed by a filesystem, for the
// operations a site actually performs.
//
// ⚠️ The contract suite proves the METHODS agree. This proves the SITE-shaped
// use of them does: the keys a real site writes, the per-key lock both share,
// and the append the store provides because object stores have none.
func TestAnObjectBackedSiteBehavesLikeAPosixOne(t *testing.T) {
	ctx := context.Background()

	cases := map[string]Site{
		"posix": At(t.TempDir()),
		"blob":  On(t.TempDir(), NewObjectStore("tenants/alice")),
	}

	for name, site := range cases {
		t.Run(name, func(t *testing.T) {
			if !site.Valid() {
				t.Fatal("handle is not valid")
			}

			// the keys a real site writes
			for key, body := range map[string]string{
				".well-known/polis":                         `{"public_key":"ssh-ed25519 AAAA"}`,
				"content/pub.polis.core/post/20260101/a.md": "---\ntitle: A\n---\n\nbody\n",
				"content/pub.polis.core/index.jsonl":        "",
				".polis/keys/id_ed25519":                    "PRIVATE",
			} {
				if err := site.Store().Put(ctx, key, []byte(body)); err != nil {
					t.Fatalf("Put(%s): %v", key, err)
				}
			}

			// ⭐ the store's append, which an object store cannot do natively
			for i := 0; i < 3; i++ {
				if err := AppendLine(ctx, site.Store(), "content/pub.polis.core/index.jsonl",
					[]byte(`{"n":1}`)); err != nil {
					t.Fatalf("AppendLine: %v", err)
				}
			}
			got, err := site.Store().Get(ctx, "content/pub.polis.core/index.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			if want := "{\"n\":1}\n{\"n\":1}\n{\"n\":1}\n"; string(got) != want {
				t.Errorf("index after 3 appends = %q, want %q", got, want)
			}

			// ⭐ the shallow listing a render walk uses
			entries, err := site.Store().List(ctx, "content/pub.polis.core")
			if err != nil {
				t.Fatal(err)
			}
			var dirs, files int
			for _, e := range entries {
				if e.IsDir {
					dirs++
				} else {
					files++
				}
			}
			if dirs != 1 || files != 1 {
				t.Errorf("List(content/pub.polis.core) = %d dirs, %d files; want 1 and 1 "+
					"(the post/ prefix and index.jsonl)", dirs, files)
			}

			// ⚠️ .polis holds a private key on both backends. On POSIX the
			// directory is 0700; on blob there are no modes, and the property is
			// carried by nothing being served from .polis. ⛔ So this asserts
			// only what both can promise: the key is readable BY THE SITE.
			if _, err := site.Store().Get(ctx, ".polis/keys/id_ed25519"); err != nil {
				t.Errorf("the site cannot read its own key: %v", err)
			}
		})
	}
}

// ⛔ The per-key lock must work on an object store too, because its whole
// purpose is to make read-modify-write safe where there is no append.
func TestConcurrentAppendsOnAnObjectStoreLoseNothing(t *testing.T) {
	ctx := context.Background()
	s := NewObjectStore("")
	const n = 40
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			_ = AppendLine(ctx, s, "index.jsonl", []byte("line"))
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
	got, err := s.Get(ctx, "index.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, c := range got {
		if c == '\n' {
			lines++
		}
	}
	if lines != n {
		t.Errorf("%d lines survived of %d — read-modify-write is interleaving on an "+
			"object store, which is exactly the case the per-key lock exists for", lines, n)
	}
}

// ⭐ An object store's listing carries each key's size and modification time,
// so a caller diffing a tree against a record of it reads nothing unchanged.
func TestAnObjectListingCarriesSizeAndModTime(t *testing.T) {
	ctx := context.Background()
	o := NewObjectStore("tenant")
	if err := o.Put(ctx, "posts/a.md", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	sub, err := Sub(o, "posts")
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Store{"object": o, "sub": sub} {
		entries, err := s.List(ctx, "", Recursive())
		if err != nil || len(entries) != 1 {
			t.Fatalf("%s: %v %v", name, entries, err)
		}
		info, _ := s.Stat(ctx, entries[0].Key)
		if entries[0].Size != 5 || !entries[0].ModTime.Equal(info.ModTime) || entries[0].ModTime.IsZero() {
			t.Errorf("%s: entry %+v; Stat says %+v", name, entries[0], info)
		}
	}
}
