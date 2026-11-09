package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/did"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

func handleRotateKey(args []string) {
	fs := flag.NewFlagSet("rotate-key", flag.ExitOnError)
	fs.Parse(args)

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	if !jsonOutput {
		fmt.Println("[i] Rotating key pair...")
	}

	ctx := context.Background()

	// ⭐ What this rotation starts from: a fresh keypair, or an earlier
	// rotation's staged key to resume (the discovery service may already hold
	// it), signing with whichever key .well-known/polis publishes as current.
	// ⛔ It refuses when the key on disk is not the published one: a handover
	// signed by anything but the chain's head breaks the chain for good.
	keys, err := site.PrepareRotation(ctx, getSite())
	if err != nil {
		exitError("Key rotation not started: %v", err)
	}
	resuming := keys.Resuming
	newPubKey := keys.NewPub
	if resuming && !jsonOutput {
		fmt.Println("[i] Resuming an interrupted rotation with the staged key")
	}

	// Get domain from POLIS_BASE_URL
	polisBaseURL := os.Getenv("POLIS_BASE_URL")
	if polisBaseURL == "" {
		exitError("POLIS_BASE_URL not set")
	}
	domain := polisurl.ExtractDomain(polisBaseURL)
	if domain == "" {
		exitError("Could not extract domain from POLIS_BASE_URL")
	}

	// Notify DS of key rotation BEFORE local key swap (strict ordering)
	dsURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if dsURL == "" {
		dsURL = DefaultDiscoveryServiceURL
	}

	// What happened at the DS, reported as-is in --json. "success" or
	// "skipped" — never "failed": a rejected notification exits the command
	// before any key changes, with an error envelope.
	dsRotation := "skipped"

	// ⭐ The offline steps (sign, stage, install, record, finish, re-sign,
	// republish did.json) are site.CompleteRotation, shared with `polis
	// branch`. This command adds one thing around them: the discovery-service
	// announcement, which runs after the new key is staged and before it is
	// installed.
	//
	// SIGNET epic 32: the DS's countersignature over this rotation, when it
	// issues one, rides inside the new key-history entry.
	announce := func(req discovery.KeyRotationRequest, resuming bool) ([]discovery.Witness, error) {
		if !discovery.IsRegisteredLocally(storage.At(dir), dsURL) {
			if !jsonOutput {
				fmt.Println("[i] DS notification skipped: site not registered with discovery service")
			}
			return nil, nil
		}
		dsClient := discovery.NewClient(dsURL)
		if !jsonOutput {
			fmt.Println("[i] Notifying discovery service of key rotation...")
		}

		// ⛔ Only a refusal drops the staged key; a lost answer keeps it
		// (site.AnnounceRotation).
		witness, already, err := site.AnnounceRotation(ctx, getSite(), dsClient, req, resuming)
		if errors.Is(err, site.ErrRotationPending) || errors.Is(err, site.ErrRotationDiverged) {
			exitError("Key rotation not finished: %v", err)
		}
		if err != nil {
			exitError("Discovery service rejected key rotation: %v", err)
		}
		var witnesses []discovery.Witness
		if witness != nil {
			witnesses = append(witnesses, *witness)
		}
		dsRotation = "success"

		if !jsonOutput {
			if already {
				fmt.Println("[✓] Discovery service already holds the staged key; finishing the rotation")
			} else {
				fmt.Println("[✓] Discovery service acknowledged key rotation")
			}
		}
		return witnesses, nil
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
	//
	// ⛔ public_key and public_key_history move only through
	// site.RecordKeyRotation, inside CompleteRotation. Rotation is implemented
	// more than once (the webapp has its own complete handler), and a chain
	// appended in one path and not the other is a site whose published key
	// disagrees with its own history.
	res, err := site.CompleteRotation(ctx, getSite(), keys, site.RotationOptions{
		SigningDomain: domain,
		Announce:      announce,
	})
	if err != nil {
		var re *site.RotationError
		if !errors.As(err, &re) {
			exitError("Key rotation failed: %v", err)
		}
		switch re.Stage {
		case site.StageCanonical:
			exitError("Failed to build canonical rotation JSON: %v", re.Err)
		case site.StageSign:
			exitError("Failed to sign transition message with old key: %v", re.Err)
		case site.StageStage:
			exitError("Key rotation not started: %v", re.Err)
		case site.StageInstall:
			exitError("Failed to install the new key (it is staged at %s): %v", site.StagedKeyKey, re.Err)
		case site.StageRecord:
			exitError("Failed to record the key rotation in .well-known/polis: %v. "+
				"Run 'polis rotate-key' again to finish it: the retired key is kept at %s", re.Err, site.PreviousKeyKey)
		case site.StageMessagesKey:
			exitError("Failed to re-sign public_key_messages: %v", re.Err)
		default:
			exitError("Key rotation failed: %v", re.Err)
		}
	}
	if res.FinishErr != nil && !jsonOutput {
		// Not fatal: the rotation is recorded, and the next rotation removes
		// what is left. But it is a private key on disk, so say so.
		fmt.Printf("[!] %v\n", res.FinishErr)
	}
	epoch := res.Epoch
	followResign, followErr := res.FollowResign, res.FollowErr
	blessedResign, blessedErr := res.BlessedResign, res.BlessedErr
	for _, r := range []struct {
		file string
		out  signing.ResignOutcome
		err  error
	}{{"following.json", followResign, followErr}, {"blessed.json", blessedResign, blessedErr}} {
		fields := map[string]interface{}{"file": r.file, "outcome": string(r.out), "trigger": "rotate-key"}
		if r.err != nil {
			fields["error"] = r.err.Error()
		}
		logCLIAction("rotate_key.resign", fields)
	}

	didPublished, didRemoved := res.DIDPublished, res.DIDRemoved
	if didPublished {
		logCLIAction("did.published", map[string]interface{}{
			"did":     did.ID(domain),
			"trigger": "rotate-key",
		})
	} else {
		logCLIAction("did.publish_failed", map[string]interface{}{
			"host":    domain,
			"trigger": "rotate-key",
			"error":   res.DIDErr.Error(),
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
		printResign("following.json", followResign, followErr)
		printResign("blessed.json", blessedResign, blessedErr)
		fmt.Println()
		fmt.Println("[!] Deploy the updated .well-known/polis file, and any re-signed follow file or blessing list")
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
				"following_resign":  string(followResign),
				"blessed_resign":    string(blessedResign),
			},
		})
	}
}

// printResign reports what the rotation did with one signed list. Only the
// outcomes a person might act on are printed.
func printResign(file string, out signing.ResignOutcome, err error) {
	switch out {
	case signing.ResignDone:
		fmt.Printf("[i] Re-signed %s under the new key\n", file)
	case signing.ResignNotValid:
		fmt.Printf("[!] Left %s as it was: its signature did not verify under the old key, so it was not re-signed\n", file)
	case signing.ResignRefused:
		fmt.Printf("[!] Left %s as it was: %v\n", file, err)
	case signing.ResignAbsent, signing.ResignUnsigned:
		// Nothing to say: there was nothing to re-sign.
	default:
		if err != nil {
			fmt.Printf("[!] Could not re-sign %s: %v\n", file, err)
		}
	}
}
