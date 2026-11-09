package cmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// --json chooses an output format; it is not consent. An unpublish under
// --json without -y used to skip the prompt and go ahead, so the callers most
// likely to pass --json — scripts and agents — got the least protection.
func TestRunUnpublish_JSONWithoutYesRefusesAndRemovesNothing(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = true

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	postPath := createTestPost(t, dir, "20260201", "my-post", "My Post")

	err := RunUnpublish(dir, postPath, false)
	if err == nil {
		t.Fatal("RunUnpublish under --json without -y succeeded; want a refusal")
	}
	if !strings.Contains(err.Error(), "-y") {
		t.Errorf("refusal does not name -y: %v", err)
	}

	for _, rel := range []string{
		postPath,
		strings.TrimSuffix(postPath, ".md") + ".html",
		filepath.Join("content", "pub.polis.core", "post", "20260201", ".versions", "my-post.md"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("%s was touched by a refused unpublish: %v", rel, err)
		}
	}
	draft := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "my-post.md")
	if _, err := os.Stat(draft); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused unpublish wrote a draft: %v", err)
	}
	entries, err := metadata.LoadPublicIndex(storage.At(dir))
	if err != nil {
		t.Fatalf("load public index: %v", err)
	}
	found := false
	for _, e := range entries {
		found = found || e.Path == postPath
	}
	if !found {
		t.Error("a refused unpublish removed the index entry")
	}

	// -y under --json still unpublishes.
	if err := RunUnpublish(dir, postPath, true); err != nil {
		t.Fatalf("RunUnpublish under --json with -y: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, postPath)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("unpublish with -y under --json left the post in place")
	}
}

func TestRunUnpublishURL_JSONWithoutYesRefuses(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = true

	err := RunUnpublishURL(dir, "https://example.com/posts/20260201/x.md", "", false)
	if err == nil || !strings.Contains(err.Error(), "-y") {
		t.Fatalf("RunUnpublishURL under --json without -y = %v; want a refusal naming -y", err)
	}
}
