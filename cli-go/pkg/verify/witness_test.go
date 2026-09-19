package verify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
)

// SIGNET epic 32 — the witness axis is OPT-IN on pkg/verify.
//
// ⛔ The regression this guards: Rosie's blessed-comment cache and the webapp's
// sync call verify.VerifyContent for every comment they ingest. If that function
// evaluated witnesses, every ingest on the hosted fleet would fetch the site's
// witness file and a discovery-service key — an actor behaviour change no epic
// scoped. So VerifyContent must not touch the witness set; only
// VerifyContentWitnessed (used by `polis preview`) does.
func TestVerifyContentDoesNotTouchTheWitnessSetButTheWitnessedFormDoes(t *testing.T) {
	priv, pubBytes, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	pub := strings.TrimSpace(string(pubBytes))
	post := signPublishStyleAt(t, priv, "Hello, witness.\n", "2026-09-13T12:00:00Z")

	wk, _ := json.Marshal(map[string]string{
		"public_key": pub,
		"witnesses":  "/content/witness/witnesses.json",
	})
	// A witness for DIFFERENT bytes: it is read and evaluated, but it applies to
	// nothing here, so no discovery-service key is ever fetched and the test
	// makes no call outside the test server.
	set, _ := json.Marshal(map[string]interface{}{
		"witnesses": map[string][]discovery.Witness{
			"https://alice.polis.pub/posts/hello.md": {{
				Action: discovery.WitnessActionContent, Type: "pub.polis.post",
				URL: "https://alice.polis.pub/posts/hello.md", Version: "sha256:" + strings.Repeat("f", 64),
				ArtifactHash: "sha256:" + strings.Repeat("e", 64),
				DS:           "https://ds.polis.pub", DSKeyID: "ds-primary",
				WitnessedAt: "2026-09-13T12:00:01.000Z", Signature: "sig",
			}},
		},
	})

	var witnessFetches int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			w.Write(wk)
		case "/posts/hello.md":
			w.Write([]byte(post))
		case "/content/witness/witnesses.json":
			atomic.AddInt32(&witnessFetches, 1)
			w.Write(set)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	plain, err := VerifyContent(ts.URL + "/posts/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Signature.Status != "valid" {
		t.Fatalf("setup: the post must verify: %+v", plain.Signature)
	}
	if plain.Witness != nil {
		t.Fatalf("VerifyContent must not evaluate the witness axis: %+v", plain.Witness)
	}
	if n := atomic.LoadInt32(&witnessFetches); n != 0 {
		t.Fatalf("VerifyContent fetched the witness set %d time(s); the actors that call it must not", n)
	}

	witnessed, err := VerifyContentWitnessed(ts.URL+"/posts/hello.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if witnessed.Witness == nil || witnessed.Witness.State != sitecheck.WitnessUnwitnessed {
		t.Fatalf("the witnessed form must report the axis (a witness for other bytes leaves this post unwitnessed): %+v", witnessed.Witness)
	}
	if len(witnessed.Witness.Checks) != 1 || witnessed.Witness.Checks[0].Status != sitecheck.WitnessOtherBytes {
		t.Fatalf("the served record must be read and classified as other bytes: %+v", witnessed.Witness.Checks)
	}
	if n := atomic.LoadInt32(&witnessFetches); n != 1 {
		t.Fatalf("the witnessed form must read the published set once, got %d fetch(es)", n)
	}
	if witnessed.Signature.Status != plain.Signature.Status || witnessed.Hash.Status != plain.Hash.Status {
		t.Fatal("the witness axis must never change the signature or hash result")
	}
}
