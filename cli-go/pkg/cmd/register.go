package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

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
		discoveryURL = "https://ds.polis.pub"
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
	// Get author_name from .well-known/polis (email is private, not sent to DS)
	var authorName string
	if wk, err := site.LoadWellKnown(dir); err == nil {
		authorName = wk.AuthorName
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

	// Write local registration marker
	if err := discovery.WriteRegistrationMarker(dir, client.BaseURL, domain, result.ServiceAttestation); err != nil {
		fmt.Fprintf(os.Stderr, "[!] Warning: could not write registration marker: %v\n", err)
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
		discoveryURL = "https://ds.polis.pub"
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

	// Remove local registration marker
	if err := discovery.RemoveRegistrationMarker(dir, discoveryURL); err != nil {
		fmt.Fprintf(os.Stderr, "[!] Warning: could not remove registration marker: %v\n", err)
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
