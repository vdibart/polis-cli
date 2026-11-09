package stream

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// A site whose registration marker lives only in its STORE, not on the local
// disk under its directory. A check that rebuilt the handle from the directory
// would find no marker and suppress the event.
func objectBackedRegisteredSite(t *testing.T, dsURL string) storage.Site {
	t.Helper()
	site := storage.On(t.TempDir(), storage.NewObjectStore(""))
	if err := discovery.WriteRegistrationMarker(site, dsURL, "alice.example", ""); err != nil {
		t.Fatalf("WriteRegistrationMarker: %v", err)
	}
	return site
}

func countingDS(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var posts atomic.Int32
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(ds.Close)
	return ds, &posts
}

func restoreGlobals(t *testing.T) {
	t.Helper()
	dsURL, base, dir, def := DiscoveryURL, BaseURL, DataDir, DefaultSite
	t.Cleanup(func() { DiscoveryURL, BaseURL, DataDir, DefaultSite = dsURL, base, dir, def })
}

// The per-call handle is what the registration check reads.
func TestPublishEventChecksTheConfiguredSiteHandle(t *testing.T) {
	restoreGlobals(t)
	ds, posts := countingDS(t)
	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	DataDir = t.TempDir() // unregistered: must not be the one consulted

	cfg := &DiscoveryConfig{DiscoveryURL: ds.URL, BaseURL: "https://alice.example", Site: objectBackedRegisteredSite(t, ds.URL)}
	if err := PublishEvent("pub.polis.test", map[string]interface{}{"k": "v"}, priv, cfg); err != nil {
		t.Fatalf("PublishEvent: %v", err)
	}
	if posts.Load() == 0 {
		t.Error("event was suppressed: the registration check did not read the configured site's store")
	}
}

// The process-wide handle wins over the process-wide directory.
func TestPublishEventPrefersTheDefaultSiteOverDataDir(t *testing.T) {
	restoreGlobals(t)
	ds, posts := countingDS(t)
	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	DiscoveryURL, BaseURL = ds.URL, "https://alice.example"
	DataDir = t.TempDir() // unregistered
	DefaultSite = objectBackedRegisteredSite(t, ds.URL)

	if err := PublishEvent("pub.polis.test", map[string]interface{}{"k": "v"}, priv); err != nil {
		t.Fatalf("PublishEvent: %v", err)
	}
	if posts.Load() == 0 {
		t.Error("event was suppressed: DefaultSite was not consulted before DataDir")
	}
}

// With neither handle set, the directory is still what is checked — the CLI's
// path, unchanged.
func TestPublishEventFallsBackToDataDir(t *testing.T) {
	restoreGlobals(t)
	ds, posts := countingDS(t)
	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	DiscoveryURL, BaseURL = ds.URL, "https://alice.example"
	DefaultSite = storage.Site{}
	DataDir = t.TempDir() // unregistered: the event is suppressed

	if err := PublishEvent("pub.polis.test", map[string]interface{}{"k": "v"}, priv); err != nil {
		t.Fatalf("PublishEvent: %v", err)
	}
	if posts.Load() != 0 {
		t.Error("an unregistered DataDir published an event")
	}
}
