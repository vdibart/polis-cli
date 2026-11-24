package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"io"
	"os"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

func handleRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	_ = fs.Parse(args) // ExitOnError: a bad flag exits, so Parse never returns an error

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load discovery config from env
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
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

	client := discovery.NewClient(discoveryURL)

	// Register the site
	handleSiteRegister(client, dir, domain, privKey)
}

func handleSiteRegister(client *discovery.Client, dir, domain string, privKey []byte) {
	// Get author_name from .well-known/polis (email is private, not sent to DS)
	var authorName string
	if wk, err := site.LoadWellKnown(storage.At(dir)); err == nil {
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
	if err := discovery.WriteRegistrationMarker(storage.At(dir), client.BaseURL, domain, result.ServiceAttestation); err != nil {
		fmt.Fprintf(os.Stderr, "[!] Warning: could not write registration marker: %v\n", err)
	}

	// Upstream follow-drift guard. A follow made while this site was
	// unregistered had its announce suppressed (writes require registration),
	// leaving the DS short of following.json. Now that we're registered,
	// reconcile the DS up to local so those follows are announced instead of
	// lingering as a "dangling removed" until a Chaplain cycle — and so self-
	// hosters (no Chaplain) recover at all. Best-effort.
	reconciledFollows := 0
	dsCfg := &stream.DiscoveryConfig{
		DiscoveryURL: client.BaseURL,
		BaseURL:      "https://" + domain,
		DataDir:      dir,
	}
	if reannounced, rerr := following.ReconcileFollowsToDS(storage.At(dir), domain, client, privKey, dsCfg); rerr != nil {
		fmt.Fprintf(os.Stderr, "[!] Warning: follow reconcile skipped: %v\n", rerr)
	} else {
		reconciledFollows = len(reannounced)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success":            result.Success,
			"domain":             domain,
			"created_at":         result.CreatedAt,
			"registry_url":       result.RegistryURL,
			"reconciled_follows": reconciledFollows,
		})
	} else {
		fmt.Printf("Site registered: %s\n", domain)
		fmt.Printf("Registry URL: %s\n", result.RegistryURL)
		if reconciledFollows > 0 {
			fmt.Printf("[i] Reconciled %d follow(s) to the discovery service\n", reconciledFollows)
		}
	}
}

func handleUnregister(args []string) {
	fs := flag.NewFlagSet("unregister", flag.ExitOnError)
	var force bool
	fs.BoolVar(&force, "force", false, "Skip the confirmation prompt (same as -f)")
	fs.BoolVar(&force, "f", false, "Skip the confirmation prompt (same as --force)")
	_ = fs.Parse(args) // ExitOnError: a bad flag exits, so Parse never returns an error

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load discovery config from env
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
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

	proceed, err := confirmUnregister(domain, force, os.Stdin)
	if err != nil {
		exitError("%v", err)
	}
	if !proceed {
		fmt.Println("[i] Unregistration cancelled.")
		return
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	client := discovery.NewClient(discoveryURL)

	// Unregister the site
	result, err := client.UnregisterSite(domain, privKey)
	if err != nil {
		exitError("Failed to unregister site: %v", err)
	}

	// Remove local registration marker
	if err := discovery.RemoveRegistrationMarker(storage.At(dir), discoveryURL); err != nil {
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

// confirmUnregister asks before a site leaves the discovery service, as the
// bash CLI does: only a typed "yes" proceeds. --force skips the question.
// --json cannot show one, so without --force it refuses rather than proceed —
// choosing an output format is not consent.
func confirmUnregister(domain string, force bool, in io.Reader) (bool, error) {
	if force {
		return true, nil
	}
	if jsonOutput {
		return false, fmt.Errorf("unregister under --json requires --force (-f): it cannot ask for confirmation. Nothing was unregistered")
	}
	fmt.Println()
	fmt.Println("[!] WARNING: Unregistering will remove your site from the public directory.")
	fmt.Println()
	fmt.Println("This means:")
	fmt.Println("  - Your site will no longer be publicly listed or discoverable")
	fmt.Println("  - Other authors cannot find or interact with your content through the network")
	fmt.Println("  - You can re-register anytime to rejoin the community")
	fmt.Println()
	fmt.Printf("Are you sure you want to unregister %s? (type 'yes' to confirm) ", domain)
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.TrimSpace(line) == "yes", nil
}
