package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
)

func handlePublish(args []string) {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	filename := fs.String("filename", "", "Custom filename for the post (without .md)")
	fs.Parse(args)

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

	// Read the input file
	content, err := os.ReadFile(inputFile)
	if err != nil {
		exitError("Failed to read file: %v", err)
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// Strip frontmatter if present
	markdown := string(content)
	if publish.HasFrontmatter(markdown) {
		markdown = publish.StripFrontmatter(markdown)
	}

	// Publish the post
	dsCfg := &publish.DiscoveryConfig{
		DiscoveryURL: discoveryURL,
		DiscoveryKey: discoveryKey,
		BaseURL:      baseURL,
		Generator:    generator,
	}
	result, err := publish.PublishPost(dir, markdown, *filename, privKey, dsCfg)
	if err != nil {
		exitError("Failed to publish: %v", err)
	}

	// v4 incremental render: cascade siblings + index. v3 path unchanged
	// (CLI publish has never auto-rendered for v3, and continues not to).
	if shape, err := bundle.GetActiveShapeName(dir); err == nil && shape == "v4" {
		if err := publishStreamCascade(dir, result.Path, false); err != nil && !jsonOutput {
			fmt.Fprintf(os.Stderr, "[!] v4 cascade render failed: %v\n", err)
		}
	}

	// Remove original file if not already in posts/ (matches bash CLI behavior)
	inputAbs, err1 := filepath.Abs(inputFile)
	postAbs, err2 := filepath.Abs(filepath.Join(dir, result.Path))
	if err1 == nil && err2 == nil && inputAbs != postAbs {
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
	fs.Parse(args)

	remaining := fs.Args()
	if len(remaining) < 1 {
		exitError("Usage: polis republish <posts/YYYYMMDD/post.md | comments/YYYYMMDD/comment.md> [new-content.md]")
	}

	postPath := remaining[0]
	dir := getDataDir()

	// Verify it's a polis site
	if !isPolisSite(dir) {
		exitError("Not a polis site directory (no .well-known/polis found)")
	}

	// Comment republish: branch before the post-specific path check. Accept either
	// the comments/ mount form or the content-relative path.
	if strings.HasPrefix(postPath, "comments/") || strings.Contains(postPath, "content/pub.polis.core/comment/") {
		handleRepublishComment(dir, postPath, remaining)
		return
	}

	// Validate the post path
	if !strings.HasPrefix(postPath, "posts/") {
		exitError("Post path must be under posts/ directory")
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// Read the post content (either from second arg or from the post itself)
	var markdown string
	if len(remaining) > 1 {
		// Read from provided file
		content, err := os.ReadFile(remaining[1])
		if err != nil {
			exitError("Failed to read file: %v", err)
		}
		markdown = string(content)
	} else {
		// Read from the existing post
		fullPath := filepath.Join(dir, postPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			exitError("Failed to read post: %v", err)
		}
		markdown = publish.StripFrontmatter(string(content))
	}

	// Strip frontmatter if present
	if publish.HasFrontmatter(markdown) {
		markdown = publish.StripFrontmatter(markdown)
	}

	// Republish the post
	dsCfg := &publish.DiscoveryConfig{
		DiscoveryURL: discoveryURL,
		DiscoveryKey: discoveryKey,
		BaseURL:      baseURL,
		Generator:    generator,
	}
	result, err := publish.RepublishPost(dir, postPath, markdown, privKey, dsCfg)
	if err != nil {
		exitError("Failed to republish: %v", err)
	}

	// v4 incremental cascade. RepublishPost preserves the post's path/position;
	// PublishStream handles republish identically (same lookup, same cascade).
	if shape, err := bundle.GetActiveShapeName(dir); err == nil && shape == "v4" {
		if err := publishStreamCascade(dir, result.Path, true); err != nil && !jsonOutput {
			fmt.Fprintf(os.Stderr, "[!] v4 cascade render failed: %v\n", err)
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
		fmt.Printf("Republished: %s\n", result.Path)
		fmt.Printf("Title: %s\n", result.Title)
		fmt.Printf("Version: %s\n", result.Version)
	}
}

// handleRepublishComment republishes an already-published comment, producing a new
// signed version. It accepts either the comments/ mount form or the content-relative
// path and normalizes to the content path comment.RepublishComment expects.
func handleRepublishComment(dir, rawPath string, remaining []string) {
	contentPath := rawPath
	if strings.HasPrefix(rawPath, "comments/") {
		contentPath = "content/pub.polis.core/comment/" + strings.TrimPrefix(rawPath, "comments/")
	}

	fullPath := filepath.Join(dir, contentPath)
	if _, err := os.Stat(fullPath); err != nil {
		exitError("Comment not found at %s", contentPath)
	}

	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// New body: from the optional second arg, else the existing comment's body.
	var markdown string
	if len(remaining) > 1 {
		content, err := os.ReadFile(remaining[1])
		if err != nil {
			exitError("Failed to read file: %v", err)
		}
		markdown = comment.StripFrontmatter(string(content))
	} else {
		content, err := os.ReadFile(fullPath)
		if err != nil {
			exitError("Failed to read comment: %v", err)
		}
		markdown = comment.StripFrontmatter(string(content))
	}

	// DS config comes from the comment package globals set in root.go Execute().
	result, err := comment.RepublishComment(dir, contentPath, markdown, privKey)
	if err != nil {
		exitError("Failed to republish comment: %v", err)
	}

	// NOTE: the v4 stream re-render cascade for comment republish is deferred until
	// the feature is surfaced in the UI (see plans/comment-versioning-design.md).

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

func loadPrivateKey(dir string) ([]byte, error) {
	privKeyPath := filepath.Join(dir, ".polis", "keys", "id_ed25519")
	return os.ReadFile(privKeyPath)
}

// publishStreamCascade constructs a PageRenderer and dispatches the v4 incremental
// render-on-publish (per step-03/3.a). Used by both publish and republish; both
// flow through PublishStream because the cascade derivation is the same — a
// republish keeps the post's index position but its sibling neighbors still
// need re-rendering for any excerpt/title changes. The isRepublish flag is
// retained for future telemetry / unpublish symmetry.
func publishStreamCascade(dir, postPath string, isRepublish bool) error {
	url := os.Getenv("POLIS_BASE_URL")
	if url == "" {
		url = getBaseURLFromSite(dir)
	}
	coreBundle := loadOrDefaultBundle(dir)
	postsSource, _ := coreBundle.ContentDir("pub.polis.post")
	postsMountDir, _ := coreBundle.MountDir("pub.polis.post")
	commentsSource, _ := coreBundle.ContentDir("pub.polis.comment")
	commentsMountDir, _ := coreBundle.MountDir("pub.polis.comment")

	renderer, err := render.NewPageRenderer(render.PageConfig{
		DataDir:           dir,
		CLIThemesDir:      findCLIThemesDir(),
		BaseURL:           url,
		PostsSourceDir:    postsSource,
		PostsMountDir:     postsMountDir,
		CommentsSourceDir: commentsSource,
		CommentsMountDir:  commentsMountDir,
	})
	if err != nil {
		return fmt.Errorf("create renderer: %w", err)
	}
	_ = isRepublish // reserved for future use; cascade logic identical today
	return renderer.PublishStream(postPath)
}
