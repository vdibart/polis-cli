package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func handlePublish(args []string) {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	filename := fs.String("filename", "", "Custom filename for the post (without .md)")
	licenseFlag := fs.String("license", "", "Licence for this post: reserved, open, or none (default: the site's terms)")
	_ = fs.Parse(args) // ExitOnError: a bad flag exits, so Parse never returns an error

	remaining := fs.Args()
	if len(remaining) < 1 {
		exitError("Usage: polis post <file.md> [--filename <name>]")
	}

	inputFile := remaining[0]
	dir := getDataDir()

	// Verify it's a polis site
	if !isPolisSite(dir) {
		exitError("Not a polis site directory (no .well-known/polis found)")
	}

	// NOT the storage interface: inputFile is the path the user typed on the
	// command line and may be anywhere on their disk. The store addresses
	// keys inside the site and refuses absolute paths by design.
	content, err := os.ReadFile(inputFile) //nolint:gosec // G304: a path the user typed
	if err != nil {
		exitError("Failed to read file: %v", err)
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// The author's per-work licence is the markdown's own `license:` line,
	// read by the operation; --license states it for them.
	markdown := string(content)
	if *licenseFlag != "" {
		markdown = withLicenseLine(markdown, *licenseFlag)
	}

	// ⭐ Publish through the action layer: the operation /v1 serves, in
	// process, with its render (incremental on v4, full on v5, none on v3).
	result := dispatchPost(dir, privKey, "create", map[string]any{"markdown": markdown, "filename": *filename})

	// Remove original file if not already in posts/ (matches bash CLI behavior)
	inputAbs, err1 := filepath.Abs(inputFile)
	postAbs, err2 := filepath.Abs(filepath.Join(dir, result.Path))
	if err1 == nil && err2 == nil && inputAbs != postAbs {
		// NOT the storage interface: inputAbs is the user's original file,
		// outside the site, being cleaned up after the post was copied in.
		if err := os.Remove(inputAbs); err != nil {
			if !jsonOutput {
				fmt.Fprintf(os.Stderr, "[!] Could not remove original file: %v\n", err)
			}
		} else if !jsonOutput {
			fmt.Println("[✓] Moved original file into posts/")
		}
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success":   result.Success,
			"path":      result.Path,
			"title":     result.Title,
			"version":   result.Version,
			"signature": result.Signature,
		})
	} else {
		fmt.Printf("Published: %s\n", result.Path)
		fmt.Printf("Title: %s\n", result.Title)
		fmt.Printf("Version: %s\n", result.Version)
	}
}

func handleRepublish(args []string) {
	fs := flag.NewFlagSet("republish", flag.ExitOnError)
	_ = fs.Parse(args) // ExitOnError: a bad flag exits, so Parse never returns an error

	remaining := fs.Args()
	if len(remaining) < 1 {
		exitError("Usage: polis republish <path> [new-content.md]\n" +
			"  <path> may be the mount form (posts/YYYYMMDD/post.md) or the\n" +
			"  content path that `polis post` prints\n" +
			"  (content/pub.polis.core/post/YYYYMMDD/post.md).")
	}

	postPath := remaining[0]
	dir := getDataDir()

	// Verify it's a polis site
	if !isPolisSite(dir) {
		exitError("Not a polis site directory (no .well-known/polis found)")
	}

	// NORMALISE FIRST, AND ONLY ONCE. Accept either the mount form
	// (posts/YYYYMMDD/x.md) or the content-relative path that `polis post`
	// itself prints, and convert to the content path before anything else
	// looks at it.
	//
	// ⛔ This must happen BEFORE the existence check, the type dispatch, and
	// RepublishPost — because postPath is then used for four different things:
	// reading the file, writing it back, version.AppendHistory, and
	// UpdateIndexEntry. index.jsonl keys entries on the CONTENT path
	// ("content/<bundle>/<dir>/…"), so letting a mount-form path through to
	// the tail of that list would write the new version and silently miss the
	// index entry — a post whose bytes moved on while the index still points
	// at the old hash. Normalising here is what keeps those four in agreement.
	b := loadOrDefaultBundle(dir)
	postPath = b.MountToSourcePath(postPath)

	// Comment republish: branch on the type the BUNDLE says this path belongs
	// to, never on a path prefix. `dir` and `mount` are user-configurable, so
	// a hardcoded path is wrong for any bundle that sets them — the same
	// reason .well-known/polis discovers by pointer instead of by convention.
	//
	// The TYPE NAME, by contrast, is protocol vocabulary and is stable, which
	// is why comparing to "pub.polis.comment" here is not the same kind of
	// hardcoding the old "content/pub.polis.core/comment/" prefix was.
	if contentTypeForPath(b, postPath) == "pub.polis.comment" {
		handleRepublishComment(dir, postPath, remaining)
		return
	}

	// Name the path we actually tried. The previous message ("must be under
	// posts/ directory") pointed at the one directory where the .md is NOT —
	// posts/ is the render mount and holds .html.
	if present, _ := storage.NewPOSIX(dir).Exists(context.Background(), filepath.ToSlash(postPath)); !present {
		exitError("Post not found at %s", postPath)
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// Read the post content (either from second arg or from the post itself)
	var markdown string
	if len(remaining) > 1 {
		// Read from provided file. NOT the storage interface: a path the
		// user typed, which may be anywhere.
		content, err := os.ReadFile(remaining[1])
		if err != nil {
			exitError("Failed to read file: %v", err)
		}
		markdown = string(content)
	} else {
		// Read from the existing post
		content, err := storage.NewPOSIX(dir).Get(context.Background(), filepath.ToSlash(postPath))
		if err != nil {
			exitError("Failed to read post: %v", err)
		}
		markdown = publish.StripFrontmatter(string(content))
	}

	// ⭐ Republish through the action layer: post.update, the operation /v1
	// serves, in process. It reads the new version's own `license:` line (a
	// post re-signed as it stands has only the stamped block, so it takes the
	// site's current terms) and renders as publish does.
	result := dispatchPost(dir, privKey, "update", map[string]any{"path": filepath.ToSlash(postPath), "markdown": markdown})

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success":   result.Success,
			"path":      result.Path,
			"title":     result.Title,
			"version":   result.Version,
			"signature": result.Signature,
		})
	} else {
		fmt.Printf("Republished: %s\n", result.Path)
		fmt.Printf("Title: %s\n", result.Title)
		fmt.Printf("Version: %s\n", result.Version)
	}
}

// handleRepublishComment republishes an already-published comment, producing a new
// signed version. It accepts either the comments/ mount form or the content-relative
// path and normalizes to the content path comment.RepublishComment expects.
// contentTypeForPath reports which of a bundle's declared content types owns a
// content-relative path ("post", "comment", …), or "" when none does. It is the
// dispatch counterpart to MountToSourcePath: that maps a mount path onto the
// content tree, this says what the bundle calls the thing it landed on.
func contentTypeForPath(b *bundle.Bundle, contentPath string) string {
	for name, ct := range b.Types {
		if ct.Dir == "" {
			continue
		}
		if strings.HasPrefix(contentPath, filepath.Join("content", b.Name, ct.Dir)+"/") {
			return name
		}
	}
	return ""
}

func handleRepublishComment(dir, rawPath string, remaining []string) {
	// Idempotent when handleRepublish already normalised; still correct when
	// this is reached with a mount-form path. Uses the bundle's own mount
	// config rather than a hardcoded "content/pub.polis.core/comment/", which
	// was wrong for any bundle declaring a different dir or mount.
	contentPath := loadOrDefaultBundle(dir).MountToSourcePath(rawPath)

	commentKey := filepath.ToSlash(contentPath)
	if present, _ := storage.NewPOSIX(dir).Exists(context.Background(), commentKey); !present {
		exitError("Comment not found at %s", contentPath)
	}

	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// New body: from the optional second arg, else the existing comment's body.
	var markdown string
	if len(remaining) > 1 {
		// NOT the storage interface: a path the user typed, which may be
		// anywhere on their disk.
		content, err := os.ReadFile(remaining[1])
		if err != nil {
			exitError("Failed to read file: %v", err)
		}
		markdown = comment.StripFrontmatter(string(content))
	} else {
		content, err := storage.NewPOSIX(dir).Get(context.Background(), commentKey)
		if err != nil {
			exitError("Failed to read comment: %v", err)
		}
		markdown = comment.StripFrontmatter(string(content))
	}

	// DS config comes from the comment package globals set in root.go Execute().
	result, err := comment.RepublishComment(storage.At(dir), contentPath, markdown, privKey)
	if err != nil {
		exitError("Failed to republish comment: %v", err)
	}

	// NOTE: the v4 stream re-render cascade for comment republish is deferred until
	// the feature is surfaced in the UI.

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success":        true,
			"comment_id":     result.CommentID,
			"path":           result.Path,
			"version":        result.Version,
			"blessing_state": result.BlessingState,
			"rebeseeched":    result.Rebeseeched,
			"deferred":       result.Deferred,
		})
	} else {
		fmt.Printf("Republished comment: %s\n", result.Path)
		fmt.Printf("Version: %s\n", result.Version)
		fmt.Printf("Blessing: %s\n", result.BlessingState)
		if result.Deferred {
			fmt.Println("[i] Discovery contact deferred — site not registered.")
		}
		if result.RegisterError != "" {
			fmt.Printf("[!] Discovery registration skipped: %s\n", result.RegisterError)
		}
	}
}

// postResult is what post.create and post.update answer.
type postResult struct {
	Success                              bool
	Path, Title, Version, Signature, URL string
}

// dispatchPost runs a post operation through the CLI's action layer and
// exits on failure, as the commands always have.
func dispatchPost(dir string, privKey []byte, action string, payload map[string]any) postResult {
	pubKey, _ := storage.NewPOSIX(dir).Get(context.Background(), ".polis/keys/id_ed25519.pub")
	engine, err := cliEngine(dir, privKey, pubKey)
	if err != nil {
		exitError("Failed to start: %v", err)
	}
	res, err := engine.Dispatch(cliContext(), ops.ActionRequest{Action: action, ContentType: "pub.polis.post", Payload: payload})
	if err != nil {
		verb := map[string]string{"create": "publish", "update": "republish"}[action]
		exitError("Failed to %s: %v", verb, err)
	}
	str := func(k string) string { v, _ := res.Data[k].(string); return v }
	return postResult{Success: true, Path: str("path"), Title: str("title"), Version: str("version"), Signature: str("signature"), URL: str("url")}
}

// withLicenseLine sets the markdown's top-level `license:` line to profile,
// adding frontmatter when it has none: how --license states a work's terms.
func withLicenseLine(markdown, profile string) string {
	line := "license: " + profile
	if !publish.HasFrontmatter(markdown) {
		return "---\n" + line + "\n---\n" + markdown
	}
	rest := strings.TrimPrefix(markdown, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "---\n" + line + "\n---\n" + publish.StripFrontmatter(markdown)
	}
	var kept []string
	skipping := false // inside a license block being replaced
	for _, l := range strings.Split(rest[:end], "\n") {
		indented := strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")
		if skipping && indented {
			continue
		}
		skipping = false
		if k, _, ok := strings.Cut(l, ":"); ok && !indented && strings.TrimSpace(k) == license.FrontmatterKey {
			skipping = true
			continue
		}
		kept = append(kept, l)
	}
	kept = append(kept, line)
	return "---\n" + strings.Join(kept, "\n") + rest[end:]
}

func loadPrivateKey(dir string) ([]byte, error) {
	return storage.NewPOSIX(dir).Get(context.Background(), ".polis/keys/id_ed25519")
}

// renderV5AfterChange renders a v5 site after a post is published,
// republished or unpublished (contentPath, its source): incrementally when the
// render's record allows (the post's page, the pages showing it, the home page,
// the sitemap), else in full, as /v1's writes do (ops.RenderAfterRemoval). A
// comment ("" or a comment's path) renders in full. A no-op on any other
// shape. A failure is reported and the change stands, as v4's cascade does.
func renderV5AfterChange(dir, contentPath string) {
	site := storage.At(dir)
	if shape, err := bundle.GetActiveShapeName(site); err != nil || shape != "v5" {
		return
	}
	renderer, err := newCascadeRenderer(dir)
	if err == nil {
		if strings.HasPrefix(contentPath, "content/pub.polis.core/post/") {
			_, _, err = ops.RenderAfterRemoval(site, renderer, contentPath)
		} else {
			_, err = renderer.RenderAll(false)
		}
	}
	if err != nil && !jsonOutput {
		fmt.Fprintf(os.Stderr, "[!] v5 render failed: %v\n", err)
	}
}

// newCascadeRenderer is the renderer a publish uses: the site's base URL and
// the core bundle's folders for posts and comments.
func newCascadeRenderer(dir string) (*render.PageRenderer, error) {
	url := os.Getenv("POLIS_BASE_URL")
	if url == "" {
		url = getBaseURLFromSite(dir)
	}
	coreBundle := loadOrDefaultBundle(dir)
	postsSource, _ := coreBundle.ContentDir("pub.polis.post")
	postsMountDir, _ := coreBundle.MountDir("pub.polis.post")
	commentsSource, _ := coreBundle.ContentDir("pub.polis.comment")
	commentsMountDir, _ := coreBundle.MountDir("pub.polis.comment")

	return render.NewPageRenderer(render.PageConfig{
		Site:              storage.At(dir),
		CLIThemesDir:      findCLIThemesDir(),
		BaseURL:           url,
		PostsSourceDir:    postsSource,
		PostsMountDir:     postsMountDir,
		CommentsSourceDir: commentsSource,
		CommentsMountDir:  commentsMountDir,
	})
}
