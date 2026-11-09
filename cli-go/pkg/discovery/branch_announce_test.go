package discovery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ⭐ The announcement reaches the DS carrying the handover verbatim and a
// request signature the site's key verifies over the canonical bytes, and the
// DS's witness comes back. A 4xx is a BranchRefusedError.
func TestAnnounceBranchSendsAVerifiableRequest(t *testing.T) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	h := BranchHandover{OldKey: "ssh-ed25519 K1", NewKey: "ssh-ed25519 K2", Timestamp: "2026-10-17T04:00:00Z", TransitionSig: "SIG"}
	var verified bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sites/branched" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Domain       string         `json:"domain"`
			SourceDomain string         `json:"source_domain"`
			Handover     BranchHandover `json:"handover"`
			Timestamp    string         `json:"timestamp"`
			Signature    string         `json:"signature"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Handover != h || body.Domain != "b.example" || body.SourceDomain != "a.example" {
			t.Errorf("body %+v", body)
		}
		canonical, _ := MakeSiteBranchedCanonicalJSON(body.Domain, body.SourceDomain, body.Handover.TransitionSig, body.Timestamp)
		verified, _ = signing.VerifySignature(canonical, pub, body.Signature)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "witness": map[string]string{"action": WitnessActionSiteBranched, "domain": "b.example"}})
	}))
	defer srv.Close()

	w, err := NewClient(srv.URL).AnnounceBranch("b.example", "a.example", h, priv)
	if err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("the request signature does not verify against the site's key")
	}
	if w == nil || w.Action != WitnessActionSiteBranched {
		t.Fatalf("witness %+v", w)
	}

	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Domain not registered: register the branch first"}`, http.StatusNotFound)
	}))
	defer refusing.Close()
	_, err = NewClient(refusing.URL).AnnounceBranch("b.example", "a.example", h, priv)
	if re, ok := err.(*BranchRefusedError); !ok || re.Status != 404 {
		t.Fatalf("err = %v; want a 404 BranchRefusedError", err)
	}
}
