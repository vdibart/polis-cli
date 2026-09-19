package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

func handleInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	siteTitle := fs.String("site-title", "", "Site display name")
	author := fs.String("author", "", "Author name (default: git config user.name)")
	email := fs.String("email", "", "Email address (optional, private by default)")
	theme := fs.String("theme", "", "Initial theme (default: sols)")
	licenseFlag := fs.String("license", "", "Terms for your posts: reserved, open, or none (omit to be asked)")
	fs.Parse(args)

	dir := getDataDir()

	// Ask, unless the caller already answered or there is nobody to ask.
	//
	// A default nobody saw is not consent, so there are exactly two honest
	// paths: prompt an interactive user, with nothing pre-selected, or take an
	// explicit flag. A non-interactive run with no flag states
	// NOTHING rather than choosing silently — absent is a defined state, and
	// scripts that want terms can say so.
	licenseChoice := *licenseFlag
	if licenseChoice == "" && !jsonOutput && isInteractive() {
		licenseChoice = PromptForLicense(os.Stdin, os.Stdout)
	}

	// SIGNET epic 11 D11: Rosie, asked the same way — only when a person is
	// there, nothing pre-selected, in the words Settings → Rosie shows. A
	// non-interactive init states nothing about her.
	askedRosie, rosieOn := false, false
	if !jsonOutput && isInteractive() {
		askedRosie = true
		rosieOn = PromptForRosie(os.Stdin, os.Stdout)
	}

	opts := site.InitOptions{
		SiteTitle: *siteTitle,
		Author:    *author,
		Email:     *email,
		Theme:     *theme,
		Generator: generator,
		License:   licenseChoice,
		BaseURL:   baseURL,
	}

	result, err := site.Init(dir, opts)
	if err != nil {
		exitError("Failed to initialize site: %v", err)
	}

	// Record whether the site came into existence with a DID. A site
	// initialised without a canonical host gets none, which is correct but
	// invisible; logging both outcomes keeps "nobody told us where this site
	// lives" distinguishable from "the projection failed".
	if result.DID != "" {
		logCLIAction("did.published", map[string]interface{}{
			"did":     result.DID,
			"trigger": "init",
		})
	} else {
		logCLIAction("did.not_published", map[string]interface{}{
			"reason":  "no canonical host at init (POLIS_BASE_URL unset)",
			"trigger": "init",
		})
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "init",
			"data": map[string]interface{}{
				"directories_created": result.DirsCreated,
				"files_created":       result.FilesCreated,
				"key_paths": map[string]interface{}{
					"private": result.KeyPaths.Private,
					"public":  result.KeyPaths.Public,
				},
				"did": result.DID,
			},
		})
	} else {
		fmt.Printf("[✓] Initialized polis site at: %s\n", result.SiteDir)
		fmt.Printf("[i] Public key: %s\n", result.PublicKey[:50]+"...")
		if result.DID != "" {
			// The same key, said the way the rest of the identity world says
			// it. Shown because a user who does not know they have a DID
			// cannot hand it to anyone.
			fmt.Printf("[i] DID: %s\n", result.DID)
		}

		// SHOW what was chosen. Recording it is not enough — the consent
		// argument rests on the user having seen it.
		terms, _ := site.SiteTerms(dir)
		describeLicenseChoice(result.License, terms)

		if askedRosie {
			if rosieOn {
				switchRosieOnAtInit(dir, baseURL, result.KeyPaths.Private)
			} else {
				describeRosieDeclined()
			}
		}

		fmt.Println("\nNext steps:")
		fmt.Println("  1. Set POLIS_BASE_URL in .env file")
		fmt.Println("  2. Create your first post: polis post my-post.md")
		fmt.Println("  3. Deploy your site, then run: polis register")
	}
}
