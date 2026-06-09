package comment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBeseechFlagContract_SharedFixture is WS5 ⑤: the Go PRODUCER
// (commentRegistrationMetadata) asserts against the SAME shared golden fixture the
// Deno CONSUMER (commentBeseechRequested) does, so the cross-language beseech-flag
// wire shape (Defect 4 decouple) cannot drift. The Go side proves that the metadata
// it emits for each intent (beseech true/false) lands in a fixture case whose
// beseech_requested interpretation matches that intent.
func TestBeseechFlagContract_SharedFixture(t *testing.T) {
	root := repoRootForContract(t)
	data, err := os.ReadFile(filepath.Join(root, "discovery-service", "core", "contract-fixtures", "beseech-flag.json"))
	if err != nil {
		t.Fatalf("read shared fixture: %v", err)
	}
	var fx struct {
		Cases []struct {
			Name             string                 `json:"name"`
			Metadata         map[string]interface{} `json:"metadata"`
			BeseechRequested bool                   `json:"beseech_requested"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	// Classify the beseech-key state a metadata object encodes.
	stateOf := func(md map[string]interface{}) string {
		v, ok := md["beseech"]
		if !ok {
			return "absent"
		}
		if b, isBool := v.(bool); isBool {
			if b {
				return "true"
			}
			return "false"
		}
		return "other"
	}
	wantRequested := map[string]bool{}
	for _, c := range fx.Cases {
		wantRequested[stateOf(c.Metadata)] = c.BeseechRequested
	}

	meta := &CommentMeta{InReplyTo: "https://a/posts/x.md", RootPost: "https://a/posts/x.md", Timestamp: "2026-01-01T00:00:00Z"}
	for _, beseech := range []bool{true, false} {
		state := stateOf(commentRegistrationMetadata(meta, beseech))
		req, ok := wantRequested[state]
		if !ok {
			t.Fatalf("producer emitted beseech-state %q (intent beseech=%v) that the shared fixture does not cover", state, beseech)
		}
		if req != beseech {
			t.Errorf("contract drift: producer intent beseech=%v emits state %q, but the fixture maps that to beseech_requested=%v", beseech, state, req)
		}
	}
}

// repoRootForContract walks up from the test's working dir to the repo root
// (the directory containing both cli-go/ and discovery-service/).
func repoRootForContract(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if dirExists(filepath.Join(dir, "cli-go")) && dirExists(filepath.Join(dir, "discovery-service")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repo root (cli-go/ + discovery-service/) not found — skipping cross-language contract test")
		}
		dir = parent
	}
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func TestBeseechComment_NotConfigured(t *testing.T) {
	oldURL, oldKey, oldBase := DiscoveryURL, DiscoveryKey, BaseURL
	defer func() { DiscoveryURL, DiscoveryKey, BaseURL = oldURL, oldKey, oldBase }()

	DiscoveryURL = ""
	DiscoveryKey = ""
	BaseURL = ""

	_, err := BeseechComment(t.TempDir(), "test-id", nil)
	if err == nil {
		t.Error("expected error when not configured")
	}
	if err.Error() != "discovery service not configured" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBeseechComment_NoBaseURL(t *testing.T) {
	oldURL, oldKey, oldBase := DiscoveryURL, DiscoveryKey, BaseURL
	defer func() { DiscoveryURL, DiscoveryKey, BaseURL = oldURL, oldKey, oldBase }()

	DiscoveryURL = "https://discovery.example.com"
	DiscoveryKey = "test-key"
	BaseURL = ""

	_, err := BeseechComment(t.TempDir(), "test-id", nil)
	if err == nil {
		t.Error("expected error when base URL not configured")
	}
	if err.Error() != "POLIS_BASE_URL not configured" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBeseechComment_CommentNotFound(t *testing.T) {
	oldURL, oldKey, oldBase := DiscoveryURL, DiscoveryKey, BaseURL
	defer func() { DiscoveryURL, DiscoveryKey, BaseURL = oldURL, oldKey, oldBase }()

	DiscoveryURL = "https://discovery.example.com"
	DiscoveryKey = "test-key"
	BaseURL = "https://test.polis.pub"

	dataDir := t.TempDir()

	_, err := BeseechComment(dataDir, "nonexistent-id", nil)
	if err == nil {
		t.Error("expected error when comment not found")
	}
}

// TestBeseechComment_NotRegisteredLocally exercises the "deferred" path
// introduced when the site has DS config + base URL + a pending comment
// but no local registration marker. The expected outcome is:
//   - no error (was previously: error "site not registered with...")
//   - result.Status == "deferred"
//   - result.Success == true
//   - the comment IS published to content/pub.polis.core/comment/YYYYMMDD/{id}.md
//     (PublishComment runs BEFORE the registration check now — local
//     state captured even when DS contact is skipped)
//
// Regression guard: re-ordering or removing the deferred branch (or moving
// the PublishComment step back below the registration check) would fail
// this test.
func TestBeseechComment_NotRegisteredLocally(t *testing.T) {
	oldURL, oldKey, oldBase := DiscoveryURL, DiscoveryKey, BaseURL
	defer func() { DiscoveryURL, DiscoveryKey, BaseURL = oldURL, oldKey, oldBase }()

	DiscoveryURL = "https://discovery.example.com"
	DiscoveryKey = "test-key"
	BaseURL = "https://test.polis.pub"

	dataDir := t.TempDir()

	// Set up the comment directory tree SignComment + PublishComment expect.
	dirs := []string{
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "drafts"),
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "pending"),
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "denied"),
		filepath.Join(dataDir, "content", "pub.polis.core", "comment"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Sign a comment so beseech has something in pending/ to act on.
	privKey := generateTestKey(t)
	draft := &CommentDraft{
		InReplyTo: "https://alice.polis.pub/posts/20260101/hello.md",
		Content:   "Local-only comment — DS deferred until registration.",
	}
	signed, err := SignComment(dataDir, draft, "bob.polis.pub", "https://bob.polis.pub", privKey)
	if err != nil {
		t.Fatalf("SignComment failed: %v", err)
	}
	if signed.Meta.ID == "" {
		t.Fatal("SignComment returned empty comment ID")
	}

	// Intentionally do NOT write a registration marker; this is the
	// scenario under test.

	result, err := BeseechComment(dataDir, signed.Meta.ID, privKey)
	if err != nil {
		t.Fatalf("BeseechComment returned error when not registered (should defer): %v", err)
	}
	if result == nil {
		t.Fatal("BeseechComment returned nil result")
	}
	if !result.Success {
		t.Errorf("result.Success = false, want true")
	}
	if result.Status != "deferred" {
		t.Errorf("result.Status = %q, want %q", result.Status, "deferred")
	}
	if result.Message == "" {
		t.Error("result.Message should explain why DS contact was deferred")
	}
	if result.AutoBlessed {
		t.Error("result.AutoBlessed should be false in deferred path")
	}

	// PublishComment runs BEFORE the registration check, so the .md file
	// should now exist under content/pub.polis.core/comment/YYYYMMDD/.
	ts, err := time.Parse("2006-01-02T15:04:05Z", signed.Meta.Timestamp)
	if err != nil {
		t.Fatalf("parse signed timestamp: %v", err)
	}
	publishedPath := filepath.Join(
		dataDir, "content", "pub.polis.core", "comment",
		ts.Format("20060102"), signed.Meta.ID+".md",
	)
	if _, err := os.Stat(publishedPath); err != nil {
		t.Errorf("expected published comment at %s: %v", publishedPath, err)
	}

	// And the pending file should still be there (deferred path doesn't
	// move it to blessed/ since no auto-bless response from DS).
	pendingPath := filepath.Join(
		dataDir, ".polis", "bundles", "pub.polis.core", "comments",
		"pending", signed.Meta.ID+".md",
	)
	if _, err := os.Stat(pendingPath); err != nil {
		t.Errorf("expected pending comment to remain at %s: %v", pendingPath, err)
	}
}

// TestCommentRegistrationMetadata_BeseechFlag is the regression guard for the
// Defect-4 publish-vs-beseech decouple. When beseech=true (the default flow)
// the metadata must be IDENTICAL to the historical payload — no `beseech` key —
// so existing clients and signatures are unaffected. When beseech=false the
// signed metadata must carry beseech=false so the DS registers content without
// requesting a blessing (publish-only).
func TestCommentRegistrationMetadata_BeseechFlag(t *testing.T) {
	meta := &CommentMeta{
		InReplyTo: "https://alice.polis.pub/content/pub.polis.core/post/20260101/hello.md",
		RootPost:  "https://alice.polis.pub/content/pub.polis.core/post/20260101/hello.md",
		Timestamp: "2026-06-01T15:04:05Z",
	}

	// beseech=true → no `beseech` key (byte-identical to the historical default).
	withBeseech := commentRegistrationMetadata(meta, true)
	if _, present := withBeseech["beseech"]; present {
		t.Errorf("beseech=true must OMIT the beseech key, got %v", withBeseech["beseech"])
	}
	for _, k := range []string{"in_reply_to", "root_post", "timestamp"} {
		if _, ok := withBeseech[k]; !ok {
			t.Errorf("missing expected metadata key %q", k)
		}
	}

	// beseech=false → metadata.beseech == false (publish-only signal to the DS).
	noBeseech := commentRegistrationMetadata(meta, false)
	if v, ok := noBeseech["beseech"]; !ok || v != false {
		t.Errorf("beseech=false must set metadata.beseech=false, got %v (present=%v)", v, ok)
	}

	// in_reply_to_version is included only when set.
	if _, ok := commentRegistrationMetadata(meta, true)["in_reply_to_version"]; ok {
		t.Error("in_reply_to_version must be omitted when empty")
	}
	meta.InReplyToVersion = "sha256:deadbeef"
	if v := commentRegistrationMetadata(meta, true)["in_reply_to_version"]; v != "sha256:deadbeef" {
		t.Errorf("in_reply_to_version = %v, want sha256:deadbeef", v)
	}
}
