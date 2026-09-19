package blessing

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// fakeBlessingDS serves one pending request and records the relationship
// update it receives.
func fakeBlessingDS(t *testing.T, got *discovery.RelationshipUpdateRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(discovery.RelationshipQueryResponse{
				Count: 1,
				Records: []discovery.RelationshipRecord{{
					ID:        json.Number("1"),
					Type:      "pub.polis.comment.blessing",
					SourceURL: "https://alice.com/comments/20260127/reply.md",
					TargetURL: "https://bob.com/posts/20260127/hello.md",
					Actor:     "alice.com",
					Status:    "pending",
					Metadata:  map[string]interface{}{"comment_version": "sha256:abc123"},
				}},
			})
		case http.MethodPost:
			json.NewDecoder(r.Body).Decode(got)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		}
	}))
}

// `polis blessing grant <version>` used to send the DS a grant with EMPTY
// source_url and target_url — it only ever had the version — which the DS
// rejects. The pending request carries both, so the version is resolved
// against it first.
func TestGrantPending_ResolvesURLsFromTheVersion(t *testing.T) {
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, metadata.BundleContentDir, "comment"), 0755)
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	var got discovery.RelationshipUpdateRequest
	ts := fakeBlessingDS(t, &got)
	defer ts.Close()
	client := &discovery.Client{BaseURL: ts.URL, HTTPClient: ts.Client()}

	res, err := GrantPending(siteDir, "bob.com", "sha256:abc123", client, nil, privPEM)
	if err != nil {
		t.Fatalf("GrantPending: %v", err)
	}
	if got.SourceURL != "https://alice.com/comments/20260127/reply.md" {
		t.Errorf("DS got source_url %q, want the pending request's comment URL", got.SourceURL)
	}
	if got.TargetURL != "https://bob.com/posts/20260127/hello.md" {
		t.Errorf("DS got target_url %q, want the pending request's in-reply-to", got.TargetURL)
	}
	if res.CommentURL != got.SourceURL {
		t.Errorf("result CommentURL = %q", res.CommentURL)
	}
}

func TestResolvePendingRequest_ByVersionOrURL(t *testing.T) {
	var got discovery.RelationshipUpdateRequest
	ts := fakeBlessingDS(t, &got)
	defer ts.Close()
	client := &discovery.Client{BaseURL: ts.URL, HTTPClient: ts.Client()}

	for _, ref := range []string{"sha256:abc123", "https://alice.com/comments/20260127/reply.md"} {
		req, err := ResolvePendingRequest(client, "bob.com", ref)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if req.CommentVersion != "sha256:abc123" || req.InReplyTo == "" || req.CommentURL == "" {
			t.Errorf("%s: resolved %+v", ref, req)
		}
	}

	if _, err := ResolvePendingRequest(client, "bob.com", "sha256:nope"); !errors.Is(err, ErrNoPendingRequest) {
		t.Errorf("unknown version: err = %v, want ErrNoPendingRequest", err)
	}
	if got.Action != "" {
		t.Error("resolving must not update the DS")
	}
}
