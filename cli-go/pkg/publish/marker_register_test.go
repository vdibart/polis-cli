package publish

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func capturingDS(t *testing.T, got *discovery.ContentRegisterRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"status":"created"}`))
	}))
}

// ⭐ The registration of a post an agent published carries the agent's marker,
// signed into the request (delegation.md §7), the same marker its frontmatter
// carries. ⛔ A plain RegisterPost (upkeep: Chaplain, repairs) carries none
// (delegation.md §5.4), whatever the file says.
func TestAnAgentsPostIsRegisteredMarkedAndUpkeepIsNot(t *testing.T) {
	marker := discovery.AgentMarker{Agent: "Claude Code", Grant: "https://alice.example/content/pub.polis.core/attestation/g.json"}
	for name, m := range map[string]discovery.AgentMarker{"agent": marker, "upkeep": {}} {
		t.Run(name, func(t *testing.T) {
			var got discovery.ContentRegisterRequest
			srv := capturingDS(t, &got)
			defer srv.Close()
			dataDir, priv := setupDataDir(t, srv.URL)
			result := writeWitnessTestPost(t, dataDir)
			cfg := &DiscoveryConfig{DiscoveryURL: srv.URL, BaseURL: "https://alice.example"}
			var err error
			if name == "agent" {
				err = registerPost(storage.At(dataDir), result, priv, cfg, m)
			} else {
				err = RegisterPost(storage.At(dataDir), result, priv, cfg)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Agent != m.Agent || got.Grant != m.Grant {
				t.Fatalf("request marker = %q %q, want %q %q", got.Agent, got.Grant, m.Agent, m.Grant)
			}
			want, err := discovery.MakeContentCanonicalJSONMarked(got.Type, got.URL, got.Version, got.Author, got.Metadata, m)
			if err != nil {
				t.Fatal(err)
			}
			if sig, _ := signing.SignContent(want, priv); sig != got.Signature {
				t.Error("the request's signature is not over its canonical bytes, marker included")
			}
		})
	}
}
