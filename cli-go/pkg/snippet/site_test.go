package snippet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func objectSite(t *testing.T, st storage.Store) storage.Site {
	t.Helper()
	return storage.On(filepath.Join(t.TempDir(), "no-such-dir"), st)
}

// ⭐ THE SNIPPET EDITOR ON A STORE WITH NO FILESYSTEM.
//
// Create, list, read, overwrite and delete a global snippet on an
// object-backed site whose directory does not exist. Every step must go
// through the store: anything still building a path would fail or write to a
// disk the site does not have.
func TestSnippetsWorkOnAStoreWithNoFilesystem(t *testing.T) {
	ctx := context.Background()
	obj := storage.NewObjectStore("tenants/alice")
	site := objectSite(t, obj)

	if err := CreateSnippet(site, "about.html", "<p>hi</p>"); err != nil {
		t.Fatalf("CreateSnippet: %v", err)
	}
	if err := CreateSnippet(site, "about.html", "again"); err == nil {
		t.Error("a second create of the same snippet succeeded")
	}

	tree, err := ListSnippets(site, "", "zane", "", "global")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Entries) != 1 || tree.Entries[0].Path != "about.html" || tree.Entries[0].Type != "global" {
		t.Errorf("entries = %+v, want the one global snippet", tree.Entries)
	}

	if err := WriteSnippet(site, "", "zane", "about", "<p>edited</p>", "global"); err != nil {
		t.Fatalf("WriteSnippet: %v", err)
	}
	got, err := ReadSnippet(site, "", "zane", "about.html", "global")
	if err != nil || got.Content != "<p>edited</p>" {
		t.Fatalf("ReadSnippet = %+v, %v; want the edit", got, err)
	}
	if ok, _ := obj.Exists(ctx, "site/snippets/about.html.tmp"); ok {
		t.Error("a temp key was left behind on a store whose Put is already atomic")
	}

	if err := DeleteSnippet(site, "about.html"); err != nil {
		t.Fatalf("DeleteSnippet: %v", err)
	}
	if err := DeleteSnippet(site, "about.html"); err == nil {
		t.Error("deleting an absent snippet succeeded")
	}
	if _, err := os.Stat(site.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something was written to the site's directory: %v", err)
	}
}

// failingList is an object store whose listings fail.
type failingList struct{ *storage.ObjectStore }

func (failingList) List(context.Context, string, ...storage.ListOption) ([]storage.Entry, error) {
	return nil, errors.New("listing refused")
}

// ⛔ ONLY A GENUINE ABSENCE FALLS BACK TO THE CLI THEME.
//
// A site with no local theme lists the CLI copy. A site whose local theme
// could not be looked at is not replaced by the CLI's copy: "could not look"
// is not "nothing there".
func TestThemeFallbackNeedsAGenuineAbsence(t *testing.T) {
	cli := t.TempDir()
	writeFile(t, filepath.Join(cli, "zane", "snippets", "footer.html"), "cli footer")

	empty := objectSite(t, storage.NewObjectStore("tenants/empty"))
	tree, err := ListSnippets(empty, cli, "zane", "", "theme")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Entries) != 1 || tree.Entries[0].Name != "footer.html" {
		t.Errorf("a site with no local theme listed %+v, want the CLI footer", tree.Entries)
	}

	failing := objectSite(t, failingList{storage.NewObjectStore("tenants/broken")})
	tree, err = ListSnippets(failing, cli, "zane", "", "theme")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Entries) != 0 {
		t.Errorf("a local theme that could not be read was replaced by the CLI copy: %+v", tree.Entries)
	}
}
