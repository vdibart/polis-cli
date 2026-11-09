package stream

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// An absent state or config file is "nothing yet", not an error, and the
// target is left alone. An unreadable one is an error: the store could not
// look, which is a different answer.
func TestLoadStateAndConfigTellAbsenceFromAFailedRead(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(storage.At(dir), "ds.polis.pub", "pub.polis.core")

	var st FollowerState
	if err := s.LoadState("pub.polis.follow", &st); err != nil {
		t.Fatalf("LoadState on an absent file: %v", err)
	}
	var cfg map[string]any
	if err := s.LoadConfig("feed", &cfg); err != nil {
		t.Fatalf("LoadConfig on an absent file: %v", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file; the failed-read half needs a non-root user")
	}
	if err := s.SaveState("pub.polis.follow", &FollowerState{Count: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveConfig("feed", map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{s.StateKey("pub.polis.follow"), s.configKey + "/feed.json"} {
		p := filepath.Join(dir, filepath.FromSlash(k))
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(p, 0o600) })
	}
	if err := s.LoadState("pub.polis.follow", &st); err == nil {
		t.Error("LoadState on an unreadable file returned nil; it could not look and must say so")
	}
	if err := s.LoadConfig("feed", &cfg); err == nil {
		t.Error("LoadConfig on an unreadable file returned nil; it could not look and must say so")
	}
}

// The writes create their directories through the site's store, which makes
// everything at or under .polis exactly 0700. A permissive umask must not
// expose the stream state.
func TestStoreDirectoriesArePrivate(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	s := NewStore(storage.At(dir), "ds.polis.pub", "pub.polis.core")
	if err := s.SaveConfig("feed", map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveState("pub.polis.follow", &FollowerState{Count: 1}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		".polis",
		".polis/ds",
		".polis/ds/ds.polis.pub",
		".polis/ds/ds.polis.pub/pub.polis.core",
		".polis/ds/ds.polis.pub/pub.polis.core/config",
		".polis/ds/ds.polis.pub/pub.polis.core/state",
	} {
		fi, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o700 {
			t.Errorf("%s mode = %o, want 700", rel, got)
		}
	}
}

// A legacy state file is moved to its current name on first load, on a
// filesystem by rename and on a store without one by write-then-delete.
func TestLoadStateMigratesALegacyName(t *testing.T) {
	for _, tc := range []struct {
		name string
		site func(t *testing.T) storage.Site
	}{
		{"posix", func(t *testing.T) storage.Site { return storage.At(t.TempDir()) }},
		{"object", func(t *testing.T) storage.Site {
			return storage.On("/nonexistent-site-root", storage.NewObjectStore(""))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := tc.site(t)
			s := NewStore(site, "ds.polis.pub", "pub.polis.core")
			ctx := context.Background()
			legacy := []byte(`{"followers":["alice.com"],"count":1}`)
			if err := site.Store().Put(ctx, s.StateKey("polis.follow"), legacy); err != nil {
				t.Fatal(err)
			}

			var st FollowerState
			if err := s.LoadState("pub.polis.follow", &st); err != nil {
				t.Fatalf("LoadState: %v", err)
			}
			if st.Count != 1 {
				t.Fatalf("migrated count = %d, want 1", st.Count)
			}
			if _, err := site.Store().Get(ctx, s.StateKey("pub.polis.follow")); err != nil {
				t.Errorf("current name not written: %v", err)
			}
			if _, err := site.Store().Get(ctx, s.StateKey("polis.follow")); err == nil {
				t.Error("legacy name still present after the migration")
			}
		})
	}
}
