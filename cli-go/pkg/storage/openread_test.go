package storage

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"
)

func TestOpenReadOnBothKindsOfStore(t *testing.T) {
	for name, site := range map[string]Site{
		"filesystem":    At(t.TempDir()),
		"no filesystem": On("/nonexistent-site-root", NewObjectStore("")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := OpenRead(site, ".polis/x/state.jsonl"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("absent key: want fs.ErrNotExist, got %v", err)
			}
			if err := site.Store().Put(context.Background(), ".polis/x/state.jsonl", []byte("a\nb\n")); err != nil {
				t.Fatal(err)
			}
			rc, err := OpenRead(site, ".polis/x/state.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			got, _ := io.ReadAll(rc)
			if string(got) != "a\nb\n" {
				t.Errorf("read %q", got)
			}
		})
	}
}
