package index

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
)

// ⛔ This package does NOT import pkg/attestation or pkg/tag, and must not.
//
// Signet epic 37 D1: `attestation.Issue` and the tag writers refresh their own
// index entries AT WRITE TIME by calling RebuildContentIndex, so they import
// this package — and an import back would be a cycle. The two contributors
// below therefore read the few JSON fields an entry needs through local
// structs, and TestContributorsMatchTheirPackages holds the field names and the
// type name to the packages that own them.
//
// ⚠️ The ENTRY SHAPE still lives only here. That is the point of the seam: a
// write and a rebuild produce byte-identical lines because they are the same
// function, not two functions that agree today.

// attestationTypeName is pub.polis.attestation's bundle type name, spelled out
// for the reason above.
const attestationTypeName = "pub.polis.attestation"

// ⭐ A projection may have many sources. Any regenerator that knows one source
// must not own the whole file.
//
// That is the rule this file exists to enforce, and R24-9 is what happens
// without it: `rebuildPostsIndex` walked post/ and then overwrote the WHOLE of
// index.jsonl, deleting every comment entry — which arrives by an entirely
// different route (comment.MoveComment appends on blessing). Patching the
// truncation would have left the shape intact and the next content type would
// have re-introduced it.
//
// So each content type CONTRIBUTES its own slice and the rebuild COMPOSES. A
// partial rebuild replaces the lines it owns and carries every other line
// through byte-identically.
//
// ⚠️ The seam is a LIST OF FUNCTIONS, deliberately. No registration side
// effects, no lookup table, no interface with one implementation — adding a
// content type is a struct literal in Contributors(). If this ever starts to
// look like a plugin registry, it has gone a level too high.

// Entry-type values written into an index line's `type` field.
//
// ⚠️ These are the index's own SHORT vocabulary, not bundle type names: the
// live published data has said "post" and "comment" since the file existed, and
// a line saying "pub.polis.post" would be a format change nobody asked for.
// The mapping to a fully-qualified bundle type is the Contributor's TypeName.
const (
	EntryTypePost        = "post"
	EntryTypeComment     = "comment"
	EntryTypeTag         = "tag"
	EntryTypeAttestation = "attestation"
)

// Contributor produces one content type's slice of index.jsonl.
type Contributor struct {
	// EntryType is the value written into each entry's `type` field. It is also
	// what a partial rebuild matches on when deciding which lines it owns, so a
	// contributor's entries and the lines it may delete are the same set by
	// construction.
	EntryType string

	// TypeName is the bundle content type whose directory this walks, e.g.
	// "pub.polis.post". Resolved through the bundle rather than assumed, so a
	// tenant who moves `dir` is still indexed correctly.
	TypeName string

	// Flag is the `polis rebuild` flag that selects this contributor alone.
	Flag string

	// Entries walks the site and returns this type's entries, already carrying
	// a site-relative slash-separated Path, plus the number of files it walked
	// and did NOT index. Skips are reported rather than silent.
	Entries func(d *siteDirs) (entries []metadata.IndexEntry, skipped int, err error)
}

// Contributors returns every content type that contributes to index.jsonl.
//
// Order matters only for readability — the composed file is sorted.
func Contributors() []Contributor {
	return []Contributor{
		{EntryTypePost, "pub.polis.post", "--posts", postEntries},
		{EntryTypeComment, "pub.polis.comment", "--comments", commentEntries},
		{EntryTypeTag, "pub.polis.tag", "--tags", tagEntries},
		{EntryTypeAttestation, attestationTypeName, "--attestations", attestationEntries},
	}
}

// ContributorFor returns the contributor owning an entry type.
func ContributorFor(entryType string) (Contributor, bool) {
	for _, c := range Contributors() {
		if c.EntryType == entryType {
			return c, true
		}
	}
	return Contributor{}, false
}

// ─── where things live ──────────────────────────────────────────────

// siteDirs answers "where does this content type live on this site" by
// FOLLOWING THE POINTER — `.well-known/polis` names each bundle.json, and
// bundle.json declares each type's `dir`. Both are user-configurable, so a
// hardcoded content/pub.polis.core would be wrong on any site that moved one.
//
// Same shape as clone's dirResolver (epic 20), for the same reason.
type siteDirs struct {
	siteDir string
	bundles []*bundle.Bundle
	root    string // site-relative dir holding the core bundle's content
}

func newSiteDirs(siteDir string) *siteDirs {
	d := &siteDirs{siteDir: siteDir}

	for name, rel := range bundlePointers(siteDir) {
		b, err := bundle.LoadBundle(filepath.Join(siteDir, rel))
		if err != nil {
			continue
		}
		d.bundles = append(d.bundles, b)
		if name == "pub.polis.core" || b.Name == "pub.polis.core" {
			d.root = filepath.ToSlash(filepath.Dir(rel))
		}
	}

	// No pointer, or nothing it named could be loaded: fall back to the
	// default layout, which is what an un-customised site has anyway.
	if len(d.bundles) == 0 {
		if b, err := bundle.LoadBundle(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")); err == nil {
			d.bundles = append(d.bundles, b)
		} else {
			d.bundles = append(d.bundles, bundle.DefaultCoreBundle())
		}
	}
	if d.root == "" {
		d.root = "content/pub.polis.core"
	}

	// Deterministic type lookup: bundle order comes from a map otherwise.
	sort.Slice(d.bundles, func(i, j int) bool { return d.bundles[i].Name < d.bundles[j].Name })

	return d
}

// bundlePointers reads the `bundles` block of .well-known/polis as
// name → site-relative path of its bundle.json. A minimal local read, like
// clone's: the full identity document has many fields this has no business
// modelling.
func bundlePointers(siteDir string) map[string]string {
	data, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis"))
	if err != nil {
		return nil
	}
	var wk struct {
		Bundles map[string]struct {
			Path string `json:"path"`
		} `json:"bundles"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return nil
	}
	out := map[string]string{}
	for name, entry := range wk.Bundles {
		if entry.Path != "" {
			out[name] = filepath.FromSlash(strings.TrimPrefix(entry.Path, "/"))
		}
	}
	return out
}

// contentRel returns the site-relative source directory for a content type,
// falling back to the default layout leaf when nothing declares it.
func (d *siteDirs) contentRel(typeName, fallbackLeaf string) string {
	for _, b := range d.bundles {
		if dir, err := b.ContentDir(typeName); err == nil {
			return filepath.ToSlash(dir)
		}
	}
	return d.root + "/" + fallbackLeaf
}

// contentAbs is contentRel rooted at the site directory.
func (d *siteDirs) contentAbs(typeName, fallbackLeaf string) string {
	return filepath.Join(d.siteDir, filepath.FromSlash(d.contentRel(typeName, fallbackLeaf)))
}

// IndexPath returns the absolute path of index.jsonl for a site, resolved
// through the bundle pointer rather than assumed.
func IndexPath(siteDir string) string {
	return newSiteDirs(siteDir).indexPath()
}

func (d *siteDirs) indexPath() string {
	return filepath.Join(d.siteDir, filepath.FromSlash(d.root), metadata.PublicIndexFilename)
}

// relPath converts an absolute path under the site into the slash-separated
// site-relative form every index entry uses.
func (d *siteDirs) relPath(abs string) string {
	rel, err := filepath.Rel(d.siteDir, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// ─── the contributors ───────────────────────────────────────────────

// walkFiles visits every non-directory file under dir with the given suffix,
// skipping version snapshots. A missing directory yields nothing and is not an
// error: a site with no attestations is not a broken site.
func walkFiles(dir, suffix string, visit func(path string)) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Unreadable entry: don't abort the walk, and don't lose it either —
			// hand it to the contributor, whose read fails and COUNTS the skip.
			// A subdirectory that cannot be listed is counted once.
			if info == nil || info.IsDir() || strings.HasSuffix(path, suffix) {
				visit(path)
			}
			return nil
		}
		if info.IsDir() {
			if info.Name() == ".versions" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), suffix) {
			return nil
		}
		visit(path)
		return nil
	})
}

// postEntries indexes the markdown under the post type's directory.
//
// ⚠️ `current-version` is read from FRONTMATTER first and only computed when
// absent — the frontmatter value is what was signed and what `publish` wrote,
// and recomputing it would quietly replace a signed fact with a fresh
// computation. Bash has always read it this way; the Go rebuild computed it
// unconditionally, which is one of the ways its lines stopped matching
// publish's.
func postEntries(d *siteDirs) ([]metadata.IndexEntry, int, error) {
	return markdownEntries(d, EntryTypePost, d.contentAbs("pub.polis.post", "post"), false)
}

// commentEntries indexes the markdown under the comment type's directory.
func commentEntries(d *siteDirs) ([]metadata.IndexEntry, int, error) {
	return markdownEntries(d, EntryTypeComment, d.contentAbs("pub.polis.comment", "comment"), true)
}

func markdownEntries(d *siteDirs, entryType, dir string, withReply bool) ([]metadata.IndexEntry, int, error) {
	var entries []metadata.IndexEntry
	skipped := 0
	err := walkFiles(dir, ".md", func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			// Walked and not indexed: counted like every other skip, never
			// dropped in silence (close-out F15).
			skipped++
			return
		}
		content := string(data)
		fm, body := parseFrontmatter(content)

		// A file that declares a different type than the directory it sits in
		// belongs to that type's contributor, not this one. Bash keys the whole
		// walk on `type:` for the same reason.
		if declared := fm["type"]; declared != "" && declared != entryType {
			return // another contributor's file; not a skip
		}

		published := fm["published"]
		if published == "" {
			published = fm["timestamp"] // webapp-written comments
		}
		if published == "" {
			// An entry with no `published` fails the site's own F6 integrity
			// check and cannot be ordered. Skipping is what bash does; the
			// count is reported so it is never silent.
			skipped++
			return
		}

		version := fm["current-version"]
		if version == "" {
			version = fm["comment_version"]
		}
		if version == "" {
			version = hashBody(body)
		}

		entry := metadata.IndexEntry{
			Type:           entryType,
			Path:           d.relPath(path),
			Title:          fm["title"],
			Published:      published,
			CurrentVersion: version,
			License:        license.ParseBlock(content),
		}
		if withReply {
			entry.InReplyTo = parseInReplyTo(content)
		}
		entries = append(entries, entry)
	})
	return entries, skipped, err
}

// tagEntries indexes the signed tag files.
//
// D4 — tag and attestation are content types under the same bundle with the
// same path/published/current_version shape, and index.jsonl already carries a
// `type` field and already serves two types. A sibling manifest would be a
// second thing to discover, a second thing to keep in sync, and a second thing
// for a consumer to miss.
func tagEntries(d *siteDirs) ([]metadata.IndexEntry, int, error) {
	var entries []metadata.IndexEntry
	skipped := 0
	err := walkFiles(d.contentAbs("pub.polis.tag", "tag"), ".json", func(path string) {
		// The fields of tag.TagFile an entry needs — see the import note.
		var tf struct {
			Tag     string `json:"tag"`
			Created string `json:"created"`
			Updated string `json:"updated"`
			Version string `json:"current_version"`
		}
		if !readJSON(path, &tf) {
			skipped++
			return
		}
		published := tf.Created
		if published == "" {
			published = tf.Updated
		}
		if !indexable(published, tf.Version) {
			skipped++
			return
		}
		entries = append(entries, metadata.IndexEntry{
			Type:           EntryTypeTag,
			Path:           d.relPath(path),
			Title:          tf.Tag,
			Published:      published,
			CurrentVersion: tf.Version,
		})
	})
	return entries, skipped, err
}

// attestationEntries indexes the signed attestation records.
//
// ⚠️ `title` carries the PREDICATE. A reader that does not recognise the
// predicate still gets a legible label, which is the same tolerance rule the
// record format itself follows.
func attestationEntries(d *siteDirs) ([]metadata.IndexEntry, int, error) {
	var entries []metadata.IndexEntry
	skipped := 0
	err := walkFiles(d.contentAbs(attestationTypeName, "attestation"), ".json", func(path string) {
		// The fields of attestation.Record an entry needs — see the import note.
		var r struct {
			Predicate string `json:"predicate"`
			Asserted  string `json:"asserted"`
			Version   string `json:"current_version"`
		}
		if !readJSON(path, &r) {
			skipped++
			return
		}
		if !indexable(r.Asserted, r.Version) {
			skipped++
			return
		}
		entries = append(entries, metadata.IndexEntry{
			Type:           EntryTypeAttestation,
			Path:           d.relPath(path),
			Title:          r.Predicate,
			Published:      r.Asserted,
			CurrentVersion: r.Version,
		})
	})
	return entries, skipped, err
}

// readJSON decodes a record file, reporting whether it could.
func readJSON(path string, v any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

// indexable reports whether a JSON record carries what Patrol's F6 check
// demands of EVERY index line: a `published` that parses as RFC 3339 and a
// `current_version` that starts `sha256:`.
//
// ⛔ Patrol fails the WHOLE SITE on the first line it rejects, not the line.
// Since epic 37 these entries are written on every issue and healed by Medic
// across the fleet, so one hand-edited record with `asserted: yesterday` must
// cost one skipped entry — reported in the rebuild's skip count — and never a
// site-wide integrity failure. Our own writers always produce valid values;
// this guards what they did not write.
func indexable(published, version string) bool {
	if published == "" || !strings.HasPrefix(version, "sha256:") {
		return false
	}
	_, err := time.Parse(time.RFC3339, published)
	return err == nil
}

// parseInReplyTo reads the nested in-reply-to block from a comment's
// frontmatter:
//
//	in-reply-to:
//	  url: https://…
//	  version: sha256:…
//
// ⚠️ Read with an explicit indentation walk rather than the flat frontmatter
// parser, which trims keys and would surface `url:` and `version:` as if they
// were top-level fields — the exact trap the licence block's key rules warn
// about. Falls back to the flat `in_reply_to:` form the webapp writes.
func parseInReplyTo(content string) *metadata.InReplyToEntry {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}

	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			break
		}
		if flat := strings.TrimPrefix(line, "in_reply_to:"); flat != line {
			if v := strings.TrimSpace(flat); v != "" {
				return &metadata.InReplyToEntry{URL: v}
			}
			continue
		}
		if strings.TrimSpace(line) != "in-reply-to:" {
			continue
		}
		entry := &metadata.InReplyToEntry{}
		for j := i + 1; j < len(lines); j++ {
			child := lines[j]
			if strings.TrimSpace(child) == "---" {
				break
			}
			if child == "" || (child[0] != ' ' && child[0] != '\t') {
				break // dedented: the block is over
			}
			key, value, ok := strings.Cut(strings.TrimSpace(child), ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(key) {
			case "url":
				entry.URL = strings.TrimSpace(value)
			case "version":
				entry.Version = strings.TrimSpace(value)
			}
		}
		if entry.URL == "" && entry.Version == "" {
			return nil
		}
		return entry
	}
	return nil
}

func hashBody(body string) string {
	sum := sha256.Sum256([]byte(canonicalizeContent(body)))
	return fmt.Sprintf("sha256:%x", sum)
}
