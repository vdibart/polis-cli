// Package sitemap builds and incrementally updates the tenant's sitemap.xml
// per the sitemaps.org 0.9 schema. Per step-03/3.c: emit one entry per
// canonical post URL, ordered newest-first, with ISO 8601 lastmod. Used by
// the stream render path; blog callers are unaffected.
package sitemap

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Filename is the on-disk basename emitted at the tenant root.
const Filename = "sitemap.xml"

// Namespace is the sitemaps.org 0.9 XML namespace.
const Namespace = "http://www.sitemaps.org/schemas/sitemap/0.9"

// Entry is one indexed URL + its last-modification time.
type Entry struct {
	URL     string
	LastMod time.Time
}

// urlset and urlEntry mirror the sitemaps.org 0.9 wire format. Optional
// elements (changefreq, priority) are intentionally omitted — there's no
// signal we'd want to add today, and unfurlers/crawlers default sensibly.
type urlset struct {
	XMLName xml.Name   `xml:"urlset"`
	XMLNS   string     `xml:"xmlns,attr"`
	URLs    []urlEntry `xml:"url"`
}

type urlEntry struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// Build serializes entries as a sitemap, ordered newest-first by LastMod
// (entries with zero LastMod sort to the end). The returned bytes start with
// the XML declaration and are ready to write to disk.
func Build(entries []Entry) ([]byte, error) {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		// Zero times sort last so a parser-recovery rebuild doesn't shuffle
		// well-dated entries behind dateless ones.
		if sorted[i].LastMod.IsZero() {
			return false
		}
		if sorted[j].LastMod.IsZero() {
			return true
		}
		return sorted[i].LastMod.After(sorted[j].LastMod)
	})

	set := urlset{XMLNS: Namespace}
	for _, e := range sorted {
		ue := urlEntry{Loc: e.URL}
		if !e.LastMod.IsZero() {
			// RFC3339Nano (rather than RFC3339) so source-file mtime survives
			// the Read → InsertOrUpdate → Write roundtrip without truncation.
			// Plain RFC3339 silently drops sub-second precision, which means
			// two republishes of the same post within one second would appear
			// to leave LastMod unchanged on disk. RFC3339Nano is also W3C
			// Datetime / sitemaps.org schema valid, so crawlers parse it
			// without complaint.
			ue.LastMod = e.LastMod.UTC().Format(time.RFC3339Nano)
		}
		set.URLs = append(set.URLs, ue)
	}

	body, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal sitemap: %w", err)
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

// Parse decodes a sitemap.xml body into entries. Tolerates missing or
// malformed <lastmod> by leaving Entry.LastMod zero (Build then writes a
// dateless <url>).
func Parse(body []byte) ([]Entry, error) {
	var set urlset
	if err := xml.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("unmarshal sitemap: %w", err)
	}
	out := make([]Entry, 0, len(set.URLs))
	for _, u := range set.URLs {
		var t time.Time
		if u.LastMod != "" {
			// time.Parse with RFC3339Nano accepts both nano-precision and
			// plain second-precision RFC3339 inputs (Go's parser tolerates
			// the missing fractional component), so we can read sitemaps
			// authored by any conforming generator.
			if parsed, err := time.Parse(time.RFC3339Nano, u.LastMod); err == nil {
				t = parsed
			} else if parsed, err := time.Parse(time.RFC3339, u.LastMod); err == nil {
				t = parsed
			}
		}
		out = append(out, Entry{URL: u.Loc, LastMod: t})
	}
	return out, nil
}

// InsertOrUpdate adds entry if its URL is absent, or replaces the existing
// entry's LastMod if present. Returns the rebuilt sitemap bytes.
//
// On parse failure (corrupt sitemap), the rebuild contains only `entry` — a
// recovery behavior chosen over hard-erroring: a corrupt sitemap should not
// block a publish.
func InsertOrUpdate(body []byte, entry Entry) ([]byte, error) {
	entries, err := Parse(body)
	if err != nil {
		return Build([]Entry{entry})
	}
	found := false
	for i := range entries {
		if entries[i].URL == entry.URL {
			entries[i].LastMod = entry.LastMod
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, entry)
	}
	return Build(entries)
}

// Remove deletes the entry with the given URL. Idempotent — absent URL is OK.
// Parse failure is treated as "empty sitemap" so unpublish doesn't error on a
// corrupt file.
func Remove(body []byte, url string) ([]byte, error) {
	entries, err := Parse(body)
	if err != nil {
		return Build(nil)
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.URL != url {
			kept = append(kept, e)
		}
	}
	return Build(kept)
}

// Write atomically writes the sitemap to <dataDir>/sitemap.xml via tmp+rename.
// Per step-03/3.c: sitemap.xml is public (served at the tenant root) so
// partial files on disk are worse than no change — atomicity is mandatory.
func Write(dataDir string, body []byte) error {
	target := filepath.Join(dataDir, Filename)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, body, 0644); err != nil {
		return fmt.Errorf("write tmp sitemap: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename sitemap into place: %w", err)
	}
	return nil
}

// Read returns the on-disk sitemap. Missing file yields an empty sitemap (so
// callers can do Read → InsertOrUpdate → Write idempotently on first publish).
func Read(dataDir string) ([]byte, error) {
	target := filepath.Join(dataDir, Filename)
	body, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return Build(nil)
	}
	if err != nil {
		return nil, fmt.Errorf("read sitemap: %w", err)
	}
	return body, nil
}
