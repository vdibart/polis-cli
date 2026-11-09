package signing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

type guardShape struct {
	Name  string `json:"name"`
	Items []struct {
		URL string `json:"url"`
	} `json:"items"`
}

func TestGuardRewriteAllowsCreationAndAModelledFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	if err := GuardRewrite("x", path, guardShape{}); err != nil {
		t.Fatalf("a missing file is a creation: %v", err)
	}
	_ = os.WriteFile(path, []byte(`{"name":"a","items":[{"url":"u"}]}`), 0644)
	if err := GuardRewrite("x", path, guardShape{}); err != nil {
		t.Fatalf("a fully modelled file must be rewritable: %v", err)
	}
}

func TestGuardRewriteRefusesAndNamesEveryUnrecognisedMember(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	_ = os.WriteFile(path, []byte(`{"name":"a","future":1,"items":[{"url":"u","note":"n"}]}`), 0644)
	err := GuardRewrite("x file", path, guardShape{})
	var refusal *RewriteRefusedError
	if !errors.As(err, &refusal) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if strings.Join(refusal.Fields, ",") != "future,items[0].note" {
		t.Fatalf("fields = %v", refusal.Fields)
	}
	for _, want := range []string{"x file", "future", "items[0].note", "Upgrade polis", "polis site rewrite-unsigned " + path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must say %q: %v", want, err)
		}
	}
}

// GuardRewriteAt is the same guard reading through a site's store. These are
// the two GuardRewrite cases above, unchanged in substance, plus what differs:
// how the refusal names the file on a store with and without a filesystem.

func TestGuardRewriteAtAllowsCreationAndAModelledFile(t *testing.T) {
	site := storage.At(t.TempDir())
	if err := GuardRewriteAt("x", site, "content/x.json", guardShape{}); err != nil {
		t.Fatalf("a missing file is a creation: %v", err)
	}
	_ = site.Store().Put(context.Background(), "content/x.json", []byte(`{"name":"a","items":[{"url":"u"}]}`))
	if err := GuardRewriteAt("x", site, "content/x.json", guardShape{}); err != nil {
		t.Fatalf("a fully modelled file must be rewritable: %v", err)
	}
}

func TestGuardRewriteAtRefusesAndNamesEveryUnrecognisedMember(t *testing.T) {
	dir := t.TempDir()
	site := storage.At(dir)
	_ = site.Store().Put(context.Background(), "content/x.json", []byte(`{"name":"a","future":1,"items":[{"url":"u","note":"n"}]}`))
	err := GuardRewriteAt("x file", site, "content/x.json", guardShape{})
	var refusal *RewriteRefusedError
	if !errors.As(err, &refusal) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if strings.Join(refusal.Fields, ",") != "future,items[0].note" {
		t.Fatalf("fields = %v", refusal.Fields)
	}
	// ⭐ On a filesystem the refusal names the file exactly as GuardRewrite
	// did, so the command it suggests still works.
	path := filepath.Join(dir, "content", "x.json")
	for _, want := range []string{"x file", "future", "items[0].note", "Upgrade polis", "polis site rewrite-unsigned " + path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must say %q: %v", want, err)
		}
	}
	// And it agrees with GuardRewrite on the same bytes.
	if old := GuardRewrite("x file", path, guardShape{}); old == nil || old.Error() != err.Error() {
		t.Errorf("GuardRewriteAt and GuardRewrite disagree:\n  at:   %v\n  path: %v", err, old)
	}
}

func TestGuardRewriteAtReadsThroughAStoreWithNoFilesystem(t *testing.T) {
	site := storage.On("/nonexistent-site-root", storage.NewObjectStore(""))
	_ = site.Store().Put(context.Background(), "content/x.json", []byte(`{"name":"a","future":1}`))
	err := GuardRewriteAt("x file", site, "content/x.json", guardShape{})
	var refusal *RewriteRefusedError
	if !errors.As(err, &refusal) {
		t.Fatalf("the guard must read the stored bytes, not a path under the root; got %v", err)
	}
	if refusal.Path != "content/x.json" {
		t.Errorf("with no filesystem the refusal names the key, got %q", refusal.Path)
	}
}
