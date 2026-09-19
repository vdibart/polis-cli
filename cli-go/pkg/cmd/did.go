package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/did"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// handleDID prints the site's did:web identifier and DID Document, and
// optionally republishes the document.
//
// It exists mostly so a user can find out they HAVE a DID. The document is
// written by init, by rotate-key, and by Medic; nobody needs this command for
// the identity to work, but a user who cannot see their DID cannot hand it to
// anyone, and "here is mine" is the entire point of being resolvable.
func handleDID(args []string) {
	fs := flag.NewFlagSet("did", flag.ExitOnError)
	write := fs.Bool("write", false, "(Re)generate .well-known/did.json")
	hostFlag := fs.String("host", "", "Canonical host (default: from POLIS_BASE_URL)")
	fs.Parse(args)

	dir := getDataDir()
	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	host := *hostFlag
	if host == "" {
		host = polisurl.ExtractDomain(baseURL)
	}
	if host == "" {
		exitError("No canonical host — set POLIS_BASE_URL or pass --host")
	}

	document, err := site.BuildDIDDocument(dir, host)
	if err != nil {
		exitError("Failed to build DID document: %v", err)
	}

	published := false
	if *write {
		if err := site.PublishDIDDocument(dir, host); err != nil {
			exitError("Failed to write DID document: %v", err)
		}
		published = true
		logCLIAction("did.published", map[string]interface{}{
			"did":     did.ID(host),
			"trigger": "did --write",
		})
	}

	// Whether what is served matches what the site's key says right now. A
	// stale document is the one failure mode a resolver cannot see: it returns
	// 200 and a key the site has retired.
	needsWrite, _, err := site.DIDDocumentNeedsWrite(dir, host)
	onDisk := "up to date"
	if err != nil {
		onDisk = "unknown"
	} else if needsWrite {
		onDisk = "missing or stale — run `polis did --write`"
	}

	relPath := filepath.Join(".well-known", "did.json")
	if jsonOutput {
		var doc map[string]interface{}
		_ = json.Unmarshal(document, &doc)
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "did",
			"data": map[string]interface{}{
				"did":       did.ID(host),
				"url":       "https://" + host + "/.well-known/did.json",
				"path":      relPath,
				"document":  doc,
				"published": published,
				"on_disk":   onDisk,
			},
		})
		return
	}

	fmt.Printf("[i] DID:      %s\n", did.ID(host))
	fmt.Printf("[i] Resolves: https://%s/.well-known/did.json\n", host)
	fmt.Printf("[i] On disk:  %s (%s)\n", relPath, onDisk)
	if published {
		fmt.Printf("[✓] Wrote %s\n", relPath)
	}
	fmt.Println()
	os.Stdout.Write(document)
}
