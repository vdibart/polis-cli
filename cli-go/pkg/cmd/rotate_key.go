package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/did"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

func handleRotateKey(args []string) {
	fs := flag.NewFlagSet("rotate-key", flag.ExitOnError)
	fs.Parse(args)

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	keysDir := filepath.Join(dir, ".polis", "keys")
	privateKeyPath := filepath.Join(keysDir, "id_ed25519")
	publicKeyPath := filepath.Join(keysDir, "id_ed25519.pub")

	if !jsonOutput {
		fmt.Println("[i] Rotating key pair...")
	}

	// Read old public key before any changes
	oldPubKeyData, err := os.ReadFile(publicKeyPath)
	if err != nil {
		exitError("Failed to read current public key: %v", err)
	}
	oldPubKey := strings.TrimSpace(string(oldPubKeyData))

	// Read old private key for transition signature
	oldPrivKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		exitError("Failed to read current private key: %v", err)
	}

	// Generate new keypair
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		exitError("Failed to generate new keypair: %v", err)
	}
	newPubKey := strings.TrimSpace(string(pubSSH))

	// Get domain from POLIS_BASE_URL
	polisBaseURL := os.Getenv("POLIS_BASE_URL")
	if polisBaseURL == "" {
		exitError("POLIS_BASE_URL not set")
	}
	domain := polisurl.ExtractDomain(polisBaseURL)
	if domain == "" {
		exitError("Could not extract domain from POLIS_BASE_URL")
	}

	// Build canonical rotation JSON and sign with OLD key
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	canonical, err := discovery.MakeKeyRotationCanonicalJSON(domain, oldPubKey, newPubKey, timestamp)
	if err != nil {
		exitError("Failed to build canonical rotation JSON: %v", err)
	}

	transitionSig, err := signing.SignContent(canonical, oldPrivKey)
	if err != nil {
		exitError("Failed to sign transition message with old key: %v", err)
	}

	// Notify DS of key rotation BEFORE local key swap (strict ordering)
	dsURL := os.Getenv("DISCOVERY_SERVICE_URL")
	dsKey := os.Getenv("DISCOVERY_SERVICE_KEY")
	if dsURL == "" {
		dsURL = DefaultDiscoveryServiceURL
	}

	// SIGNET epic 32: the DS's countersignature over this rotation, when it
	// issues one, rides inside the new key-history entry below.
	var witnesses []discovery.Witness

	// What happened at the DS, reported as-is in --json. "success" or
	// "skipped" — never "failed": a rejected notification exits the command
	// before any key changes, with an error envelope.
	dsRotation := "skipped"

	if discovery.IsRegisteredLocally(dir, dsURL) {
		dsClient := discovery.NewClient(dsURL, dsKey)
		if !jsonOutput {
			fmt.Println("[i] Notifying discovery service of key rotation...")
		}

		witness, err := dsClient.RotateKey(discovery.KeyRotationRequest{
			Domain:        domain,
			OldKey:        oldPubKey,
			NewKey:        newPubKey,
			TransitionSig: transitionSig,
			Timestamp:     timestamp,
		})
		if err != nil {
			exitError("Discovery service rejected key rotation: %v", err)
		}
		if witness != nil {
			witnesses = append(witnesses, *witness)
		}
		dsRotation = "success"

		if !jsonOutput {
			fmt.Println("[✓] Discovery service acknowledged key rotation")
		}
	} else if !jsonOutput {
		fmt.Println("[i] DS notification skipped: site not registered with discovery service")
	}

	// No .old backup is made, and --delete-old-key is gone with it.
	//
	// The single fixed .old path was a poor substitute for a chain: rotate twice
	// and the second rotation overwrote the first backup, so the earlier key was
	// simply gone. The published key history replaces it — it records every key
	// this site has held, and it records the PUBLIC halves, which is what a
	// verifier needs. Keeping a spare copy of a retired PRIVATE key on disk was
	// never the thing that made old signatures checkable; it was only a liability.
	//
	// Existing .old files are left where they are. They stop being made; they are
	// not cleaned up. (webapp/internal/hosted/reaper.go still excludes them from a
	// departing user's archive, and must keep doing so for as long as any exist.)

	// Write new keys
	if err := os.WriteFile(privateKeyPath, privPEM, 0600); err != nil {
		exitError("Failed to write new private key: %v", err)
	}
	if err := os.WriteFile(publicKeyPath, pubSSH, 0644); err != nil {
		exitError("Failed to write new public key: %v", err)
	}

	// Update .well-known/polis: the new public_key AND the appended history
	// entry, in one write, through the shared seam.
	//
	// ⛔ Do not inline either half here. This operation is implemented twice —
	// the webapp has its own complete rotation handler — and a chain appended in
	// one path and not the other is a site whose published key disagrees with
	// its own history. site.RecordKeyRotation is the single place both go
	// through, so the two facts cannot drift apart.
	//
	// timestamp is passed verbatim: it is the value the old key just signed, and
	// it becomes the new entry's valid_from. Reformatting it would break the
	// transition signature the chain is verified by.
	if err := site.RecordKeyRotation(dir, newPubKey, transitionSig, timestamp, witnesses...); err != nil {
		exitError("Failed to record the key rotation in .well-known/polis: %v", err)
	}
	epoch := 0
	if block, herr := site.LoadKeyHistory(dir); herr == nil && block != nil {
		epoch = block.Current.Epoch
	}

	// Re-sign the public_key_messages block under the NEW identity key. The DM messages
	// keys themselves don't change (they're independent of the identity key) — only the
	// signatures over them are refreshed, so no window has stale signatures. Skip tenants
	// with no DM keyring. (Phase 2.6; Judge expects a one-time re-baseline.)
	if _, err := os.Stat(filepath.Join(dm.DMDir(dir), "keyring.json")); err == nil {
		if err := site.PublishMessagesKey(dir, privPEM); err != nil {
			exitError("Failed to re-sign public_key_messages: %v", err)
		}
	}

	// Republish the DID Document so it carries the new key — and, since SIGNET
	// epic 16, the retired ones too.
	//
	// A DID document supports multiple verificationMethod entries, so the same
	// history the site publishes natively projects into the standard shape: the
	// retired key stays in verificationMethod (a resolver can still verify what
	// it signed) and drops out of assertionMethod (it no longer speaks for this
	// identity). Carry the standard, own the binding — epic 09's move, applied a
	// second time to the same key.
	//
	// ⚠️ The native block is the source; this is the projection. The projection
	// is rebuilt from .well-known/polis, so it must be republished AFTER
	// RecordKeyRotation, never before.
	//
	// Not fatal, for the same reason as at init: the rotation itself has
	// already succeeded and been announced to the DS by this point, so failing
	// the command over a derived projection would report a rotation that did
	// in fact happen as a failure. Medic republishes it on the next sweep.
	didPublished := false
	didRemoved := false
	if err := site.PublishDIDDocument(dir, domain); err == nil {
		didPublished = true
		logCLIAction("did.published", map[string]interface{}{
			"did":     did.ID(domain),
			"trigger": "rotate-key",
		})
	} else {
		// Republishing failed, so whatever is on disk now names the RETIRED
		// key. Remove it rather than leaving it: a stale document answers 200
		// with a key the site no longer holds and a resolver cannot tell,
		// while an absent one fails honestly. Deleting is safe because the
		// document is derived — `polis did --write` rebuilds it — and the
		// rotation itself is never blocked either way.
		if rmErr := os.Remove(site.DIDDocumentPath(dir)); rmErr == nil {
			didRemoved = true
		}
		logCLIAction("did.publish_failed", map[string]interface{}{
			"host":    domain,
			"trigger": "rotate-key",
			"error":   err.Error(),
			"removed": didRemoved,
		})
	}

	if !jsonOutput {
		fmt.Println()
		fmt.Println("[✓] Key rotation complete!")
		fmt.Println()
		fmt.Println("[i] New public key:")
		fmt.Printf("  %s\n", newPubKey)
		fmt.Println()
		fmt.Println("[i] Updated .well-known/polis with new public key")
		fmt.Printf("[i] Appended epoch %d to public_key_history — every signature made under the old key stays verifiable from this site alone\n", epoch)
		if didPublished {
			fmt.Printf("[i] Updated .well-known/did.json — %s now states the new key\n", did.ID(domain))
		} else if didRemoved {
			fmt.Println("[!] Removed .well-known/did.json — it stated the old key")
			fmt.Println("    Run `polis did --write` to republish it with the new one")
		}
		fmt.Println()
		fmt.Println("[!] Deploy the updated .well-known/polis file")
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "rotate-key",
			"data": map[string]interface{}{
				"new_public_key": newPubKey,
				// old_key_backed_up / old_key_path are gone: no .old file is
				// written any more. key_history_epoch is what replaces them —
				// which entry in the site's own published chain this rotation
				// became.
				"key_history_epoch": epoch,
				"ds_rotation":       dsRotation,
				"did_published":     didPublished,
				"did_removed":       didRemoved,
			},
		})
	}
}
