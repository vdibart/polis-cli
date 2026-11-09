package attestation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// objectGrantSite is a site on a store with no filesystem, holding only a
// .well-known/polis that publishes a fresh key. Its directory does not exist,
// so anything still reading a path would find nothing.
func objectGrantSite(t *testing.T) (storage.Site, *storage.ObjectStore, []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	obj := storage.NewObjectStore("tenants/alice")
	wk := `{"public_key":` + quoteJSON(strings.TrimSpace(string(pub))) + `}`
	if err := obj.Put(context.Background(), ".well-known/polis", []byte(wk)); err != nil {
		t.Fatal(err)
	}
	return storage.On(filepath.Join(t.TempDir(), "no-such-dir"), obj), obj, priv
}

// ⭐ ROSIE'S GRANT CHECK ON A STORE WITH NO FILESYSTEM.
//
// The serving path asks "may Rosie act?" through ResolveLiveGrant. On an
// object-backed site a grant issued there must resolve as live, a withdrawal
// there must retract it, and the records and the index must land in the
// store, not on a disk the site does not have.
func TestAGrantIssuesResolvesAndWithdrawsOnAStoreWithNoFilesystem(t *testing.T) {
	ctx := context.Background()
	site, obj, priv := objectGrantSite(t)

	id, err := Issue(site, grantRecord("rosie/1", CustodyBasisUserSigned), priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if ok, _ := obj.Exists(ctx, Key(id)); !ok {
		t.Fatalf("the record is not in the store at %s", Key(id))
	}
	if ok, _ := obj.Exists(ctx, "content/pub.polis.core/index.jsonl"); !ok {
		t.Error("the index was not refreshed in the store")
	}
	if _, err := os.Stat(site.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something was written to the site's directory: %v", err)
	}

	g, err := ResolveLiveGrant(site, "rosie", "rosie", 1)
	if err != nil || !g.Valid() {
		t.Fatalf("the grant does not resolve as live: %v, %v", g, err)
	}

	if _, err := Withdraw(site, id, priv); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if g, _ := ResolveLiveGrant(site, "rosie", "rosie", 1); g != nil {
		t.Error("a withdrawn grant still resolves as live")
	}
	r, err := Load(site, id)
	if err != nil {
		t.Fatal(err)
	}
	if r.WithdrawnBy == "" {
		t.Error("the withdrawn record carries no forward reference")
	}
}

// failingList is an object store whose listings fail.
type failingList struct{ *storage.ObjectStore }

func (failingList) List(context.Context, string, ...storage.ListOption) ([]storage.Entry, error) {
	return nil, errors.New("listing refused")
}

// ⛔ AN ABSENT PREFIX AND A FAILED LISTING ARE DIFFERENT ANSWERS.
//
// A site that never issued a record has no attestation prefix: the answer is
// "nothing there", with no error. A listing that fails is "could not look",
// and it must surface as an error, because the resolver reads an empty result
// as "no grant" and Withdraw reads it as "nothing already withdrawn".
func TestScanTellsAnAbsentPrefixFromAFailedListing(t *testing.T) {
	site, obj, _ := objectGrantSite(t)

	records, malformed, err := Scan(site)
	if err != nil || len(records) != 0 || len(malformed) != 0 {
		t.Fatalf("a site with no records: %d records, %d malformed, err %v", len(records), len(malformed), err)
	}
	if grants, err := Grants(site, "rosie"); err != nil || grants != nil {
		t.Errorf("Grants on a site with none = %v, %v; absence is never a finding", grants, err)
	}

	failing := storage.On(site.Dir(), failingList{obj})
	if _, _, err := Scan(failing); err == nil {
		t.Error("a listing that failed was read as a site with no records")
	}
	if _, err := ResolveLiveGrant(failing, "rosie", "rosie", 1); err == nil {
		t.Error("the grant check reported no error when it could not look")
	}
}
