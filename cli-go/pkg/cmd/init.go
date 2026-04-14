package cmd

import (
	"flag"
	"fmt"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

func handleInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	siteTitle := fs.String("site-title", "", "Site display name")
	author := fs.String("author", "", "Author name (default: git config user.name)")
	email := fs.String("email", "", "Email address (optional, private by default)")
	theme := fs.String("theme", "", "Initial theme (default: sols)")
	fs.Parse(args)

	dir := getDataDir()

	opts := site.InitOptions{
		SiteTitle: *siteTitle,
		Author:    *author,
		Email:     *email,
		Theme:     *theme,
		Generator: generator,
	}

	result, err := site.Init(dir, opts)
	if err != nil {
		exitError("Failed to initialize site: %v", err)
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
			},
		})
	} else {
		fmt.Printf("[✓] Initialized polis site at: %s\n", result.SiteDir)
		fmt.Printf("[i] Public key: %s\n", result.PublicKey[:50]+"...")
		fmt.Println("\nNext steps:")
		fmt.Println("  1. Set POLIS_BASE_URL in .env file")
		fmt.Println("  2. Create your first post: polis post my-post.md")
		fmt.Println("  3. Deploy your site, then run: polis register")
	}
}
