package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

func handleAbout(args []string) {
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load .well-known/polis
	wk, err := site.LoadWellKnown(dir)
	if err != nil {
		exitError("Failed to load .well-known/polis: %v", err)
	}

	// Load manifest.json
	manifestPath := filepath.Join(dir, "metadata", "manifest.json")
	var manifest map[string]interface{}
	if data, err := os.ReadFile(manifestPath); err == nil {
		json.Unmarshal(data, &manifest)
	}

	// Get environment config
	baseURL := os.Getenv("POLIS_BASE_URL")
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if discoveryURL == "" {
		discoveryURL = "https://ltfpezriiaqvjupxbttw.supabase.co/functions/v1"
	}

	// Check discovery service registration
	var registrationStatus string
	var registeredAt string
	if baseURL != "" {
		apiKey := os.Getenv("DISCOVERY_SERVICE_KEY")
		client := discovery.NewClient(discoveryURL, apiKey)
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
	postCount := countFiles(filepath.Join(dir, "posts"), ".md")
	commentCount := countFiles(filepath.Join(dir, "comments"), ".md")

	// Count following
	followingCount := 0
	followingPath := filepath.Join(dir, "metadata", "following.json")
	if data, err := os.ReadFile(followingPath); err == nil {
		var f struct {
			Following []interface{} `json:"following"`
		}
		if json.Unmarshal(data, &f) == nil {
			followingCount = len(f.Following)
		}
	}

	// Get config directories (handle nil Config)
	var keysDir, postsDir, commentsDir, snippetsDir, themesDir string
	if wk.Config != nil {
		keysDir = wk.Config.Directories.Keys
		postsDir = wk.Config.Directories.Posts
		commentsDir = wk.Config.Directories.Comments
		snippetsDir = wk.Config.Directories.Snippets
		themesDir = wk.Config.Directories.Themes
	}

	// Resolve domain identity from POLIS_BASE_URL
	domain := extractDomain(baseURL)

	if jsonOutput {
		siteData := map[string]interface{}{
			"author":          wk.Author,
			"domain":          domain,
			"created":         wk.Created,
			"public_key":      wk.PublicKey,
			"site_title":      wk.SiteTitle,
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
		fmt.Printf("  Author: %s\n", wk.Author)
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

func countFiles(dir, ext string) int {
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(path) == ext {
			// Skip .versions directories
			if filepath.Base(filepath.Dir(path)) != ".versions" {
				count++
			}
		}
		return nil
	})
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
