package ops

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐ A comment an agent writes is sent to the discovery service marked: its
// registration, which is also its blessing request, carries the agent's
// marker inside the signed bytes (delegation.md §7), the same marker its
// frontmatter carries. The owner's own comment is sent unmarked.
func TestAnAgentsCommentIsSentMarked(t *testing.T) {
	marker := discovery.AgentMarker{Agent: "Claude Code", Grant: "https://bob.polis.pub/content/pub.polis.core/attestation/g.json"}
	for name, m := range map[string]discovery.AgentMarker{"agent": marker, "owner": {}} {
		t.Run(name, func(t *testing.T) {
			var got discovery.ContentRegisterRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &got)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"status":"created","relationship_status":"pending"}`))
			}))
			defer srv.Close()

			dir := t.TempDir()
			for _, d := range []string{".polis/bundles/pub.polis.core/comments/drafts", ".polis/bundles/pub.polis.core/comments/pending", ".polis/bundles/pub.polis.core/comments/denied", "content/pub.polis.core/comment"} {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			site := storage.At(dir)
			if err := discovery.WriteRegistrationMarker(site, srv.URL, "bob.polis.pub", ""); err != nil {
				t.Fatal(err)
			}
			priv, _, err := signing.GenerateKeypair()
			if err != nil {
				t.Fatal(err)
			}
			cs := CommentSite{Site: site, PrivateKey: priv, BaseURL: "https://bob.polis.pub", DiscoveryURL: srv.URL, Marker: m}
			_, sent, err := WriteComment(cs, &comment.CommentDraft{
				InReplyTo: "https://alice.polis.pub/posts/20260101/hello.md",
				Content:   "A reply.",
				Agent:     m.Agent,
				Grant:     m.Grant,
			})
			if err != nil || sent.Err != nil {
				t.Fatalf("write: %v / send: %v", err, sent.Err)
			}
			if got.Agent != m.Agent || got.Grant != m.Grant {
				t.Fatalf("registration marker = %q %q, want %q %q", got.Agent, got.Grant, m.Agent, m.Grant)
			}
			want, err := discovery.MakeContentCanonicalJSONMarked(got.Type, got.URL, got.Version, got.Author, got.Metadata, m)
			if err != nil {
				t.Fatal(err)
			}
			if sig, _ := signing.SignContent(want, priv); sig != got.Signature {
				t.Error("the registration's signature is not over its canonical bytes, marker included")
			}
		})
	}
}
