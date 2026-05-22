package sitemap

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuild_ValidXMLAndOrder(t *testing.T) {
	a := Entry{URL: "https://x.example/posts/20260101/a.html", LastMod: parseTime(t, "2026-01-01T12:00:00Z")}
	b := Entry{URL: "https://x.example/posts/20260315/b.html", LastMod: parseTime(t, "2026-03-15T12:00:00Z")}
	c := Entry{URL: "https://x.example/posts/20260220/c.html", LastMod: parseTime(t, "2026-02-20T12:00:00Z")}

	body, err := Build([]Entry{a, b, c})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Must parse with encoding/xml (sanity check on XML well-formedness).
	var probe urlset
	if err := xml.Unmarshal(body, &probe); err != nil {
		t.Fatalf("emitted XML doesn't round-trip via encoding/xml: %v\n%s", err, body)
	}

	// Namespace declared.
	if probe.XMLNS != Namespace {
		t.Errorf("xmlns = %q, want %q", probe.XMLNS, Namespace)
	}

	// Newest-first order: B (mar) → C (feb) → A (jan).
	if len(probe.URLs) != 3 {
		t.Fatalf("URL count = %d, want 3", len(probe.URLs))
	}
	wantOrder := []string{b.URL, c.URL, a.URL}
	for i, want := range wantOrder {
		if probe.URLs[i].Loc != want {
			t.Errorf("entry %d: loc = %q, want %q", i, probe.URLs[i].Loc, want)
		}
	}

	// XML declaration present.
	if !strings.HasPrefix(string(body), "<?xml") {
		t.Errorf("missing XML declaration; first 30 chars: %q", string(body[:30]))
	}
}

func TestBuild_EmptyEntries(t *testing.T) {
	body, err := Build(nil)
	if err != nil {
		t.Fatalf("Build(nil): %v", err)
	}
	var probe urlset
	if err := xml.Unmarshal(body, &probe); err != nil {
		t.Fatalf("empty sitemap not parseable: %v", err)
	}
	if len(probe.URLs) != 0 {
		t.Errorf("empty sitemap should have 0 URLs, got %d", len(probe.URLs))
	}
}

func TestInsertOrUpdate_AddsMissing(t *testing.T) {
	a := Entry{URL: "https://x.example/posts/20260101/a.html", LastMod: parseTime(t, "2026-01-01T12:00:00Z")}
	body, err := Build([]Entry{a})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	b := Entry{URL: "https://x.example/posts/20260315/b.html", LastMod: parseTime(t, "2026-03-15T12:00:00Z")}
	updated, err := InsertOrUpdate(body, b)
	if err != nil {
		t.Fatalf("InsertOrUpdate: %v", err)
	}

	entries, err := Parse(updated)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(entries))
	}
	// Newest-first → B then A.
	if entries[0].URL != b.URL {
		t.Errorf("first entry = %q, want %q (newer should sort first)", entries[0].URL, b.URL)
	}
}

func TestInsertOrUpdate_UpdatesExisting(t *testing.T) {
	a := Entry{URL: "https://x.example/posts/20260101/a.html", LastMod: parseTime(t, "2026-01-01T12:00:00Z")}
	body, _ := Build([]Entry{a})

	// Republish A with a newer mtime.
	aPrime := Entry{URL: a.URL, LastMod: parseTime(t, "2026-04-01T12:00:00Z")}
	updated, err := InsertOrUpdate(body, aPrime)
	if err != nil {
		t.Fatalf("InsertOrUpdate: %v", err)
	}

	entries, _ := Parse(updated)
	if len(entries) != 1 {
		t.Fatalf("entry count after update = %d, want 1 (must REPLACE, not duplicate)", len(entries))
	}
	if !entries[0].LastMod.Equal(aPrime.LastMod) {
		t.Errorf("LastMod = %v, want %v (update should replace)", entries[0].LastMod, aPrime.LastMod)
	}
}

func TestRemove_DropsEntry(t *testing.T) {
	a := Entry{URL: "https://x.example/posts/20260101/a.html", LastMod: parseTime(t, "2026-01-01T12:00:00Z")}
	b := Entry{URL: "https://x.example/posts/20260315/b.html", LastMod: parseTime(t, "2026-03-15T12:00:00Z")}
	body, _ := Build([]Entry{a, b})

	updated, err := Remove(body, a.URL)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}

	entries, _ := Parse(updated)
	if len(entries) != 1 {
		t.Fatalf("entry count after remove = %d, want 1", len(entries))
	}
	if entries[0].URL != b.URL {
		t.Errorf("remaining entry = %q, want %q", entries[0].URL, b.URL)
	}

	// Surrounding XML stays valid (parseable).
	var probe urlset
	if err := xml.Unmarshal(updated, &probe); err != nil {
		t.Errorf("post-remove XML invalid: %v", err)
	}
}

func TestRemove_AbsentURL_Idempotent(t *testing.T) {
	a := Entry{URL: "https://x.example/posts/20260101/a.html", LastMod: parseTime(t, "2026-01-01T12:00:00Z")}
	body, _ := Build([]Entry{a})

	updated, err := Remove(body, "https://x.example/posts/9999/missing.html")
	if err != nil {
		t.Fatalf("Remove of absent URL: %v", err)
	}
	entries, _ := Parse(updated)
	if len(entries) != 1 {
		t.Errorf("removing absent URL should leave entries untouched; got %d", len(entries))
	}
}

func TestWriteRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := Entry{URL: "https://x.example/posts/20260101/a.html", LastMod: parseTime(t, "2026-01-01T12:00:00Z")}
	body, _ := Build([]Entry{a})

	if err := Write(dir, body); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// File at expected path.
	written, err := os.ReadFile(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatalf("read sitemap: %v", err)
	}
	if string(written) != string(body) {
		t.Errorf("on-disk bytes != input bytes")
	}

	// Atomic write left no .tmp behind.
	if _, err := os.Stat(filepath.Join(dir, Filename+".tmp")); !os.IsNotExist(err) {
		t.Errorf(".tmp file should have been renamed away; err = %v", err)
	}
}

func TestRead_MissingFile_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	body, err := Read(dir)
	if err != nil {
		t.Fatalf("Read of missing file: %v", err)
	}
	entries, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse of empty sitemap: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("missing-file Read should yield 0 entries, got %d", len(entries))
	}
}

func TestInsertOrUpdate_CorruptInputRebuilds(t *testing.T) {
	corrupt := []byte("this is not xml")
	a := Entry{URL: "https://x.example/posts/20260101/a.html", LastMod: parseTime(t, "2026-01-01T12:00:00Z")}

	body, err := InsertOrUpdate(corrupt, a)
	if err != nil {
		t.Fatalf("InsertOrUpdate on corrupt input: %v", err)
	}
	entries, err := Parse(body)
	if err != nil {
		t.Fatalf("rebuilt sitemap not parseable: %v", err)
	}
	if len(entries) != 1 || entries[0].URL != a.URL {
		t.Errorf("corrupt-input recovery should rebuild from the new entry; got %v", entries)
	}
}

func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	out, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return out
}
