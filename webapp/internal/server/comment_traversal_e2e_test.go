package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
)

// ⛔⛔ END TO END: A CRAFTED COMMENT URL IN THE FEED CANNOT READ A PRIVATE FILE.
// The security scan proved an unauthenticated GET of this exact PQL returned a
// tenant's draft text, through the comment-thread enrichment reading a local
// file for a foreign comment URL.
func TestATraversalCommentURLInTheFeedReadsNoPrivateFile(t *testing.T) {
	s := newConfiguredServer(t)
	ctx := context.Background()
	const secret = "TOPSECRET-draft-text"
	draft := ".polis/bundles/pub.polis.core/comments/drafts/secret.md"
	if err := s.Site().Store().Put(ctx, draft, []byte("---\ntitle: d\n---\n"+secret+"\n")); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	up := strings.Repeat("../", 3)
	items := []feed.FeedItem{
		{Type: "comment", Title: "c", URL: "https://evil.example/content/pub.polis.core/comment/" + up + draft,
			TargetURL: "https://a.pub/posts/x.html", Published: now, AuthorURL: "https://evil.example", AuthorDomain: "evil.example"},
		{Type: "comment", Title: "c2", URL: "https://evil.example/x.md?q=/comments/x/" + up + draft,
			TargetURL: "https://a.pub/posts/x.html", Published: now, AuthorURL: "https://evil.example", AuthorDomain: "evil.example"},
	}
	for _, scope := range []string{"default", "global"} {
		if _, err := feed.NewCacheManager(s.Site(), scope).MergeItems(items); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/pql/all+comments+from+all+polis?format=json", "/pql/all+comments?format=json"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		body, _ := io.ReadAll(w.Body)
		if strings.Contains(string(body), secret) {
			t.Errorf("⛔ GET %s returned the private draft's text", path)
		}
	}
}
