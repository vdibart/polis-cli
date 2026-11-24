package cmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// keepDriftedPayload copies every installed shape/theme file whose bytes
// differ from the embedded copy into .polis/render-backup/<timestamp>/ (0700,
// files 0600), before the refresh replaces it. It returns the site-relative
// paths kept and the directory, or nothing when no file differs.
func keepDriftedPayload(dir string, now time.Time) ([]string, string, error) {
	drift, installed, err := sitecheck.ReferencePayloadDrift(dir)
	if err != nil {
		return nil, "", err
	}
	if !installed || len(drift) == 0 {
		return nil, "", nil
	}
	keptRel := ".polis/render-backup/" + now.Format("20060102-150405")
	// NOT the storage interface for the root: created 0700 because it is under
	// .polis and may hold copies of the owner's edits.
	if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(keptRel)), 0o700); err != nil {
		return nil, "", err
	}
	out := storage.NewPOSIX(filepath.Join(dir, filepath.FromSlash(keptRel)), storage.WithDirMode(0o700))
	var kept []string
	for _, rel := range drift {
		key := ".polis/bundles/pub.polis.core/" + rel
		data, err := storage.At(dir).Store().Get(context.Background(), key)
		if err != nil {
			continue // missing on disk: nothing of the owner's to keep
		}
		if err := out.Put(context.Background(), key, data, storage.WithMode(0o600)); err != nil {
			return nil, "", err
		}
		kept = append(kept, key)
	}
	return kept, keptRel, nil
}

func handleRender(args []string) {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	force := fs.Bool("force", false, "Force re-render all files")
	cliThemesDir := fs.String("cli-themes-dir", "", "CLI themes directory")
	baseURL := fs.String("base-url", "", "Site base URL")
	_ = fs.Parse(args) // ExitOnError: a bad flag exits, so Parse never returns an error

	dir := getDataDir()

	// Verify it's a polis site
	if !isPolisSite(dir) {
		exitError("Not a polis site directory (no .well-known/polis found)")
	}

	// Refresh the installed shape/theme reference payload from the embedded
	// fixtures BEFORE loading templates. In production, Medic does this on
	// its own cadence via version-gated resync; calling it here closes the
	// developer-experience hole where `polis render` against a freshly-built
	// CLI binary would still read stale templates from the tenant's installed
	// shape dir until the next Medic cycle. EnsureReferencePayload is
	// mtime-idempotent (skips writes when bytes match) so the production
	// case is a no-op when versions already align.
	// ⛔ An edited shipped file is about to be replaced: keep a copy first, and
	// say where. A refresh that would destroy the only copy of an edit refuses.
	kept, keptDir, err := keepDriftedPayload(dir, time.Now())
	if err != nil {
		exitError("Not refreshing the installed shapes and themes: could not keep a copy of the ones that differ: %v", err)
	}
	if err := bundle.EnsureReferencePayload(storage.At(dir), "pub.polis.core"); err != nil {
		exitError("Failed to refresh reference payload: %v", err)
	}
	if len(kept) > 0 && !jsonOutput {
		fmt.Fprintf(os.Stderr, "[!] %d installed shape/theme file(s) differed from this polis's copy (an older version, or edited by hand) and were replaced; the previous copies are in %s\n", len(kept), keptDir)
		fmt.Fprintf(os.Stderr, "    To keep a change to a shipped theme, make it in a theme of your own.\n")
	}

	// Find CLI themes directory if not specified
	themesDir := *cliThemesDir
	if themesDir == "" {
		themesDir = findCLIThemesDir()
	}

	// Get base URL: flag > env > .well-known/polis (legacy fallback)
	url := *baseURL
	if url == "" {
		url = os.Getenv("POLIS_BASE_URL")
	}
	if url == "" {
		url = getBaseURLFromSite(dir) // legacy fallback for pre-upgrade sites
	}

	// Load bundle for source/mount path resolution
	coreBundle := loadOrDefaultBundle(dir)
	postsSource, _ := coreBundle.ContentDir("pub.polis.post")
	postsMountDir, _ := coreBundle.MountDir("pub.polis.post")
	commentsSource, _ := coreBundle.ContentDir("pub.polis.comment")
	commentsMountDir, _ := coreBundle.MountDir("pub.polis.comment")

	// Create renderer
	renderer, err := render.NewPageRenderer(render.PageConfig{
		Site:              getSite(),
		CLIThemesDir:      themesDir,
		BaseURL:           url,
		RenderMarkers:     false, // CLI rendering doesn't need edit markers
		PostsSourceDir:    postsSource,
		PostsMountDir:     postsMountDir,
		CommentsSourceDir: commentsSource,
		CommentsMountDir:  commentsMountDir,
	})
	if err != nil {
		exitError("Failed to create renderer: %v", err)
	}

	// Render all pages
	stats, err := renderer.RenderAll(*force)
	if err != nil {
		exitError("Render failed: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success":           true,
			"posts_rendered":    stats.PostsRendered,
			"posts_skipped":     stats.PostsSkipped,
			"comments_rendered": stats.CommentsRendered,
			"comments_skipped":  stats.CommentsSkipped,
			"index_generated":   stats.IndexGenerated,
		})
	} else {
		fmt.Printf("Rendered %d posts, %d comments\n", stats.PostsRendered, stats.CommentsRendered)
		if stats.PostsSkipped > 0 || stats.CommentsSkipped > 0 {
			fmt.Printf("Skipped %d posts, %d comments (up to date)\n", stats.PostsSkipped, stats.CommentsSkipped)
		}
		if stats.IndexGenerated {
			fmt.Println("Generated index.html")
		}
	}
}

func isPolisSite(dir string) bool {
	ok, _ := storage.NewPOSIX(dir).Exists(context.Background(), ".well-known/polis")
	return ok
}

func getBaseURLFromSite(dir string) string {
	data, err := storage.NewPOSIX(dir).Get(context.Background(), ".well-known/polis")
	if err != nil {
		return ""
	}

	var wk struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return ""
	}
	return wk.BaseURL
}

// loadOrDefaultBundle loads the site's bundle.json if present, otherwise returns
// the default pub.polis.core bundle.
func loadOrDefaultBundle(dir string) *bundle.Bundle {
	bundlePath := filepath.Join(dir, "content", "pub.polis.core", "bundle.json")
	if b, err := bundle.LoadBundle(bundlePath); err == nil {
		return b
	}
	return bundle.DefaultCoreBundle()
}

func findCLIThemesDir() string {
	// NOT the storage interface, for either candidate loop below: these probe
	// for a themes directory shipped BESIDE THE BINARY or under the working
	// directory, neither of which is inside the site. The store addresses the
	// site and nothing else.
	// Try paths relative to the executable
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		candidates := []string{
			filepath.Join(execDir, "..", "..", "cli-bash", "themes"), // From cli-go/cmd/polis
			filepath.Join(execDir, "..", "themes"),                   // From cli-go/cmd
			filepath.Join(execDir, "themes"),                         // Same dir
		}
		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil {
				abs, _ := filepath.Abs(path)
				return abs
			}
		}
	}

	// Try from working directory
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, "..", "cli-bash", "themes"),
		filepath.Join(cwd, "cli-bash", "themes"),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			abs, _ := filepath.Abs(path)
			return abs
		}
	}

	return ""
}
