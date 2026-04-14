package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
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
		exitError("Usage: polis republish <posts/YYYYMMDD/post.md> [new-content.md]")
	}

	postPath := remaining[0]
	dir := getDataDir()

	// Verify it's a polis site
	if !isPolisSite(dir) {
		exitError("Not a polis site directory (no .well-known/polis found)")
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

func loadPrivateKey(dir string) ([]byte, error) {
	privKeyPath := filepath.Join(dir, ".polis", "keys", "id_ed25519")
	return os.ReadFile(privKeyPath)
}
