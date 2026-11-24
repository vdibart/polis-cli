package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
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

	tags, err := tag.ListTags(getSite())
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

	normalized, err := tag.NormalizeTagName(name)
	if err != nil {
		exitError("Invalid tag name: %v", err)
	}

	tf, err := tag.LoadTag(getSite(), normalized)
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
	// ⭐ The operation /v1 serves (tag.apply), in process: the signed tag
	// file, its discovery service sync, its render and its event.
	data := dispatchTag("apply", map[string]any{"tag": name, "target_uri": targetURI})
	tagName, _ := data["tag"].(string)
	count := toInt(data["count"])

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "tag",
			"data": map[string]interface{}{
				"tag":        tagName,
				"target_uri": targetURI,
				"count":      count,
			},
		})
		return
	}

	fmt.Printf("[✓] Tagged %s with %q (%d targets)\n", targetURI, tagName, count)
}

func handleTagRemove(name, targetURI string) {
	// ⭐ The operation /v1 serves (tag.remove), in process.
	data := dispatchTag("remove", map[string]any{"tag": name, "target_uri": targetURI})
	tagName, _ := data["tag"].(string)
	count := toInt(data["count"])

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "tag",
			"data": map[string]interface{}{
				"tag":        tagName,
				"target_uri": targetURI,
				"count":      count,
			},
		})
		return
	}

	fmt.Printf("[✓] Removed %s from %q (%d targets remaining)\n", targetURI, tagName, count)
}

func handleTagDelete(name string) {
	normalized, err := tag.NormalizeTagName(name)
	if err != nil {
		exitError("Invalid tag name: %v", err)
	}

	// ⭐ The operation /v1 serves (tag.delete), in process: it unregisters
	// every target from the discovery service, then deletes the file.
	dispatchTag("delete", map[string]any{"tag": normalized})

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

// dispatchTag runs a tag operation through the CLI's action layer and exits on
// failure, as the commands always have.
func dispatchTag(action string, payload map[string]any) map[string]any {
	dir := getDataDir()
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}
	defer signing.ZeroKey(privKey)
	pubKey, _ := storage.NewPOSIX(dir).Get(context.Background(), ".polis/keys/id_ed25519.pub")
	engine, err := cliEngine(dir, privKey, pubKey)
	if err != nil {
		exitError("Failed to start: %v", err)
	}
	res, err := engine.Dispatch(cliContext(), ops.ActionRequest{Action: action, ContentType: "pub.polis.tag", Payload: payload})
	if err != nil {
		verbs := map[string]string{"apply": "apply tag", "remove": "remove target", "delete": "delete tag"}
		exitError("Failed to %s: %v", verbs[action], err)
	}
	return res.Data
}

// toInt reads a JSON-ish number.
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}
