package index_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/index"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
)

// ⛔ Signet epic 37 D1 — the guard, written before the write path.
//
// Tags and attestations used to reach index.jsonl ONLY when somebody ran
// `polis rebuild`, so a site's answer to "what have you attested?" was
// truthful and arbitrarily stale at once. The operator site issued two
// agent-disclosure records and published an empty index.
//
// Writing an entry at write time is only safe if the write and the rebuild
// agree about every byte: the hosted service validates every line of the file
// and fails the WHOLE SITE on the first one it rejects (asserted where that
// check lives), and epic 25 already found one
// write path that disagreed with the rebuild. So the assertion is not "the
// entry appears" but "a rebuild afterwards has nothing to change".
func TestWritesProduceTheIndexARebuildWould(t *testing.T) {
	dataDir := t.TempDir()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	// Withdraw verifies the target against the published key.
	wk, _ := json.Marshal(map[string]string{"public_key": string(pub)})
	writeFile(t, filepath.Join(dataDir, ".well-known", "polis"), string(wk))

	// A post already in the index, in the shape publish writes it, so the test
	// also proves a write-time refresh carries other types' lines through.
	core := filepath.Join(dataDir, "content", "pub.polis.core")
	writeFile(t, filepath.Join(core, "post", "20260101", "hello.md"), "---\ntitle: Hello\npublished: 2026-01-01T00:00:00Z\ncurrent-version: sha256:aaaa\n---\n\nHello.\n")
	postLine := `{"type":"post","path":"content/pub.polis.core/post/20260101/hello.md","title":"Hello","published":"2026-01-01T00:00:00Z","current_version":"sha256:aaaa"}`
	writeFile(t, filepath.Join(core, "index.jsonl"), postLine+"\n")

	// An attestation dated BEFORE the post, so an append-at-the-end write
	// would disagree with the rebuild's ordering.
	early, err := attestation.Issue(dataDir, &attestation.Record{
		Issuer:    "https://alice.polis.pub",
		Predicate: attestation.PredicateAgentDisclosure,
		Subject:   attestation.Subject{Type: attestation.SubjectIdentity, ID: "https://judge.alice.polis.pub"},
		Asserted:  "2025-12-31T00:00:00Z",
	}, priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := attestation.Issue(dataDir, &attestation.Record{
		Issuer:    "https://alice.polis.pub",
		Predicate: attestation.PredicateEndorsement,
		Subject:   attestation.Subject{Type: attestation.SubjectIdentity, ID: "https://bob.polis.pub"},
	}, priv); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	withdrawal, err := attestation.Withdraw(dataDir, early, priv)
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}

	if _, err := tag.ApplyTag(dataDir, "reading", "https://bob.polis.pub/posts/a.html", priv); err != nil {
		t.Fatalf("ApplyTag: %v", err)
	}
	if _, err := tag.ApplyTag(dataDir, "reading", "https://bob.polis.pub/posts/b.html", priv); err != nil {
		t.Fatalf("ApplyTag: %v", err)
	}
	if _, err := tag.RemoveTarget(dataDir, "reading", "https://bob.polis.pub/posts/b.html", priv); err != nil {
		t.Fatalf("RemoveTarget: %v", err)
	}
	if _, err := tag.ApplyTag(dataDir, "gone", "https://bob.polis.pub/posts/a.html", priv); err != nil {
		t.Fatalf("ApplyTag: %v", err)
	}
	if err := tag.DeleteTag(dataDir, "gone"); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}

	indexPath := filepath.Join(core, "index.jsonl")
	written, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	text := string(written)

	// The entries exist because of the WRITES — nothing has rebuilt yet.
	for _, want := range []string{
		`"path":"content/pub.polis.core/attestation/` + early + `.json"`,
		`"path":"content/pub.polis.core/attestation/` + withdrawal + `.json"`,
		`"path":"content/pub.polis.core/tag/reading.json"`,
		postLine,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("index after writes is missing %s:\n%s", want, text)
		}
	}
	if got := strings.Count(text, `"type":"attestation"`); got != 3 {
		t.Errorf("attestation lines = %d, want 3 (two issued, one withdrawal):\n%s", got, text)
	}
	if strings.Contains(text, "tag/gone.json") {
		t.Errorf("a deleted tag is still indexed:\n%s", text)
	}

	// ⭐ THE GUARD: a full rebuild has nothing to change.
	result, err := index.RebuildContentIndex(dataDir, nil)
	if err != nil {
		t.Fatalf("RebuildContentIndex: %v", err)
	}
	rebuilt, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if string(rebuilt) != text {
		t.Fatalf("a rebuild after the writes changed the index — the two paths disagree.\n--- written ---\n%s--- rebuilt ---\n%s", text, rebuilt)
	}
	if result.Changed {
		t.Errorf("rebuild reported Changed = true over an index the writes had already made current")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
