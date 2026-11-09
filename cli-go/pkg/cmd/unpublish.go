package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// unpublishArgs is what `polis unpublish` was given on its command line.
type unpublishArgs struct {
	yes   bool
	url   string
	typ   string
	paths []string
}

// parseUnpublishArgs reads unpublish's flags wherever they appear, before or
// after the path, so `polis unpublish <path> -y` skips the prompt the same as
// `polis unpublish -y <path>`.
func parseUnpublishArgs(args []string) (unpublishArgs, error) {
	var a unpublishArgs
	fs := flag.NewFlagSet("unpublish", flag.ContinueOnError)
	fs.BoolVar(&a.yes, "y", false, "Skip confirmation prompt (same as --yes)")
	fs.BoolVar(&a.yes, "yes", false, "Skip confirmation prompt (same as -y)")
	fs.StringVar(&a.url, "url", "", "Unpublish this exact discovery-service URL only; local files are NOT touched. For removing a stale or duplicate (type,url) registration row.")
	fs.StringVar(&a.typ, "type", "", "Content type for --url (pub.polis.post | pub.polis.comment); inferred from the URL when omitted")
	paths, err := parseFlagsInterspersed(fs, args)
	a.paths = paths
	return a, err
}

// parseFlagsInterspersed parses fs over args, accepting flags after positional
// arguments as well as before them. Go's flag package stops at the first
// positional, so it is re-run on what follows each one. A "--" still ends flag
// parsing: everything after it is positional.
func parseFlagsInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return positional, err
		}
		rest := fs.Args()
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			return append(positional, rest...), nil
		}
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func handleUnpublish(args []string) {
	parsed, err := parseUnpublishArgs(args)
	if err != nil {
		// The flag package has already printed the problem and the usage.
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}

	dir := getDataDir()
	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Discovery-only unpublish of an explicit URL — does not read or mutate any
	// local file. Use for superseded/duplicate DS rows (e.g. a legacy mount-path
	// registration left behind by a path migration) where the local content and
	// its canonical registration must stay intact.
	if parsed.url != "" {
		if err := RunUnpublishURL(dir, parsed.url, parsed.typ, parsed.yes); err != nil {
			exitError("%v", err)
		}
		return
	}

	if len(parsed.paths) < 1 {
		exitError("Usage: polis unpublish <path> [-y|--yes]\n  -y, --yes  skip the confirmation prompt (before or after the path)\n  Example: polis unpublish content/pub.polis.core/post/20260201/my-post.md\n  Example: polis unpublish content/pub.polis.core/comment/20260201/comment-id.md\n  Discovery-only: polis unpublish --url https://you.example/comments/20260201/x.md")
	}

	contentPath := parsed.paths[0]

	if err := RunUnpublish(dir, contentPath, parsed.yes); err != nil {
		exitError("%v", err)
	}
}

// errUnpublishNeedsYes is the refusal of an unconfirmed unpublish under --json.
// Unpublishing cannot be undone, and the callers most likely to pass --json —
// scripts and agents — are the ones a skipped prompt would hurt most.
var errUnpublishNeedsYes = errors.New("unpublish under --json requires -y (--yes): it cannot ask for confirmation, and unpublishing cannot be undone. Nothing was unpublished")

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
	st := storage.NewSite(dataDir)
	ctx := context.Background()
	fullPath := filepath.Join(dataDir, contentPath)
	contentKey := filepath.ToSlash(contentPath)
	// Only a GENUINE absence is "not found"; any other failure is reported by the
	// read below with its own cause, as it was before.
	if present, existsErr := st.Exists(ctx, contentKey); existsErr == nil && !present {
		return fmt.Errorf("content not found: %s", contentPath)
	}

	// Read content for frontmatter extraction
	contentBytes, err := st.Get(ctx, contentKey)
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

	// Confirmation prompt unless -y. --json chooses an output format, not
	// consent: it cannot show a prompt, so it refuses rather than proceed.
	if !skipConfirm && jsonOutput {
		return errUnpublishNeedsYes
	}
	if !skipConfirm {
		contentKind := "post"
		if isComment {
			contentKind = "comment"
		}
		// The stored title is YAML-escaped; show what it says, not how it is
		// stored, or a quoted title prints as ""Foo: Bar"".
		fmt.Printf("Unpublish \"%s\"?\n\n", publish.UnquoteYAMLString(title))
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
			fmt.Println("[i] Cancelled")
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
	if dsURL == "" {
		dsURL = "https://ds.polis.pub"
	}

	if discovery.IsRegisteredLocally(storage.At(dataDir), dsURL) {
		dsClient := discovery.NewClient(dsURL)

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

	body := publish.StripFrontmatter(contentStr)

	// Build draft content
	var draftContent string
	if isPost {
		// Post draft format: # Title\n\nbody. ⛔ Unquote BEFORE the strip: the
		// publisher escapes the title (publish.escapeYAMLString) while the body
		// heading keeps the plain text, so comparing the raw frontmatter value
		// never matched and each cycle stacked a heading AND re-escaped the
		// title (close-out R3-1). Same helper as the comment builder.
		title = publish.UnquoteYAMLString(title)
		body = render.StripLeadingTitleHeading(body, title)
		draftContent = "# " + title + "\n\n" + body
	} else {
		// A comment draft keeps its reply fields in the format LoadDraft reads,
		// so `polis comment sign` can sign it again (close-out E2). The builder
		// owns the unquote + heading strip; the body arrives as published.
		draftContent = comment.UnpublishedDraftContent(contentStr, title, body)
	}

	// Determine draft destination path
	baseName := filepath.Base(contentPath)
	var draftsDir string
	if isPost {
		draftsDir = filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	} else {
		draftsDir = filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "drafts")
	}

	// No directory is created: Put makes parent prefixes implicitly, which is
	// the whole of what the MkdirAll here used to do.
	draftPath := filepath.Join(draftsDir, baseName)
	draftKey, err := storage.RelKey(dataDir, draftPath)
	if err != nil {
		return fmt.Errorf("failed to resolve the draft path: %v", err)
	}
	if err := st.Put(ctx, draftKey, []byte(draftContent)); err != nil {
		return fmt.Errorf("failed to write draft: %v", err)
	}

	// Delete the .versions/ history file (posts only — comments don't have .versions)
	if isPost {
		dateDir := filepath.Base(filepath.Dir(contentPath)) // e.g., "20260201"
		versionsFile := filepath.Join(dataDir, "content", "pub.polis.core", "post", dateDir, ".versions", baseName)
		if k, kerr := storage.RelKey(dataDir, versionsFile); kerr == nil {
			_ = st.Delete(ctx, k, storage.IgnoreMissing())
		}
	}

	// Delete the original published file
	if err := st.Delete(ctx, contentKey); err != nil {
		return fmt.Errorf("failed to delete published file: %v", err)
	}

	// Delete rendered HTML if exists
	if isPost {
		htmlPath := strings.TrimSuffix(fullPath, ".md") + ".html"
		if k, kerr := storage.RelKey(dataDir, htmlPath); kerr == nil {
			_ = st.Delete(ctx, k, storage.IgnoreMissing())
		}

		// Also check the posts mount point
		dateDir := filepath.Base(filepath.Dir(contentPath))
		htmlName := strings.TrimSuffix(baseName, ".md") + ".html"
		mountHtmlPath := filepath.Join(dataDir, "posts", dateDir, htmlName)
		if k, kerr := storage.RelKey(dataDir, mountHtmlPath); kerr == nil {
			_ = st.Delete(ctx, k, storage.IgnoreMissing())
		}
	} else {
		// Comment HTML in comments mount dir
		htmlPath := strings.TrimSuffix(fullPath, ".md") + ".html"
		if k, kerr := storage.RelKey(dataDir, htmlPath); kerr == nil {
			_ = st.Delete(ctx, k, storage.IgnoreMissing())
		}
	}

	// Remove from index.jsonl
	if err := metadata.RemoveIndexEntry(storage.At(dataDir), contentPath); err != nil {
		// Non-fatal: index entry may not exist
		if !jsonOutput {
			fmt.Printf("[!] Warning: could not remove index entry: %v\n", err)
		}
	}

	// Update manifest
	if err := publish.UpdateManifest(storage.At(dataDir)); err != nil {
		if !jsonOutput {
			fmt.Printf("[!] Warning: failed to update manifest: %v\n", err)
		}
	}

	// v4 incremental cascade for unpublished posts. Delete-and-cascade:
	// neighbors who showed it as a sibling get re-rendered; the index focus
	// shifts if it was the newest. Only relevant for posts (comments don't
	// participate in v4's per-post cascade today).
	if isPost {
		if shape, err := bundle.GetActiveShapeName(storage.At(dataDir)); err == nil && shape == "v4" {
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

	if !skipConfirm && jsonOutput {
		return errUnpublishNeedsYes
	}
	if !skipConfirm {
		fmt.Printf("Unpublish discovery registration for:\n  %s\n  (type: %s)\n\n", contentURL, contentType)
		fmt.Println("This removes ONLY the discovery-service row. Local files are NOT touched.")
		fmt.Print("Proceed? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if r := strings.TrimSpace(strings.ToLower(response)); r != "y" && r != "yes" {
			fmt.Println("[i] Cancelled")
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
	dsClient := discovery.NewClient(dsURL)
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
		Site:              storage.At(dir),
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
