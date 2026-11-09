package render

import (
	"fmt"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ The reply-context cache is bounded per site: an anonymous caller adding an
// entry per distinct URL cannot grow a tenant's .polis file without limit. The
// oldest entries go first, and the newest always survives.
func TestTheReplyContextCacheIsBounded(t *testing.T) {
	old := maxReplyContextEntries
	maxReplyContextEntries = 10
	defer func() { maxReplyContextEntries = old }()

	site := storage.At(t.TempDir())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cache := replyContextCache{"legacy": {Title: "no timestamp"}}
	for i := 0; i < 25; i++ {
		cache[fmt.Sprintf("https://a.example/%02d", i)] = replyContextEntry{
			FetchedAt: base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)}
	}
	saveReplyContextCache(site, cache)
	got := loadReplyContextCache(site)
	if len(got) != 10 {
		t.Fatalf("%d entries on disk, want 10", len(got))
	}
	if _, ok := got["legacy"]; ok {
		t.Error("an entry with no timestamp outlived newer ones")
	}
	if _, ok := got["https://a.example/24"]; !ok {
		t.Error("the newest entry was evicted")
	}
	if _, ok := got["https://a.example/14"]; ok {
		t.Error("an old entry survived past the bound")
	}
}
