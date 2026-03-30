package cmd

import (
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
)

func handleTag(args []string) {
	if len(args) < 1 {
		printTagUsage()
		os.Exit(1)
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "list":
		handleTagList()
	case "show":
		if len(subArgs) < 1 {
			exitError("Usage: polis tag show <name>")
		}
		handleTagShow(subArgs[0])
	case "apply":
		if len(subArgs) < 2 {
			exitError("Usage: polis tag apply <name> <target-uri>")
		}
		handleTagApply(subArgs[0], subArgs[1])
	case "remove":
		if len(subArgs) < 2 {
			exitError("Usage: polis tag remove <name> <target-uri>")
		}
		handleTagRemove(subArgs[0], subArgs[1])
	case "delete":
		if len(subArgs) < 1 {
			exitError("Usage: polis tag delete <name>")
		}
		handleTagDelete(subArgs[0])
	default:
		fmt.Fprintf(os.Stderr, "Unknown tag subcommand: %s\n", subcommand)
		printTagUsage()
		os.Exit(1)
	}
}

func printTagUsage() {
	fmt.Print(`Polis Tag - Content Tagging

Usage:
  polis tag <subcommand> [options]

Subcommands:
  list                          List all tags
  show <name>                   Show targets for a tag
  apply <name> <target-uri>     Tag content with a tag name
  remove <name> <target-uri>    Remove a target from a tag
  delete <name>                 Delete an entire tag

Examples:
  polis tag list
  polis tag show rust
  polis tag apply rust https://alice.com/posts/hello
  polis tag remove rust https://alice.com/posts/hello
  polis tag delete rust
`)
}

func handleTagList() {
	dataDir := getDataDir()

	tags, err := tag.ListTags(dataDir)
	if err != nil {
		exitError("Failed to list tags: %v", err)
	}

	if jsonOutput {
		tagMaps := make([]map[string]interface{}, 0, len(tags))
		for _, tf := range tags {
			tagMaps = append(tagMaps, map[string]interface{}{
				"tag":     tf.Tag,
				"count":   len(tf.Targets),
				"updated": tf.Updated,
			})
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "tag",
			"data": map[string]interface{}{
				"tags":  tagMaps,
				"count": len(tagMaps),
			},
		})
		return
	}

	if len(tags) == 0 {
		fmt.Println("[i] No tags found")
		return
	}

	for _, tf := range tags {
		fmt.Printf("  %s (%d targets)\n", tf.Tag, len(tf.Targets))
	}
}

func handleTagShow(name string) {
	dataDir := getDataDir()

	normalized, err := tag.NormalizeTagName(name)
	if err != nil {
		exitError("Invalid tag name: %v", err)
	}

	path := tag.TagPath(dataDir, normalized)
	tf, err := tag.Load(path)
	if err != nil {
		exitError("Failed to load tag %q: %v", normalized, err)
	}

	if jsonOutput {
		targets := make([]map[string]interface{}, 0, len(tf.Targets))
		for _, t := range tf.Targets {
			targets = append(targets, map[string]interface{}{
				"uri":   t.URI,
				"added": t.Added,
			})
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "tag",
			"data": map[string]interface{}{
				"tag":     tf.Tag,
				"targets": targets,
				"count":   len(targets),
			},
		})
		return
	}

	fmt.Printf("[i] Tag: %s (%d targets)\n", tf.Tag, len(tf.Targets))
	for _, t := range tf.Targets {
		fmt.Printf("  %s (added %s)\n", t.URI, t.Added)
	}
}

func handleTagApply(name, targetURI string) {
	dataDir := getDataDir()

	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}
	defer signing.ZeroKey(privKey)

	tf, err := tag.ApplyTag(dataDir, name, targetURI, privKey)
	if err != nil {
		exitError("Failed to apply tag: %v", err)
	}

	// DS sync (non-fatal)
	if discoveryURL != "" && baseURL != "" {
		dsCfg := &tag.DiscoveryConfig{
			DiscoveryURL: discoveryURL,
			DiscoveryKey: discoveryKey,
			BaseURL:      baseURL,
		}
		if err := tag.SyncTag(dataDir, tf, privKey, dsCfg); err != nil {
			fmt.Fprintf(os.Stderr, "[!] Discovery sync skipped: %v\n", err)
		}
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "tag",
			"data": map[string]interface{}{
				"tag":        tf.Tag,
				"target_uri": targetURI,
				"count":      len(tf.Targets),
			},
		})
		return
	}

	fmt.Printf("[✓] Tagged %s with %q (%d targets)\n", targetURI, tf.Tag, len(tf.Targets))
}

func handleTagRemove(name, targetURI string) {
	dataDir := getDataDir()

	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}
	defer signing.ZeroKey(privKey)

	tf, err := tag.RemoveTarget(dataDir, name, targetURI, privKey)
	if err != nil {
		exitError("Failed to remove target: %v", err)
	}

	// DS unregister (non-fatal)
	if discoveryURL != "" && baseURL != "" {
		dsCfg := &tag.DiscoveryConfig{
			DiscoveryURL: discoveryURL,
			DiscoveryKey: discoveryKey,
			BaseURL:      baseURL,
		}
		if err := tag.UnregisterTarget(name, targetURI, privKey, dsCfg); err != nil {
			fmt.Fprintf(os.Stderr, "[!] Discovery unregister skipped: %v\n", err)
		}
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "tag",
			"data": map[string]interface{}{
				"tag":        tf.Tag,
				"target_uri": targetURI,
				"count":      len(tf.Targets),
			},
		})
		return
	}

	fmt.Printf("[✓] Removed %s from %q (%d targets remaining)\n", targetURI, tf.Tag, len(tf.Targets))
}

func handleTagDelete(name string) {
	dataDir := getDataDir()

	normalized, err := tag.NormalizeTagName(name)
	if err != nil {
		exitError("Invalid tag name: %v", err)
	}

	// Load tag first to get targets for DS cleanup
	path := tag.TagPath(dataDir, normalized)
	tf, err := tag.Load(path)
	if err != nil {
		exitError("Failed to load tag %q: %v", normalized, err)
	}

	if err := tag.DeleteTag(dataDir, normalized); err != nil {
		exitError("Failed to delete tag: %v", err)
	}

	// DS unregister all targets (non-fatal)
	if discoveryURL != "" && baseURL != "" {
		privKey, err := loadPrivateKey(dataDir)
		if err == nil {
			defer signing.ZeroKey(privKey)
			dsCfg := &tag.DiscoveryConfig{
				DiscoveryURL: discoveryURL,
				DiscoveryKey: discoveryKey,
				BaseURL:      baseURL,
			}
			for _, target := range tf.Targets {
				tag.UnregisterTarget(tf.Tag, target.URI, privKey, dsCfg)
			}
		}
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "tag",
			"data": map[string]interface{}{
				"tag":     normalized,
				"deleted": true,
			},
		})
		return
	}

	fmt.Printf("[✓] Deleted tag %q\n", normalized)
}
