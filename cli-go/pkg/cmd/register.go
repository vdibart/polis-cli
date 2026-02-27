package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// Note: filepath is used by handleSiteRegister

func handleRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	fs.Parse(args)

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load discovery config from env
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	discoveryKey := os.Getenv("DISCOVERY_SERVICE_KEY")
	if discoveryURL == "" {
		discoveryURL = "https://ltfpezriiaqvjupxbttw.supabase.co/functions/v1"
	}

	// Get domain from POLIS_BASE_URL
	baseURL := os.Getenv("POLIS_BASE_URL")
	if baseURL == "" {
		exitError("POLIS_BASE_URL not set")
	}
	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		exitError("Could not extract domain from POLIS_BASE_URL")
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	client := discovery.NewClient(discoveryURL, discoveryKey)

	// Register the site
	handleSiteRegister(client, dir, domain, privKey)
}

func handleSiteRegister(client *discovery.Client, dir, domain string, privKey []byte) {
	// Get author from .well-known/polis (email is private, not sent to DS)
	var authorName string
	wellKnownPath := filepath.Join(dir, ".well-known", "polis")
	data, err := os.ReadFile(wellKnownPath)
	if err == nil {
		var wkp map[string]interface{}
		if json.Unmarshal(data, &wkp) == nil {
			if a, ok := wkp["author"].(string); ok {
				authorName = a
			}
		}
	}

	result, err := client.RegisterSite(domain, privKey, "", authorName)
	if err != nil {
		if strings.Contains(err.Error(), "WELLKNOWN_FETCH_FAILED") {
			if jsonOutput {
				exitError("Failed to register site: could not reach .well-known/polis on %s", domain)
			}
			fmt.Fprintf(os.Stderr, "[x] Failed to register: could not reach .well-known/polis on %s\n", domain)
			fmt.Fprintf(os.Stderr, "[i] Is your site deployed? Registration requires your site to be publicly accessible.\n")
			os.Exit(1)
		}
		exitError("Failed to register site: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success":       result.Success,
			"domain":        domain,
			"created_at": result.CreatedAt,
			"registry_url":  result.RegistryURL,
		})
	} else {
		fmt.Printf("Site registered: %s\n", domain)
		fmt.Printf("Registry URL: %s\n", result.RegistryURL)
	}
}

func handleUnregister(args []string) {
	fs := flag.NewFlagSet("unregister", flag.ExitOnError)
	fs.Parse(args)

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load discovery config from env
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	discoveryKey := os.Getenv("DISCOVERY_SERVICE_KEY")
	if discoveryURL == "" {
		discoveryURL = "https://ltfpezriiaqvjupxbttw.supabase.co/functions/v1"
	}

	// Get domain from POLIS_BASE_URL
	baseURL := os.Getenv("POLIS_BASE_URL")
	if baseURL == "" {
		exitError("POLIS_BASE_URL not set")
	}
	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		exitError("Could not extract domain from POLIS_BASE_URL")
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	client := discovery.NewClient(discoveryURL, discoveryKey)

	// Unregister the site
	result, err := client.UnregisterSite(domain, privKey)
	if err != nil {
		exitError("Failed to unregister site: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success": result.Success,
			"domain":  domain,
			"message": result.Message,
		})
	} else {
		fmt.Printf("Site unregistered: %s\n", domain)
	}
}
