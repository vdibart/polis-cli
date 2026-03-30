package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

func handleRotateKey(args []string) {
	fs := flag.NewFlagSet("rotate-key", flag.ExitOnError)
	deleteOldKey := fs.Bool("delete-old-key", false, "Delete the old key after rotation")
	fs.Parse(args)

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	keysDir := filepath.Join(dir, ".polis", "keys")
	privateKeyPath := filepath.Join(keysDir, "id_ed25519")
	publicKeyPath := filepath.Join(keysDir, "id_ed25519.pub")
	oldPrivateKeyPath := filepath.Join(keysDir, "id_ed25519.old")
	oldPublicKeyPath := filepath.Join(keysDir, "id_ed25519.pub.old")

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

	if discovery.IsRegisteredLocally(dir, dsURL) {
		dsClient := discovery.NewClient(dsURL, dsKey)
		if !jsonOutput {
			fmt.Println("[i] Notifying discovery service of key rotation...")
		}

		err = dsClient.RotateKey(discovery.KeyRotationRequest{
			Domain:        domain,
			OldKey:        oldPubKey,
			NewKey:        newPubKey,
			TransitionSig: transitionSig,
			Timestamp:     timestamp,
		})
		if err != nil {
			exitError("Discovery service rejected key rotation: %v", err)
		}

		if !jsonOutput {
			fmt.Println("[✓] Discovery service acknowledged key rotation")
		}
	} else if !jsonOutput {
		fmt.Println("[i] DS notification skipped: site not registered with discovery service")
	}

	// Backup old keys
	if *deleteOldKey {
		if !jsonOutput {
			fmt.Println("[i] Old key will be deleted (--delete-old-key)")
		}
	} else {
		if err := os.Rename(privateKeyPath, oldPrivateKeyPath); err != nil {
			exitError("Failed to backup old private key: %v", err)
		}
		if err := os.Rename(publicKeyPath, oldPublicKeyPath); err != nil {
			exitError("Failed to backup old public key: %v", err)
		}
		if !jsonOutput {
			fmt.Printf("[i] Old keys backed up to %s.old\n", privateKeyPath)
		}
	}

	// Write new keys
	if err := os.WriteFile(privateKeyPath, privPEM, 0600); err != nil {
		exitError("Failed to write new private key: %v", err)
	}
	if err := os.WriteFile(publicKeyPath, pubSSH, 0644); err != nil {
		exitError("Failed to write new public key: %v", err)
	}

	// Update .well-known/polis with new public key
	wellKnownPath := filepath.Join(dir, ".well-known", "polis")
	wkData, err := os.ReadFile(wellKnownPath)
	if err != nil {
		exitError("Failed to read .well-known/polis: %v", err)
	}

	var wkJSON map[string]interface{}
	if err := json.Unmarshal(wkData, &wkJSON); err != nil {
		exitError("Failed to parse .well-known/polis: %v", err)
	}
	wkJSON["public_key"] = newPubKey
	updatedWK, err := json.MarshalIndent(wkJSON, "", "  ")
	if err != nil {
		exitError("Failed to marshal .well-known/polis: %v", err)
	}
	if err := os.WriteFile(wellKnownPath, append(updatedWK, '\n'), 0644); err != nil {
		exitError("Failed to write .well-known/polis: %v", err)
	}

	if !jsonOutput {
		fmt.Println()
		fmt.Println("[✓] Key rotation complete!")
		fmt.Println()
		fmt.Println("[i] New public key:")
		fmt.Printf("  %s\n", newPubKey)
		fmt.Println()
		fmt.Println("[i] Updated .well-known/polis with new public key")
		fmt.Println("[i] Old content remains verifiable via key history")
		fmt.Println()
		fmt.Println("[!] Deploy the updated .well-known/polis file")
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "rotate-key",
			"data": map[string]interface{}{
				"new_public_key":    newPubKey,
				"old_key_backed_up": !*deleteOldKey,
				"old_key_path":      oldPrivateKeyPath,
				"ds_rotation":       "success",
			},
		})
	}
}
