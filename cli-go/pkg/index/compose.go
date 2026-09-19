package index

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
)

// ContentIndexResult reports what a content-index rebuild did, PER TYPE.
//
// ⚠️ The per-type split is the point, not decoration. A silent "8 posts
// rebuilt" is exactly what let R24-9's comment loss hide for months: the
// command reported the half it knew about and said nothing about the half it
// deleted. Preserved counts make the other half visible.
type ContentIndexResult struct {
	// Rebuilt counts the lines each contributor produced this run.
	Rebuilt map[string]int `json:"rebuilt"`
	// Preserved counts the lines carried through byte-identically because no
	// contributor in this run owned them.
	Preserved map[string]int `json:"preserved"`
	// Skipped counts files a contributor walked and did not index — a post
	// with no `published`, a tag with no `current_version`. Reported rather
	// than silent.
	Skipped map[string]int `json:"skipped,omitempty"`
	// Total is the number of lines in the composed file.
	Total int `json:"total"`
	// Changed is false when the composed bytes equal what was already on disk.
	Changed bool `json:"changed"`

	// EntriesChanged is true when the REBUILT TYPES' lines differ from the ones
	// that were there, IGNORING ORDER.
	//
	// ⚠️ Changed is the wrong question for a background healer. It also flips
	// when only the ORDER of the file differs — an index built by appends whose
	// `published` values are not monotonic — so a Medic gated on it would
	// re-sort every such tenant's posts once, fleet-wide, while claiming to have
	// healed attestations. This asks only "is anything of mine missing, extra,
	// or different?" (Signet epic 37 D2.)
	EntriesChanged bool `json:"-"`
}

func newContentIndexResult() *ContentIndexResult {
	return &ContentIndexResult{
		Rebuilt:   map[string]int{},
		Preserved: map[string]int{},
		Skipped:   map[string]int{},
	}
}

// PostsRebuilt is the post count, for callers that only ever wanted that one.
func (r *ContentIndexResult) PostsRebuilt() int { return r.Rebuilt[EntryTypePost] }

// indexLine is one line of index.jsonl, kept as the BYTES THAT WERE THERE plus
// the three fields composition needs. A preserved line is re-emitted from `raw`
// and never re-marshalled — re-marshalling through any struct would silently
// drop every field that struct does not model, which is how `license` and
// `in_reply_to` disappear from a rewritten line.
type indexLine struct {
	raw       []byte
	typ       string
	published string
	path      string
}

// parseIndexLines splits an index file into lines, keeping each one's bytes.
// Unparseable lines are kept too, with empty fields: this file is a projection
// of content we may not own, and discarding a line we cannot read is the
// destructive choice.
func parseIndexLines(data []byte) []indexLine {
	var out []indexLine
	for _, raw := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		line := indexLine{raw: raw}
		var fields struct {
			Type      string `json:"type"`
			Path      string `json:"path"`
			Published string `json:"published"`
		}
		if json.Unmarshal(raw, &fields) == nil {
			line.typ = fields.Type
			line.published = fields.Published
			line.path = fields.Path
		}
		out = append(out, line)
	}
	return out
}

// composeIndex replaces the lines owned by the rebuilt types and carries every
// other line through unchanged.
//
// ⚠️ Ordering is ASCENDING by `published`, which is what an append-built index
// already has and what three separate consumers assume — `render.loadPublicIndex`,
// `ops.listPosts` and the webapp's `/api/posts` all reverse the file to get
// newest-first. The pre-fix rebuild sorted DESCENDING, so a `rebuild --posts`
// silently flipped the site's post order as well as deleting its comments.
func composeIndex(existing []byte, rebuilt map[string][]metadata.IndexEntry) ([]byte, *ContentIndexResult, error) {
	result := newContentIndexResult()

	var lines []indexLine
	var replaced, produced []string
	for _, line := range parseIndexLines(existing) {
		if _, owned := rebuilt[line.typ]; owned {
			replaced = append(replaced, string(line.raw))
			continue // this run regenerates it
		}
		typ := line.typ
		if typ == "" {
			typ = "(unreadable)"
		}
		result.Preserved[typ]++
		lines = append(lines, line)
	}

	for typ, entries := range rebuilt {
		result.Rebuilt[typ] = len(entries)
		for i := range entries {
			raw, err := json.Marshal(entries[i])
			if err != nil {
				return nil, nil, fmt.Errorf("marshal %s entry %s: %w", typ, entries[i].Path, err)
			}
			produced = append(produced, string(raw))
			lines = append(lines, indexLine{
				raw:       raw,
				typ:       entries[i].Type,
				published: entries[i].Published,
				path:      entries[i].Path,
			})
		}
	}

	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].published != lines[j].published {
			return lines[i].published < lines[j].published
		}
		if lines[i].path != lines[j].path {
			return lines[i].path < lines[j].path
		}
		return bytes.Compare(lines[i].raw, lines[j].raw) < 0
	})

	var buf bytes.Buffer
	for _, line := range lines {
		buf.Write(line.raw)
		buf.WriteByte('\n')
	}

	result.Total = len(lines)
	result.Changed = !bytes.Equal(buf.Bytes(), existing)
	result.EntriesChanged = !sameLines(replaced, produced)
	return buf.Bytes(), result, nil
}

// sameLines reports whether two sets of raw lines are equal as multisets.
func sameLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// PlanContentIndex computes what index.jsonl would become, WITHOUT writing it.
//
// `only` names the entry types to rebuild; an empty slice rebuilds every
// contributor. Everything not named is preserved byte-identically.
func PlanContentIndex(siteDir string, only []string) ([]byte, *ContentIndexResult, error) {
	d := newSiteDirs(siteDir)

	wanted := map[string]bool{}
	for _, t := range only {
		wanted[t] = true
	}

	rebuilt := map[string][]metadata.IndexEntry{}
	skipped := map[string]int{}
	for _, c := range Contributors() {
		if len(wanted) > 0 && !wanted[c.EntryType] {
			continue
		}
		entries, skip, err := c.Entries(d)
		if err != nil {
			return nil, nil, fmt.Errorf("contributor %s: %w", c.EntryType, err)
		}
		if entries == nil {
			entries = []metadata.IndexEntry{}
		}
		rebuilt[c.EntryType] = entries
		if skip > 0 {
			skipped[c.EntryType] = skip
		}
	}

	existing, err := os.ReadFile(d.indexPath())
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("read index: %w", err)
	}

	content, result, err := composeIndex(existing, rebuilt)
	if err != nil {
		return nil, nil, err
	}
	result.Skipped = skipped
	return content, result, nil
}

// RebuildContentIndex composes index.jsonl from the named contributors and
// writes it once.
//
// ⚠️ Mode is 0644. index.jsonl is PUBLIC content served over HTTP; the 0600 the
// pre-fix rebuild used would 403 the site's own index on a self-hosted
// split-user nginx setup. 0600 is right for `.polis/` state and wrong here —
// do not "fix" the sibling state writers to match this.
func RebuildContentIndex(siteDir string, only []string) (*ContentIndexResult, error) {
	content, result, err := PlanContentIndex(siteDir, only)
	if err != nil {
		return nil, err
	}

	path := newSiteDirs(siteDir).indexPath()

	// Nothing to change: leave the file, and its mtime, alone. Since epic 37
	// this runs on every tag and attestation write, and Patrol baselines file
	// mtimes — a rewrite of identical bytes is noise it would have to explain.
	if !result.Changed {
		if _, err := os.Stat(path); err == nil {
			return result, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if err := atomicfile.WriteFile(path, content, 0644); err != nil {
		return nil, err
	}
	return result, nil
}
