package attestation

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// Close-out P8: a record that was written without touching the DS (`polis
// actor register`'s disclosures) had no way to be announced — Register is only
// reached at issue time. RegisterIssued announces it, and refuses loudly where
// Register is silent.
func TestRegisterIssuedAnnouncesAnAlreadySignedRecord(t *testing.T) {
	priv, pub := newTestKeys(t)
	site := t.TempDir()
	writeWellKnown(t, site, pub)

	calls := 0
	var captured map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &captured)
	}))
	defer srv.Close()
	cfg := &DiscoveryConfig{DiscoveryURL: srv.URL, BaseURL: "https://vdibart.polis.pub"}

	rec := sampleRecord()
	id, err := Issue(site, rec, priv) // written, never announced
	if err != nil {
		t.Fatal(err)
	}

	// Not registered with the DS: refused by name, nothing sent.
	if _, err := RegisterIssued(site, id, priv, cfg); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unregistered site: err = %v", err)
	}
	if calls != 0 {
		t.Fatal("the DS was called for an unregistered site")
	}

	if err := discovery.WriteRegistrationMarker(site, srv.URL, "vdibart.polis.pub", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterIssued(site, id, priv, cfg); err != nil {
		t.Fatalf("RegisterIssued: %v", err)
	}
	if calls != 1 || captured["url"] != RecordURL(cfg.BaseURL, id) || captured["version"] != rec.Version {
		t.Fatalf("calls=%d payload=%v", calls, captured)
	}

	// Someone else's record, and a record that does not verify: refused.
	if _, err := RegisterIssued(site, id, priv, &DiscoveryConfig{DiscoveryURL: srv.URL, BaseURL: "https://other.example"}); err == nil || !strings.Contains(err.Error(), "only its issuer") {
		t.Fatalf("foreign issuer: err = %v", err)
	}
	tampered, _ := Load(Path(site, id))
	tampered.Payload = map[string]string{"added": "later"}
	if err := writeRecord(Path(site, id), tampered); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterIssued(site, id, priv, cfg); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("tampered record: err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("the DS was called %d times, want 1", calls)
	}
}
