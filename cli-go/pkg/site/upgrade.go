package site

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// CleanV3LegacyArchives removes stale v3 SHAPE archive HTML files from a
// tenant that has migrated to v4 SHAPE. v3's RenderAll emitted flat
// archive pages (posts/index.html, comments/index.html, tag/index.html,
// comment/index.html) that v4's renderStreamAll does NOT emit AND does
// NOT delete. Pre-migration tenants accumulate these as dead-on-disk
// artifacts that get served by static-file fallthrough with stale
// content — visitors see HTTP 200 with old data, search engines keep
// indexing them.
//
// Returns the relative paths of files removed. No-op unless the active
// shape is one that writes no archive pages (v4, v5): on v3 those files are
// LIVE content. v5 is named explicitly, as v4 is: a shape this does not know
// is left alone. Errors during stat/remove are swallowed (best-effort
// remediation pattern); next call re-attempts.
//
// Pairs with the no-archive emission contract test in cli-go/pkg/render/stream_test.go which
// locks down the emission side; this helper handles the cleanup side
// for tenants that have legacy files on disk.
//
// Top-level only — deeper subdirectories (tag/<slug>.html,
// comment/YYYYMMDD/index.html) follow tenant-content-shaped naming
// and are out of scope for this clean. Operators wanting a deeper
// purge can run `find` manually; the user-visible breakage is on the
// index entry points.
func CleanV3LegacyArchives(site storage.Site) []string {
	shapeName, err := bundle.GetActiveShapeName(site)
	if err != nil || !writesNoArchives(shapeName) {
		return nil
	}
	candidates := []string{
		filepath.Join("posts", "index.html"),
		filepath.Join("comments", "index.html"),
		filepath.Join("tag", "index.html"),
		filepath.Join("comment", "index.html"),
	}
	var removed []string
	ctx := context.Background()
	for _, rel := range candidates {
		// ⚠️ The presence check keeps discarding its error, as before: a file
		// we cannot see is one we do not try to remove, and that was true of
		// PathExists too.
		if present, _ := site.Store().Exists(ctx, rel); !present {
			continue
		}
		if err := site.Store().Delete(ctx, rel); err == nil {
			removed = append(removed, rel)
		}
	}
	return removed
}

// V3LegacyArchivesExist reports whether any v3 archive file is still
// present on disk. Detection-only; no side effects. Returns false on
// any tenant not on active_shape=v4 (those files are live content for
// v3 tenants).
func V3LegacyArchivesExist(site storage.Site) bool {
	shapeName, err := bundle.GetActiveShapeName(site)
	if err != nil || !writesNoArchives(shapeName) {
		return false
	}
	candidates := []string{
		filepath.Join("posts", "index.html"),
		filepath.Join("comments", "index.html"),
		filepath.Join("tag", "index.html"),
		filepath.Join("comment", "index.html"),
	}
	for _, rel := range candidates {
		if present, _ := site.Store().Exists(context.Background(), rel); present {
			return true
		}
	}
	return false
}

// writesNoArchives: the shapes whose render emits none of v3's flat archive
// pages, so on them any such file is stale.
func writesNoArchives(shape string) bool {
	return shape == "v4" || shape == "v5"
}

// CleanStrayRenderedPages removes the HTML pages a misconfigured render left
// beside their signed sources under content/ (a post's or comment's page
// written at content/pub.polis.core/<type>/<date>/<slug>.html instead of its
// mount, /posts/ or /comments/). Every hosted re-render built its renderer
// without the bundle's folders until 2026-11-17 and did exactly that. Such a
// page is never served (the server redirects a source's .html address to its
// mount), so it is dead output in the source tree.
//
// ⛔ Only a .html whose .md source sits beside it is removed: that pairing is
// what proves it a rendered copy of a source, never something an author put
// there. Nothing signed is touched. Returns the paths removed.
func CleanStrayRenderedPages(site storage.Site) []string {
	ctx := context.Background()
	var removed []string
	for _, root := range []string{"content/pub.polis.core/post", "content/pub.polis.core/comment"} {
		files, _ := site.Store().List(ctx, root, storage.Recursive())
		have := map[string]bool{}
		for _, f := range files {
			have[f.Key] = true
		}
		for _, f := range files {
			if f.IsDir || !strings.HasSuffix(f.Key, ".html") {
				continue
			}
			if !have[strings.TrimSuffix(f.Key, ".html")+".md"] {
				continue
			}
			if err := site.Store().Delete(ctx, f.Key); err == nil {
				removed = append(removed, f.Key)
			}
		}
	}
	return removed
}
