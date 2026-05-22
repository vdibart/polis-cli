package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/sitemap"
)

// PublishStream incrementally re-renders v4 output after a single post is published
// or republished. Compared to renderStreamAll (full corpus), PublishStream touches only
// the affected post + the neighbors whose sibling rail referenced it + the
// homepage. Per-publish work is bounded by maxStreamSiblings + 2, regardless of
// corpus size — the design goal of step 3.a.
//
// postSourceRelPath is the index-entry-style path
// ("content/pub.polis.core/post/YYYYMMDD/slug.md"). The corresponding URL is
// derived via sourceToMountPath so it matches what loadPublicIndex emits.
//
// Caller is responsible for confirming the tenant's active_shape is v4. This
// method has no v3 fallback by design — blog-shape tenants continue through their
// existing render path.
func (r *PageRenderer) PublishStream(postSourceRelPath string) error {
	posts, _, err := r.loadPublicIndex()
	if err != nil {
		return fmt.Errorf("load public index: %w", err)
	}

	if len(posts) == 0 {
		// publish.PublishPost should have appended to the index before this
		// runs. Empty corpus here is a race or test-fixture quirk; emit the
		// empty-state index so the tenant resolves to *something*.
		return r.renderStreamEmptyIndex()
	}

	targetURL := r.postSourceToURL(postSourceRelPath)
	affectedIdx := -1
	for i, p := range posts {
		if p.URL == targetURL {
			affectedIdx = i
			break
		}
	}

	rendered := make(map[string]struct{})
	bodyCache := make(map[string]siblingBodyCacheEntry)
	totalPosts := len(posts)

	// The affected post itself — content + frontmatter changed; excerpt may
	// have changed; always re-render with force=true.
	if affectedIdx >= 0 {
		focus := posts[affectedIdx]
		above, below := pickStreamSiblings(posts, affectedIdx)
		r.populateSiblingBodies(above, bodyCache)
		r.populateSiblingBodies(below, bodyCache)
		if err := r.renderStreamFile(focus, above, below, totalPosts, streamOutputPath(focus.URL), true); err != nil {
			return fmt.Errorf("re-render affected post: %w", err)
		}
		rendered[focus.URL] = struct{}{}
	}

	// Cascade: any post whose post-mutation sibling window includes the
	// affected post needs its sibling-body-of-the-affected-post refreshed.
	// With pickStreamSiblings's "older-first + pad-back-with-newer" rule a NEW
	// post at position 0 lands in the pad-back tail of every short-older
	// post, and a republish (same position) cascades to the same set. For
	// corpora where pad-back kicks in for everyone (n ≤ maxStreamSiblings+1 —
	// see plan 3.a task 3 edge case), that means the entire corpus cascades.
	for i, focus := range posts {
		if _, done := rendered[focus.URL]; done {
			continue
		}
		above, below := pickStreamSiblings(posts, i)
		// Cascade-detection: an affected URL appearing in either rail
		// triggers re-render. Above-rail inclusion is rare in practice
		// (default 1 newer + pad-back at corpus end) but mechanically
		// possible.
		hit := false
		for _, s := range above {
			if s.URL == targetURL {
				hit = true
				break
			}
		}
		if !hit {
			for _, s := range below {
				if s.URL == targetURL {
					hit = true
					break
				}
			}
		}
		if hit {
			r.populateSiblingBodies(above, bodyCache)
			r.populateSiblingBodies(below, bodyCache)
			if err := r.renderStreamFile(focus, above, below, totalPosts, streamOutputPath(focus.URL), true); err != nil {
				return fmt.Errorf("re-render cascade neighbor %s: %w", focus.URL, err)
			}
			rendered[focus.URL] = struct{}{}
		}
	}

	// /index.html — homepage focus is always the newest post. Re-render
	// unconditionally: focus may have changed (new publish), focus's content
	// may have changed (republish of newest), or this may be the first publish
	// into a previously-empty tenant.
	// Pass relative path; renderStreamFile auto-joins DataDir. Pre-joining here
	// would double-join when DataDir is relative (see v4.go renderStreamAll).
	_, indexBelow := pickStreamSiblings(posts, 0)
	r.populateSiblingBodies(indexBelow, bodyCache)
	if err := r.renderStreamFile(posts[0], nil, indexBelow, totalPosts, "index.html", true); err != nil {
		return fmt.Errorf("re-render index: %w", err)
	}

	// Sitemap: incremental InsertOrUpdate. New publish → add; republish →
	// update LastMod. Failure is non-fatal — posts/index are already on disk;
	// next render --force will reconcile.
	if err := r.updateSitemapForPost(postSourceRelPath); err != nil {
		fmt.Fprintf(os.Stderr, "[!] sitemap update for publish failed: %v\n", err)
	}

	return nil
}

// UnpublishStream cascades v4 re-renders after a post is removed from the corpus.
// The post's HTML is deleted; every neighbor in the pad-back zone is
// re-rendered (their sibling rail may have referenced the removed post via
// pickStreamSiblings's pad-back-with-newer logic); /index.html is re-rendered.
//
// Caller is responsible for: (a) confirming active_shape is v4; (b) ensuring
// the index entry has already been removed (e.g. via metadata.RemoveIndexEntry)
// so loadPublicIndex returns the post-removal corpus.
func (r *PageRenderer) UnpublishStream(postSourceRelPath string) error {
	posts, _, err := r.loadPublicIndex()
	if err != nil {
		return fmt.Errorf("load public index: %w", err)
	}

	// Delete the post's HTML. ENOENT is fine — earlier renders may not have
	// produced one (e.g. if the post was published+unpublished without a
	// render in between).
	targetURL := r.postSourceToURL(postSourceRelPath)
	htmlPath := filepath.Join(r.config.DataDir, streamOutputPath(targetURL))
	if err := os.Remove(htmlPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove post HTML %s: %w", htmlPath, err)
	}
	// Best-effort: drop the date directory if it's now empty. os.Remove fails
	// if the directory still has siblings, which is the desired behavior.
	_ = os.Remove(filepath.Dir(htmlPath))

	if len(posts) == 0 {
		// Last post unpublished — render the empty-state index.
		return r.renderStreamEmptyIndex()
	}

	// Pad-back zone: positions where (n-1-i) < maxStreamSiblings — i.e. posts with
	// fewer than maxStreamSiblings older posts. These are the only positions where
	// pickStreamSiblings's pad-back loop can run, so they are the only positions
	// whose sibling identities could have included the now-removed post. For
	// small corpora (n ≤ maxStreamSiblings) every post is in this zone and
	// everyone re-renders.
	bodyCache := make(map[string]siblingBodyCacheEntry)
	n := len(posts)
	for i, focus := range posts {
		olderCount := n - 1 - i
		if olderCount < maxStreamSiblings {
			above, below := pickStreamSiblings(posts, i)
			r.populateSiblingBodies(above, bodyCache)
			r.populateSiblingBodies(below, bodyCache)
			if err := r.renderStreamFile(focus, above, below, n, streamOutputPath(focus.URL), true); err != nil {
				return fmt.Errorf("re-render unpublish cascade %s: %w", focus.URL, err)
			}
		}
	}

	// /index.html — focus may have shifted to a new newest post; always
	// re-render.
	// Pass relative path; renderStreamFile auto-joins DataDir. Pre-joining here
	// would double-join when DataDir is relative (see v4.go renderStreamAll).
	_, indexBelow := pickStreamSiblings(posts, 0)
	r.populateSiblingBodies(indexBelow, bodyCache)
	if err := r.renderStreamFile(posts[0], nil, indexBelow, n, "index.html", true); err != nil {
		return fmt.Errorf("re-render index after unpublish: %w", err)
	}

	// Sitemap: drop the unpublished URL. Idempotent; non-fatal on failure.
	if err := r.removeSitemapEntry(targetURL); err != nil {
		fmt.Fprintf(os.Stderr, "[!] sitemap remove for unpublish failed: %v\n", err)
	}

	return nil
}

// postSourceToURL converts a content-source-relative path
// ("content/pub.polis.core/post/YYYYMMDD/slug.md") to the public URL
// ("/posts/YYYYMMDD/slug.html") that loadPublicIndex emits as PostData.URL.
// Centralizing this avoids drift between PublishStream's lookup key and the
// PostData URLs returned by the index loader.
func (r *PageRenderer) postSourceToURL(postSourceRelPath string) string {
	mountPath := r.sourceToMountPath(postSourceRelPath, "post")
	htmlPath := strings.TrimSuffix(mountPath, ".md") + ".html"
	return "/" + htmlPath
}

// updateSitemapForPost reads the existing sitemap, inserts-or-updates the
// entry for postSourceRelPath using the post's source-file mtime as LastMod,
// and writes atomically. Used by PublishStream. Source mtime tracks republishes
// correctly: RepublishPost rewrites the source file, advancing mtime even
// though the frontmatter "published" timestamp is preserved.
func (r *PageRenderer) updateSitemapForPost(postSourceRelPath string) error {
	htmlURL := r.postSourceToURL(postSourceRelPath)
	absURL := r.buildURL(strings.TrimPrefix(htmlURL, "/"))

	srcPath := filepath.Join(r.config.DataDir, postSourceRelPath)
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stat post source for sitemap: %w", err)
	}

	body, err := sitemap.Read(r.config.DataDir)
	if err != nil {
		return fmt.Errorf("read sitemap: %w", err)
	}
	updated, err := sitemap.InsertOrUpdate(body, sitemap.Entry{
		URL:     absURL,
		LastMod: info.ModTime(),
	})
	if err != nil {
		return fmt.Errorf("insert into sitemap: %w", err)
	}
	return sitemap.Write(r.config.DataDir, updated)
}

// removeSitemapEntry strips the entry for the given path-form URL (e.g.
// "/posts/YYYYMMDD/slug.html") from the sitemap. Used by UnpublishStream.
// Idempotent — absent URL is OK.
func (r *PageRenderer) removeSitemapEntry(htmlURL string) error {
	absURL := r.buildURL(strings.TrimPrefix(htmlURL, "/"))
	body, err := sitemap.Read(r.config.DataDir)
	if err != nil {
		return fmt.Errorf("read sitemap: %w", err)
	}
	updated, err := sitemap.Remove(body, absURL)
	if err != nil {
		return fmt.Errorf("remove from sitemap: %w", err)
	}
	return sitemap.Write(r.config.DataDir, updated)
}
