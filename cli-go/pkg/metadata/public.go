// Package metadata provides management for polis public metadata files.
package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const (
	// PublicIndexFilename is the name of the bundle index file.
	PublicIndexFilename = "index.jsonl"

	// BundleContentDir is the relative path to the core bundle content directory.
	BundleContentDir = "content/pub.polis.core"
)

// IndexEntry represents a single entry in public.jsonl.
// Can be either a post or a comment.
type IndexEntry struct {
	Type           string          `json:"type"`                  // "post" or "comment"
	Path           string          `json:"path"`                  // Relative file path
	Title          string          `json:"title"`                 // Entry title
	Published      string          `json:"published"`             // ISO timestamp
	CurrentVersion string          `json:"current_version"`       // sha256:... hash
	InReplyTo      *InReplyToEntry `json:"in_reply_to,omitempty"` // Only for comments

	// License is the work's materialised terms, carried so the JSON API can
	// answer "what may I do with this?" without a second fetch of the markdown.
	//
	// It is a PROJECTION, derived from the signed file at index time, never a
	// second source of truth — which is why AppendToPublicIndex reads it off
	// disk rather than accepting it from a caller. A caller could pass terms
	// that disagree with what was signed; the file cannot.
	//
	// Absent for works that state none.
	License *license.Terms `json:"license,omitempty"`

	// Extra holds members of the line this build does not model. index.jsonl
	// is unsigned, so a line this build REPLACES keeps them (index_extra.go);
	// a line it does not touch is written back as its original bytes.
	//
	// ⚠️ License carries no Extra, on purpose: it is a projection re-derived
	// from the signed file whenever the line is replaced, never carried over.
	Extra map[string]json.RawMessage `json:"-"`
}

// InReplyToEntry represents the in-reply-to reference in a comment index entry.
type InReplyToEntry struct {
	URL     string `json:"url"`
	Version string `json:"version,omitempty"`

	// Extra holds members this build does not model — see IndexEntry.Extra.
	Extra map[string]json.RawMessage `json:"-"`
}

// AppendToPublicIndex adds an entry to public.jsonl.
// If an entry with the same Path already exists, it is updated in place.
// Otherwise the entry is appended. Creates the metadata directory and file if they don't exist.
func AppendToPublicIndex(site storage.Site, entry *IndexEntry) error {
	// Carry the work's own terms into the index, read from the SIGNED FILE.
	// Doing it here rather than at each call site means every entry gets it,
	// and means the index can never claim terms the work does not carry.
	//
	// ⛔ The caller's value is discarded rather than preferred. A caller can
	// pass terms that disagree with what was signed; the file on disk cannot.
	// An unreadable file therefore yields NO terms rather than the caller's —
	// absent means unstated, which is honest, where a caller's guess would be a
	// claim nobody made.
	entry.License = nil
	if data, err := site.Store().Get(context.Background(), entry.Path); err == nil {
		entry.License = license.ParseBlock(string(data))
	}

	// Load existing lines to check for duplicates
	lines, err := readIndexLines(site)
	if err != nil {
		return err
	}

	// Replace in place if an entry already exists by path. Only that line is
	// re-encoded; it keeps what this build does not model.
	for i, l := range lines {
		if l.entry == nil || l.entry.Path != entry.Path {
			continue
		}
		replaced := *entry
		carryUnmodelled(&replaced, l.entry)
		lines[i] = indexLine{entry: &replaced}
		return writeIndexLines(site, lines)
	}

	// No duplicate - append.
	//
	// ⭐ THE COMMENT THAT STOOD HERE WAS RIGHT ABOUT THE RISK AND WRONG ABOUT
	// THE BASELINE. It argued against read-modify-write because it would change
	// the concurrency story "from the kernel appends to last writer wins",
	// losing a line when Medic and a publisher run at once.
	//
	// ⛔⛔ But the story was ALREADY last-writer-wins. This index has three
	// writers and two replace the whole file: RemoveIndexEntry rewrites it, and
	// MEDIC REBUILDS IT ON EVERY SWEEP (index.RebuildContentIndex). O_APPEND's
	// atomicity bought nothing against either — a rebuild landing after an
	// append simply clobbered it, invisibly, precisely because this path looked
	// atomic.
	//
	// ⭐⭐ So the fix is not to keep O_APPEND; it is the PER-KEY LOCK that all
	// three writers now take. Serialising appends against each other would have
	// fixed nothing.
	jsonLine, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}
	if err := storage.AppendLine(context.Background(), site.Store(), indexKey, jsonLine); err != nil {
		return fmt.Errorf("failed to write to index: %w", err)
	}
	return nil
}

// AppendCommentToIndex is a convenience function for appending a comment entry.
func AppendCommentToIndex(site storage.Site, path, title, published, currentVersion, inReplyToURL string) error {
	entry := &IndexEntry{
		Type:           "comment",
		Path:           path,
		Title:          title,
		Published:      published,
		CurrentVersion: currentVersion,
		InReplyTo: &InReplyToEntry{
			URL:     inReplyToURL,
			Version: "", // Version is typically not tracked for the target
		},
	}
	return AppendToPublicIndex(site, entry)
}

// AppendPostToIndex is a convenience function for appending a post entry.
func AppendPostToIndex(site storage.Site, path, title, published, currentVersion string) error {
	entry := &IndexEntry{
		Type:           "post",
		Path:           path,
		Title:          title,
		Published:      published,
		CurrentVersion: currentVersion,
	}
	return AppendToPublicIndex(site, entry)
}

// IndexReadReport says what a read of index.jsonl could NOT use.
type IndexReadReport struct {
	// Skipped counts non-blank lines that did not parse as an entry.
	Skipped int `json:"skipped"`
	// SkippedLines are their 1-based line numbers, in file order.
	SkippedLines []int `json:"skipped_lines,omitempty"`
}

// LoadPublicIndex reads all entries from index.jsonl, SKIPPING lines it cannot
// parse. Kept for existing callers.
//
// ⚠️ A caller that reports on the index — a validator, a listing — must use
// ReadPublicIndex instead, or a half-garbage file reads as a clean one over
// the half that parses (Signet epic 44 C1).
func LoadPublicIndex(site storage.Site) ([]IndexEntry, error) {
	entries, _, err := ReadPublicIndex(site)
	return entries, err
}

// ReadPublicIndex reads all entries from index.jsonl and reports every line it
// could not parse. A missing file is an empty index and a clean report.
func ReadPublicIndex(site storage.Site) ([]IndexEntry, *IndexReadReport, error) {
	report := &IndexReadReport{}
	lines, err := readIndexLines(site)
	if err != nil {
		return nil, report, err
	}
	entries := []IndexEntry{}
	for _, l := range lines {
		if l.entry == nil {
			report.Skipped++
			report.SkippedLines = append(report.SkippedLines, l.number)
			continue
		}
		entries = append(entries, *l.entry)
	}
	return entries, report, nil
}

// indexLine is one non-blank line of index.jsonl: the bytes read, and the
// entry when they parse. A line built by a writer has an entry and no bytes.
type indexLine struct {
	raw    []byte      // as read (without the newline); nil for a replaced line
	entry  *IndexEntry // nil when the line does not parse — kept verbatim
	number int         // 1-based line number in the file as read
}

// indexKey is the public index, relative to the site root.
//
// ⭐ A KEY, NOT A PATH. Every writer of this file addresses it the same way, so
// the per-key lock they share names one thing on any backend.
var indexKey = BundleContentDir + "/" + PublicIndexFilename

// readIndexLines reads index.jsonl line by line. A missing file is empty.
// Blank lines are dropped; every other line is returned, parsed or not.
func readIndexLines(site storage.Site) ([]indexLine, error) {
	data, err := site.Store().Get(context.Background(), indexKey)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read public.jsonl: %w", err)
	}
	var lines []indexLine
	for i, line := range splitLines(data) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		// ⚠️ Decoded into a POINTER so a literal `null` — valid JSON, and no
		// error — comes back nil instead of the zero entry, and is counted as
		// skipped rather than read as an entry (round 1, R1-5). ⚠️ The rule is
		// exactly that: an empty object `{}` IS an entry here, field-less as it
		// is, because nothing distinguishes it from a line this build cannot
		// model yet.
		var entry *IndexEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			entry = nil
		}
		lines = append(lines, indexLine{raw: line, entry: entry, number: i + 1})
	}
	return lines, nil
}

// carryUnmodelled gives a replacement entry the members its predecessor
// carried that this build does not model. in_reply_to's are carried only when
// the replacement still replies to the same URL — a new parent is a new
// statement, and nothing about the old one's members is known to still hold.
func carryUnmodelled(replacement, old *IndexEntry) {
	if replacement.Extra == nil {
		replacement.Extra = old.Extra
	}
	if r, o := replacement.InReplyTo, old.InReplyTo; r != nil && o != nil && r.URL == o.URL && r.Extra == nil {
		carried := *r
		carried.Extra = o.Extra
		replacement.InReplyTo = &carried
	}
}

// splitLines splits byte slice by newlines, handling both \n and \r\n.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			end := i
			if end > start && data[end-1] == '\r' {
				end--
			}
			lines = append(lines, data[start:end])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// RemoveIndexEntry removes an entry from public.jsonl by path.
func RemoveIndexEntry(site storage.Site, path string) error {
	lines, err := readIndexLines(site)
	if err != nil {
		return err
	}

	// Filter out the entry. A line that does not parse names no path, so it
	// is never the one removed.
	var kept []indexLine
	for _, l := range lines {
		if l.entry == nil || l.entry.Path != path {
			kept = append(kept, l)
		}
	}

	return writeIndexLines(site, kept)
}

// writeIndexLines writes index.jsonl atomically.
//
// ⛔ A line read from the file is written back AS ITS ORIGINAL BYTES — whether
// or not it parsed. The struct rewrite this replaces re-marshalled every line,
// which dropped every member this build does not model and DELETED every line
// it could not parse, on every replace and every remove. Only a line a writer
// built (raw == nil) is encoded.
func writeIndexLines(site storage.Site, lines []indexLine) error {
	// ⛔ THE SAME LOCK THE APPEND PATH TAKES. This function replaces the whole
	// index, so without it a concurrent append is clobbered — which is the bug
	// the append path's old comment warned about and could not prevent on its
	// own.
	return storage.WithKeyLock(site.Store(), indexKey, func() error {
		return writeIndexLinesLocked(site, lines)
	})
}

func writeIndexLinesLocked(site storage.Site, lines []indexLine) error {
	// ⛔ ATOMIC, because this is the file every reader of the site walks and a
	// half-written index is worse than an unchanged one. atomicfile.Write keeps
	// temp-and-rename on a filesystem and is a single all-or-nothing Put on a
	// store without one, so the guarantee holds on every backend.
	var buf bytes.Buffer
	for _, l := range lines {
		jsonLine := l.raw
		if jsonLine == nil {
			var err error
			if jsonLine, err = json.Marshal(l.entry); err != nil {
				return fmt.Errorf("failed to marshal entry: %w", err)
			}
		}
		buf.Write(jsonLine)
		buf.WriteByte('\n')
	}
	if err := atomicfile.Write(context.Background(), site, indexKey, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to write public index: %w", err)
	}
	return nil
}
