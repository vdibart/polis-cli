package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/render"
)

func handleRender(args []string) {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	force := fs.Bool("force", false, "Force re-render all files")
	cliThemesDir := fs.String("cli-themes-dir", "", "CLI themes directory")
	baseURL := fs.String("base-url", "", "Site base URL")
	fs.Parse(args)

	dir := getDataDir()

	// Verify it's a polis site
	if !isPolisSite(dir) {
		exitError("Not a polis site directory (no .well-known/polis found)")
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

	// Create renderer
	renderer, err := render.NewPageRenderer(render.PageConfig{
		DataDir:       dir,
		CLIThemesDir:  themesDir,
		BaseURL:       url,
		RenderMarkers: false, // CLI rendering doesn't need edit markers
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
	wellKnown := filepath.Join(dir, ".well-known", "polis")
	_, err := os.Stat(wellKnown)
	return err == nil
}

func getBaseURLFromSite(dir string) string {
	wellKnown := filepath.Join(dir, ".well-known", "polis")
	data, err := os.ReadFile(wellKnown)
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

func findCLIThemesDir() string {
	// Try paths relative to the executable
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		candidates := []string{
			filepath.Join(execDir, "..", "..", "cli-bash", "themes"),     // From cli-go/cmd/polis
			filepath.Join(execDir, "..", "themes"),                       // From cli-go/cmd
			filepath.Join(execDir, "themes"),                             // Same dir
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
