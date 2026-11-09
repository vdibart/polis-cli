package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func handleAbout(args []string) {
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load .well-known/polis
	wk, err := site.LoadWellKnown(getSite())
	if err != nil {
		exitError("Failed to load .well-known/polis: %v", err)
	}

	// Active theme from .polis/bundles/registry.json (with legacy fallback).
	activeTheme := site.GetActiveTheme(getSite())

	// Get environment config
	baseURL := os.Getenv("POLIS_BASE_URL")
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if discoveryURL == "" {
		discoveryURL = "https://ds.polis.pub"
	}

	// Check discovery service registration
	var registrationStatus string
	var registeredAt string
	if baseURL != "" {
		client := discovery.NewClient(discoveryURL)
		domain := extractDomain(baseURL)
		if domain != "" {
			resp, err := client.CheckSiteRegistration(domain)
			if err == nil && resp.IsRegistered {
				registrationStatus = "registered"
				registeredAt = resp.CreatedAt
			} else {
				registrationStatus = "not registered"
			}
		}
	}

	// Count posts and comments
	postCount := countFiles(dir, filepath.Join(dir, "content", "pub.polis.core", "post"), ".md")
	commentCount := countFiles(dir, filepath.Join(dir, "content", "pub.polis.core", "comment"), ".md")

	// Count following
	followingCount := 0
	st := storage.NewPOSIX(dir)
	if data, err := st.Get(context.Background(), following.DefaultKey); err == nil {
		var f struct {
			Following []interface{} `json:"following"`
		}
		if json.Unmarshal(data, &f) == nil {
			followingCount = len(f.Following)
		}
	}

	// Standard directory paths (new bundle-based layout)
	keysDir := ".polis/keys"
	postsDir := "content/pub.polis.core/post"
	commentsDir := "content/pub.polis.core/comment"
	snippetsDir := "site/snippets"
	themesDir := "site/themes"

	// Resolve domain identity from POLIS_BASE_URL
	domain := extractDomain(baseURL)

	if jsonOutput {
		siteData := map[string]interface{}{
			"author_name":     wk.AuthorName,
			"domain":          domain,
			"created":         wk.Created,
			"public_key":      wk.PublicKey,
			"site_title":      wk.SiteTitle,
			"active_theme":    activeTheme,
			"base_url":        baseURL,
			"post_count":      postCount,
			"comment_count":   commentCount,
			"following_count": followingCount,
		}
		// Only include email in JSON output if explicitly set
		if wk.Email != "" {
			siteData["email"] = wk.Email
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "about",
			"data": map[string]interface{}{
				"cli_version": Version,
				"site":        siteData,
				"discovery": map[string]interface{}{
					"url":                 discoveryURL,
					"registration_status": registrationStatus,
					"registered_at":       registeredAt,
				},
				"config": map[string]interface{}{
					"keys_dir":     keysDir,
					"posts_dir":    postsDir,
					"comments_dir": commentsDir,
					"snippets_dir": snippetsDir,
					"themes_dir":   themesDir,
				},
			},
		})
	} else {
		fmt.Println()
		fmt.Printf("[i] Polis CLI version: %s\n", Version)
		fmt.Println()

		fmt.Println("=== Site Information ===")
		fmt.Printf("  Author: %s\n", wk.AuthorName)
		if domain != "" {
			fmt.Printf("  Domain: %s\n", domain)
		}
		if wk.Email != "" {
			fmt.Printf("  Email: %s\n", wk.Email)
		}
		fmt.Printf("  Created: %s\n", wk.Created)
		if wk.SiteTitle != "" {
			fmt.Printf("  Site Title: %s\n", wk.SiteTitle)
		}
		if baseURL != "" {
			fmt.Printf("  Base URL: %s\n", baseURL)
		}
		if activeTheme != "" {
			fmt.Printf("  Theme: %s\n", activeTheme)
		}
		fmt.Printf("  Posts: %d\n", postCount)
		fmt.Printf("  Comments: %d\n", commentCount)
		fmt.Printf("  Following: %d\n", followingCount)
		fmt.Println()

		fmt.Println("=== Public Key ===")
		fmt.Printf("  %s\n", wk.PublicKey)
		fmt.Println()

		fmt.Println("=== Discovery Service ===")
		fmt.Printf("  URL: %s\n", discoveryURL)
		fmt.Printf("  Status: %s\n", registrationStatus)
		if registeredAt != "" {
			fmt.Printf("  Registered: %s\n", registeredAt)
		}
		fmt.Println()

		fmt.Println("=== Directories ===")
		fmt.Printf("  Keys: %s\n", keysDir)
		fmt.Printf("  Posts: %s\n", postsDir)
		fmt.Printf("  Comments: %s\n", commentsDir)
		fmt.Printf("  Snippets: %s\n", snippetsDir)
		fmt.Printf("  Themes: %s\n", themesDir)
		fmt.Println()
	}
}

// countFiles counts files with ext beneath absDir, which is a path under the
// site root. It takes the root separately because the store addresses keys
// relative to it.
func countFiles(root, absDir, ext string) int {
	prefix, err := storage.RelKey(root, absDir)
	if err != nil {
		return 0
	}
	// ⛔ The error is ignored ON PURPOSE. A recursive List returns what it could
	// read plus the first failure, and the walk this replaced skipped an entry it
	// could not read and counted the rest. Returning 0 on any error made one
	// unreadable subdirectory read as "Posts: 0".
	entries, _ := storage.NewPOSIX(root).List(context.Background(), prefix, storage.Recursive())
	count := 0
	for _, e := range entries {
		if e.IsDir || path.Ext(e.Key) != ext {
			continue
		}
		// Skip .versions directories
		if path.Base(path.Dir(e.Key)) != ".versions" {
			count++
		}
	}
	return count
}

func extractDomain(url string) string {
	// Remove protocol
	if len(url) > 8 && url[:8] == "https://" {
		url = url[8:]
	} else if len(url) > 7 && url[:7] == "http://" {
		url = url[7:]
	}
	// Remove path
	for i := 0; i < len(url); i++ {
		if url[i] == '/' {
			return url[:i]
		}
	}
	return url
}
