package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/actor"
	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
)

// handleSite is `polis site`: routine edits to the site's identity, and the
// escape hatch for a signed file this build refuses to rewrite.
//
// ⭐ Setting a display name used to be file
// surgery on .well-known/polis. These go through the one lossless writer.
func handleSite(args []string) {
	if len(args) == 0 {
		exitError("Usage: polis site set author-name <name> | polis site set avatar [flags] | polis site rewrite-unsigned <path>")
	}
	switch args[0] {
	case "set":
		handleSiteSet(args[1:])
	case "rewrite-unsigned":
		handleSiteRewriteUnsigned(args[1:])
	default:
		exitError("Unknown site subcommand: %s", args[0])
	}
}

func handleSiteSet(args []string) {
	if len(args) == 0 {
		exitError("Usage: polis site set author-name <name> | polis site set avatar [flags]")
	}
	dir := getDataDir()
	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	switch args[0] {
	case "author-name":
		if len(args) != 2 {
			exitError("Usage: polis site set author-name <name>")
		}
		name, err := site.SetAuthorName(dir, args[1])
		if err != nil {
			exitError("Failed to set author name: %v", err)
		}
		logCLIAction("site.author_name_update", map[string]interface{}{"author_name": name})
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"status":  "success",
				"command": "site set author-name",
				"data":    map[string]interface{}{"author_name": name},
			})
			return
		}
		fmt.Printf("[✓] author_name set to %q in .well-known/polis\n", name)

	case "avatar":
		fs := flag.NewFlagSet("site set avatar", flag.ExitOnError)
		bg := fs.String("bg", "", "Background colour (#RRGGBB)")
		fg := fs.String("fg", "", "Foreground colour (#RRGGBB)")
		border := fs.String("border", "", "Border colour (#RRGGBB)")
		borderW := fs.Int("border-w", -1, "Border width (0-3)")
		pattern := fs.String("pattern", "", "Pattern: none, rings, cross, grid, dots, stripes, diamond, halves")
		patternColor := fs.String("pattern-color", "", "Pattern colour (#RRGGBB)")
		clearAvatar := fs.Bool("clear", false, "Remove the custom avatar")
		fs.Parse(args[1:])

		var avatar *site.AvatarConfig
		if !*clearAvatar {
			wk, err := site.LoadWellKnown(dir)
			if err != nil {
				exitError("Failed to load .well-known/polis: %v", err)
			}
			// Flags override the current avatar field by field; anything not
			// passed — including members this build does not model — is kept.
			avatar = &site.AvatarConfig{}
			if wk.Avatar != nil {
				copied := *wk.Avatar
				avatar = &copied
			}
			if *bg != "" {
				avatar.BG = *bg
			}
			if *fg != "" {
				avatar.FG = *fg
			}
			if *border != "" {
				avatar.Border = *border
			}
			if *borderW >= 0 {
				avatar.BorderW = *borderW
			}
			if *pattern != "" {
				avatar.Pattern = *pattern
			}
			if *patternColor != "" {
				avatar.PatternColor = *patternColor
			}
		}
		faviconErr, err := site.SetAvatar(dir, avatar)
		if err != nil {
			exitError("Failed to set avatar: %v", err)
		}
		logCLIAction("site.avatar_update", map[string]interface{}{"has_avatar": avatar != nil})
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"status":  "success",
				"command": "site set avatar",
				"data":    map[string]interface{}{"avatar": avatar},
			})
			return
		}
		fmt.Println("[✓] avatar updated in .well-known/polis")
		if faviconErr != nil {
			fmt.Fprintf(os.Stderr, "[!] favicon.svg was not regenerated: %v\n", faviconErr)
		}

	default:
		exitError("Unknown site setting: %s (expected author-name or avatar)", args[0])
	}
}

// handleSiteRewriteUnsigned is the ESCAPE HATCH named by every refusal to
// rewrite a signed file carrying members this build does not understand.
//
// ⛔ It never signs. It rewrites the file from what this build can read,
// without a signature, and says exactly what it dropped. The user asks for it
// by name; nothing else calls it.
func handleSiteRewriteUnsigned(args []string) {
	if len(args) != 1 {
		exitError("Usage: polis site rewrite-unsigned <path>")
	}
	dir := getDataDir()
	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}
	path, err := filepath.Abs(args[0])
	if err != nil {
		exitError("Invalid path: %v", err)
	}
	artifact, rewrite := unsignedRewriterFor(dir, path)
	if rewrite == nil {
		exitError("%s is not one of the signed files this command rewrites (blessed.json, following.json, a tag file, license.json, an attestation record, the actor registry)", args[0])
	}
	dropped, err := rewrite()
	if err != nil {
		exitError("Failed to rewrite %s: %v", artifact, err)
	}
	logCLIAction("site.rewrite_unsigned", map[string]interface{}{"artifact": artifact, "dropped": dropped})
	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "site rewrite-unsigned",
			"data":    map[string]interface{}{"artifact": artifact, "path": path, "dropped": dropped, "signed": false},
		})
		return
	}
	if len(dropped) == 0 {
		fmt.Printf("[i] %s carries nothing this polis does not understand; left untouched\n", artifact)
		return
	}
	fmt.Println("[!] " + signing.RewrittenUnsignedNote(artifact, dropped))
}

// unsignedRewriterFor resolves a path to the one package that owns it. Paths
// come from the site's own layout, never from the file's contents.
func unsignedRewriterFor(dataDir, path string) (string, func() ([]string, error)) {
	abs := func(p string) string {
		a, _ := filepath.Abs(p)
		return a
	}
	switch {
	case path == abs(filepath.Join(dataDir, metadata.BundleContentDir, "comment", metadata.BlessedCommentsFilename)):
		return metadata.BlessedCommentsFilename, func() ([]string, error) { return metadata.RewriteBlessedUnsigned(dataDir) }
	case path == abs(following.DefaultPath(dataDir)):
		return "following.json", func() ([]string, error) { return following.RewriteUnsigned(path) }
	case path == abs(site.LicensePath(dataDir)):
		return "license.json", func() ([]string, error) { return license.RewriteUnsigned(path) }
	case path == abs(actor.RegistryPath(dataDir)):
		return "actor registry", func() ([]string, error) { return actor.RewriteUnsigned(path) }
	case filepath.Dir(path) == abs(filepath.Dir(tag.TagPath(dataDir, "x"))):
		return "tag file", func() ([]string, error) { return tag.RewriteUnsigned(dataDir, path) }
	case filepath.Dir(path) == abs(attestation.Dir(dataDir)):
		return "attestation", func() ([]string, error) { return attestation.RewriteUnsigned(path) }
	}
	return "", nil
}
