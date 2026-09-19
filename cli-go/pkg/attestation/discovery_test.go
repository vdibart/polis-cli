package attestation

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// TestRegisterIsTheConsumerOfCurrentVersion is the assertion D2 rests on.
//
// The follow file dropped its version hash because nothing read it. This one
// keeps it because the DS does — so the test that matters is not "the field
// exists" but "the field arrives at the DS, in the payload, as the version of
// the exact bytes that were signed". A future refactor that quietly stopped
// sending it would leave `current_version` in the same unconsumed state epic 02
// deleted.
func TestRegisterIsTheConsumerOfCurrentVersion(t *testing.T) {
	priv, pub := newTestKeys(t)
	site := t.TempDir()

	var captured map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/content" {
			t.Errorf("registered against %q, want /v1/content", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("unmarshal payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := discovery.WriteRegistrationMarker(site, srv.URL, "vdibart.polis.pub", ""); err != nil {
		t.Fatalf("WriteRegistrationMarker: %v", err)
	}

	rec := sampleRecord()
	id, err := Issue(site, rec, priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	cfg := &DiscoveryConfig{
		DiscoveryURL: srv.URL,
		BaseURL:      "https://vdibart.polis.pub",
		DataDir:      site,
	}
	if err := Register(rec, id, priv, cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if captured == nil {
		t.Fatal("the DS was never called")
	}

	if got := captured["version"]; got != rec.Version {
		t.Errorf("registered version = %v, want %q — the DS cannot tell when the row changed",
			got, rec.Version)
	}
	if got := captured["type"]; got != TypeName {
		t.Errorf("type = %v, want %q", got, TypeName)
	}
	if got := captured["url"]; got != RecordURL("https://vdibart.polis.pub", id) {
		t.Errorf("url = %v, want the record's content path", got)
	}
	if got := captured["author"]; got != "vdibart.polis.pub" {
		t.Errorf("author = %v, want the tenant domain", got)
	}

	meta, _ := captured["metadata"].(map[string]interface{})
	if meta["predicate"] != PredicateSameAs {
		t.Errorf("metadata.predicate = %v", meta["predicate"])
	}
	if meta["subject"] != "https://vincent.example.com" {
		t.Errorf("metadata.subject = %v", meta["subject"])
	}
	if meta["subject_type"] != SubjectIdentity {
		t.Errorf("metadata.subject_type = %v", meta["subject_type"])
	}
	if _, ok := meta["subject_version"]; ok {
		t.Error("an unpinned subject must omit subject_version, not send it empty")
	}

	// The registration signature must verify over the canonical payload the DS
	// rebuilds. If these bytes diverge, the DS rejects every attestation and the
	// failure looks like a network problem.
	canonical, err := discovery.MakeContentCanonicalJSON(
		TypeName, captured["url"].(string), rec.Version, "vdibart.polis.pub", meta,
	)
	if err != nil {
		t.Fatalf("MakeContentCanonicalJSON: %v", err)
	}
	ok, err := signing.VerifySignature(canonical, pub, captured["signature"].(string))
	if err != nil || !ok {
		t.Errorf("the registration signature does not verify over the canonical payload: %v", err)
	}
}

// TestRegisterSendsThePinWhenThereIsOne — a correction's whole value is the
// pin, so the DS row has to carry it or a consumer querying the DS cannot tell
// a pinned claim from an unpinned one without fetching every record.
func TestRegisterSendsThePinWhenThereIsOne(t *testing.T) {
	priv, _ := newTestKeys(t)
	site := t.TempDir()

	var captured map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := discovery.WriteRegistrationMarker(site, srv.URL, "vdibart.polis.pub", ""); err != nil {
		t.Fatalf("WriteRegistrationMarker: %v", err)
	}

	pin := "sha256:" + "9f2a" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab"
	rec := sampleRecord()
	rec.Predicate = PredicateCorrection
	rec.Subject = Subject{Type: SubjectURI, ID: "https://site.example/posts/x.md", Version: pin}

	id, err := Issue(site, rec, priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cfg := &DiscoveryConfig{DiscoveryURL: srv.URL, BaseURL: "https://vdibart.polis.pub", DataDir: site}
	if err := Register(rec, id, priv, cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}

	meta, _ := captured["metadata"].(map[string]interface{})
	if meta["subject_version"] != pin {
		t.Errorf("metadata.subject_version = %v, want %q", meta["subject_version"], pin)
	}
}

// TestRegisterIsSilentWhenNotRegisteredLocally — a site that never registered
// with a DS must not attempt to, and must not treat that as an error. Same
// behaviour pub.polis.tag has.
func TestRegisterIsSilentWhenNotRegisteredLocally(t *testing.T) {
	priv, _ := newTestKeys(t)
	site := t.TempDir()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec := sampleRecord()
	id, err := Issue(site, rec, priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cfg := &DiscoveryConfig{DiscoveryURL: srv.URL, BaseURL: "https://vdibart.polis.pub", DataDir: site}
	if err := Register(rec, id, priv, cfg); err != nil {
		t.Errorf("an unregistered site is not an error: %v", err)
	}
	if called {
		t.Error("a site with no registration marker must not call the DS")
	}
}

// TestRegisterWithNoDiscoveryConfigured — no DS URL, no base URL, no error.
func TestRegisterWithNoDiscoveryConfigured(t *testing.T) {
	priv, _ := newTestKeys(t)
	site := t.TempDir()

	rec := sampleRecord()
	id, err := Issue(site, rec, priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := Register(rec, id, priv, &DiscoveryConfig{DataDir: site}); err != nil {
		t.Errorf("an unconfigured site is not an error: %v", err)
	}
}
