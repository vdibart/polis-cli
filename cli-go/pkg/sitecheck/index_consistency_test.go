package sitecheck

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

// SIGNET epic 42 V1 — index.consistency examined one entry type of four.
//
// ⛔ Every test here reported OK on the unmodified tree: the check skipped any
// entry whose type was not "post", so a stale tag, a missing comment or a
// tampered attestation was invisible to `polis validate` and to Judge alike.

func (b *siteBuilder) appendIndex(t *testing.T, entry map[string]string) {
	t.Helper()
	indexPath := filepath.Join(b.dir, "content", "pub.polis.core", "index.jsonl")
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write(append(mustJSONLine(entry), '\n'))
}

// addComment writes a signed comment and its index entry.
func (b *siteBuilder) addComment(t *testing.T, slug, body string) string {
	t.Helper()
	rel := filepath.ToSlash(filepath.Join("content", "pub.polis.core", "comment", "20260101", slug+".md"))
	content := signedPost(t, b.priv, body, true)
	b.write(rel, []byte(content))
	fm, _, _ := ParseFrontmatter(content)
	b.appendIndex(t, map[string]string{
		"type": "comment", "path": rel, "published": "2026-01-15T10:00:00Z", "current_version": fm.CurrentVersion,
	})
	return rel
}

// addTag writes a signed tag file, versioned the way tag.signAndWrite does, and
// its index entry.
func (b *siteBuilder) addTag(t *testing.T, name string) string {
	t.Helper()
	tf := &tag.TagFile{
		Tag:       name,
		Targets:   []tag.TagTarget{{URI: "https://alice.example/content/pub.polis.core/post/20260101/hello.md"}},
		Created:   "2026-01-15T10:00:00Z",
		Updated:   "2026-01-15T10:00:00Z",
		Generator: "polis-cli-go/test",
	}
	canonical, err := tag.CanonicalJSON(tf)
	if err != nil {
		t.Fatal(err)
	}
	tf.Version = "sha256:" + SHA256Hex(canonical)
	if tf.Signature, err = signing.SignContent(canonical, b.priv); err != nil {
		t.Fatal(err)
	}
	rel := "content/pub.polis.core/tag/" + name + ".json"
	b.write(rel, mustJSON(tf))
	b.appendIndex(t, map[string]string{
		"type": "tag", "path": rel, "title": name, "published": tf.Created, "current_version": tf.Version,
	})
	return rel
}

// addAttestation writes a signed attestation record and its index entry.
func (b *siteBuilder) addAttestation(t *testing.T) string {
	t.Helper()
	r := &attestation.Record{
		Type:      attestation.TypeName,
		Issuer:    "https://alice.example",
		Predicate: "pub.polis.attestation.same-as",
		Subject:   attestation.Subject{Type: "identity", ID: "https://alice.other.example"},
		Asserted:  "2026-01-15T10:00:00Z",
		Generator: "polis-cli-go/test",
	}
	canonical, err := attestation.CanonicalJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Version = attestation.ContentVersion(canonical)
	if r.Signature, err = signing.SignContent(canonical, b.priv); err != nil {
		t.Fatal(err)
	}
	rel := "content/pub.polis.core/attestation/" + attestation.ID(r) + ".json"
	b.write(rel, mustJSON(r))
	b.appendIndex(t, map[string]string{
		"type": "attestation", "path": rel, "title": r.Predicate, "published": r.Asserted, "current_version": r.Version,
	})
	return rel
}

func fourTypeSite(t *testing.T) (*siteBuilder, map[string]string) {
	t.Helper()
	b := newSite(t)
	b.addPost(t, "hello", "Hello.\n", true)
	paths := map[string]string{
		"comment":     b.addComment(t, "reply", "A reply.\n"),
		"tag":         b.addTag(t, "essays"),
		"attestation": b.addAttestation(t),
	}
	return b, paths
}

func TestIndexConsistencyNamesEveryTypeItChecked(t *testing.T) {
	b, _ := fourTypeSite(t)

	got := IndexConsistency(b.dir)
	if !got.OK {
		t.Fatalf("a consistent four-type site failed: %s", got.Message)
	}
	want := "4 entries checked (attestation 1, comment 1, post 1, tag 1)"
	if got.Message != want {
		t.Errorf("a clean result must say what it examined:\n got %q\nwant %q", got.Message, want)
	}
}

// ⛔ The failure that makes per-type hashing necessary rather than a nicety:
// the body-hash rule applied to a JSON record reports every valid one as a
// mismatch. A correct tag and attestation must PASS.
func TestIndexConsistencyHashesEachTypeByItsOwnRule(t *testing.T) {
	b, _ := fourTypeSite(t)
	if got := IndexConsistency(b.dir); !got.OK || strings.Contains(got.Message, "hash_mismatch") {
		t.Fatalf("valid JSON records must not read as mismatches: %+v", got)
	}
}

func TestIndexConsistencyCatchesAChangedTag(t *testing.T) {
	b, paths := fourTypeSite(t)
	path := filepath.Join(b.dir, paths["tag"])
	var tf tag.TagFile
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &tf)
	tf.Targets = append(tf.Targets, tag.TagTarget{URI: "https://alice.example/added-after.md"})
	b.write(paths["tag"], mustJSON(tf))

	got := IndexConsistency(b.dir)
	if got.OK || !strings.Contains(got.Message, "hash_mismatch: "+paths["tag"]) {
		t.Fatalf("a tag whose bytes no longer match its index entry must fail: %+v", got)
	}
}

func TestIndexConsistencyCatchesATamperedAttestation(t *testing.T) {
	b, paths := fourTypeSite(t)
	path := filepath.Join(b.dir, paths["attestation"])
	var r attestation.Record
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &r)
	r.Predicate = "pub.polis.attestation.integrity"
	b.write(paths["attestation"], mustJSON(r))

	got := IndexConsistency(b.dir)
	if got.OK || !strings.Contains(got.Message, "hash_mismatch: "+paths["attestation"]) {
		t.Fatalf("an attestation whose bytes no longer match its index entry must fail: %+v", got)
	}
}

func TestIndexConsistencyCatchesAnOrphanComment(t *testing.T) {
	b, paths := fourTypeSite(t)
	os.Remove(filepath.Join(b.dir, paths["comment"]))

	got := IndexConsistency(b.dir)
	if got.OK || !strings.Contains(got.Message, "orphan: "+paths["comment"]) {
		t.Fatalf("an indexed comment that is gone must fail: %+v", got)
	}
}

func TestIndexConsistencyCatchesAPhantomTagAndAttestation(t *testing.T) {
	b, _ := fourTypeSite(t)
	b.write("content/pub.polis.core/tag/unindexed.json", []byte(`{"tag":"unindexed"}`))
	b.write("content/pub.polis.core/attestation/unindexed.json", []byte(`{"type":"pub.polis.attestation"}`))

	got := IndexConsistency(b.dir)
	for _, want := range []string{"phantom: content/pub.polis.core/tag/unindexed.json", "phantom: content/pub.polis.core/attestation/unindexed.json"} {
		if got.OK || !strings.Contains(got.Message, filepath.FromSlash(strings.TrimPrefix(want, "phantom: "))) {
			t.Errorf("%s not reported: %+v", want, got)
		}
	}
}

// A file indexed as a JSON type that is not JSON is a finding, not a silent
// mismatch and not a crash.
func TestIndexConsistencyReportsARecordThatDoesNotParse(t *testing.T) {
	b, paths := fourTypeSite(t)
	b.write(paths["tag"], []byte("not json"))

	got := IndexConsistency(b.dir)
	if got.OK || !strings.Contains(got.Message, "unreadable: "+paths["tag"]) {
		t.Fatalf("an unparseable indexed record must be named: %+v", got)
	}
}

// D8 applied to the fifth type: an entry type the table does not know is
// COUNTED and NAMED, never skipped as though it had been looked at.
func TestIndexConsistencyDisclosesTypesItHasNoRuleFor(t *testing.T) {
	b, _ := fourTypeSite(t)
	b.appendIndex(t, map[string]string{"type": "recipe", "path": "content/pub.polis.core/recipe/x.json"})

	got := IndexConsistency(b.dir)
	if !got.OK {
		t.Fatalf("an unknown type is not a finding: %+v", got)
	}
	if !strings.Contains(got.Message, "1 entries NOT checked — no rule for their type (recipe 1)") {
		t.Errorf("an unchecked type must be disclosed: %q", got.Message)
	}
}

// ⛔ The guard D2 exists for: index.Contributors is where a new content type
// starts contributing entries, and this is where its consistency rule must be
// declared. Adding one without the other fails here, not silently in the field.
func TestEveryIndexContributorHasARule(t *testing.T) {
	for _, c := range index.Contributors() {
		if _, ok := IndexEntryRuleFor(c.EntryType); !ok {
			t.Errorf("index contributor %q (%s) has no IndexConsistency rule — its entries would be reported as NOT checked", c.EntryType, c.TypeName)
		}
	}
}

// A line of index.jsonl that does not parse used to be
// skipped silently, so a half-garbage index read as clean over the half that
// parsed. It is a not-OK result naming the line numbers.
func TestIndexConsistencyNamesMalformedLines(t *testing.T) {
	b, _ := fourTypeSite(t)
	indexPath := filepath.Join(b.dir, "content", "pub.polis.core", "index.jsonl")
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{not json\nnull\n")
	f.Close()

	got := IndexConsistency(b.dir)
	if got.OK {
		t.Fatalf("an index with unparseable lines passed: %s", got.Message)
	}
	if !strings.Contains(got.Message, "2 line(s) of index.jsonl do not parse") || !strings.Contains(got.Message, "lines 5, 6") {
		t.Errorf("the result must name the lines, got %q", got.Message)
	}
}

// An index whose every line is malformed is not an "empty index".
func TestIndexConsistencyAllMalformedIsNotEmpty(t *testing.T) {
	b := newSite(t)
	b.write("content/pub.polis.core/index.jsonl", []byte("garbage\n"))
	if got := IndexConsistency(b.dir); got.OK {
		t.Fatalf("an all-garbage index passed: %s", got.Message)
	}
}

// Epic 44 C2: attestation.List and tag.ListTags skip a
// file that does not parse, so `polis validate` counted a directory holding one
// good record and one garbage file as one good record. The garbage is a
// finding, named.
func TestRunLocalNamesRecordFilesThatDoNotParse(t *testing.T) {
	b, _ := fourTypeSite(t)
	b.write("content/pub.polis.core/attestation/20260101T000000Z-bad.json", []byte("{nope"))
	b.write("content/pub.polis.core/tag/broken.json", []byte("[1,2]"))

	r := RunLocal(b.dir)
	for id, file := range map[string]string{
		"content.attestations": "20260101T000000Z-bad.json: does not parse",
		"content.tags":         "broken.json: does not parse",
	} {
		c := checkByID(t, r, id)
		if c.Outcome != OutcomeFailed {
			t.Errorf("%s: outcome = %s, want failed", id, c.Outcome)
		}
		if !strings.Contains(strings.Join(c.Findings, "\n"), file) {
			t.Errorf("%s: findings %v do not name %q", id, c.Findings, file)
		}
	}
}
