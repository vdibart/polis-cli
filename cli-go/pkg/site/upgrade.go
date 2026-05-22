package site

import (
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
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
// Returns the relative paths of files removed. No-op when active_shape
// is not v4 — the tenant is still on v3 and those archive files are
// LIVE content. Errors during stat/remove are swallowed (best-effort
// remediation pattern); next call re-attempts.
//
// Closes R17-2 (operational-hardening.md). Pairs with the no-archive
// emission contract test in cli-go/pkg/render/stream_test.go which
// locks down the emission side; this helper handles the cleanup side
// for tenants that have legacy files on disk.
//
// Top-level only — deeper subdirectories (tag/<slug>.html,
// comment/YYYYMMDD/index.html) follow tenant-content-shaped naming
// and are out of scope for this clean. Operators wanting a deeper
// purge can run `find` manually; the user-visible breakage is on the
// index entry points.
func CleanV3LegacyArchives(siteDir string) []string {
	shapeName, err := bundle.GetActiveShapeName(siteDir)
	if err != nil || shapeName != "v4" {
		return nil
	}
	candidates := []string{
		filepath.Join("posts", "index.html"),
		filepath.Join("comments", "index.html"),
		filepath.Join("tag", "index.html"),
		filepath.Join("comment", "index.html"),
	}
	var removed []string
	for _, rel := range candidates {
		full := filepath.Join(siteDir, rel)
		if _, err := os.Stat(full); err != nil {
			continue
		}
		if err := os.Remove(full); err == nil {
			removed = append(removed, rel)
		}
	}
	return removed
}

// V3LegacyArchivesExist reports whether any v3 archive file is still
// present on disk. Detection-only; no side effects. Returns false on
// any tenant not on active_shape=v4 (those files are live content for
// v3 tenants).
func V3LegacyArchivesExist(siteDir string) bool {
	shapeName, err := bundle.GetActiveShapeName(siteDir)
	if err != nil || shapeName != "v4" {
		return false
	}
	candidates := []string{
		filepath.Join("posts", "index.html"),
		filepath.Join("comments", "index.html"),
		filepath.Join("tag", "index.html"),
		filepath.Join("comment", "index.html"),
	}
	for _, rel := range candidates {
		if _, err := os.Stat(filepath.Join(siteDir, rel)); err == nil {
			return true
		}
	}
	return false
}
