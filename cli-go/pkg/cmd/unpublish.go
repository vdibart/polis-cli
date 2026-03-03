package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

func handleUnpublish(args []string) {
	fs := flag.NewFlagSet("unpublish", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() < 1 {
		exitError("Usage: polis unpublish <path>\n  Example: polis unpublish content/pub.polis.core/post/20260201/my-post.md")
	}

	postPath := fs.Arg(0)
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	if err := RunUnpublish(dir, postPath); err != nil {
		exitError("%v", err)
	}
}

// RunUnpublish removes a published post locally and from the discovery service.
func RunUnpublish(dataDir, postPath string) error {
	// Validate path is under content/pub.polis.core/post/ (prevent directory traversal)
	if !strings.HasPrefix(postPath, "content/pub.polis.core/post/") {
		return fmt.Errorf("path must start with content/pub.polis.core/post/: %s", postPath)
	}
	if strings.Contains(postPath, "..") {
		return fmt.Errorf("path must not contain '..': %s", postPath)
	}

	// Verify file exists
	fullPath := filepath.Join(dataDir, postPath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return fmt.Errorf("post not found: %s", postPath)
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

	// Build content URL: base_url + "/" + postPath (strip .md extension)
	contentPath := strings.TrimSuffix(postPath, ".md")
	contentURL := strings.TrimRight(polisBaseURL, "/") + "/" + contentPath + ".md"

	// Load private key for signing
	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		return fmt.Errorf("failed to load private key: %v", err)
	}

	// Build canonical unregister JSON and sign
	canonical := discovery.MakeContentUnregisterCanonicalJSON("pub.polis.post", contentURL)
	sig, err := signing.SignContent([]byte(canonical), privKey)
	if err != nil {
		return fmt.Errorf("failed to sign unregister request: %v", err)
	}

	// Call DS to unregister (now does soft-delete)
	dsURL := os.Getenv("DISCOVERY_SERVICE_URL")
	dsKey := os.Getenv("DISCOVERY_SERVICE_KEY")
	if dsURL == "" {
		dsURL = "https://ds.polis.pub"
	}
	dsClient := discovery.NewClient(dsURL, dsKey)

	if !jsonOutput {
		fmt.Println("[i] Removing from discovery service...")
	}

	if err := dsClient.UnregisterContent("pub.polis.post", contentURL, sig); err != nil {
		// Non-fatal: DS might be unreachable, still clean up locally
		if !jsonOutput {
			fmt.Printf("[!] Warning: DS unregister failed: %v\n", err)
		}
	} else {
		if !jsonOutput {
			fmt.Println("[✓] Removed from discovery service")
		}
	}

	// Delete local .md file
	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("failed to delete post file: %v", err)
	}

	// Delete rendered .html file if exists
	htmlPath := strings.TrimSuffix(fullPath, ".md") + ".html"
	os.Remove(htmlPath) // Ignore error — may not exist

	// Remove from metadata/public.jsonl
	if err := metadata.RemoveIndexEntry(dataDir, postPath); err != nil {
		// Non-fatal: index entry may not exist
		if !jsonOutput {
			fmt.Printf("[!] Warning: could not remove index entry: %v\n", err)
		}
	}

	// Remove version history if exists
	baseName := filepath.Base(postPath)
	versionsPath := filepath.Join(dataDir, ".versions", baseName)
	os.Remove(versionsPath) // Ignore error — may not exist

	// Update manifest
	if err := publish.UpdateManifest(dataDir); err != nil {
		if !jsonOutput {
			fmt.Printf("[!] Warning: failed to update manifest: %v\n", err)
		}
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "unpublish",
			"data": map[string]interface{}{
				"path":        postPath,
				"content_url": contentURL,
			},
		})
	} else {
		fmt.Println()
		fmt.Printf("[✓] Post unpublished: %s\n", postPath)
	}

	return nil
}
