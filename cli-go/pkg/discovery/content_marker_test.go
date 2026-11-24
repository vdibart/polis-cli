package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type contentMarkerFixture struct {
	PublicKey string `json:"public_key"`
	Cases     []struct {
		Name      string                 `json:"name"`
		Kind      string                 `json:"kind"`
		Canonical string                 `json:"canonical"`
		Request   map[string]interface{} `json:"request"`
	} `json:"cases"`
}

// ⭐ The Go client builds exactly the bytes the shared fixture pins, and the
// discovery service verifies the same fixture (content_marker.test.ts). The
// unmarked vectors are the pre-marker bytes and never move.
func TestContentRequestCanonicalBytesMatchTheSharedFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "discovery-service", "core", "contract-fixtures", "content-marker-signature.json"))
	if err != nil {
		// The public repository: the discovery service is closed source and
		// absent there, as is plans/. Anywhere else a missing fixture fails.
		if _, perr := os.Stat(filepath.Join("..", "..", "..", "plans")); os.IsNotExist(perr) && os.IsNotExist(err) {
			t.Skip("the discovery service's shared fixture is closed source and not in this tree")
		}
		t.Fatal(err)
	}
	var f contentMarkerFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) != 4 {
		t.Fatalf("fixture has %d cases, want 4", len(f.Cases))
	}
	for _, c := range f.Cases {
		r := c.Request
		str := func(k string) string { s, _ := r[k].(string); return s }
		m := AgentMarker{Agent: str("agent"), Grant: str("grant")}
		var got string
		switch c.Kind {
		case "register":
			meta, _ := r["metadata"].(map[string]interface{})
			b, err := MakeContentCanonicalJSONMarked(str("type"), str("url"), str("version"), str("author"), meta, m)
			if err != nil {
				t.Fatal(err)
			}
			got = string(b)
			if m.Agent == "" {
				plain, _ := MakeContentCanonicalJSON(str("type"), str("url"), str("version"), str("author"), meta)
				if string(plain) != got {
					t.Errorf("%s: the unmarked builder moved", c.Name)
				}
			}
		case "unpublish":
			got = MakeContentUnpublishCanonicalJSONMarked(str("type"), str("url"), str("timestamp"), m)
			if m.Agent == "" && MakeContentUnpublishCanonicalJSON(str("type"), str("url"), str("timestamp")) != got {
				t.Errorf("%s: the unmarked builder moved", c.Name)
			}
		}
		if got != c.Canonical {
			t.Errorf("%s:\n got: %s\nwant: %s", c.Name, got, c.Canonical)
		}
	}
}

// A half marker is never signed or sent.
func TestAHalfMarkerIsRefusedOnContentRequests(t *testing.T) {
	half := AgentMarker{Agent: "Claude"}
	if _, err := MakeContentCanonicalJSONMarked("pub.polis.post", "https://a.example/p.md", "v", "a.example", nil, half); err == nil {
		t.Error("a half marker was signed into a registration")
	}
	if MakeContentUnpublishCanonicalJSONMarked("pub.polis.post", "https://a.example/p.md", "t", half) != "" {
		t.Error("a half marker was signed into an unpublish")
	}
	if err := NewClient("http://unused.invalid").UnpublishContentMarked("pub.polis.post", "https://a.example/p.md", "t", "s", half); err == nil {
		t.Error("a half-marked unpublish was sent")
	}
}
