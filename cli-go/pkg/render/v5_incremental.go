package render

// ⭐ v5 RENDERS INCREMENTALLY after a post is published, republished or
// unpublished: the post's own page, the pages whose stream shows it (up to
// three newer and three older, more at an edge), the home page, the sitemap,
// and the licence surfaces only when the post's terms differ from the site's.
// The work per change does not grow with the number of posts.
//
// How it knows what changed: every render records, per post page, the inputs
// that page is drawn from (which posts it shows, and each one's version,
// title, date and comment count), and per post source its file time and
// whether its terms override the site's (v5Manifest). A change compares the
// index's inputs with the record and rewrites the pages that differ, plus the
// changed post's own page and every page showing it whatever the record says
// (a frontmatter-only edit leaves the version alone).
//
// ⭐ Byte-identical to a full render, with one stated exception: the toolbar's
// counts on a page the change did not rewrite stay as they were (Vincent,
// 2026-11-22). The controller replaces them from /pql/ as the page loads.
//
// ⛔ A change the record cannot bound renders the whole site: no record, a
// record made under another site key (the shape, the widget, the theme, the
// site's name, address, mark or terms), or a page it would keep that is not
// on disk.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// v5ManifestKey is the record: private, derived, and rebuilt by every full render.
const v5ManifestKey = ".polis/render/v5-pages.json"

type v5Manifest struct {
	SiteKey string                    `json:"site_key"`
	Pages   map[string]string         `json:"pages"`   // a post's page → a hash of its inputs
	Sources map[string]v5SourceRecord `json:"sources"` // a post's source → what the sitemap and robots need
}

type v5SourceRecord struct {
	Key      string `json:"key"`                // its card key when recorded
	Mod      string `json:"mod,omitempty"`      // its file's time, RFC 3339 with nanoseconds
	Override bool   `json:"override,omitempty"` // its terms differ from the site's (robots.txt names it)
}

// v5BlessedCounts is each post's blessed-comment count, from one read of the
// blessing list: the number a folded card shows, and what "with comments"
// counts. The same answer as loadBlessedCommentsForPost's length, per post.
func (r *PageRenderer) v5BlessedCounts(posts []v5Post) map[string]int {
	out := make(map[string]int, len(posts))
	bc, err := metadata.LoadBlessedComments(r.config.Site)
	if err != nil || bc == nil {
		return out
	}
	for _, p := range posts {
		for _, pc := range bc.Comments {
			if metadata.MatchesPostPath(pc.Post, p.source) {
				out[p.source] = len(pc.Blessed)
				break
			}
		}
	}
	return out
}

func v5CountsOf(posts []v5Post, comments []v5Comment, blessed map[string]int) v5Counts {
	c := v5Counts{posts: len(posts), comments: len(comments)}
	for _, p := range posts {
		if blessed[p.source] > 0 {
			c.withComments++
		}
	}
	return c
}

// v5CardKey is what a post's card is drawn from, as far as the index says.
func v5CardKey(p v5Post, blessed int) string {
	return strings.Join([]string{p.source, p.version, p.title, p.published, fmt.Sprint(blessed)}, "\x00")
}

// v5PageInputs is a hash of what post i's page shows: its neighbours' cards,
// folded, and its own, open.
func v5PageInputs(keys []string, i int) string {
	above, below := v5Neighbours(len(keys), i)
	h := sha256.New()
	for _, k := range above {
		fmt.Fprintf(h, "f\x00%s\x01", keys[k])
	}
	fmt.Fprintf(h, "o\x00%s\x01", keys[i])
	for _, k := range below {
		fmt.Fprintf(h, "f\x00%s\x01", keys[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// v5SiteKey is everything every page carries that no single post decides. A
// change to any of it is a full render.
func (r *PageRenderer) v5SiteKey() string {
	terms, _ := site.SiteTerms(r.config.Site)
	h := sha256.Sum256([]byte(strings.Join([]string{
		bundle.V5ShapeVersion, WidgetVersion, r.themeName, r.config.BaseURL,
		r.getSiteTitle(), r.getAuthorDomain(), r.getAuthorName(),
		theme.V5FontsHref(r.v5theme), r.v5Mark(), license.ContentUsage(terms),
	}, "\x00")))
	return hex.EncodeToString(h[:])
}

func (r *PageRenderer) loadV5Manifest() *v5Manifest {
	data, err := r.config.Site.Store().Get(context.Background(), v5ManifestKey)
	if err != nil {
		return nil
	}
	var m v5Manifest
	if json.Unmarshal(data, &m) != nil || m.Pages == nil || m.Sources == nil {
		return nil
	}
	return &m
}

func (r *PageRenderer) saveV5Manifest(m *v5Manifest) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := r.config.Site.Store().Put(context.Background(), v5ManifestKey, data, storage.Rebuildable()); err != nil {
		fmt.Fprintf(os.Stderr, "[!] could not record the v5 render: %v\n", err)
	}
}

// v5SourceMod is a source's file time, as the sitemap dates it.
func (r *PageRenderer) v5SourceMod(key string) time.Time {
	if info, err := r.config.Site.Store().Stat(context.Background(), key); err == nil {
		return info.ModTime
	}
	return time.Time{}
}

// v5Override reports whether a post's own terms differ from the site's, as
// robots.txt decides (site.IsLicenseOverride).
func (r *PageRenderer) v5Override(source string) bool {
	data, err := r.config.Site.Store().Get(context.Background(), source)
	if err != nil {
		return false
	}
	return site.IsLicenseOverride(r.config.Site, data)
}

// v5Record builds the record of a render that drew these posts.
func (r *PageRenderer) v5Record(posts []v5Post, comments []v5Comment, blessed map[string]int, mods map[string]time.Time, overrides map[string]bool) *v5Manifest {
	m := &v5Manifest{SiteKey: r.v5SiteKey(), Pages: map[string]string{}, Sources: map[string]v5SourceRecord{}}
	keys := make([]string, len(posts))
	for i, p := range posts {
		keys[i] = v5CardKey(p, blessed[p.source])
	}
	for i, p := range posts {
		m.Pages[p.page] = v5PageInputs(keys, i)
		rec := v5SourceRecord{Key: keys[i], Override: overrides[p.source]}
		if t := mods[p.source]; !t.IsZero() {
			rec.Mod = t.UTC().Format(time.RFC3339Nano)
		}
		m.Sources[p.source] = rec
	}
	// The site's own comments, for their file times only: a change to one is
	// a full render, so their pages are not tracked here.
	for _, c := range comments {
		rec := v5SourceRecord{Key: "comment"}
		if t := mods[c.source]; !t.IsZero() {
			rec.Mod = t.UTC().Format(time.RFC3339Nano)
		}
		m.Sources[c.source] = rec
	}
	return m
}

// RenderV5Change renders a v5 site after the post at postSource changed
// (published, republished or unpublished). It reports whether it could do so
// incrementally; when it could not, it rendered the whole site.
func (r *PageRenderer) RenderV5Change(postSource string) (*RenderStats, bool, error) {
	postSource = strings.TrimPrefix(postSource, "/")
	m := r.loadV5Manifest()
	r.v5theme, _, _ = theme.LoadV5SiteTheme(r.config.Site, r.themeName)
	if m == nil || m.SiteKey != r.v5SiteKey() {
		stats, err := r.renderV5All(true)
		return stats, false, err
	}
	ctx := context.Background()
	if ok, _ := r.config.Site.Store().Exists(ctx, "index.html"); !ok {
		stats, err := r.renderV5All(true)
		return stats, false, err
	}
	posts, comments, err := r.loadV5Items()
	if err != nil {
		return nil, false, fmt.Errorf("load the public index: %w", err)
	}
	blessed := r.v5BlessedCounts(posts)
	keys := make([]string, len(posts))
	target := -1
	for i, p := range posts {
		keys[i] = v5CardKey(p, blessed[p.source])
		if p.source == postSource {
			target = i
		}
	}

	// The pages to draw: every page whose inputs changed, the changed post's
	// own page, and every page that shows it.
	draw := map[int]bool{}
	for i, p := range posts {
		if m.Pages[p.page] != v5PageInputs(keys, i) {
			draw[i] = true
		}
	}
	if target >= 0 {
		draw[target] = true
		for i := range posts {
			above, below := v5Neighbours(len(posts), i)
			for _, k := range append(above, below...) {
				if k == target {
					draw[i] = true
				}
			}
		}
	}
	// A page it would keep must be there; one that is not means the record
	// cannot be trusted.
	for i, p := range posts {
		if draw[i] {
			continue
		}
		if ok, _ := r.config.Site.Store().Exists(ctx, p.page); !ok {
			stats, err := r.renderV5All(true)
			return stats, false, err
		}
	}

	// The folded cards those pages and the home page show, drawn once each.
	need := map[int]bool{}
	for i := range draw {
		above, below := v5Neighbours(len(posts), i)
		for _, k := range append(above, below...) {
			need[k] = true
		}
	}
	for i := 0; i < len(posts) && i <= v5NeighbourBudget; i++ {
		need[i] = true
	}
	folded := make([]string, len(posts))
	for i := range need {
		card, _, err := r.v5PostCard(posts[i], false)
		if err != nil {
			return nil, true, err
		}
		folded[i] = card
	}
	counts := v5CountsOf(posts, comments, blessed)
	stats := &RenderStats{}
	for i := range draw {
		card, head, err := r.v5PostCard(posts[i], true)
		if err != nil {
			return nil, true, err
		}
		if err := r.writeV5Page(posts[i].page, "posts", true, v5Stream(folded, i, card), posts[i].title, head, "", counts); err != nil {
			return nil, true, err
		}
		stats.PostsRendered++
	}
	if err := r.writeV5Index(posts, folded, counts); err != nil {
		return nil, true, err
	}
	stats.IndexGenerated = true

	// The sitemap, dating each page by its source: from the record for a
	// source the change did not touch, from the file for one it did.
	mods := map[string]time.Time{}
	overrides := map[string]bool{}
	recheckTerms := false
	for _, p := range posts {
		rec, ok := m.Sources[p.source]
		if ok && rec.Key == v5CardKey(p, blessed[p.source]) && p.source != postSource {
			if t, err := time.Parse(time.RFC3339Nano, rec.Mod); err == nil {
				mods[p.source] = t
			}
			overrides[p.source] = rec.Override
			continue
		}
		mods[p.source] = r.v5SourceMod(p.source)
		overrides[p.source] = r.v5Override(p.source)
		if overrides[p.source] || rec.Override {
			recheckTerms = true
		}
	}
	for _, c := range comments {
		if rec, ok := m.Sources[c.source]; ok {
			if t, err := time.Parse(time.RFC3339Nano, rec.Mod); err == nil {
				mods[c.source] = t
				continue
			}
		}
		mods[c.source] = r.v5SourceMod(c.source)
	}
	if rec, ok := m.Sources[postSource]; ok && rec.Override && target < 0 {
		recheckTerms = true // an unpublished post that robots.txt named
	}
	if err := r.writeV5Sitemap(posts, comments, func(key string) time.Time {
		if t, ok := mods[key]; ok {
			return t
		}
		return r.v5SourceMod(key)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "[!] sitemap rebuild failed: %v\n", err)
	}
	if recheckTerms {
		if err := r.RenderLicenseSurfaces(); err != nil {
			return nil, true, fmt.Errorf("failed to render licence surfaces: %w", err)
		}
	}
	r.saveV5Manifest(r.v5Record(posts, comments, blessed, mods, overrides))
	return stats, true, nil
}
