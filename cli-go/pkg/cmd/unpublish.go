package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

func handleUnpublish(args []string) {
	fs := flag.NewFlagSet("unpublish", flag.ExitOnError)
	yes := fs.Bool("y", false, "Skip confirmation prompt")
	urlFlag := fs.String("url", "", "Unpublish this exact discovery-service URL only; local files are NOT touched. For removing a stale or duplicate (type,url) registration row.")
	typeFlag := fs.String("type", "", "Content type for --url (pub.polis.post | pub.polis.comment); inferred from the URL when omitted")
	fs.Parse(args)

	dir := getDataDir()
	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Discovery-only unpublish of an explicit URL — does not read or mutate any
	// local file. Use for superseded/duplicate DS rows (e.g. a legacy mount-path
	// registration left behind by a path migration) where the local content and
	// its canonical registration must stay intact.
	if *urlFlag != "" {
		if err := RunUnpublishURL(dir, *urlFlag, *typeFlag, *yes); err != nil {
			exitError("%v", err)
		}
		return
	}

	if fs.NArg() < 1 {
		exitError("Usage: polis unpublish <path>\n  Example: polis unpublish content/pub.polis.core/post/20260201/my-post.md\n  Example: polis unpublish content/pub.polis.core/comment/20260201/comment-id.md\n  Discovery-only: polis unpublish --url https://you.example/comments/20260201/x.md")
	}

	contentPath := fs.Arg(0)

	if err := RunUnpublish(dir, contentPath, *yes); err != nil {
		exitError("%v", err)
	}
}

// RunUnpublish unpublishes a post or comment: notifies the DS, strips frontmatter,
// deletes version history, and saves the content as a draft.
//
// This is a clean break — all published identity (signature, version hash, version
// history) is erased. Any future republish is treated as a completely fresh publication.
func RunUnpublish(dataDir, contentPath string, skipConfirm bool) error {
	// Detect content type from path
	isPost := strings.HasPrefix(contentPath, "content/pub.polis.core/post/")
	isComment := strings.HasPrefix(contentPath, "content/pub.polis.core/comment/")

	if !isPost && !isComment {
		return fmt.Errorf("path must start with content/pub.polis.core/post/ or content/pub.polis.core/comment/: %s", contentPath)
	}
	if strings.Contains(contentPath, "..") {
		return fmt.Errorf("path must not contain '..': %s", contentPath)
	}

	// Verify file exists
	fullPath := filepath.Join(dataDir, contentPath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return fmt.Errorf("content not found: %s", contentPath)
	}

	// Read content for frontmatter extraction
	contentBytes, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("failed to read content file: %v", err)
	}
	contentStr := string(contentBytes)

	// Parse frontmatter for title (used in confirmation) and comment metadata
	fm := publish.ParseFrontmatter(contentStr)
	title := fm["title"]
	if title == "" {
		title = filepath.Base(contentPath)
	}

	// Determine content type for DS
	dsContentType := "pub.polis.post"
	if isComment {
		dsContentType = "pub.polis.comment"
	}

	// Confirmation prompt (unless -y flag or JSON output)
	if !skipConfirm && !jsonOutput {
		contentKind := "post"
		if isComment {
			contentKind = "comment"
		}
		fmt.Printf("Unpublish \"%s\"?\n\n", title)
		fmt.Println("This will:")
		fmt.Printf("  - Remove the %s from your site and from discovery\n", contentKind)
		if isPost {
			fmt.Println("  - Delete all version history")
		}
		fmt.Println("  - Move the content back to drafts (stripped of signatures and versions)")
		if isPost {
			fmt.Println("  - Orphan any blessed comments on this post")
		}
		fmt.Println()
		fmt.Println("If you republish this content later, it will be treated as a brand new publication.")
		fmt.Println()
		fmt.Print("Proceed? [y/N] ")

		var response string
		fmt.Scanln(&response)
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			if !jsonOutput {
				fmt.Println("[i] Cancelled")
			}
			return nil
		}
	}

	// Read .well-known/polis for domain + base URL
	polisBaseURL := os.Getenv("POLIS_BASE_URL")
	if polisBaseURL == "" {
		return fmt.Errorf("POLIS_BASE_URL not set")
	}
	domain := polisurl.ExtractDomain(polisBaseURL)
	if domain == "" {
		return fmt.Errorf("could not extract domain from POLIS_BASE_URL")
	}

	// Build content URL
	contentURLPath := strings.TrimSuffix(contentPath, ".md")
	contentURL := strings.TrimRight(polisBaseURL, "/") + "/" + contentURLPath + ".md"

	// Load private key for signing
	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		return fmt.Errorf("failed to load private key: %v", err)
	}

	// Build canonical JSON and sign. R20-C-F1 (2026-05-18): the
	// canonical now includes a fresh ISO 8601 timestamp. DS rejects
	// timestamps further than ±5min from server clock — defeats
	// signature replay (captured unpublish events can't be re-submitted
	// later to delete URLs a second time).
	timestamp := time.Now().UTC().Format(time.RFC3339)
	canonical := discovery.MakeContentUnpublishCanonicalJSON(dsContentType, contentURL, timestamp)
	sig, err := signing.SignContent([]byte(canonical), privKey)
	if err != nil {
		return fmt.Errorf("failed to sign unpublish request: %v", err)
	}

	// Call DS to unpublish — skip if not registered
	dsURL := os.Getenv("DISCOVERY_SERVICE_URL")
	dsKey := os.Getenv("DISCOVERY_SERVICE_KEY")
	if dsURL == "" {
		dsURL = "https://ds.polis.pub"
	}

	if discovery.IsRegisteredLocally(dataDir, dsURL) {
		dsClient := discovery.NewClient(dsURL, dsKey)

		if !jsonOutput {
			fmt.Println("[i] Unpublishing from discovery service...")
		}

		if err := dsClient.UnpublishContent(dsContentType, contentURL, timestamp, sig); err != nil {
			// Non-fatal: DS might be unreachable, still clean up locally
			if !jsonOutput {
				fmt.Printf("[!] Warning: DS unpublish failed: %v\n", err)
			}
		} else {
			if !jsonOutput {
				fmt.Println("[✓] Unpublished from discovery service")
			}
		}
	} else if !jsonOutput {
		fmt.Println("[i] DS unpublish skipped: site not registered with discovery service")
	}

	// Strip frontmatter, preserve only title + body (and in-reply-to for comments)
	body := publish.StripFrontmatter(contentStr)

	// Build draft content
	var draftContent string
	if isPost {
		// Post draft format: # Title\n\nbody
		draftContent = "# " + title + "\n\n" + body
	} else {
		// Comment draft: preserve in-reply-to as a comment at the top so the author
		// knows what post this was replying to, then title + body
		inReplyTo := fm["in-reply-to"]
		var header string
		if inReplyTo != "" {
			header = "<!-- in-reply-to: " + inReplyTo + " -->\n"
		}
		draftContent = header + "# " + title + "\n\n" + body
	}

	// Determine draft destination path
	baseName := filepath.Base(contentPath)
	var draftsDir string
	if isPost {
		draftsDir = filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	} else {
		draftsDir = filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "drafts")
	}

	// Create drafts directory if it doesn't exist
	if err := os.MkdirAll(draftsDir, 0755); err != nil {
		return fmt.Errorf("failed to create drafts directory: %v", err)
	}

	// Write draft file
	draftPath := filepath.Join(draftsDir, baseName)
	if err := os.WriteFile(draftPath, []byte(draftContent), 0644); err != nil {
		return fmt.Errorf("failed to write draft: %v", err)
	}

	// Delete the .versions/ history file (posts only — comments don't have .versions)
	if isPost {
		dateDir := filepath.Base(filepath.Dir(contentPath)) // e.g., "20260201"
		versionsFile := filepath.Join(dataDir, "content", "pub.polis.core", "post", dateDir, ".versions", baseName)
		os.Remove(versionsFile) // Ignore error — may not exist
	}

	// Delete the original published file
	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("failed to delete published file: %v", err)
	}

	// Delete rendered HTML if exists
	if isPost {
		htmlPath := strings.TrimSuffix(fullPath, ".md") + ".html"
		os.Remove(htmlPath) // Ignore error — may not exist

		// Also check the posts mount point
		dateDir := filepath.Base(filepath.Dir(contentPath))
		htmlName := strings.TrimSuffix(baseName, ".md") + ".html"
		mountHtmlPath := filepath.Join(dataDir, "posts", dateDir, htmlName)
		os.Remove(mountHtmlPath) // Ignore error — may not exist
	} else {
		// Comment HTML in comments mount dir
		htmlPath := strings.TrimSuffix(fullPath, ".md") + ".html"
		os.Remove(htmlPath) // Ignore error — may not exist
	}

	// Remove from index.jsonl
	if err := metadata.RemoveIndexEntry(dataDir, contentPath); err != nil {
		// Non-fatal: index entry may not exist
		if !jsonOutput {
			fmt.Printf("[!] Warning: could not remove index entry: %v\n", err)
		}
	}

	// Update manifest
	if err := publish.UpdateManifest(dataDir); err != nil {
		if !jsonOutput {
			fmt.Printf("[!] Warning: failed to update manifest: %v\n", err)
		}
	}

	// v4 incremental cascade for unpublished posts. Delete-and-cascade:
	// neighbors who showed it as a sibling get re-rendered; the index focus
	// shifts if it was the newest. Only relevant for posts (comments don't
	// participate in v4's per-post cascade today).
	if isPost {
		if shape, err := bundle.GetActiveShapeName(dataDir); err == nil && shape == "v4" {
			if err := unpublishStreamCascade(dataDir, contentPath); err != nil && !jsonOutput {
				fmt.Printf("[!] Warning: v4 unpublish cascade failed: %v\n", err)
			}
		}
	}

	if jsonOutput {
		contentKind := "post"
		if isComment {
			contentKind = "comment"
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "unpublish",
			"data": map[string]interface{}{
				"type":        contentKind,
				"path":        contentPath,
				"content_url": contentURL,
				"draft_path":  filepath.Join(draftsDir, baseName),
			},
		})
	} else {
		contentKind := "Post"
		if isComment {
			contentKind = "Comment"
		}
		fmt.Println()
		fmt.Printf("[✓] %s unpublished: %s\n", contentKind, contentPath)
		fmt.Printf("[i] Draft saved to: %s\n", filepath.Join(draftsDir, baseName))
	}

	return nil
}

// inferContentTypeFromURL guesses the DS content type from a content URL's path:
// comment URLs contain ".../comment(s)/...", post URLs ".../post(s)/...".
func inferContentTypeFromURL(u string) string {
	switch {
	case strings.Contains(u, "/comment"):
		return "pub.polis.comment"
	case strings.Contains(u, "/post"):
		return "pub.polis.post"
	default:
		return ""
	}
}

// RunUnpublishURL retires a single discovery-service registration by EXACT URL,
// signed with the site's identity key. Unlike RunUnpublish it performs NO local
// file changes — it does not strip frontmatter, delete the file, or rebuild the
// index. Use it to drop a superseded or duplicate (type,url) row at the DS (e.g.
// a legacy mount-path registration left by a path migration) while the local
// content and its canonical registration stay intact.
func RunUnpublishURL(dataDir, contentURL, contentType string, skipConfirm bool) error {
	if contentType == "" {
		contentType = inferContentTypeFromURL(contentURL)
	}
	if contentType != "pub.polis.post" && contentType != "pub.polis.comment" {
		return fmt.Errorf("could not determine content type from URL; pass --type pub.polis.post|pub.polis.comment")
	}
	if polisurl.ExtractDomain(contentURL) == "" {
		return fmt.Errorf("could not extract domain from URL: %s", contentURL)
	}

	if !skipConfirm && !jsonOutput {
		fmt.Printf("Unpublish discovery registration for:\n  %s\n  (type: %s)\n\n", contentURL, contentType)
		fmt.Println("This removes ONLY the discovery-service row. Local files are NOT touched.")
		fmt.Print("Proceed? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if r := strings.TrimSpace(strings.ToLower(response)); r != "y" && r != "yes" {
			if !jsonOutput {
				fmt.Println("[i] Cancelled")
			}
			return nil
		}
	}

	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		return fmt.Errorf("failed to load private key: %v", err)
	}

	timestamp := time.Now().UTC().Format(time.RFC3339)
	canonical := discovery.MakeContentUnpublishCanonicalJSON(contentType, contentURL, timestamp)
	sig, err := signing.SignContent([]byte(canonical), privKey)
	if err != nil {
		return fmt.Errorf("failed to sign unpublish request: %v", err)
	}

	dsURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if dsURL == "" {
		dsURL = "https://ds.polis.pub"
	}
	dsClient := discovery.NewClient(dsURL, os.Getenv("DISCOVERY_SERVICE_KEY"))
	if err := dsClient.UnpublishContent(contentType, contentURL, timestamp, sig); err != nil {
		return fmt.Errorf("DS unpublish failed: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "unpublish",
			"data": map[string]interface{}{
				"type": contentType, "content_url": contentURL, "discovery_only": true,
			},
		})
	} else {
		fmt.Printf("[✓] Unpublished discovery registration: %s\n", contentURL)
		fmt.Println("[i] Local files were not modified.")
	}
	return nil
}

// unpublishStreamCascade dispatches the v4-side cascade after a post is removed
// from a v4 tenant. Mirrors publishStreamCascade in publish.go but routes to
// UnpublishStream for the delete + pad-back-zone re-render.
func unpublishStreamCascade(dir, postPath string) error {
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
	return renderer.UnpublishStream(postPath)
}
