package tailor

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// semverPattern matches a bare semver string like "0.42.0".
var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// downloadClient is the HTTP client used for the polis-full binary fetch in
// checkLatestCLIDownloaded. Bounded total request time so a stalled upstream
// can't hang the Tailor tick (sibling fix to R10-4).
var downloadClient = &http.Client{Timeout: 60 * time.Second}

// ── Phase 1: Well-known identity ────────────────────────────────────

// checkWellKnownVersion ensures the version field uses generator format.
func checkWellKnownVersion(ctx *runContext) CheckResult {
	const name = "wellknown-version"
	const reason = "CLI v0.50.0 changed the version field to generator format (polis-cli-go/X.Y.Z) for tool identification"

	wkPath := filepath.Join(ctx.siteDir, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
	if err != nil {
		return skip(name, "Cannot read .well-known/polis", reason)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return skip(name, "Cannot parse .well-known/polis as JSON", reason)
	}

	version, _ := raw["version"].(string)
	if version == "" {
		// Missing version — set it
		raw["version"] = ctx.generator
	} else if !semverPattern.MatchString(version) {
		// Already in generator format or some other format — pass
		return pass(name, fmt.Sprintf("Version already in generator format: %q", version))
	} else {
		// Bare semver — needs update
		raw["version"] = ctx.generator
	}

	if version == ctx.generator {
		return pass(name, fmt.Sprintf("Version already in generator format: %q", version))
	}

	actions := []Action{{Op: "update", Path: ".well-known/polis", Detail: fmt.Sprintf("%q → %q", version, ctx.generator)}}

	if ctx.dryRun {
		return fail(name, fmt.Sprintf("Version %q → %q", version, ctx.generator), reason, actions)
	}

	backupFile(ctx.siteDir, "", ".well-known/polis") // best-effort
	if err := site.SaveWellKnownRaw(ctx.siteDir, raw); err != nil {
		return fail(name, fmt.Sprintf("Failed to update: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("Updated %q → %q", version, ctx.generator), Reason: reason, Actions: actions}
}

// checkAuthorFieldMigration renames the legacy `author` field to `author_name`
// in .well-known/polis. Idempotent — no-op when only `author_name` is present.
// Must run before content-aware F9 (which flags an empty author_name).
//
// Mirror of patrol.checkAuthorFieldStale + site.MigrateAuthorField. Sibling
// to checkWellKnownLegacyConfig but kept separate because it's a value
// migration, not a key removal.
func checkAuthorFieldMigration(ctx *runContext) CheckResult {
	const name = "author-field-migration"
	const reason = "CLI renamed the .well-known/polis `author` field to `author_name` for consistency with content-type field naming"

	wkPath := filepath.Join(ctx.siteDir, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
	if err != nil {
		return skip(name, "cannot read .well-known/polis", reason)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return skip(name, "cannot parse .well-known/polis", reason)
	}
	author, hasAuthor := raw["author"].(string)
	_, hasAuthorName := raw["author_name"].(string)
	if !hasAuthor || hasAuthorName {
		return pass(name, "author_name already in use (or no author field)")
	}
	action := Action{Op: "migrate", Path: ".well-known/polis",
		Detail: fmt.Sprintf("rename `author` (%q) → `author_name`", author)}
	if ctx.dryRun {
		return fail(name, "legacy `author` field present; should rename to `author_name`", reason, []Action{action})
	}
	if err := site.MigrateAuthorField(ctx.siteDir); err != nil {
		return fail(name, fmt.Sprintf("MigrateAuthorField failed: %v", err), reason, []Action{action})
	}
	return CheckResult{Name: name, Status: StatusFail, Reason: reason,
		Message: fmt.Sprintf("renamed `author` (%q) → `author_name`", author),
		Actions: []Action{action}}
}

// checkWellKnownLegacyConfig removes old config.directories/config.files sections.
func checkWellKnownLegacyConfig(ctx *runContext) CheckResult {
	const name = "wellknown-legacy-config"
	const reason = "CLI v0.50.0 removed per-site directory config from .well-known/polis — paths are now defined by the bundle system"

	wkPath := filepath.Join(ctx.siteDir, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
	if err != nil {
		return skip(name, "Cannot read .well-known/polis", reason)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return skip(name, "Cannot parse .well-known/polis", reason)
	}

	// Legacy keys to remove
	legacyKeys := []string{"config", "domain", "base_url", "subdomain"}
	var removed []string
	for _, key := range legacyKeys {
		if _, exists := raw[key]; exists {
			removed = append(removed, key)
			if !ctx.dryRun {
				delete(raw, key)
			}
		}
	}

	if len(removed) == 0 {
		return pass(name, "No legacy config sections found")
	}

	actions := []Action{{Op: "update", Path: ".well-known/polis", Detail: fmt.Sprintf("remove keys: %s", strings.Join(removed, ", "))}}

	if ctx.dryRun {
		return fail(name, fmt.Sprintf("Legacy keys found: %s", strings.Join(removed, ", ")), reason, actions)
	}

	if err := site.SaveWellKnownRaw(ctx.siteDir, raw); err != nil {
		return fail(name, fmt.Sprintf("Failed to update: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("Removed legacy keys: %s", strings.Join(removed, ", ")), Reason: reason, Actions: actions}
}

// checkWellKnownBundles adds the bundles registry if missing.
func checkWellKnownBundles(ctx *runContext) CheckResult {
	const name = "wellknown-bundles"
	const reason = "CLI v0.58.0 introduced the bundle system — .well-known/polis must declare installed bundles for the CLI to locate content"

	wkPath := filepath.Join(ctx.siteDir, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
	if err != nil {
		return skip(name, "Cannot read .well-known/polis", reason)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return skip(name, "Cannot parse .well-known/polis", reason)
	}

	if bundles, ok := raw["bundles"]; ok {
		if m, ok := bundles.(map[string]interface{}); ok && len(m) > 0 {
			return pass(name, "Bundles registry present")
		}
	}

	actions := []Action{{Op: "update", Path: ".well-known/polis", Detail: "add bundles registry with pub.polis.core"}}

	if ctx.dryRun {
		return fail(name, "Missing bundles registry", reason, actions)
	}

	raw["bundles"] = map[string]interface{}{
		"pub.polis.core": map[string]interface{}{
			"path": "content/pub.polis.core/bundle.json",
		},
	}

	if err := site.SaveWellKnownRaw(ctx.siteDir, raw); err != nil {
		return fail(name, fmt.Sprintf("Failed to update: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Added bundles registry with pub.polis.core", Reason: reason, Actions: actions}
}

// ── Phase 2: Bundle definition ──────────────────────────────────────

// checkBundleJSON creates the bundle.json if missing.
func checkBundleJSON(ctx *runContext) CheckResult {
	const name = "bundle-json"
	const reason = "CLI v0.58.0 requires a bundle definition that declares content types, storage patterns, and notification rules"

	bundlePath := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "bundle.json")

	if fileExists(bundlePath) {
		// Exists — try loading to validate, and merge defaults
		existing, err := bundle.LoadBundle(bundlePath)
		if err != nil {
			return skip(name, fmt.Sprintf("bundle.json exists but invalid: %v", err), reason)
		}
		defaults := bundle.DefaultCoreBundle()
		if !existing.MergeDefaults(defaults) {
			return pass(name, "bundle.json present and up to date")
		}

		actions := []Action{{Op: "update", Path: "content/pub.polis.core/bundle.json", Detail: "merged missing content types from defaults"}}
		if ctx.dryRun {
			return fail(name, "bundle.json missing some default content types", reason, actions)
		}
		if err := bundle.SaveBundle(bundlePath, existing); err != nil {
			return fail(name, fmt.Sprintf("Failed to save merged bundle: %v", err), reason, actions)
		}
		return CheckResult{Name: name, Status: StatusFail, Message: "Merged missing content types into bundle.json", Reason: reason, Actions: actions}
	}

	actions := []Action{{Op: "create", Path: "content/pub.polis.core/bundle.json"}}

	if ctx.dryRun {
		return fail(name, "Missing bundle definition", reason, actions)
	}

	coreBundle := bundle.DefaultCoreBundle()
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0755); err != nil {
		return fail(name, fmt.Sprintf("Failed to create directory: %v", err), reason, actions)
	}
	if err := bundle.SaveBundle(bundlePath, coreBundle); err != nil {
		return fail(name, fmt.Sprintf("Failed to create bundle.json: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Created bundle.json with default core bundle", Reason: reason, Actions: actions}
}

// ── Phase 3: Layout migration ───────────────────────────────────────

// checkLayoutPosts moves .md + .versions/ from posts/YYYYMMDD/ → content/pub.polis.core/post/YYYYMMDD/.
func checkLayoutPosts(ctx *runContext) CheckResult {
	const name = "layout-posts"
	const reason = "CLI v0.58.0 moved post sources into content/pub.polis.core/post/ — rendered HTML is placed at the posts/ mount point by the render pass"

	oldPostsDir := filepath.Join(ctx.siteDir, "posts")
	if !dirExists(oldPostsDir) {
		return pass(name, "No posts/ directory found")
	}

	// Scan for date directories containing .md files
	var actions []Action
	mdCount := 0

	dateDirs, err := os.ReadDir(oldPostsDir)
	if err != nil {
		return skip(name, fmt.Sprintf("Cannot read posts/: %v", err), reason)
	}

	for _, dateEntry := range dateDirs {
		if !dateEntry.IsDir() {
			continue
		}
		dateDir := dateEntry.Name()
		oldDatePath := filepath.Join(oldPostsDir, dateDir)
		newDatePath := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "post", dateDir)

		// Check for .md files in this date dir
		files, _ := os.ReadDir(oldDatePath)
		for _, f := range files {
			if f.IsDir() && f.Name() == ".versions" {
				// Move .versions/ directory
				oldVersions := filepath.Join(oldDatePath, ".versions")
				newVersions := filepath.Join(newDatePath, ".versions")
				if !fileExists(newVersions) {
					actions = append(actions, Action{
						Op:   "move",
						Path: filepath.ToSlash(filepath.Join("posts", dateDir, ".versions")) + "/",
						Dest: filepath.ToSlash(filepath.Join("content/pub.polis.core/post", dateDir, ".versions")) + "/",
					})
					if !ctx.dryRun {
						moveDir(oldVersions, newVersions)
					}
				}
				continue
			}
			if f.IsDir() {
				continue // Skip other dirs (.obsidian, etc.)
			}
			if !strings.HasSuffix(f.Name(), ".md") {
				continue // Only move .md files
			}

			oldPath := filepath.Join(oldDatePath, f.Name())
			newPath := filepath.Join(newDatePath, f.Name())
			if fileExists(newPath) {
				continue // Already migrated, skip
			}
			mdCount++
			actions = append(actions, Action{
				Op:   "move",
				Path: filepath.ToSlash(filepath.Join("posts", dateDir, f.Name())),
				Dest: filepath.ToSlash(filepath.Join("content/pub.polis.core/post", dateDir, f.Name())),
			})
			if !ctx.dryRun {
				moveFile(oldPath, newPath)
			}
		}
	}

	if len(actions) == 0 {
		return pass(name, "No posts to migrate")
	}

	msg := fmt.Sprintf("%d post(s) in old location", mdCount)
	if !ctx.dryRun {
		msg = fmt.Sprintf("Moved %d post(s) to content/pub.polis.core/post/", mdCount)
	}
	return fail(name, msg, reason, actions)
}

// checkLayoutComments moves .md from comments/ → content/pub.polis.core/comment/.
func checkLayoutComments(ctx *runContext) CheckResult {
	const name = "layout-comments"
	const reason = "CLI v0.58.0 moved comment sources into content/pub.polis.core/comment/"

	oldCommentsDir := filepath.Join(ctx.siteDir, "comments")
	if !dirExists(oldCommentsDir) {
		return pass(name, "No comments to migrate")
	}

	var actions []Action
	mdCount := 0

	err := filepath.Walk(oldCommentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		rel, _ := filepath.Rel(oldCommentsDir, path)
		newPath := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "comment", rel)
		if fileExists(newPath) {
			return nil
		}

		mdCount++
		actions = append(actions, Action{
			Op:   "move",
			Path: filepath.ToSlash(filepath.Join("comments", rel)),
			Dest: filepath.ToSlash(filepath.Join("content/pub.polis.core/comment", rel)),
		})
		if !ctx.dryRun {
			moveFile(path, newPath)
		}
		return nil
	})
	if err != nil {
		return skip(name, fmt.Sprintf("Error scanning comments/: %v", err), reason)
	}

	if len(actions) == 0 {
		return pass(name, "No comments to migrate")
	}

	msg := fmt.Sprintf("%d comment(s) in old location", mdCount)
	if !ctx.dryRun {
		msg = fmt.Sprintf("Moved %d comment(s) to content/pub.polis.core/comment/", mdCount)
	}
	return fail(name, msg, reason, actions)
}

// checkLayoutFollowing moves metadata/following.json → content/pub.polis.core/follow/following.json.
func checkLayoutFollowing(ctx *runContext) CheckResult {
	const name = "layout-following"
	const reason = "CLI v0.58.0 moved following.json into the core bundle at content/pub.polis.core/follow/"

	oldPath := filepath.Join(ctx.siteDir, "metadata", "following.json")
	newPath := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "follow", "following.json")

	if !fileExists(oldPath) {
		if fileExists(newPath) {
			return pass(name, "following.json already in new location")
		}
		return pass(name, "No following.json found")
	}

	if fileExists(newPath) {
		return pass(name, "following.json already in new location (old copy still exists)")
	}

	actions := []Action{{Op: "move", Path: "metadata/following.json", Dest: "content/pub.polis.core/follow/following.json"}}

	if ctx.dryRun {
		return fail(name, "following.json in old location", reason, actions)
	}

	if err := moveFile(oldPath, newPath); err != nil {
		return fail(name, fmt.Sprintf("Failed to move: %v", err), reason, actions)
	}

	// Update version string in the file
	updateJSONVersion(newPath, ctx.generator)

	return CheckResult{Name: name, Status: StatusFail, Message: "Moved following.json to core bundle", Reason: reason, Actions: actions}
}

// checkLayoutBlessed moves metadata/blessed-comments.json → content/pub.polis.core/comment/blessed.json.
func checkLayoutBlessed(ctx *runContext) CheckResult {
	const name = "layout-blessed"
	const reason = "CLI v0.58.0 moved blessed comments into the core bundle at content/pub.polis.core/comment/blessed.json"

	oldPath := filepath.Join(ctx.siteDir, "metadata", "blessed-comments.json")
	newPath := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "comment", "blessed.json")

	if !fileExists(oldPath) {
		if fileExists(newPath) {
			return pass(name, "blessed.json already in new location")
		}
		return pass(name, "No blessed-comments.json found")
	}

	if fileExists(newPath) {
		return pass(name, "blessed.json already in new location (old copy still exists)")
	}

	actions := []Action{{Op: "move", Path: "metadata/blessed-comments.json", Dest: "content/pub.polis.core/comment/blessed.json"}}

	if ctx.dryRun {
		return fail(name, "blessed-comments.json in old location", reason, actions)
	}

	if err := moveFile(oldPath, newPath); err != nil {
		return fail(name, fmt.Sprintf("Failed to move: %v", err), reason, actions)
	}

	// Update version string in the file
	updateJSONVersion(newPath, ctx.generator)

	return CheckResult{Name: name, Status: StatusFail, Message: "Moved blessed-comments.json to core bundle", Reason: reason, Actions: actions}
}

// ── Phase 4: Cross-platform fixes ───────────────────────────────────

// checkPathSeparators fixes Windows backslash → forward slash in JSON/JSONL metadata.
func checkPathSeparators(ctx *runContext) CheckResult {
	const name = "path-separators"
	const reason = "Backslash path separators from Windows cause lookup failures on other platforms and in the discovery service"

	// Files to check for backslash paths
	filesToCheck := []string{
		filepath.Join(ctx.siteDir, "content", "pub.polis.core", "index.jsonl"),
		filepath.Join(ctx.siteDir, "content", "pub.polis.core", "follow", "following.json"),
		filepath.Join(ctx.siteDir, "content", "pub.polis.core", "comment", "blessed.json"),
	}
	// Also check old locations
	filesToCheck = append(filesToCheck,
		filepath.Join(ctx.siteDir, "metadata", "public.jsonl"),
		filepath.Join(ctx.siteDir, "metadata", "following.json"),
		filepath.Join(ctx.siteDir, "metadata", "blessed-comments.json"),
	)

	var actions []Action
	fixCount := 0

	for _, path := range filesToCheck {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if !strings.Contains(content, "\\\\") && !strings.Contains(content, "\\") {
			continue
		}

		// Check if there are actual backslash path separators in JSON string values
		fixed := fixBackslashPaths(content)
		if fixed == content {
			continue
		}

		rel := relPath(ctx.siteDir, path)
		fixCount++
		actions = append(actions, Action{Op: "update", Path: rel, Detail: "fix backslash path separators"})

		if !ctx.dryRun {
			os.WriteFile(path, []byte(fixed), 0644)
		}
	}

	if fixCount == 0 {
		return pass(name, "No backslash path separators found")
	}

	msg := fmt.Sprintf("%d file(s) with backslash paths", fixCount)
	if !ctx.dryRun {
		msg = fmt.Sprintf("Fixed backslash paths in %d file(s)", fixCount)
	}
	return fail(name, msg, reason, actions)
}

// ── Phase 5: Derived data rebuild ───────────────────────────────────

// checkIndexRebuild rebuilds content/pub.polis.core/index.jsonl from post frontmatter.
func checkIndexRebuild(ctx *runContext) CheckResult {
	const name = "index-rebuild"
	const reason = "The content index must reference current paths — rebuilding from post frontmatter ensures consistency after layout migration"

	postsDir := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "post")
	indexPath := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "index.jsonl")

	// Also check for old index location
	oldIndexPath := filepath.Join(ctx.siteDir, "metadata", "public.jsonl")

	if !dirExists(postsDir) {
		// No posts directory yet — ensure empty index exists
		if !fileExists(indexPath) {
			actions := []Action{{Op: "create", Path: "content/pub.polis.core/index.jsonl", Detail: "empty index"}}
			if ctx.dryRun {
				return fail(name, "Missing content index", reason, actions)
			}
			os.MkdirAll(filepath.Dir(indexPath), 0755)
			os.WriteFile(indexPath, []byte{}, 0644)
			return CheckResult{Name: name, Status: StatusFail, Message: "Created empty content index", Reason: reason, Actions: actions}
		}
		return pass(name, "Content index exists (no posts to index)")
	}

	// Walk posts directory and build entries
	type indexEntry struct {
		Type           string `json:"type"`
		Path           string `json:"path"`
		Title          string `json:"title"`
		Published      string `json:"published"`
		CurrentVersion string `json:"current_version"`
	}

	var entries []indexEntry
	filepath.Walk(postsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && info.Name() == ".versions" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		fm := parseFrontmatter(string(content))
		rel := relPath(ctx.siteDir, path)

		entry := indexEntry{
			Type:           "post",
			Path:           rel,
			Title:          fm["title"],
			Published:      fm["published"],
			CurrentVersion: fm["current-version"],
		}

		// If current-version is missing, compute hash from body
		if entry.CurrentVersion == "" {
			body := stripFrontmatter(string(content))
			hash := sha256.Sum256([]byte(canonicalizeContent(body)))
			entry.CurrentVersion = fmt.Sprintf("sha256:%x", hash)
		}

		entries = append(entries, entry)
		return nil
	})

	// Sort by published date (newest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Published > entries[j].Published
	})

	// Build JSONL content
	var lines []string
	for _, entry := range entries {
		data, _ := json.Marshal(entry)
		lines = append(lines, string(data))
	}
	newContent := strings.Join(lines, "\n")
	if len(lines) > 0 {
		newContent += "\n"
	}

	var actions []Action
	needsRebuild := false

	// Check if old index exists at legacy location
	if fileExists(oldIndexPath) {
		actions = append(actions, Action{Op: "remove", Path: "metadata/public.jsonl"})
		if !ctx.dryRun {
			os.Remove(oldIndexPath)
		}
		needsRebuild = true
	}

	// Check if current index matches
	existingData, _ := os.ReadFile(indexPath)
	if string(existingData) != newContent {
		needsRebuild = true
	}

	if !needsRebuild && fileExists(indexPath) {
		return pass(name, fmt.Sprintf("Content index up to date (%d entries)", len(entries)))
	}

	actions = append(actions, Action{Op: "create", Path: "content/pub.polis.core/index.jsonl", Detail: fmt.Sprintf("%d entries", len(entries))})

	if ctx.dryRun {
		return fail(name, fmt.Sprintf("Content index needs rebuild (%d posts)", len(entries)), reason, actions)
	}

	os.MkdirAll(filepath.Dir(indexPath), 0755)
	os.WriteFile(indexPath, []byte(newContent), 0644)
	return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("Rebuilt content index with %d entries", len(entries)), Reason: reason, Actions: actions}
}

// ── Phase 5.5: Re-render site ───────────────────────────────────────

// checkRenderSite re-renders all posts and comments, producing HTML at mount points.
func checkRenderSite(ctx *runContext) CheckResult {
	const name = "render-site"
	const reason = "After layout migration, the site must be re-rendered so HTML appears at the correct mount points (posts/, comments/)"

	// Count source .md files in the new location
	postsDir := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "post")
	mdCount := countMDFiles(postsDir)

	// In dry-run mode, also count posts still at the old location (layout-posts
	// hasn't moved them yet), since they WILL be at the new location after apply.
	if ctx.dryRun {
		mdCount += countMDFiles(filepath.Join(ctx.siteDir, "posts"))
	}

	if mdCount == 0 {
		return pass(name, "No posts to render")
	}

	// Load bundle for source/mount path resolution
	coreBundle := loadOrDefaultBundle(ctx.siteDir)
	postsSource, _ := coreBundle.ContentDir("pub.polis.post")
	postsMountDir, _ := coreBundle.MountDir("pub.polis.post")
	commentsSource, _ := coreBundle.ContentDir("pub.polis.comment")
	commentsMountDir, _ := coreBundle.MountDir("pub.polis.comment")

	var actions []Action
	if postsMountDir != "" {
		actions = append(actions, Action{Op: "create", Path: postsMountDir + "/", Detail: fmt.Sprintf("render %d post(s) to mount point", mdCount)})
	}
	if commentsMountDir != "" {
		actions = append(actions, Action{Op: "create", Path: commentsMountDir + "/", Detail: "render comments to mount point"})
	}

	if ctx.dryRun {
		return fail(name, fmt.Sprintf("Would render %d post(s) to mount points", mdCount), reason, actions)
	}

	// Create renderer — theme is required
	renderer, err := render.NewPageRenderer(render.PageConfig{
		DataDir:           ctx.siteDir,
		BaseURL:           ctx.baseURL,
		RenderMarkers:     false,
		PostsSourceDir:    postsSource,
		PostsMountDir:     postsMountDir,
		CommentsSourceDir: commentsSource,
		CommentsMountDir:  commentsMountDir,
	})
	if err != nil {
		return skip(name, fmt.Sprintf("Cannot create renderer: %v — install a theme and run 'polis render'", err), reason)
	}

	// Force re-render everything
	stats, err := renderer.RenderAll(true)
	if err != nil {
		return skip(name, fmt.Sprintf("Render failed: %v — try running 'polis render' manually", err), reason)
	}

	// Update actions with actual counts
	actions = nil
	if stats.PostsRendered > 0 {
		actions = append(actions, Action{Op: "create", Path: postsMountDir + "/", Detail: fmt.Sprintf("rendered %d post(s)", stats.PostsRendered)})
	}
	if stats.CommentsRendered > 0 {
		actions = append(actions, Action{Op: "create", Path: commentsMountDir + "/", Detail: fmt.Sprintf("rendered %d comment(s)", stats.CommentsRendered)})
	}
	if stats.IndexGenerated {
		actions = append(actions, Action{Op: "create", Path: "index.html"})
	}
	if stats.ArchiveGenerated {
		actions = append(actions, Action{Op: "create", Path: postsMountDir + "/index.html"})
	}

	msg := fmt.Sprintf("Rendered %d post(s)", stats.PostsRendered)
	if stats.CommentsRendered > 0 {
		msg += fmt.Sprintf(", %d comment(s)", stats.CommentsRendered)
	}
	msg += " to mount points"

	return CheckResult{Name: name, Status: StatusFail, Message: msg, Reason: reason, Actions: actions}
}

// ── Phase 6: Provisioning ───────────────────────────────────────────

// checkPoliciesPublic creates policies/rules.jsonl if missing.
func checkPoliciesPublic(ctx *runContext) CheckResult {
	const name = "policies-public"
	const reason = "CLI v0.57.0 introduced the policy system for blessing auto-approval — public policies declare which comments are auto-blessed"

	policyPath := filepath.Join(ctx.siteDir, "policies", "rules.jsonl")

	if fileExists(policyPath) {
		return pass(name, "Public policy file exists")
	}

	actions := []Action{{Op: "create", Path: "policies/rules.jsonl"}}

	if ctx.dryRun {
		return fail(name, "Missing public policy file", reason, actions)
	}

	os.MkdirAll(filepath.Dir(policyPath), 0755)
	if err := os.WriteFile(policyPath, []byte(policy.DefaultPublicPolicyContent()), 0644); err != nil {
		return fail(name, fmt.Sprintf("Failed to create: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Created default public policy file", Reason: reason, Actions: actions}
}

// checkPoliciesPrivate creates .polis/policies/rules.jsonl if missing.
func checkPoliciesPrivate(ctx *runContext) CheckResult {
	const name = "policies-private"
	const reason = "CLI v0.57.0 introduced private policies for content type acceptance and DM filtering"

	policyPath := filepath.Join(ctx.siteDir, ".polis", "policies", "rules.jsonl")

	if fileExists(policyPath) {
		return pass(name, "Private policy file exists")
	}

	actions := []Action{{Op: "create", Path: ".polis/policies/rules.jsonl"}}

	if ctx.dryRun {
		return fail(name, "Missing private policy file", reason, actions)
	}

	os.MkdirAll(filepath.Dir(policyPath), 0700)
	if err := os.WriteFile(policyPath, []byte(policy.DefaultPrivatePolicyContent()), 0600); err != nil {
		return fail(name, fmt.Sprintf("Failed to create: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Created default private policy file", Reason: reason, Actions: actions}
}

// checkTagDirectory creates content/pub.polis.core/tag/ if missing.
func checkTagDirectory(ctx *runContext) CheckResult {
	const name = "tag-directory"
	const reason = "CLI v0.62.0 introduced pub.polis.tag content type for organizing content"

	tagDir := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "tag")

	if dirExists(tagDir) {
		return pass(name, "Tag directory present")
	}

	actions := []Action{{Op: "create", Path: "content/pub.polis.core/tag"}}

	if ctx.dryRun {
		return fail(name, "Missing tag directory", reason, actions)
	}

	if err := os.MkdirAll(tagDir, 0755); err != nil {
		return fail(name, fmt.Sprintf("Failed to create: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Created tag directory", Reason: reason, Actions: actions}
}

// ── Phase 7: Config migration ───────────────────────────────────────

// checkWebappConfig moves .polis/webapp-config.json → .polis/webapp/config.json.
func checkWebappConfig(ctx *runContext) CheckResult {
	const name = "webapp-config"
	const reason = "CLI v0.55.0 reorganized webapp config into .polis/webapp/ directory"

	oldPath := filepath.Join(ctx.siteDir, ".polis", "webapp-config.json")
	newPath := filepath.Join(ctx.siteDir, ".polis", "webapp", "config.json")

	if !fileExists(oldPath) {
		return pass(name, "No legacy webapp config to migrate")
	}

	if fileExists(newPath) {
		return pass(name, "Webapp config already in new location")
	}

	actions := []Action{{Op: "move", Path: ".polis/webapp-config.json", Dest: ".polis/webapp/config.json"}}

	if ctx.dryRun {
		return fail(name, "Legacy webapp config in old location", reason, actions)
	}

	if err := moveFile(oldPath, newPath); err != nil {
		return fail(name, fmt.Sprintf("Failed to move: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Moved webapp config to .polis/webapp/", Reason: reason, Actions: actions}
}

// ── Phase 8: Cleanup ────────────────────────────────────────────────

// checkManifestObsolete removes obsolete metadata/manifest.json.
func checkManifestObsolete(ctx *runContext) CheckResult {
	const name = "manifest-obsolete"
	const reason = "manifest.json is no longer used — post count, theme, and publish date are computed dynamically"

	manifestPath := filepath.Join(ctx.siteDir, "metadata", "manifest.json")

	if !fileExists(manifestPath) {
		return pass(name, "No obsolete manifest.json found")
	}

	actions := []Action{{Op: "remove", Path: "metadata/manifest.json"}}

	if ctx.dryRun {
		return fail(name, "Obsolete manifest.json found", reason, actions)
	}

	os.Remove(manifestPath)
	return CheckResult{Name: name, Status: StatusFail, Message: "Removed obsolete manifest.json", Reason: reason, Actions: actions}
}

// checkEmptyMetadataDir removes the metadata/ directory if empty.
func checkEmptyMetadataDir(ctx *runContext) CheckResult {
	const name = "empty-metadata-dir"
	const reason = "The metadata/ directory is replaced by the bundle layout — removing empty directory avoids confusion"

	metadataDir := filepath.Join(ctx.siteDir, "metadata")

	if !dirExists(metadataDir) {
		return pass(name, "No metadata/ directory found")
	}

	if !isDirEmpty(metadataDir) {
		return skip(name, "metadata/ directory is not empty — skipping removal", reason)
	}

	actions := []Action{{Op: "remove", Path: "metadata/"}}

	if ctx.dryRun {
		return fail(name, "Empty metadata/ directory", reason, actions)
	}

	os.Remove(metadataDir)
	return CheckResult{Name: name, Status: StatusFail, Message: "Removed empty metadata/ directory", Reason: reason, Actions: actions}
}

// ── Phase 9: CLI binary (network, runs last) ────────────────────────

// checkCLIUpdate detects outdated CLI binaries and reports available updates.
func checkCLIUpdate(ctx *runContext) CheckResult {
	const name = "cli-update"
	reason := fmt.Sprintf("Your site is now at v%s format — the latest CLI ensures full compatibility", Version)

	// Look for existing polis binaries in the site directory
	binaryPatterns := []string{"polis", "polis.exe", "polis-full*", "polis-server*"}
	var foundBinaries []string
	for _, pattern := range binaryPatterns {
		matches, _ := filepath.Glob(filepath.Join(ctx.siteDir, pattern))
		for _, m := range matches {
			info, err := os.Stat(m)
			if err == nil && !info.IsDir() {
				foundBinaries = append(foundBinaries, filepath.Base(m))
			}
		}
	}

	// Determine target binary name
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	binaryName := fmt.Sprintf("polis-full-%s-%s%s", goos, goarch, ext)
	destPath := filepath.Join(".polis", "bin", binaryName)

	// Check if we already downloaded the latest
	fullDestPath := filepath.Join(ctx.siteDir, destPath)
	if fileExists(fullDestPath) {
		return pass(name, fmt.Sprintf("Latest CLI already downloaded at %s", destPath))
	}

	actions := []Action{{Op: "create", Path: destPath, Detail: fmt.Sprintf("download polis-full v%s for %s/%s", Version, goos, goarch)}}

	if ctx.dryRun {
		msg := fmt.Sprintf("Would download polis-full v%s for %s/%s", Version, goos, goarch)
		if len(foundBinaries) > 0 {
			msg += fmt.Sprintf(" (found existing: %s)", strings.Join(foundBinaries, ", "))
		}
		return fail(name, msg, reason, actions)
	}

	// Apply mode — download the binary
	downloadURL := fmt.Sprintf("https://github.com/vdibart/polis-cli/releases/download/v%s/%s", Version, binaryName)

	resp, err := downloadClient.Get(downloadURL)
	if err != nil {
		return skip(name, fmt.Sprintf("Network unavailable: %v", err), reason)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return skip(name, fmt.Sprintf("Download failed (HTTP %d) — check if v%s has been released", resp.StatusCode, Version), reason)
	}

	// Save to .polis/bin/
	os.MkdirAll(filepath.Dir(fullDestPath), 0700)
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return skip(name, fmt.Sprintf("Download error: %v", err), reason)
	}

	if err := os.WriteFile(fullDestPath, data, 0755); err != nil {
		return fail(name, fmt.Sprintf("Failed to save binary: %v", err), reason, actions)
	}

	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Message: fmt.Sprintf("Downloaded polis-full v%s for %s/%s", Version, goos, goarch),
		Reason:  reason,
		Actions: actions,
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

// loadOrDefaultBundle loads the site's bundle.json if present, otherwise returns
// the default pub.polis.core bundle.
func loadOrDefaultBundle(siteDir string) *bundle.Bundle {
	bundlePath := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	if b, err := bundle.LoadBundle(bundlePath); err == nil {
		return b
	}
	return bundle.DefaultCoreBundle()
}

// updateJSONVersion updates the "version" field in a JSON file to the current generator.
func updateJSONVersion(path, generator string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	if _, ok := raw["version"]; ok {
		raw["version"] = generator
		out, err := json.MarshalIndent(raw, "", "  ")
		if err != nil {
			return
		}
		out = append(out, '\n')
		os.WriteFile(path, out, 0644)
	}
}

// fixBackslashPaths replaces backslash path separators in JSON string values.
func fixBackslashPaths(content string) string {
	// Replace escaped backslashes in JSON string values that look like paths
	// e.g., "posts\\20260227\\file.md" → "posts/20260227/file.md"
	// This handles the JSON-escaped form (\\) which represents a single backslash
	result := strings.ReplaceAll(content, "\\\\", "/")
	return result
}

// parseFrontmatter extracts YAML frontmatter fields from a markdown file.
func parseFrontmatter(content string) map[string]string {
	fm := make(map[string]string)
	lines := strings.Split(content, "\n")

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm
	}

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			break
		}
		// Skip indented lines (nested YAML like version-history entries)
		if len(lines[i]) > 0 && (lines[i][0] == ' ' || lines[i][0] == '\t') {
			continue
		}
		if idx := strings.Index(lines[i], ":"); idx > 0 {
			key := strings.TrimSpace(lines[i][:idx])
			value := strings.TrimSpace(lines[i][idx+1:])
			fm[key] = value
		}
	}

	return fm
}

// stripFrontmatter removes YAML frontmatter from content.
func stripFrontmatter(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return content
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			if i+1 < len(lines) {
				return strings.Join(lines[i+1:], "\n")
			}
			return ""
		}
	}
	return content
}

// countMDFiles counts .md files in a directory tree, skipping .versions/ dirs.
func countMDFiles(dir string) int {
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == ".versions" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(info.Name(), ".md") {
			count++
		}
		return nil
	})
	return count
}

// ── Phase 6 (cont): Theme consolidation ────────────────────────────

// checkThemeConsolidation detects and removes HTML templates and snippets in
// theme directories that duplicate _base. This migrates sites from the old
// full-copy-per-theme model to the new CSS-only + _base fallback model.
func checkThemeConsolidation(ctx *runContext) CheckResult {
	const name = "theme-consolidation"

	themesDir := filepath.Join(ctx.siteDir, "site", "themes")
	baseDir := filepath.Join(themesDir, "_base")

	// If no themes directory, nothing to do
	if !dirExists(themesDir) {
		return pass(name, "no themes directory")
	}

	// If _base doesn't exist, we can't do the comparison
	if !dirExists(baseDir) {
		return pass(name, "_base not installed — skipping stale file check")
	}

	entries, err := os.ReadDir(themesDir)
	if err != nil {
		return pass(name, "cannot read themes directory")
	}

	var actions []Action
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "_base" {
			continue
		}
		themeDir := filepath.Join(themesDir, entry.Name())
		staleFiles := theme.StaleThemeFiles(themeDir, baseDir)
		for _, sf := range staleFiles {
			relP := filepath.Join("site", "themes", entry.Name(), sf)
			if !ctx.dryRun {
				backupFile(ctx.siteDir, "", relP) // backup handled by tailor framework
				os.Remove(filepath.Join(themeDir, sf))
			}
			actions = append(actions, Action{
				Op:     "remove",
				Path:   relP,
				Detail: "duplicates _base/" + sf,
			})
		}
		// Clean up empty snippets directory
		if !ctx.dryRun {
			snippetsDir := filepath.Join(themeDir, "snippets")
			if dirExists(snippetsDir) && isDirEmpty(snippetsDir) {
				os.Remove(snippetsDir)
			}
		}
	}

	if len(actions) == 0 {
		return pass(name, "no stale theme files found")
	}

	return fail(name,
		fmt.Sprintf("removed %d stale theme file(s) that duplicate _base", len(actions)),
		"Theme consolidation (base layout refactor) — themes now inherit HTML from _base/",
		actions,
	)
}

// ── Phase 1 (cont): Avatar config ──────────────────────────────────

// checkAvatarConfig ensures .well-known/polis has an avatar field.
func checkAvatarConfig(ctx *runContext) CheckResult {
	const name = "avatar-config"
	const reason = "Avatar support was added for visual identity in the webapp and discovery service"

	wk, err := site.LoadWellKnown(ctx.siteDir)
	if err != nil {
		return skip(name, "Cannot load .well-known/polis", reason)
	}

	if wk.Avatar != nil {
		return pass(name, "Avatar config present")
	}

	actions := []Action{{Op: "update", Path: ".well-known/polis", Detail: "generate default avatar"}}

	if ctx.dryRun {
		return fail(name, "Missing avatar config", reason, actions)
	}

	wk.Avatar = site.GenerateDefaultAvatar()
	if err := site.SaveWellKnown(ctx.siteDir, wk); err != nil {
		return fail(name, fmt.Sprintf("Failed to save: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Generated default avatar config", Reason: reason, Actions: actions}
}

// ── Phase 6 (cont): DM provisioning ───────────────────────────────

// checkStorageSalt ensures .polis/storage-salt exists for DM encryption.
func checkStorageSalt(ctx *runContext) CheckResult {
	const name = "storage-salt"
	const reason = "DM encryption requires a site-wide storage salt at .polis/storage-salt"

	saltPath := filepath.Join(ctx.siteDir, ".polis", "storage-salt")

	if fileExists(saltPath) {
		return pass(name, "Storage salt present")
	}

	actions := []Action{{Op: "create", Path: ".polis/storage-salt"}}

	if ctx.dryRun {
		return fail(name, "Missing storage salt", reason, actions)
	}

	if err := dm.EnsureSalt(ctx.siteDir); err != nil {
		return fail(name, fmt.Sprintf("Failed to create salt: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Created storage salt", Reason: reason, Actions: actions}
}

// checkDMKeyring provisions the tenant's epoch-0 DM keys (keyring.json + bootstrap epoch)
// and publishes the signed public_key_messages block — the self-host mirror of the
// Patrol/Medic deploy-upgrade detector. Non-destructive: an existing keyring is untouched.
func checkDMKeyring(ctx *runContext) CheckResult {
	const name = "dm-keyring"
	const reason = "Encrypted DM requires an epoch-0 keyring (.polis/bundles/pub.polis.core/dm/keyring.json) with a published public_key_messages block"

	kr, err := dm.LoadKeyring(dm.DMDir(ctx.siteDir))
	if err == nil && len(kr.Epochs) > 0 && kr.SchemaVersion >= dm.KeyringSchemaVersion {
		return pass(name, "DM keyring present")
	}

	actions := []Action{{Op: "create", Path: ".polis/bundles/pub.polis.core/dm/keyring.json", Detail: "epoch-0 bootstrap keyring + published messages key"}}
	if ctx.dryRun {
		return fail(name, "Missing epoch-0 DM keyring", reason, actions)
	}

	privPEM, err := os.ReadFile(filepath.Join(ctx.siteDir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		return skip(name, "Cannot read private key to provision DM keyring", reason)
	}
	if err := site.ProvisionAndPublishMessagesKey(ctx.siteDir, privPEM); err != nil {
		return skip(name, fmt.Sprintf("Failed to provision DM keyring: %v", err), reason)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Created epoch-0 DM keyring", Reason: reason, Actions: actions}
}

// checkDMDirectories ensures .polis/bundles/pub.polis.core/dm/conversations/ exists with 0700 perms.
func checkDMDirectories(ctx *runContext) CheckResult {
	const name = "dm-directories"
	const reason = "DM conversations are stored in .polis/bundles/pub.polis.core/dm/conversations/ with restricted permissions"

	convPath := filepath.Join(ctx.siteDir, ".polis", "bundles", "pub.polis.core", "dm", "conversations")

	if dirExists(convPath) {
		// Check permissions
		info, err := os.Stat(convPath)
		if err == nil && info.Mode().Perm() != 0700 {
			actions := []Action{{Op: "update", Path: ".polis/bundles/pub.polis.core/dm/conversations", Detail: fmt.Sprintf("chmod %o → 0700", info.Mode().Perm())}}
			if ctx.dryRun {
				return fail(name, fmt.Sprintf("DM directory has unsafe permissions: %o", info.Mode().Perm()), reason, actions)
			}
			os.Chmod(convPath, 0700)
			return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("Fixed DM directory permissions: %o → 0700", info.Mode().Perm()), Reason: reason, Actions: actions}
		}
		return pass(name, "DM directories present with correct permissions")
	}

	actions := []Action{{Op: "create", Path: ".polis/bundles/pub.polis.core/dm/conversations"}}

	if ctx.dryRun {
		return fail(name, "Missing DM directories", reason, actions)
	}

	if err := os.MkdirAll(convPath, 0700); err != nil {
		return fail(name, fmt.Sprintf("Failed to create: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Created DM directories", Reason: reason, Actions: actions}
}

// ── Phase 6 (cont): Policy content convergence ──────────────────────

// checkPolicyContentConverge overwrites both policy files with the canonical
// templates if their content has drifted. Tenants cannot customize policy
// files, so all existing files should match the defaults.
// checkPolicyContentConverge overwrites drifted policy files with the
// canonical default content. This single check subsumes two distinct
// remediation needs on the Tailor side:
//
//  1. R21-1 (DM policy auto-remediation gap). Legacy tenants missing
//     the DM `allow` rule in private rules.jsonl: their file won't
//     match DefaultPrivatePolicyContent() so this converge overwrites,
//     restoring the rule. Hosted Medic still needs explicit wiring for
//     the same case (see operational-hardening.md R21-1) — that's a
//     parallel fix on the hosted side.
//
//  2. Policy v1 → v2 silent upgrade (commit 26acb46b). v1 files differ
//     from the v2 canonical content so this same converge upgrades.
//     Medic has a distinct PolicyFormatVersion check + dedicated upgrade
//     message for observability; Tailor doesn't need the distinction
//     because it's a single-pass user-invoked tool.
//
// Assumes per-tenant policy customization is not yet supported; when
// it lands this needs a translator instead of overwrite.
func checkPolicyContentConverge(ctx *runContext) CheckResult {
	const name = "policy-content-converge"
	const reason = "Policy files must match the canonical templates for consistent behavior"

	privPath := filepath.Join(ctx.siteDir, ".polis", "policies", "rules.jsonl")
	pubPath := filepath.Join(ctx.siteDir, "policies", "rules.jsonl")

	var drifted []string
	var actions []Action

	if privData, err := os.ReadFile(privPath); err == nil {
		if strings.TrimSpace(string(privData)) != strings.TrimSpace(policy.DefaultPrivatePolicyContent()) {
			drifted = append(drifted, "private")
			actions = append(actions, Action{Op: "update", Path: ".polis/policies/rules.jsonl", Detail: "converge to canonical template"})
		}
	}

	if pubData, err := os.ReadFile(pubPath); err == nil {
		if strings.TrimSpace(string(pubData)) != strings.TrimSpace(policy.DefaultPublicPolicyContent()) {
			drifted = append(drifted, "public")
			actions = append(actions, Action{Op: "update", Path: "policies/rules.jsonl", Detail: "converge to canonical template"})
		}
	}

	if len(drifted) == 0 {
		return pass(name, "Policy files match canonical templates")
	}

	if ctx.dryRun {
		return fail(name, "Policy content drift: "+strings.Join(drifted, ", "), reason, actions)
	}

	for _, d := range drifted {
		switch d {
		case "private":
			os.MkdirAll(filepath.Dir(privPath), 0700)
			os.WriteFile(privPath, []byte(policy.DefaultPrivatePolicyContent()), 0600)
		case "public":
			os.MkdirAll(filepath.Dir(pubPath), 0755)
			os.WriteFile(pubPath, []byte(policy.DefaultPublicPolicyContent()), 0644)
		}
	}

	return CheckResult{Name: name, Status: StatusFail, Message: "Converged policy files: " + strings.Join(drifted, ", "), Reason: reason, Actions: actions}
}

// ── Phase 7 (cont): Webapp config cleanup ──────────────────────────

// checkWebappViewMode removes the deprecated view_mode key from webapp config.
func checkWebappViewMode(ctx *runContext) CheckResult {
	const name = "webapp-view-mode"
	const reason = "The view_mode webapp config key is deprecated — the webapp now uses a unified layout"

	configPath := filepath.Join(ctx.siteDir, ".polis", "webapp", "config.json")
	if !fileExists(configPath) {
		return pass(name, "No webapp config file")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return pass(name, "Cannot read webapp config")
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return pass(name, "Cannot parse webapp config")
	}

	if _, exists := obj["view_mode"]; !exists {
		return pass(name, "No deprecated view_mode key")
	}

	actions := []Action{{Op: "update", Path: ".polis/webapp/config.json", Detail: "remove deprecated view_mode key"}}

	if ctx.dryRun {
		return fail(name, "Deprecated view_mode key present", reason, actions)
	}

	delete(obj, "view_mode")
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fail(name, fmt.Sprintf("Failed to marshal: %v", err), reason, actions)
	}
	out = append(out, '\n')
	_ = atomicfile.WriteFile(configPath, out, 0600)
	return CheckResult{Name: name, Status: StatusFail, Message: "Removed deprecated view_mode key", Reason: reason, Actions: actions}
}

// ── Helpers for new checks ─────────────────────────────────────────

// appendPolicyRule appends a JSONL rule line to a policy file.
func appendPolicyRule(path, ruleLine string) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(ruleLine + "\n")
	return err
}

// checkBlessedCacheGC is the self-hoster equivalent of the hosted Rosie actor's
// GC pass: it removes structurally-orphaned blessed-comment cache entries — a
// cached body with no readable .meta.json provenance sidecar, which is unsafe
// to display and regenerable by a later sync.
//
// SCOPE: this is the OFFLINE slice of Rosie a self-hoster can run without a live
// discovery client. The full custodian work — reconcile (desired-vs-present:
// re-fetch missing, evict withdrawn/denied) and integrity re-verification —
// needs DS access + network and runs in the hosted Rosie goroutine
// (webapp/internal/hosted/rosie.go) or whenever the self-hosted webapp syncs.
// See plans/rosie-cache-custodian-design.md (WS-R2: "Tailor-integrated check").
// checkForeignContentInPublicPath enforces the core principle that no content
// authored by ANOTHER tenant may live in this site's PUBLIC paths. It walks the
// public comment + post SOURCE trees (content/pub.polis.core/{comment,post}) for
// .md files whose frontmatter `author` is not this site's own domain — the
// Defect-3 copies the old blessing flow left behind — backs each up (Apply mode)
// and removes it, plus its rendered mount sibling ({comments,posts}/…html). This
// is the self-hoster equivalent of Medic's foreign-content quarantine; the
// isolated blessed-comment cache (+ Rosie/render read-through) is the only place
// a foreign comment may live. The owner's OWN content (author == site domain) is
// canonical and left untouched.
func checkForeignContentInPublicPath(ctx *runContext) CheckResult {
	name := "foreign-content-in-public-path"
	reason := "no content authored by another tenant may live in a public path; the isolated blessed-comment cache is its only home (comment-registration-severe-bug / rosie-cache-custodian)"

	owner := tailorSiteDomain(ctx.baseURL)
	if owner == "" {
		return pass(name, "site domain unknown — skipped")
	}

	var actions []Action
	for _, ct := range []struct{ sub, mount string }{{"comment", "comments"}, {"post", "posts"}} {
		root := filepath.Join(ctx.siteDir, "content", "pub.polis.core", ct.sub)
		srcPrefix := "content/pub.polis.core/" + ct.sub + "/"
		filepath.WalkDir(root, func(path string, d os.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if d.IsDir() {
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir // .versions/, etc.
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			author := tailorFrontmatterAuthor(path)
			if author == "" || author == owner {
				return nil // owner's own content (or unknowable) — leave it
			}
			rel, _ := filepath.Rel(ctx.siteDir, path)
			rel = filepath.ToSlash(rel)
			if !ctx.dryRun {
				backupFile(ctx.siteDir, ctx.backupDir, rel)
				os.Remove(path)
			}
			actions = append(actions, Action{Op: "remove", Path: rel, Detail: fmt.Sprintf("foreign content in public path (author=%s)", author)})

			// Rendered mount sibling (no frontmatter to scan) — remove by association.
			mountRel := ct.mount + "/" + strings.TrimSuffix(strings.TrimPrefix(rel, srcPrefix), ".md") + ".html"
			if _, e := os.Stat(filepath.Join(ctx.siteDir, mountRel)); e == nil {
				if !ctx.dryRun {
					backupFile(ctx.siteDir, ctx.backupDir, mountRel)
					os.Remove(filepath.Join(ctx.siteDir, mountRel))
				}
				actions = append(actions, Action{Op: "remove", Path: mountRel, Detail: fmt.Sprintf("foreign content in public path, rendered mount (author=%s)", author)})
			}
			return nil
		})
	}

	if len(actions) == 0 {
		return pass(name, "no foreign content in public paths")
	}
	return fail(name, fmt.Sprintf("removed %d foreign file(s) from public paths", len(actions)), reason, actions)
}

// tailorSiteDomain extracts the bare host from the site's base URL (no net/url
// dependency): strips scheme, path, and port. "" if baseURL is empty.
func tailorSiteDomain(baseURL string) string {
	s := strings.TrimSpace(baseURL)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	return s
}

// tailorFrontmatterAuthor reads the YAML frontmatter `author` field of a markdown
// file (mirrors clerk/patrol's local extractors — kept local to keep the actors
// independent). Returns "" when not found.
func tailorFrontmatterAuthor(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	inFM := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			if inFM {
				return "" // end of frontmatter, author not found
			}
			inFM = true
			continue
		}
		if !inFM {
			continue
		}
		if strings.HasPrefix(line, "author:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "author:"))
		}
	}
	return ""
}

func checkBlessedCacheGC(ctx *runContext) CheckResult {
	name := "blessed-cache-gc"
	reason := "an orphan blessed-comment cache body (no provenance sidecar) is unsafe to display and regenerable; Rosie GCs them (rosie-cache-custodian)"

	dsDir := filepath.Join(ctx.siteDir, ".polis", "ds")
	entries, err := os.ReadDir(dsDir)
	if err != nil {
		return CheckResult{Name: name, Status: StatusPass, Message: "no DS directory"}
	}

	var actions []Action
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		blessedRoot := filepath.Join(dsDir, entry.Name(), "pub.polis.core", "cache", "blessed")
		filepath.WalkDir(blessedRoot, func(path string, d os.DirEntry, werr error) error {
			if werr != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".md") || strings.HasSuffix(d.Name(), ".meta.json") {
				return nil
			}
			// Orphan = body whose sidecar is missing or unreadable.
			if data, e := os.ReadFile(path + ".meta.json"); e == nil {
				var probe map[string]any
				if json.Unmarshal(data, &probe) == nil {
					return nil // healthy
				}
			}
			relPath, _ := filepath.Rel(ctx.siteDir, path)
			if !ctx.dryRun {
				os.Remove(path)
				os.Remove(path + ".meta.json")
			}
			actions = append(actions, Action{Op: "remove", Path: relPath, Detail: "orphan blessed-cache body (no sidecar)"})
			return nil
		})
	}

	if len(actions) == 0 {
		return CheckResult{Name: name, Status: StatusPass, Message: "no orphan blessed-cache entries"}
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: fmt.Sprintf("removed %d orphan blessed-cache entr(ies)", len(actions)),
		Actions: actions,
	}
}

// checkStaleScopedFeed removes deprecated scoped feed cache files (followers, me).
// These scopes are now served as runtime filters over the network feed cache.
func checkStaleScopedFeed(ctx *runContext) CheckResult {
	name := "stale-scoped-feed"
	reason := "followers/me feed scopes collapsed to runtime filters; separate cache files are obsolete"

	dsDir := filepath.Join(ctx.siteDir, ".polis", "ds")
	entries, err := os.ReadDir(dsDir)
	if err != nil {
		return CheckResult{Name: name, Status: StatusPass, Message: "no DS directory"}
	}

	deprecated := []string{"followers", "me"}
	var actions []Action
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		stateDir := filepath.Join(dsDir, entry.Name(), "pub.polis.core", "state")
		for _, scope := range deprecated {
			fname := "pub.polis.feed." + scope + ".jsonl"
			p := filepath.Join(stateDir, fname)
			if _, err := os.Stat(p); err == nil {
				relPath, _ := filepath.Rel(ctx.siteDir, p)
				if !ctx.dryRun {
					os.Remove(p)
				}
				actions = append(actions, Action{
					Op:     "remove",
					Path:   relPath,
					Detail: "deprecated scoped feed cache",
				})
			}
		}
	}

	if len(actions) == 0 {
		return CheckResult{Name: name, Status: StatusPass, Message: "no deprecated scoped feed files"}
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: fmt.Sprintf("removed %d deprecated scoped feed file(s)", len(actions)),
		Actions: actions,
	}
}

// checkStaleFeedViewedAt removes the deprecated pub.polis.feed.viewed_at cursor key
// from cursors.json. This timestamp-based key was replaced by the position-based
// pub.polis.feed.viewed cursor.
func checkStaleFeedViewedAt(ctx *runContext) CheckResult {
	name := "stale-feed-viewed-at"
	reason := "pub.polis.feed.viewed_at cursor replaced by position-based pub.polis.feed.viewed"

	dsDir := filepath.Join(ctx.siteDir, ".polis", "ds")
	entries, err := os.ReadDir(dsDir)
	if err != nil {
		return CheckResult{Name: name, Status: StatusPass, Message: "no DS directory"}
	}

	var actions []Action
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cursorsPath := filepath.Join(dsDir, entry.Name(), "pub.polis.core", "state", "cursors.json")
		data, err := os.ReadFile(cursorsPath)
		if err != nil {
			continue
		}
		var raw map[string]interface{}
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		cursors, ok := raw["cursors"].(map[string]interface{})
		if !ok {
			continue
		}
		if _, has := cursors["pub.polis.feed.viewed_at"]; !has {
			continue
		}
		relPath, _ := filepath.Rel(ctx.siteDir, cursorsPath)
		if !ctx.dryRun {
			delete(cursors, "pub.polis.feed.viewed_at")
			if out, err := json.MarshalIndent(raw, "", "  "); err == nil {
				os.WriteFile(cursorsPath, append(out, '\n'), 0644)
			}
		}
		actions = append(actions, Action{
			Op:     "remove_key",
			Path:   relPath,
			Detail: "deprecated pub.polis.feed.viewed_at cursor",
		})
	}

	if len(actions) == 0 {
		return CheckResult{Name: name, Status: StatusPass, Message: "no deprecated feed viewed_at cursor"}
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: fmt.Sprintf("removed pub.polis.feed.viewed_at from %d cursors file(s)", len(actions)),
		Actions: actions,
	}
}

// checkLegacyFeedScaffolding retires the legacy pub.polis.feed content-type
// scaffolding: the empty public content/pub.polis.core/feed/ directory created
// by older polis init, and any stale "pub.polis.feed" entry left in
// content/pub.polis.core/bundle.json after the type was retired (MergeDefaults
// is additive-only and won't strip removed types). The real feed cache lives
// under .polis/ds/<domain>/state/pub.polis.feed*.jsonl and is unaffected.
//
// Defensive: only remove the dir if it's empty — nothing should ever write
// there, but if something has, leave the contents in place for the operator.
func checkLegacyFeedScaffolding(ctx *runContext) CheckResult {
	name := "legacy-feed-scaffolding"
	reason := "pub.polis.feed retired as a public content type; only the private DS state files remain"

	var actions []Action

	dirRel := filepath.Join("content", "pub.polis.core", "feed")
	dirFull := filepath.Join(ctx.siteDir, dirRel)
	if entries, err := os.ReadDir(dirFull); err == nil {
		if len(entries) == 0 {
			if !ctx.dryRun {
				os.Remove(dirFull)
			}
			actions = append(actions, Action{
				Op:     "remove",
				Path:   dirRel,
				Detail: "retired empty legacy pub.polis.feed content dir",
			})
		} else {
			actions = append(actions, Action{
				Op:     "flag",
				Path:   dirRel,
				Detail: fmt.Sprintf("legacy pub.polis.feed dir has %d unexpected entries; left in place for operator review", len(entries)),
			})
		}
	}

	bundleRel := filepath.Join("content", "pub.polis.core", "bundle.json")
	bundleFull := filepath.Join(ctx.siteDir, bundleRel)
	if b, err := bundle.LoadBundle(bundleFull); err == nil {
		if _, has := b.Types["pub.polis.feed"]; has {
			delete(b.Types, "pub.polis.feed")
			if !ctx.dryRun {
				bundle.SaveBundle(bundleFull, b)
			}
			actions = append(actions, Action{
				Op:     "remove_key",
				Path:   bundleRel,
				Detail: "removed retired pub.polis.feed declaration",
			})
		}
	}

	if len(actions) == 0 {
		return CheckResult{Name: name, Status: StatusPass, Message: "no legacy pub.polis.feed scaffolding"}
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: fmt.Sprintf("retired %d legacy pub.polis.feed artifact(s)", len(actions)),
		Actions: actions,
	}
}

// checkStudio13RenameMigration detects self-hosted tenants still pointing at
// the pre-rename studio13 theme FQN and rewrites active_theme to
// pub.polis.themes.studio13-nk. Mirror of medic.healRegistryIntegrity's
// shape-gated branch for step-02/2.0.
//
// Permanent check, not a one-shot migration: continues to fire defensively
// any time a tenant's registry ends up in the pre-rename state (rolled-back
// config, restored backup, hand-edited FQN). Shape-gated to v3 (empty
// active_shape treated as v3 per bundle.GetActiveShapeName's default) so a
// future v4 tenant legitimately picking the new v4-era studio13 (step-02/2.a.1)
// is left untouched.
func checkStudio13RenameMigration(ctx *runContext) CheckResult {
	name := "studio13-rename-migration"
	reason := "step-02/2.0 renamed the old studio13 theme to studio13-nk to free the studio13 name for a future v4-era CSS-only theme; blog-shape tenants on pre-rename studio13 need to migrate to preserve their visual identity"

	raw, err := bundle.LoadRegistryRaw(ctx.siteDir)
	if err != nil || raw == nil {
		return pass(name, "no registry to migrate")
	}

	at, _ := raw["active_theme"].(string)
	if at != "pub.polis.themes.studio13" {
		return pass(name, "active_theme is not pre-rename studio13")
	}

	as, _ := raw["active_shape"].(string)
	if as != "pub.polis.shapes.v3" && as != "" {
		// v4 (or any other shape) — don't migrate; the tenant legitimately
		// wants whatever "studio13" resolves to in their shape context.
		return pass(name, fmt.Sprintf("active_theme is pre-rename studio13 but active_shape is %q (not v3); leaving alone", as))
	}

	action := Action{
		Op:     "migrate",
		Path:   ".polis/bundles/registry.json",
		Detail: "active_theme pub.polis.themes.studio13 → pub.polis.themes.studio13-nk",
	}

	if ctx.dryRun {
		return fail(name, "active_theme needs migration to studio13-nk", reason, []Action{action})
	}

	if err := bundle.SetActiveThemeName(ctx.siteDir, "studio13-nk"); err != nil {
		return fail(name, fmt.Sprintf("SetActiveThemeName failed: %v", err), reason, []Action{action})
	}

	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: "migrated active_theme: pub.polis.themes.studio13 → pub.polis.themes.studio13-nk",
		Actions: []Action{action},
	}
}

// canonicalizeContent normalizes content for hashing.
func canonicalizeContent(content string) string {
	content = strings.TrimLeft(content, "\n")
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n") + "\n"
}

// checkOrphanedThemeDirs reaps theme directories installed at
// .polis/bundles/pub.polis.core/themes/<name>/ that don't appear in the
// tenant's registry (installed_bundles[].theme_versions).
//
// Mirror of medic.healOrphanedThemeDirs + patrol.checkOrphanedThemeDirs for
// self-hosted tenants. Registry is the authoritative "what SHOULD be
// installed" state; disk-level drift is either leftover from prior bundle
// versions (e.g. themes/studio13/ after step-02/2.0 renamed to studio13-nk)
// or operator-introduced junk.
//
// Permanent check (not one-shot). Safety: refuses to reap when the registry
// is missing, unreadable, or has no theme_versions for pub.polis.core.
func checkOrphanedThemeDirs(ctx *runContext) CheckResult {
	name := "orphaned-theme-dirs"
	reason := "installed theme directories that don't appear in registry.installed_bundles[].theme_versions indicate drift from prior bundle versions or operator-introduced state"

	themesDir := filepath.Join(ctx.siteDir, ".polis", "bundles", "pub.polis.core", "themes")
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		return pass(name, "no themes directory")
	}

	reg, err := bundle.LoadRegistry(ctx.siteDir)
	if err != nil || reg == nil {
		return pass(name, "registry unreadable; deferred to registry-integrity check")
	}

	declared := make(map[string]bool)
	for _, ib := range reg.InstalledBundles {
		if ib.Name != "pub.polis.core" {
			continue
		}
		for tname := range ib.ThemeVersions {
			declared[tname] = true
		}
	}
	if len(declared) == 0 {
		return pass(name, "registry has no theme_versions for pub.polis.core")
	}

	var orphans []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !declared[e.Name()] {
			orphans = append(orphans, e.Name())
		}
	}
	if len(orphans) == 0 {
		return pass(name, "no orphan theme dirs")
	}

	actions := make([]Action, 0, len(orphans))
	for _, o := range orphans {
		actions = append(actions, Action{
			Op:     "remove",
			Path:   filepath.Join(".polis", "bundles", "pub.polis.core", "themes", o),
			Detail: fmt.Sprintf("orphan (not in registry.theme_versions): %s", o),
		})
	}

	if ctx.dryRun {
		return fail(name, fmt.Sprintf("%d orphan theme dir(s): %s", len(orphans), strings.Join(orphans, ", ")), reason, actions)
	}

	var failures []string
	for _, o := range orphans {
		orphanDir := filepath.Join(themesDir, o)
		if err := os.RemoveAll(orphanDir); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", o, err))
		}
	}
	if len(failures) > 0 {
		return fail(name, fmt.Sprintf("partial reap, failures: %s", strings.Join(failures, "; ")), reason, actions)
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: fmt.Sprintf("reaped %d orphan theme dir(s): %s", len(orphans), strings.Join(orphans, ", ")),
		Actions: actions,
	}
}

// ── Phase 4.5: step-01 bundle/registry/theme refactor migrations ─────────
//
// Mirrors the hosted Patrol+Medic pair for the SHAPE/BUNDLE/THEME refactor.
// Five independent migrations; the ordering below matches medic.go:464-538,
// which is "install first (smallest blast radius), then path/registry
// migrations, destructive theme cleanup last." All five must run before
// Phase 5 (index rebuild + render) because Phase 5 reads the new paths.

// checkBundleReferencePayload installs or refreshes the embedded reference
// payload (shapes + per-theme CSS) into .polis/bundles/pub.polis.core/.
// Version-gated via BundleRegistry.NeedsRefresh: a no-op when the on-disk
// payload matches what the binary ships.
//
// Mirror of patrol.checkBundleReferencePayload + medic.go:474-484.
func checkBundleReferencePayload(ctx *runContext) CheckResult {
	const name = "bundle-reference-payload"
	const reason = "step-01 (SHAPE/BUNDLE/THEME refactor) — the reference payload (shapes + themes) ships embedded in the binary and is installed per-tenant at .polis/bundles/pub.polis.core/; version-gated refresh on each upgrade"

	reg, err := bundle.LoadRegistry(ctx.siteDir)
	if err != nil || reg == nil || reg.NeedsRefresh(bundle.DefaultCoreBundle()) {
		action := Action{
			Op:     "update",
			Path:   ".polis/bundles/pub.polis.core/",
			Detail: "install/refresh reference payload from embedded fixtures",
		}
		if ctx.dryRun {
			return fail(name, "reference payload needs install or refresh", reason, []Action{action})
		}
		if err := bundle.EnsureReferencePayload(ctx.siteDir, "pub.polis.core"); err != nil {
			return fail(name, fmt.Sprintf("EnsureReferencePayload failed: %v", err), reason, []Action{action})
		}
		return CheckResult{
			Name:    name,
			Status:  StatusFail,
			Reason:  reason,
			Message: "installed/refreshed reference payload",
			Actions: []Action{action},
		}
	}
	return pass(name, "reference payload up to date")
}

// checkLegacyContentPath moves per-bundle private state from the pre-1f
// .polis/content/<bundle>/ tree to the post-1f .polis/bundles/<bundle>/ tree.
// MigratePrivateBundlesPath refuses to merge into an existing destination
// subtree (errors instead of silently overlaying), so a partial migration
// surfaces here rather than corrupting state.
//
// Mirror of patrol.checkLegacyContentPath + medic.go:487-497.
func checkLegacyContentPath(ctx *runContext) CheckResult {
	const name = "legacy-content-path"
	const reason = "step-01 (SHAPE/BUNDLE/THEME refactor) — private bundle state moved from .polis/content/<bundle>/ to .polis/bundles/<bundle>/"

	if !site.LegacyPrivateContentExists(ctx.siteDir) {
		return pass(name, "no legacy .polis/content/ tree")
	}
	action := Action{
		Op:     "move",
		Path:   ".polis/content/",
		Dest:   ".polis/bundles/",
		Detail: "rename per-bundle subtrees (posts/, comments/, dm/, …) into .polis/bundles/<bundle>/",
	}
	if ctx.dryRun {
		return fail(name, ".polis/content/ exists; should be migrated to .polis/bundles/", reason, []Action{action})
	}
	if err := site.MigratePrivateBundlesPath(ctx.siteDir); err != nil {
		return fail(name, fmt.Sprintf("MigratePrivateBundlesPath failed: %v", err), reason, []Action{action})
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: "migrated .polis/content/ → .polis/bundles/",
		Actions: []Action{action},
	}
}

// checkRegistryMigration moves active_theme out of .well-known/polis and into
// .polis/bundles/registry.json (with FQN qualification). The migration is
// raw-map based to preserve any unknown-to-struct fields in well-known.
//
// Mirror of patrol.checkRegistryMigration + medic.go:500-510.
func checkRegistryMigration(ctx *runContext) CheckResult {
	const name = "registry-migration"
	const reason = "step-01 (SHAPE/BUNDLE/THEME refactor) — active_theme moved from .well-known/polis to .polis/bundles/registry.json (with FQN qualification, e.g. vice → pub.polis.themes.vice)"

	if !site.LegacyActiveThemeInWellKnown(ctx.siteDir) {
		return pass(name, "active_theme already migrated out of .well-known/polis")
	}
	action := Action{
		Op:     "migrate",
		Path:   ".well-known/polis",
		Dest:   ".polis/bundles/registry.json",
		Detail: "move active_theme to registry.json with FQN qualification",
	}
	if ctx.dryRun {
		return fail(name, ".well-known/polis has active_theme; should migrate to registry.json", reason, []Action{action})
	}
	if err := bundle.MigrateActiveThemeToRegistry(ctx.siteDir); err != nil {
		return fail(name, fmt.Sprintf("MigrateActiveThemeToRegistry failed: %v", err), reason, []Action{action})
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: "migrated active_theme to registry.json (FQN-qualified)",
		Actions: []Action{action},
	}
}

// checkLegacyThemeLocations blindly deletes site/themes/ and .polis/themes/.
// Forced-upgrade policy — shipped defaults always win; the canonical theme
// home is .polis/bundles/pub.polis.core/themes/, populated by
// checkBundleReferencePayload above.
//
// Mirror of patrol.checkLegacyThemeLocations + medic.go:513-523.
func checkLegacyThemeLocations(ctx *runContext) CheckResult {
	const name = "legacy-theme-locations"
	const reason = "step-01 (SHAPE/BUNDLE/THEME refactor) — themes now live at .polis/bundles/<bundle>/themes/; the legacy site/themes/ and .polis/themes/ trees are stripped on upgrade (forced-upgrade policy)"

	if !theme.LegacyThemeLocationsExist(ctx.siteDir) {
		return pass(name, "no legacy theme locations")
	}
	action := Action{
		Op:     "remove",
		Path:   "site/themes/, .polis/themes/",
		Detail: "delete legacy theme dirs (canonical now in .polis/bundles/<bundle>/themes/)",
	}
	if ctx.dryRun {
		return fail(name, "legacy theme dirs exist; should be removed", reason, []Action{action})
	}
	if err := theme.RemoveLegacyThemeLocations(ctx.siteDir); err != nil {
		return fail(name, fmt.Sprintf("RemoveLegacyThemeLocations failed: %v", err), reason, []Action{action})
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: "removed legacy theme directories",
		Actions: []Action{action},
	}
}

// checkBundleActiveFields strips the legacy "active" boolean from
// .well-known/polis (bundles.<name>.active) and .polis/bundles/registry.json
// (installed_bundles[].active). Cleanup C1+C4: listing-is-activation; the
// field was always true, never read, and removed from the struct.
//
// Mirror of patrol.checkBundleActiveFields + medic.go:528-538.
func checkBundleActiveFields(ctx *runContext) CheckResult {
	const name = "bundle-active-fields"
	const reason = "step-01 cleanup C1+C4 — the legacy 'active' boolean on bundles was always true and unread; removed from the struct, must be stripped from existing tenant files"

	if !site.LegacyBundleActiveFieldsExist(ctx.siteDir) {
		return pass(name, "no legacy 'active' field present")
	}
	action := Action{
		Op:     "update",
		Path:   ".well-known/polis, .polis/bundles/registry.json",
		Detail: "delete 'active' key under bundles.<name> and installed_bundles[]",
	}
	if ctx.dryRun {
		return fail(name, "legacy 'active' field present; should be stripped", reason, []Action{action})
	}
	if err := site.StripBundleActiveFields(ctx.siteDir); err != nil {
		return fail(name, fmt.Sprintf("StripBundleActiveFields failed: %v", err), reason, []Action{action})
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: "stripped legacy 'active' field from bundle config files",
		Actions: []Action{action},
	}
}

// ── Phase 4.7: post-step-01 migrations ──────────────────────────────────
//
// Migrations that landed after step-01 but before the F1–F9 content-aware
// checks. Order mirrors medic.go HealTenant: notification rules move first
// (independent), then the v3→v4 active_shape flip, then the v3 archive
// cleanup (gated on the just-flipped active_shape value). All must run
// before Phase 5 (index rebuild + render) so the new state is in place
// when re-rendering picks up the bundle/registry state.

// checkNotificationRulesMigration relocates per-content-type
// `notifications` arrays out of bundle.json's types map into the flat
// top-level `notifications` field on registry.json. Sanctioned step-06/
// 6.f.1 schema exception — rules are now private (registry.json is
// in .polis/), bundle-agnostic, and flat.
//
// Mirror of bundle.MigrateNotificationRulesToRegistry + medic.go
// HealTenant notification-migration block.
func checkNotificationRulesMigration(ctx *runContext) CheckResult {
	const name = "notification-rules-migration"
	const reason = "step-06/6.f.1 — notification rule declarations moved from public bundle.json (per content type) to private registry.json (flat top-level list) for privacy and bundle-agnosticism"

	if !bundle.NotificationRulesNeedMigration(ctx.siteDir) {
		return pass(name, "no per-content-type notification rules in bundle.json")
	}
	action := Action{
		Op:     "migrate",
		Path:   "content/pub.polis.core/bundle.json",
		Dest:   ".polis/bundles/registry.json",
		Detail: "move types[].notifications → registry.json notifications (registry-wins dedup)",
	}
	if ctx.dryRun {
		return fail(name, "bundle.json carries per-type notification rules; should be relocated to registry.json", reason, []Action{action})
	}
	migrated, removed, err := bundle.MigrateNotificationRulesToRegistry(ctx.siteDir)
	if err != nil {
		return fail(name, fmt.Sprintf("MigrateNotificationRulesToRegistry failed: %v", err), reason, []Action{action})
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: fmt.Sprintf("relocated %d notification rule(s) to registry (%d new, %d duplicates dropped)", removed, migrated, removed-migrated),
		Actions: []Action{action},
	}
}

// checkActiveShapeUpgrade flips a tenant's active_shape from
// pub.polis.shapes.v3 to pub.polis.shapes.v4 on the v4 cutover.
// Idempotent on v4-already and on any non-v3 shape value. Tailor's
// existing checkRenderSite (Phase 5) re-renders after the flip in the
// same pass, so on-disk HTML matches the new shape without a separate
// hook.
//
// Mirror of bundle.UpgradeActiveShape + medic.go HealTenant
// upgrade-active-shape block.
func checkActiveShapeUpgrade(ctx *runContext) CheckResult {
	const name = "active-shape-upgrade"
	const reason = "v3 → v4 SHAPE cutover — registry.json's active_shape is flipped from pub.polis.shapes.v3 to pub.polis.shapes.v4 so render dispatches produce v4 chrome; pairs with the v3-archive cleanup below"

	reg, err := bundle.LoadRegistry(ctx.siteDir)
	if err != nil || reg == nil {
		return pass(name, "no registry to upgrade")
	}
	if reg.ActiveShape != "pub.polis.shapes.v3" {
		return pass(name, fmt.Sprintf("active_shape is %q; not on v3 (nothing to upgrade)", reg.ActiveShape))
	}
	action := Action{
		Op:     "migrate",
		Path:   ".polis/bundles/registry.json",
		Detail: "active_shape pub.polis.shapes.v3 → pub.polis.shapes.v4",
	}
	if ctx.dryRun {
		return fail(name, "active_shape is on legacy v3; should be flipped to v4", reason, []Action{action})
	}
	flipped, err := bundle.UpgradeActiveShape(ctx.siteDir)
	if err != nil {
		return fail(name, fmt.Sprintf("UpgradeActiveShape failed: %v", err), reason, []Action{action})
	}
	if !flipped {
		// Shouldn't happen given the LoadRegistry check above, but be safe.
		return pass(name, "active_shape did not need upgrade")
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: "flipped active_shape: pub.polis.shapes.v3 → pub.polis.shapes.v4",
		Actions: []Action{action},
	}
}

// checkV3LegacyArchives deletes stale v3 SHAPE archive HTML files on
// tenants that have just migrated to v4 SHAPE. v3's RenderAll emitted
// flat archive pages (posts/index.html, comments/index.html, etc.)
// that v4 doesn't emit AND doesn't delete, so static-file fallthrough
// serves stale content. Gated on active_shape == v4 — runs after the
// upgrade above in the same pass; no-op for tenants still on v3.
//
// Mirror of site.CleanV3LegacyArchives + medic.go HealTenant
// v3-legacy-archives block. Closes R17-2.
func checkV3LegacyArchives(ctx *runContext) CheckResult {
	const name = "v3-legacy-archives"
	const reason = "R17-2 — pre-migration tenants accumulated v3 SHAPE archive HTML (posts/index.html, comments/index.html, tag/index.html, comment/index.html); v4's renderStreamAll doesn't emit or delete them, so static-file fallthrough serves stale content with HTTP 200"

	if !site.V3LegacyArchivesExist(ctx.siteDir) {
		return pass(name, "no stale v3 archive files (or tenant not on v4)")
	}
	action := Action{
		Op:     "remove",
		Path:   "posts/index.html, comments/index.html, tag/index.html, comment/index.html",
		Detail: "stale v3 SHAPE archive entries (tenant on active_shape=v4)",
	}
	if ctx.dryRun {
		return fail(name, "stale v3 archive files present on v4 tenant", reason, []Action{action})
	}
	removed := site.CleanV3LegacyArchives(ctx.siteDir)
	if len(removed) == 0 {
		// V3LegacyArchivesExist said yes, but CleanV3LegacyArchives says no — race
		// or permission error; flag for manual review on next pass.
		return pass(name, "no files removed (raced with concurrent cleanup, or permissions)")
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: fmt.Sprintf("removed %d stale v3 archive file(s): %s", len(removed), strings.Join(removed, ", ")),
		Actions: []Action{action},
	}
}

// ── Phase 6.5: Content-aware integrity (F1–F9) ──────────────────────────
//
// Mirrors of patrol-checks: content-aware integrity additions (commit
// 4baf44f1). Run after the step-01 + post-step-01 migrations have put
// the tenant on the current layout, so these checks see post-migration
// state. F1, F2, F4 actively remediate; F3, F5+F8, F6, F7, F9 are flag-
// only (their remediation requires operator judgment).

// checkReferencePayloadIntegrity is F1: per-file byte compare of the
// embedded reference payload against on-disk fixtures. Complements
// checkBundleReferencePayload (which version-gates) by catching the
// "right version, wrong bytes" case — a tenant on the correct
// shape/theme version but with hand-edited or corrupted templates.
// bundle.EnsureReferencePayload is content-idempotent (writes only on
// byte-mismatch), so the heal is a no-op when content already matches.
//
// Mirror of patrol.checkReferencePayloadIntegrity (`patrol.go:2109`) +
// medic.go HealTenant reference-payload-integrity block.
func checkReferencePayloadIntegrity(ctx *runContext) CheckResult {
	const name = "reference-payload-integrity"
	const reason = "F1 content-aware integrity — even when version stamps match, on-disk fixtures can drift from the embedded reference payload via hand-edits or partial writes; reinstall is content-idempotent so safe to run unconditionally"

	bundleDir := filepath.Join(ctx.siteDir, ".polis", "bundles", "pub.polis.core")
	if _, err := os.Stat(bundleDir); os.IsNotExist(err) {
		// checkBundleReferencePayload handles the not-installed case.
		return pass(name, "bundle not installed (deferred to bundle-reference-payload check)")
	}
	mismatches, err := bundle.CompareReferencePayload(ctx.siteDir, "pub.polis.core")
	if err != nil {
		return skip(name, fmt.Sprintf("compare reference payload: %v", err), reason)
	}
	if len(mismatches) == 0 {
		return pass(name, "reference payload matches embedded fixtures byte-for-byte")
	}
	sample := mismatches
	if len(sample) > 5 {
		sample = sample[:5]
	}
	action := Action{
		Op:     "update",
		Path:   ".polis/bundles/pub.polis.core/",
		Detail: fmt.Sprintf("re-install reference payload (%d file(s) drift, e.g. %s)", len(mismatches), strings.Join(sample, ", ")),
	}
	if ctx.dryRun {
		return fail(name, fmt.Sprintf("reference payload drift: %d file(s) differ", len(mismatches)), reason, []Action{action})
	}
	if err := bundle.EnsureReferencePayload(ctx.siteDir, "pub.polis.core"); err != nil {
		return fail(name, fmt.Sprintf("EnsureReferencePayload failed: %v", err), reason, []Action{action})
	}
	return CheckResult{
		Name:    name,
		Status:  StatusFail,
		Reason:  reason,
		Message: fmt.Sprintf("re-installed %d drifted reference-payload file(s)", len(mismatches)),
		Actions: []Action{action},
	}
}

// checkRegistryIntegrity is F2: validate registry.json structure and
// coherence with a seven-mode remediation dispatch. The studio13 rename
// case is intentionally excluded — checkStudio13RenameMigration handles
// it earlier in the run, so by the time F2 fires the registry should be
// on the post-rename name (or on something else entirely).
//
//  1. Malformed JSON → flag (operator must repair).
//  2. schema_version exceeds supported max → flag (binary upgrade).
//  3. schema_version not integer → flag.
//  4. active_theme unparseable + bare name in defaults → normalize.
//  5. active_theme unparseable + not in defaults → SelectRandomTheme.
//  6. active_theme valid FQN but not declared → SelectRandomTheme.
//  7. active_theme empty → SelectRandomTheme.
//  8. active_shape variants of 4/5/6 → normalize or reset to v4.
//
// Mirror of patrol.checkRegistryIntegrity + medic.healRegistryIntegrity
// (`medic.go:764-917`).
func checkRegistryIntegrity(ctx *runContext) CheckResult {
	const name = "registry-integrity"
	const reason = "F2 content-aware integrity — registry.json drives render dispatch, active theme selection, and bundle resync; even small corruptions cascade into render-time errors"

	raw, err := bundle.LoadRegistryRaw(ctx.siteDir)
	if err != nil {
		action := Action{Op: "flag", Path: ".polis/bundles/registry.json",
			Detail: "registry.json malformed; operator intervention required (Tailor will not overwrite a hand-edited registry)"}
		return fail(name, fmt.Sprintf("registry.json malformed: %v", err), reason, []Action{action})
	}
	if raw == nil {
		// Absent registry — checkBundleReferencePayload handles install.
		return pass(name, "no registry (deferred to bundle-reference-payload check)")
	}
	// Schema version checks.
	if sv, ok := raw["schema_version"].(float64); ok {
		if math.Trunc(sv) != sv {
			action := Action{Op: "flag", Path: ".polis/bundles/registry.json",
				Detail: fmt.Sprintf("schema_version=%v non-integer; suspected hand-edit corruption", sv)}
			return fail(name, fmt.Sprintf("schema_version=%v is not an integer", sv), reason, []Action{action})
		}
		if int(sv) > bundle.CurrentRegistrySchemaVersion {
			action := Action{Op: "flag", Path: ".polis/bundles/registry.json",
				Detail: fmt.Sprintf("schema_version=%d exceeds supported max=%d; binary upgrade required", int(sv), bundle.CurrentRegistrySchemaVersion)}
			return fail(name, fmt.Sprintf("schema_version=%d exceeds supported max=%d", int(sv), bundle.CurrentRegistrySchemaVersion), reason, []Action{action})
		}
	}
	defaults := bundle.DefaultCoreBundle()
	at, _ := raw["active_theme"].(string)
	as, _ := raw["active_shape"].(string)

	// Defer the studio13 special case — it's handled by checkStudio13RenameMigration.
	if at == "pub.polis.themes.studio13" && (as == "pub.polis.shapes.v3" || as == "") {
		return pass(name, "registry on pre-rename studio13/v3 — deferred to studio13-rename-migration check")
	}

	// active_theme: unparseable FQN, dangling FQN, or empty.
	if at != "" {
		fqn, err := bundle.ParseFQN(at)
		if err != nil {
			// Unparseable. Try normalize-if-bare-name in defaults.
			if bundle.IsBareName(at) {
				if _, ok := defaults.Themes[at]; ok {
					action := Action{Op: "update", Path: ".polis/bundles/registry.json",
						Detail: fmt.Sprintf("normalize bare active_theme %q → %s", at, bundle.QualifyTheme(at))}
					if ctx.dryRun {
						return fail(name, fmt.Sprintf("active_theme %q is a bare name; should be FQN-qualified", at), reason, []Action{action})
					}
					if err := bundle.SetActiveThemeName(ctx.siteDir, at); err != nil {
						return fail(name, fmt.Sprintf("SetActiveThemeName failed: %v", err), reason, []Action{action})
					}
					return CheckResult{Name: name, Status: StatusFail, Reason: reason,
						Message: fmt.Sprintf("normalized active_theme: bare %q → %s", at, bundle.QualifyTheme(at)),
						Actions: []Action{action}}
				}
			}
			// Unparseable + not a known bare name → reset.
			action := Action{Op: "update", Path: ".polis/bundles/registry.json",
				Detail: fmt.Sprintf("active_theme %q unparseable; reset to a random valid theme", at)}
			if ctx.dryRun {
				return fail(name, fmt.Sprintf("active_theme %q is not a valid FQN and not a known bare name", at), reason, []Action{action})
			}
			if _, err := theme.SelectRandomTheme(ctx.siteDir, ""); err != nil {
				return fail(name, fmt.Sprintf("SelectRandomTheme failed: %v", err), reason, []Action{action})
			}
			return CheckResult{Name: name, Status: StatusFail, Reason: reason,
				Message: "reset active_theme to a random valid theme (was unparseable)",
				Actions: []Action{action}}
		}
		if _, ok := defaults.Themes[fqn.Name]; !ok {
			// Valid FQN but dangling (not declared in bundle).
			action := Action{Op: "update", Path: ".polis/bundles/registry.json",
				Detail: fmt.Sprintf("active_theme %q not declared in bundle; reset to a random valid theme", at)}
			if ctx.dryRun {
				return fail(name, fmt.Sprintf("active_theme %q not declared in bundle", at), reason, []Action{action})
			}
			if _, err := theme.SelectRandomTheme(ctx.siteDir, ""); err != nil {
				return fail(name, fmt.Sprintf("SelectRandomTheme failed: %v", err), reason, []Action{action})
			}
			return CheckResult{Name: name, Status: StatusFail, Reason: reason,
				Message: fmt.Sprintf("reset active_theme to a random valid theme (was dangling: %q)", at),
				Actions: []Action{action}}
		}
	} else {
		// Empty active_theme.
		action := Action{Op: "update", Path: ".polis/bundles/registry.json",
			Detail: "active_theme empty; pick a random valid theme"}
		if ctx.dryRun {
			return fail(name, "active_theme empty", reason, []Action{action})
		}
		if _, err := theme.SelectRandomTheme(ctx.siteDir, ""); err != nil {
			return fail(name, fmt.Sprintf("SelectRandomTheme failed: %v", err), reason, []Action{action})
		}
		return CheckResult{Name: name, Status: StatusFail, Reason: reason,
			Message: "populated empty active_theme with a random valid theme",
			Actions: []Action{action}}
	}

	// active_shape: unparseable FQN, dangling FQN.
	if as != "" {
		fqn, err := bundle.ParseFQN(as)
		if err != nil {
			if bundle.IsBareName(as) {
				if _, ok := defaults.Shapes[as]; ok {
					action := Action{Op: "update", Path: ".polis/bundles/registry.json",
						Detail: fmt.Sprintf("normalize bare active_shape %q → %s", as, bundle.QualifyShape(as))}
					if ctx.dryRun {
						return fail(name, fmt.Sprintf("active_shape %q is a bare name; should be FQN-qualified", as), reason, []Action{action})
					}
					if err := bundle.SetActiveShapeName(ctx.siteDir, as); err != nil {
						return fail(name, fmt.Sprintf("SetActiveShapeName failed: %v", err), reason, []Action{action})
					}
					return CheckResult{Name: name, Status: StatusFail, Reason: reason,
						Message: fmt.Sprintf("normalized active_shape: bare %q → %s", as, bundle.QualifyShape(as)),
						Actions: []Action{action}}
				}
			}
			action := Action{Op: "update", Path: ".polis/bundles/registry.json",
				Detail: fmt.Sprintf("active_shape %q unparseable; reset to v4 default", as)}
			if ctx.dryRun {
				return fail(name, fmt.Sprintf("active_shape %q is not a valid FQN", as), reason, []Action{action})
			}
			if err := bundle.SetActiveShapeName(ctx.siteDir, "v4"); err != nil {
				return fail(name, fmt.Sprintf("SetActiveShapeName failed: %v", err), reason, []Action{action})
			}
			return CheckResult{Name: name, Status: StatusFail, Reason: reason,
				Message: "reset active_shape to v4 default (was unparseable)",
				Actions: []Action{action}}
		}
		if _, ok := defaults.Shapes[fqn.Name]; !ok {
			action := Action{Op: "update", Path: ".polis/bundles/registry.json",
				Detail: fmt.Sprintf("active_shape %q not declared in bundle; reset to v4 default", as)}
			if ctx.dryRun {
				return fail(name, fmt.Sprintf("active_shape %q not declared in bundle", as), reason, []Action{action})
			}
			if err := bundle.SetActiveShapeName(ctx.siteDir, "v4"); err != nil {
				return fail(name, fmt.Sprintf("SetActiveShapeName failed: %v", err), reason, []Action{action})
			}
			return CheckResult{Name: name, Status: StatusFail, Reason: reason,
				Message: fmt.Sprintf("reset active_shape to v4 default (was dangling: %q)", as),
				Actions: []Action{action}}
		}
	}

	return pass(name, "registry integrity OK")
}

// checkKeyConsistency is F3: verify that .polis/keys/id_ed25519.pub
// matches .well-known/polis.public_key. Divergence silently breaks
// signature verification for every downstream post/comment.
//
// Flag-only: deciding which file "wins" is a trust decision (operator
// may have rotated the private key without updating well-known, or
// well-known may be tampered). Tailor surfaces the divergence; the
// self-hoster decides.
//
// Mirror of patrol.checkKeyConsistency (`patrol.go:2207`).
func checkKeyConsistency(ctx *runContext) CheckResult {
	const name = "key-consistency"
	const reason = "F3 content-aware integrity — divergence between .polis/keys/id_ed25519.pub and .well-known/polis.public_key silently breaks signature verification; the resolution is operator-trust-dependent and not auto-remediable"

	pubKeyPath := filepath.Join(ctx.siteDir, ".polis", "keys", "id_ed25519.pub")
	pubKeyData, err := os.ReadFile(pubKeyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fail(name, "public key file missing at .polis/keys/id_ed25519.pub", reason,
				[]Action{{Op: "flag", Path: ".polis/keys/id_ed25519.pub", Detail: "missing public key file"}})
		}
		return skip(name, fmt.Sprintf("cannot read .polis/keys/id_ed25519.pub: %v", err), reason)
	}
	wk, err := site.LoadWellKnown(ctx.siteDir)
	if err != nil {
		return skip(name, fmt.Sprintf("cannot load .well-known/polis: %v", err), reason)
	}
	if strings.TrimSpace(string(pubKeyData)) == strings.TrimSpace(wk.PublicKey) {
		return pass(name, "public key matches between .polis/keys/ and .well-known/polis")
	}
	action := Action{
		Op:     "flag",
		Path:   ".well-known/polis, .polis/keys/id_ed25519.pub",
		Detail: "public_key divergence — verify which is correct (rotated key not updated in well-known? tampered well-known?) and reconcile manually",
	}
	return fail(name,
		"public_key in .well-known/polis does not match .polis/keys/id_ed25519.pub",
		reason, []Action{action})
}

// checkBundlePathIntegrity is F4+F9: validate each declared bundle's
// `path` in .well-known/polis points at a readable file, auto-repair
// pub.polis.core to the canonical path, flag non-core. Also flags
// empty author_name (F9). Other malformed well-known cases (invalid
// public_key, missing bundles list, etc.) are left to checkWellKnownBundles
// and site.Validate.
//
// Mirror of patrol.checkWellKnownFields (F4+F9 portions, `patrol.go:1645-1662`)
// + medic.healWellKnownFields + medic.rewriteCoreBundlePath.
func checkBundlePathIntegrity(ctx *runContext) CheckResult {
	const name = "bundle-path-integrity"
	const reason = "F4 content-aware integrity — every declared bundle's path must point at a readable bundle.json; pub.polis.core is repairable to a canonical path, non-core bundles need operator review. F9 — empty author_name breaks identity for rendered pages"

	wk, err := site.LoadWellKnown(ctx.siteDir)
	if err != nil {
		return skip(name, fmt.Sprintf("cannot load .well-known/polis: %v", err), reason)
	}

	const canonicalCorePath = "content/pub.polis.core/bundle.json"

	// F4: per-bundle path validation.
	for bname, entry := range wk.Bundles {
		problem := ""
		if entry.Path == "" {
			problem = fmt.Sprintf("bundle %s has empty path", bname)
		} else if _, err := os.Stat(filepath.Join(ctx.siteDir, entry.Path)); err != nil {
			if os.IsNotExist(err) {
				problem = fmt.Sprintf("bundle %s path %q missing on disk", bname, entry.Path)
			} else {
				problem = fmt.Sprintf("bundle %s path %q: %v", bname, entry.Path, err)
			}
		}
		if problem == "" {
			continue
		}
		if bname == "pub.polis.core" {
			action := Action{
				Op:     "update",
				Path:   ".well-known/polis",
				Detail: fmt.Sprintf("rewrite bundles.pub.polis.core.path → %s (canonical)", canonicalCorePath),
			}
			if ctx.dryRun {
				return fail(name, problem, reason, []Action{action})
			}
			raw, rerr := site.LoadWellKnownRaw(ctx.siteDir)
			if rerr != nil || raw == nil {
				return fail(name, fmt.Sprintf("load well-known raw: %v", rerr), reason, []Action{action})
			}
			bundles, _ := raw["bundles"].(map[string]interface{})
			if bundles == nil {
				bundles = map[string]interface{}{}
				raw["bundles"] = bundles
			}
			core, _ := bundles["pub.polis.core"].(map[string]interface{})
			if core == nil {
				core = map[string]interface{}{}
			}
			core["path"] = canonicalCorePath
			bundles["pub.polis.core"] = core
			if err := site.SaveWellKnownRaw(ctx.siteDir, raw); err != nil {
				return fail(name, fmt.Sprintf("save well-known: %v", err), reason, []Action{action})
			}
			return CheckResult{Name: name, Status: StatusFail, Reason: reason,
				Message: fmt.Sprintf("rewrote pub.polis.core path to %s", canonicalCorePath),
				Actions: []Action{action}}
		}
		// Non-core bundle: flag-only.
		action := Action{Op: "flag", Path: ".well-known/polis",
			Detail: problem + "; operator intervention required for non-core bundles"}
		return fail(name, problem, reason, []Action{action})
	}

	// F9: author_name non-empty.
	if strings.TrimSpace(wk.AuthorName) == "" {
		action := Action{Op: "flag", Path: ".well-known/polis",
			Detail: "author_name empty; set a display name for rendered identity"}
		return fail(name, "author_name is empty in .well-known/polis", reason, []Action{action})
	}

	return pass(name, "bundle paths readable and author_name set")
}

// checkBundleDeclarations is F5+F8: deep-validate content/pub.polis.core/
// bundle.json against bundle.DefaultCoreBundle(). F5 auto-merges missing
// declarations (types, shapes, themes) via Bundle.MergeDefaults — F8
// flags per-field drift on existing types (Dir, Mount, Storage.Pattern,
// Emits superset) without auto-repair, since drift on an existing type
// may reflect legitimate per-tenant customization that Tailor cannot
// safely overwrite without operator review.
//
// The existing checkBundleJSON already merges missing types via
// MergeDefaults; this check extends coverage to Shapes and Themes,
// plus F8 deep-field comparison.
//
// Mirror of patrol.checkBundleDeclarations (`patrol.go:1015-1076`).
func checkBundleDeclarations(ctx *runContext) CheckResult {
	const name = "bundle-declarations"
	const reason = "F5+F8 content-aware integrity — bundle.json declares types/shapes/themes; missing declarations are auto-merged from defaults, per-field drift on existing types is flagged for operator review"

	path := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "bundle.json")
	b, err := bundle.LoadBundle(path)
	if err != nil {
		return skip(name, fmt.Sprintf("cannot load bundle.json: %v", err), reason)
	}
	defaults := bundle.DefaultCoreBundle()

	var missing []string
	for tname := range defaults.Types {
		if _, ok := b.Types[tname]; !ok {
			missing = append(missing, "type:"+tname)
		}
	}
	for sname := range defaults.Shapes {
		if _, ok := b.Shapes[sname]; !ok {
			missing = append(missing, "shape:"+sname)
		}
	}
	for tname := range defaults.Themes {
		if _, ok := b.Themes[tname]; !ok {
			missing = append(missing, "theme:"+tname)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		action := Action{Op: "update", Path: "content/pub.polis.core/bundle.json",
			Detail: "merge missing declarations from defaults: " + strings.Join(missing, ", ")}
		if ctx.dryRun {
			return fail(name, "missing declarations: "+strings.Join(missing, ", "), reason, []Action{action})
		}
		if b.MergeDefaults(defaults) {
			if err := bundle.SaveBundle(path, b); err != nil {
				return fail(name, fmt.Sprintf("save bundle.json: %v", err), reason, []Action{action})
			}
		}
		// Re-load and continue with F8 drift check on the merged bundle.
		b, _ = bundle.LoadBundle(path)
		// (fall through to F8 drift check below — the operator may have
		// additional issues beyond just-missing declarations)
	}

	// F8: per-type field drift.
	var drift []string
	for tname, want := range defaults.Types {
		got, ok := b.Types[tname]
		if !ok {
			continue
		}
		if got.Dir != want.Dir {
			drift = append(drift, fmt.Sprintf("type %s: dir mismatch (got %q, want %q)", tname, got.Dir, want.Dir))
		}
		if got.Mount != want.Mount {
			drift = append(drift, fmt.Sprintf("type %s: mount mismatch (got %q, want %q)", tname, got.Mount, want.Mount))
		}
		if want.Storage != nil {
			gotPattern := ""
			if got.Storage != nil {
				gotPattern = got.Storage.Pattern
			}
			if got.Storage == nil || got.Storage.Pattern != want.Storage.Pattern {
				drift = append(drift, fmt.Sprintf("type %s: storage.pattern mismatch (got %q, want %q)", tname, gotPattern, want.Storage.Pattern))
			}
		}
		wantSet := make(map[string]bool, len(got.Emits))
		for _, e := range got.Emits {
			wantSet[e] = true
		}
		for _, e := range want.Emits {
			if !wantSet[e] {
				drift = append(drift, fmt.Sprintf("type %s: missing emit %q (want superset of defaults)", tname, e))
			}
		}
	}
	if len(drift) > 0 {
		sort.Strings(drift)
		action := Action{Op: "flag", Path: "content/pub.polis.core/bundle.json",
			Detail: "per-type field drift; operator review required (auto-repair would clobber legitimate customizations): " + strings.Join(drift, "; ")}
		return fail(name, "type field drift: "+strings.Join(drift, "; "), reason, []Action{action})
	}

	if len(missing) > 0 {
		return CheckResult{Name: name, Status: StatusFail, Reason: reason,
			Message: "merged missing declarations: " + strings.Join(missing, ", "),
			Actions: []Action{{Op: "update", Path: "content/pub.polis.core/bundle.json",
				Detail: "merge missing declarations"}}}
	}
	return pass(name, "bundle declarations complete and match defaults")
}

// checkIndexEntries is F6: per-entry validation of
// content/pub.polis.core/index.jsonl. Flag-only — rebuilding the
// index is the existing checkIndexRebuild pass; this is a structural
// sanity check on whatever's currently on disk.
//
// Mirror of patrol.checkIndexJSONL (`patrol.go:1093-1137`).
func checkIndexEntries(ctx *runContext) CheckResult {
	const name = "index-entries"
	const reason = "F6 content-aware integrity — each line in index.jsonl must be parseable JSON with non-empty type/path/published/current_version; published RFC3339; current_version sha256:-prefixed"

	path := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "index.jsonl")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return pass(name, "no index.jsonl")
	}
	if err != nil {
		return skip(name, fmt.Sprintf("cannot open index.jsonl: %v", err), reason)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNum := 0
	var problems []string
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			problems = append(problems, fmt.Sprintf("line %d: invalid JSON (%v)", lineNum, err))
			continue
		}
		for _, req := range []string{"type", "path", "published", "current_version"} {
			s, _ := entry[req].(string)
			if s == "" {
				problems = append(problems, fmt.Sprintf("line %d: missing required field %q", lineNum, req))
			}
		}
		cv, _ := entry["current_version"].(string)
		if cv != "" && !strings.HasPrefix(cv, "sha256:") {
			problems = append(problems, fmt.Sprintf("line %d: current_version %q does not start with sha256:", lineNum, cv))
		}
		published, _ := entry["published"].(string)
		if published != "" {
			if _, err := time.Parse(time.RFC3339, published); err != nil {
				problems = append(problems, fmt.Sprintf("line %d: published %q not RFC3339", lineNum, published))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return skip(name, fmt.Sprintf("scan index.jsonl: %v", err), reason)
	}
	if len(problems) == 0 {
		return pass(name, "all index entries valid")
	}
	sample := problems
	if len(sample) > 3 {
		sample = sample[:3]
	}
	action := Action{Op: "flag", Path: "content/pub.polis.core/index.jsonl",
		Detail: fmt.Sprintf("%d entry problem(s); rebuild via `polis tailor` index-rebuild pass if persistent: %s", len(problems), strings.Join(sample, "; "))}
	return fail(name, fmt.Sprintf("%d index entry problem(s)", len(problems)), reason, []Action{action})
}

// checkBlessedFollowingStructure is F7: per-entry validation of
// blessed.json and following.json against the actual shipped on-disk
// schema. Flag-only — rebuilding these files is the operator's call.
//
// Mirror of patrol.checkBlessedJSON + checkFollowingJSON
// (`patrol.go:1139-1230`). Note: the planning doc hinted at
// `url/blessed_at` for both, but the shipped on-disk format wraps
// blessed entries in per-post groupings (post + blessed[]).
func checkBlessedFollowingStructure(ctx *runContext) CheckResult {
	const name = "blessed-following-structure"
	const reason = "F7 content-aware integrity — blessed.json and following.json structure must match the on-disk schema (blessed: per-post groupings; following: flat entries) to avoid silent breakage in feeds and post rendering"

	var problems []string

	// blessed.json
	blessedPath := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "comment", "blessed.json")
	if data, err := os.ReadFile(blessedPath); err == nil {
		problems = append(problems, validateBlessedJSON(data)...)
	} else if !os.IsNotExist(err) {
		problems = append(problems, fmt.Sprintf("blessed.json: read failed: %v", err))
	}

	// following.json
	followingPath := filepath.Join(ctx.siteDir, "content", "pub.polis.core", "follow", "following.json")
	if data, err := os.ReadFile(followingPath); err == nil {
		problems = append(problems, validateFollowingJSON(data)...)
	} else if !os.IsNotExist(err) {
		problems = append(problems, fmt.Sprintf("following.json: read failed: %v", err))
	}

	if len(problems) == 0 {
		return pass(name, "blessed.json + following.json structure OK (or absent)")
	}
	action := Action{Op: "flag",
		Path:   "content/pub.polis.core/comment/blessed.json, content/pub.polis.core/follow/following.json",
		Detail: "structural problems require operator review: " + strings.Join(problems, "; "),
	}
	return fail(name, strings.Join(problems, "; "), reason, []Action{action})
}

func validateBlessedJSON(data []byte) []string {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return []string{fmt.Sprintf("blessed.json: invalid JSON (%v)", err)}
	}
	commentsRaw, ok := raw["comments"]
	if !ok {
		return []string{"blessed.json: missing required field 'comments'"}
	}
	arr, ok := commentsRaw.([]interface{})
	if !ok {
		return []string{"blessed.json: 'comments' is not an array"}
	}
	var problems []string
	for i, elem := range arr {
		grp, ok := elem.(map[string]interface{})
		if !ok {
			problems = append(problems, fmt.Sprintf("blessed.json: comments[%d] is not an object", i))
			continue
		}
		if s, _ := grp["post"].(string); s == "" {
			problems = append(problems, fmt.Sprintf("blessed.json: comments[%d] missing 'post'", i))
		}
		blessedRaw, ok := grp["blessed"]
		if !ok {
			problems = append(problems, fmt.Sprintf("blessed.json: comments[%d] missing 'blessed'", i))
			continue
		}
		blessed, ok := blessedRaw.([]interface{})
		if !ok {
			problems = append(problems, fmt.Sprintf("blessed.json: comments[%d].blessed is not an array", i))
			continue
		}
		for j, b := range blessed {
			bm, ok := b.(map[string]interface{})
			if !ok {
				problems = append(problems, fmt.Sprintf("blessed.json: comments[%d].blessed[%d] is not an object", i, j))
				continue
			}
			for _, req := range []string{"url", "blessed_at"} {
				if s, _ := bm[req].(string); s == "" {
					problems = append(problems, fmt.Sprintf("blessed.json: comments[%d].blessed[%d] missing %q", i, j, req))
				}
			}
		}
	}
	return problems
}

// ── Phase 6.7: Tailor-only self-heal ─────────────────────────────────────
//
// Tailor-specific checks with no Patrol/Medic counterpart. Hosted tenants
// reach the same end state via runtime render fallbacks (e.g. page.go's
// theme picker) that fire on every visit; self-hosters who never render
// can sit indefinitely in a half-initialized state. These checks close
// that gap. Most overlap with F2's seven-mode dispatch; they're kept as
// defense-in-depth and as a forward-compatible plumbing layer for the
// registry schema-version migration path.

// checkActiveThemeUnset — for self-hosted tenants inited before the
// theme-picker landed in render/page.go (or who never rendered), the
// registry's active_theme may be "" indefinitely. F2 also covers this
// case, but checkActiveThemeUnset is kept as a defense-in-depth check
// (one clear name in the diagnose output).
//
// Tailor-only: hosted tenants self-heal via render/page.go's
// SelectRandomTheme fallback on each page request.
func checkActiveThemeUnset(ctx *runContext) CheckResult {
	const name = "active-theme-unset"
	const reason = "self-hosted tenants without an active_theme set in registry.json never auto-recover (hosted tenants self-heal via render/page.go's fallback); Tailor explicitly picks one so post-tailor sites are render-ready"

	at, err := bundle.GetActiveThemeName(ctx.siteDir)
	if err != nil {
		// No registry yet — checkBundleReferencePayload installs it earlier
		// in the run; on a second pass active_theme should be populated.
		return pass(name, "no registry to check (deferred to bundle-reference-payload)")
	}
	if at != "" {
		return pass(name, fmt.Sprintf("active_theme set: %s", at))
	}
	action := Action{Op: "update", Path: ".polis/bundles/registry.json",
		Detail: "active_theme empty; picking a random valid theme"}
	if ctx.dryRun {
		return fail(name, "active_theme empty in registry", reason, []Action{action})
	}
	picked, err := theme.SelectRandomTheme(ctx.siteDir, "")
	if err != nil {
		return fail(name, fmt.Sprintf("SelectRandomTheme failed: %v", err), reason, []Action{action})
	}
	return CheckResult{Name: name, Status: StatusFail, Reason: reason,
		Message: fmt.Sprintf("picked random valid theme: %s", picked),
		Actions: []Action{action}}
}

// checkRegistrySchemaVersion — validate that registry.json's schema_version
// matches what the binary supports, and forward-migrate when older versions
// land. Today (schema_version: 2 is current) the only relevant case is
// future-schema-version (which F2 already flags); the older-version branch
// is a no-op stub. Wiring the plumbing now is cheap; when CurrentRegistrySchemaVersion
// bumps in the future, this is where the forward-migration logic lands.
//
// Tailor-only by intent: Patrol's schema-version check is flag-only via F2;
// auto-forward-migration belongs in user-invoked Tailor where the operator
// has consented to mutation.
func checkRegistrySchemaVersion(ctx *runContext) CheckResult {
	const name = "registry-schema-version"
	const reason = "registry.json carries schema_version; older versions need forward-migration on binary upgrade. Stub today (schema_version: 2 is current); future bumps land their migration logic here"

	raw, err := bundle.LoadRegistryRaw(ctx.siteDir)
	if err != nil || raw == nil {
		return pass(name, "no registry (deferred)")
	}
	svRaw, ok := raw["schema_version"]
	if !ok {
		return pass(name, "no schema_version field (treated as 0 — older format; no migration registered yet)")
	}
	sv, ok := svRaw.(float64)
	if !ok || math.Trunc(sv) != sv {
		// Non-integer is F2's territory.
		return pass(name, "schema_version not an integer (deferred to registry-integrity)")
	}
	current := bundle.CurrentRegistrySchemaVersion
	switch {
	case int(sv) == current:
		return pass(name, fmt.Sprintf("schema_version=%d matches binary's supported version", int(sv)))
	case int(sv) > current:
		// Future schema — F2 flags this; nothing to do here.
		return pass(name, fmt.Sprintf("schema_version=%d exceeds supported %d (deferred to registry-integrity)", int(sv), current))
	default:
		// Older schema. Today nothing actionable; placeholder for future
		// per-version migration logic. Flag-only so the operator is aware.
		action := Action{Op: "flag", Path: ".polis/bundles/registry.json",
			Detail: fmt.Sprintf("schema_version=%d is older than current %d; no forward migration registered yet (no-op stub)", int(sv), current)}
		return fail(name, fmt.Sprintf("registry schema_version=%d is older than binary's supported %d", int(sv), current), reason, []Action{action})
	}
}

// checkRegistryFQNSanity — defense-in-depth wrapper around F2 for the
// FQN-parse paths. F2 catches the same cases and remediates them; this
// check exists so the diagnose output names the specific failure mode
// for self-hosters who haven't run F2 yet (e.g. on the first half of a
// dual-pass Apply where F2 was the one that fired). Idempotent and
// effectively a no-op when F2 has already run.
//
// Tailor-only: Patrol's check produces a single message string with all
// FQN failure modes folded together; this check splits them out for
// clearer self-hoster diagnose output.
func checkRegistryFQNSanity(ctx *runContext) CheckResult {
	const name = "registry-fqn-sanity"
	const reason = "active_theme and active_shape must be valid FQNs (pub.polis.themes.<name>, pub.polis.shapes.<name>) for the render pipeline to dispatch correctly; bare names like 'vice' are auto-corrected, unrecognized names are flagged"

	raw, err := bundle.LoadRegistryRaw(ctx.siteDir)
	if err != nil || raw == nil {
		return pass(name, "no registry to check")
	}
	defaults := bundle.DefaultCoreBundle()
	at, _ := raw["active_theme"].(string)
	as, _ := raw["active_shape"].(string)

	var issues []string
	if at != "" {
		if _, err := bundle.ParseFQN(at); err != nil {
			if bundle.IsBareName(at) {
				if _, ok := defaults.Themes[at]; ok {
					issues = append(issues, fmt.Sprintf("active_theme %q is a bare name (would qualify to %s)", at, bundle.QualifyTheme(at)))
				} else {
					issues = append(issues, fmt.Sprintf("active_theme %q is not a recognized theme name", at))
				}
			} else {
				issues = append(issues, fmt.Sprintf("active_theme %q is not a valid FQN", at))
			}
		}
	}
	if as != "" {
		if _, err := bundle.ParseFQN(as); err != nil {
			if bundle.IsBareName(as) {
				if _, ok := defaults.Shapes[as]; ok {
					issues = append(issues, fmt.Sprintf("active_shape %q is a bare name (would qualify to %s)", as, bundle.QualifyShape(as)))
				} else {
					issues = append(issues, fmt.Sprintf("active_shape %q is not a recognized shape name", as))
				}
			} else {
				issues = append(issues, fmt.Sprintf("active_shape %q is not a valid FQN", as))
			}
		}
	}
	if len(issues) == 0 {
		return pass(name, "FQN fields parse cleanly")
	}
	// Don't remediate here — F2 owns the remediation. Just surface the
	// specific failure mode for diagnostic clarity.
	action := Action{Op: "flag", Path: ".polis/bundles/registry.json",
		Detail: "FQN format issues; registry-integrity check will auto-correct or reset"}
	return fail(name, strings.Join(issues, "; "), reason, []Action{action})
}

func validateFollowingJSON(data []byte) []string {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return []string{fmt.Sprintf("following.json: invalid JSON (%v)", err)}
	}
	followingRaw, ok := raw["following"]
	if !ok {
		return []string{"following.json: missing required field 'following'"}
	}
	arr, ok := followingRaw.([]interface{})
	if !ok {
		return []string{"following.json: 'following' is not an array"}
	}
	var problems []string
	for i, elem := range arr {
		em, ok := elem.(map[string]interface{})
		if !ok {
			problems = append(problems, fmt.Sprintf("following.json: following[%d] is not an object", i))
			continue
		}
		for _, req := range []string{"url", "added_at"} {
			if s, _ := em[req].(string); s == "" {
				problems = append(problems, fmt.Sprintf("following.json: following[%d] missing %q", i, req))
			}
		}
	}
	return problems
}
