package tailor

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// semverPattern matches a bare semver string like "0.42.0".
var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

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
	if err := writeWellKnownRaw(wkPath, raw); err != nil {
		return fail(name, fmt.Sprintf("Failed to update: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("Updated %q → %q", version, ctx.generator), Reason: reason, Actions: actions}
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

	if err := writeWellKnownRaw(wkPath, raw); err != nil {
		return fail(name, fmt.Sprintf("Failed to update: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("Removed legacy keys: %s", strings.Join(removed, ", ")), Reason: reason, Actions: actions}
}

// checkWellKnownBundles adds the bundles registry if missing.
func checkWellKnownBundles(ctx *runContext) CheckResult {
	const name = "wellknown-bundles"
	const reason = "CLI v0.58.0 introduced the bundle system — .well-known/polis must declare active bundles for the CLI to locate content"

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
			"active": true,
			"path":   "content/pub.polis.core/bundle.json",
		},
	}

	if err := writeWellKnownRaw(wkPath, raw); err != nil {
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

	resp, err := http.Get(downloadURL)
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

// writeWellKnownRaw writes a raw map back to .well-known/polis with pretty-printing.
func writeWellKnownRaw(path string, raw map[string]interface{}) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
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

// checkDMDirectories ensures .polis/content/pub.polis.core/dm/conv/ exists with 0700 perms.
func checkDMDirectories(ctx *runContext) CheckResult {
	const name = "dm-directories"
	const reason = "DM conversations are stored in .polis/content/pub.polis.core/dm/conv/ with restricted permissions"

	convPath := filepath.Join(ctx.siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")

	if dirExists(convPath) {
		// Check permissions
		info, err := os.Stat(convPath)
		if err == nil && info.Mode().Perm() != 0700 {
			actions := []Action{{Op: "update", Path: ".polis/content/pub.polis.core/dm/conv", Detail: fmt.Sprintf("chmod %o → 0700", info.Mode().Perm())}}
			if ctx.dryRun {
				return fail(name, fmt.Sprintf("DM directory has unsafe permissions: %o", info.Mode().Perm()), reason, actions)
			}
			os.Chmod(convPath, 0700)
			return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("Fixed DM directory permissions: %o → 0700", info.Mode().Perm()), Reason: reason, Actions: actions}
		}
		return pass(name, "DM directories present with correct permissions")
	}

	actions := []Action{{Op: "create", Path: ".polis/content/pub.polis.core/dm/conv"}}

	if ctx.dryRun {
		return fail(name, "Missing DM directories", reason, actions)
	}

	if err := os.MkdirAll(convPath, 0700); err != nil {
		return fail(name, fmt.Sprintf("Failed to create: %v", err), reason, actions)
	}
	return CheckResult{Name: name, Status: StatusFail, Message: "Created DM directories", Reason: reason, Actions: actions}
}

// checkDMDomainCase normalizes mixed-case domains to lowercase in DM conversations.
func checkDMDomainCase(ctx *runContext) CheckResult {
	const name = "dm-domain-case"
	const reason = "DNS hostnames are case-insensitive (RFC 1123) — mixed-case domains cause conversation ID mismatches"

	convPath := filepath.Join(ctx.siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	if !dirExists(convPath) {
		return pass(name, "No DM conversations to check")
	}

	entries, err := os.ReadDir(convPath)
	if err != nil {
		return pass(name, "No DM conversations to check")
	}

	var mixedCaseFiles []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(convPath, entry.Name()))
		if err != nil {
			continue
		}
		var conv struct {
			PeerDomain string `json:"peer_domain"`
		}
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}
		if conv.PeerDomain != strings.ToLower(conv.PeerDomain) {
			mixedCaseFiles = append(mixedCaseFiles, entry.Name())
		}
	}

	// Also check conversations index
	idxPath := filepath.Join(ctx.siteDir, ".polis", "content", "pub.polis.core", "dm", "conversations.json")
	if idxData, err := os.ReadFile(idxPath); err == nil {
		var idx struct {
			Conversations []map[string]interface{} `json:"conversations"`
		}
		if json.Unmarshal(idxData, &idx) == nil {
			for _, c := range idx.Conversations {
				if pd, ok := c["peer_domain"].(string); ok && pd != strings.ToLower(pd) {
					if len(mixedCaseFiles) == 0 {
						mixedCaseFiles = append(mixedCaseFiles, "conversations.json")
					}
					break
				}
			}
		}
	}

	if len(mixedCaseFiles) == 0 {
		return pass(name, "All DM domains are lowercase")
	}

	actions := []Action{{Op: "update", Path: ".polis/content/pub.polis.core/dm", Detail: fmt.Sprintf("normalize %d file(s)", len(mixedCaseFiles))}}

	if ctx.dryRun {
		return fail(name, fmt.Sprintf("%d DM file(s) with mixed-case domains", len(mixedCaseFiles)), reason, actions)
	}

	healDMDomainCase(ctx.siteDir)
	return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("Normalized domains in %d DM file(s)", len(mixedCaseFiles)), Reason: reason, Actions: actions}
}

// checkDMPreviewEncryption encrypts plaintext DM previews at rest.
func checkDMPreviewEncryption(ctx *runContext) CheckResult {
	const name = "dm-preview-encryption"
	const reason = "DM conversation previews must be encrypted at rest for privacy"

	idxPath := filepath.Join(ctx.siteDir, ".polis", "content", "pub.polis.core", "dm", "conversations.json")
	if !fileExists(idxPath) {
		return pass(name, "No DM conversations index")
	}

	// Check if any conversations have plaintext previews
	data, err := os.ReadFile(idxPath)
	if err != nil {
		return pass(name, "Cannot read conversations index")
	}

	var idx struct {
		Conversations []map[string]interface{} `json:"conversations"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return pass(name, "Cannot parse conversations index")
	}

	plaintextCount := 0
	for _, c := range idx.Conversations {
		preview, _ := c["last_message_preview"].(string)
		encrypted, _ := c["last_message_preview_encrypted"].(bool)
		if preview != "" && !encrypted {
			plaintextCount++
		}
	}

	if plaintextCount == 0 {
		return pass(name, "All DM previews encrypted")
	}

	actions := []Action{{Op: "update", Path: ".polis/content/pub.polis.core/dm/conversations.json", Detail: fmt.Sprintf("encrypt %d plaintext preview(s)", plaintextCount)}}

	if ctx.dryRun {
		return fail(name, fmt.Sprintf("%d DM preview(s) need encryption", plaintextCount), reason, actions)
	}

	// Need private key to derive storage key
	privPEM, err := os.ReadFile(filepath.Join(ctx.siteDir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		return skip(name, "Cannot read private key for encryption", reason)
	}
	privKey, err := signing.ParsePrivateKey(privPEM)
	if err != nil {
		return skip(name, "Cannot parse private key for encryption", reason)
	}
	defer signing.ZeroKey(privKey)

	store, err := dm.NewStore(ctx.siteDir, privKey.Seed())
	if err != nil {
		return skip(name, fmt.Sprintf("Cannot create DM store: %v", err), reason)
	}

	if err := store.EncryptPlaintextPreviews(); err != nil {
		return skip(name, fmt.Sprintf("Encryption failed: %v", err), reason)
	}

	return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("Encrypted %d plaintext DM preview(s)", plaintextCount), Reason: reason, Actions: actions}
}

// ── Phase 6 (cont): Policy content convergence ──────────────────────

// checkPolicyContentConverge overwrites both policy files with the canonical
// templates if their content has drifted. Tenants cannot customize policy
// files, so all existing files should match the defaults.
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
	os.WriteFile(configPath, out, 0644)
	return CheckResult{Name: name, Status: StatusFail, Message: "Removed deprecated view_mode key", Reason: reason, Actions: actions}
}

// ── Helpers for new checks ─────────────────────────────────────────

// healDMDomainCase normalizes mixed-case domains to lowercase in DM conversations.
func healDMDomainCase(siteDir string) {
	dmBase := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm")
	convDir := filepath.Join(dmBase, "conv")

	entries, err := os.ReadDir(convDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") || entry.IsDir() {
			continue
		}
		path := filepath.Join(convDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var conv struct {
			PeerDomain string          `json:"peer_domain"`
			PeerURL    string          `json:"peer_url"`
			Messages   json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}

		changed := false
		lower := strings.ToLower(conv.PeerDomain)
		if lower != conv.PeerDomain {
			conv.PeerDomain = lower
			changed = true
		}

		var messages []map[string]interface{}
		if err := json.Unmarshal(conv.Messages, &messages); err == nil {
			for i, msg := range messages {
				if from, ok := msg["from"].(string); ok {
					if lf := strings.ToLower(from); lf != from {
						messages[i]["from"] = lf
						changed = true
					}
				}
				if to, ok := msg["to"].(string); ok {
					if lt := strings.ToLower(to); lt != to {
						messages[i]["to"] = lt
						changed = true
					}
				}
			}
		}

		if !changed {
			continue
		}

		msgBytes, err := json.Marshal(messages)
		if err != nil {
			continue
		}
		out := map[string]interface{}{
			"peer_domain": conv.PeerDomain,
			"peer_url":    conv.PeerURL,
			"messages":    json.RawMessage(msgBytes),
		}
		outData, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			continue
		}
		outData = append(outData, '\n')
		os.WriteFile(path, outData, 0600)
	}

	// Fix conversations index
	idxPath := filepath.Join(dmBase, "conversations.json")
	idxData, err := os.ReadFile(idxPath)
	if err != nil {
		return
	}
	var idx struct {
		Conversations []map[string]interface{} `json:"conversations"`
	}
	if err := json.Unmarshal(idxData, &idx); err != nil {
		return
	}
	changed := false
	for i, c := range idx.Conversations {
		if pd, ok := c["peer_domain"].(string); ok {
			if lp := strings.ToLower(pd); lp != pd {
				idx.Conversations[i]["peer_domain"] = lp
				changed = true
			}
		}
	}
	if changed {
		outData, err := json.MarshalIndent(idx, "", "  ")
		if err != nil {
			return
		}
		outData = append(outData, '\n')
		os.WriteFile(idxPath, outData, 0600)
	}
}

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
