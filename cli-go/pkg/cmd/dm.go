package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func handleDM(args []string) {
	if len(args) < 1 {
		printDMUsage()
		os.Exit(1)
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "list":
		handleDMList()
	case "read":
		if len(subArgs) < 1 {
			exitError("Usage: polis dm read <conversation_id>")
		}
		handleDMRead(subArgs[0])
	case "send":
		if len(subArgs) < 2 {
			exitError("Usage: polis dm send <recipient_url> <message>")
		}
		message := strings.Join(subArgs[1:], " ")
		handleDMSend(subArgs[0], message)
	case "retry":
		convID := ""
		if len(subArgs) > 0 {
			convID = subArgs[0]
		}
		handleDMRetry(convID)
	case "config":
		handleDMConfig()
	default:
		fmt.Fprintf(os.Stderr, "Unknown dm subcommand: %s\n", subcommand)
		printDMUsage()
		os.Exit(1)
	}
}

func printDMUsage() {
	fmt.Print(`Polis DM - Direct Messages

Usage:
  polis dm <subcommand> [options]

Subcommands:
  list                    List DM conversations
  read <conversation_id>  Read messages in a conversation
  send <url> <message>    Send a DM to a recipient
  retry [conversation_id] Retry delivering unsent messages
  config                  Show DM acceptance policy
`)
}

func loadDMStore() *dm.Store {
	dataDir := getDataDir()
	privPEM, err := os.ReadFile(dataDir + "/.polis/keys/id_ed25519")
	if err != nil {
		exitError("Cannot read private key: %v", err)
	}

	privKey, err := signing.ParsePrivateKey(privPEM)
	if err != nil {
		exitError("Invalid private key: %v", err)
	}

	store, err := dm.NewStore(dataDir, privKey.Seed())
	if err != nil {
		exitError("Cannot initialize DM store: %v", err)
	}
	return store
}

func handleDMList() {
	store := loadDMStore()

	idx, err := store.LoadIndex()
	if err != nil {
		exitError("Load conversations: %v", err)
	}

	if jsonOutput {
		convMaps := make([]map[string]interface{}, 0, len(idx.Conversations))
		for _, c := range idx.Conversations {
			convMaps = append(convMaps, map[string]interface{}{
				"id":              c.ID,
				"peer_domain":     c.PeerDomain,
				"last_message_at": c.LastMessageAt,
				"unread_count":    c.UnreadCount,
				"last_preview":    c.LastPreview,
			})
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "dm list",
			"data": map[string]interface{}{
				"conversations": convMaps,
				"count":         len(convMaps),
			},
		})
		return
	}

	if len(idx.Conversations) == 0 {
		fmt.Println("[i] No DM conversations")
		return
	}

	for _, c := range idx.Conversations {
		unread := ""
		if c.UnreadCount > 0 {
			unread = fmt.Sprintf(" (%d unread)", c.UnreadCount)
		}
		fmt.Printf("  %s  %s%s\n", c.ID, c.PeerDomain, unread)
		if c.LastPreview != "" {
			preview := c.LastPreview
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			fmt.Printf("           %s\n", preview)
		}
	}
}

func handleDMRead(convID string) {
	store := loadDMStore()

	conv, err := store.LoadConversation(convID)
	if err != nil {
		exitError("Load conversation: %v", err)
	}

	if jsonOutput {
		msgMaps := make([]map[string]interface{}, 0, len(conv.Messages))
		for _, msg := range conv.Messages {
			content, _ := store.DecryptMessage(&msg)
			m := map[string]interface{}{
				"id":        msg.ID,
				"from":      msg.From,
				"to":        msg.To,
				"content":   content,
				"timestamp": msg.Timestamp,
				"status":    msg.Status,
			}
			if msg.ReadAt != "" {
				m["read_at"] = msg.ReadAt
			}
			msgMaps = append(msgMaps, m)
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "dm read",
			"data": map[string]interface{}{
				"conversation_id": convID,
				"peer_domain":     conv.PeerDomain,
				"messages":        msgMaps,
			},
		})
		return
	}

	fmt.Printf("Conversation with %s\n\n", conv.PeerDomain)
	for _, msg := range conv.Messages {
		content, err := store.DecryptMessage(&msg)
		if err != nil {
			content = "[decryption error]"
		}
		status := ""
		if msg.Status == "unsent" {
			status = " [unsent]"
		}
		fmt.Printf("  [%s] %s%s\n", msg.Timestamp[:16], msg.From, status)
		fmt.Printf("    %s\n\n", content)
	}

	// Mark as read
	store.MarkRead(convID)
}

func handleDMSend(recipientURL, content string) {
	dataDir := getDataDir()
	privPEM, err := os.ReadFile(dataDir + "/.polis/keys/id_ed25519")
	if err != nil {
		exitError("Cannot read private key: %v", err)
	}
	pubSSH, err := os.ReadFile(dataDir + "/.polis/keys/id_ed25519.pub")
	if err != nil {
		exitError("Cannot read public key: %v", err)
	}

	store := loadDMStore()

	domain := baseURL
	if domain == "" {
		exitError("POLIS_BASE_URL must be set to send DMs")
	}

	sender := dm.NewSender(privPEM, pubSSH, dm.ExtractDomainFromURL(domain), store)
	msg, err := sender.SendMessage(recipientURL, content, "")

	if jsonOutput {
		data := map[string]interface{}{
			"status":  "success",
			"command": "dm send",
		}
		if msg != nil {
			data["data"] = map[string]interface{}{
				"message_id": msg.ID,
				"status":     msg.Status,
			}
		}
		if err != nil {
			data["data"].(map[string]interface{})["error"] = err.Error()
		}
		outputJSON(data)
		return
	}

	if err != nil {
		if msg != nil {
			fmt.Printf("[!] Message saved but delivery failed: %v\n", err)
			fmt.Printf("    Use 'polis dm retry' to redeliver\n")
		} else {
			exitError("Send failed: %v", err)
		}
		return
	}

	fmt.Printf("[✓] Message sent to %s\n", recipientURL)
}

func handleDMRetry(convID string) {
	store := loadDMStore()

	unsent, err := store.GetUnsentMessages()
	if err != nil {
		exitError("Get unsent messages: %v", err)
	}

	if convID != "" {
		// Filter to specific conversation
		var filtered []dm.UnsentMessage
		for _, u := range unsent {
			if u.ConvID == convID {
				filtered = append(filtered, u)
			}
		}
		unsent = filtered
	}

	if jsonOutput {
		unsentMaps := make([]map[string]interface{}, 0, len(unsent))
		for _, u := range unsent {
			unsentMaps = append(unsentMaps, map[string]interface{}{
				"conversation_id": u.ConvID,
				"message_id":      u.Message.ID,
				"to":              u.Message.To,
				"timestamp":       u.Message.Timestamp,
			})
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "dm retry",
			"data": map[string]interface{}{
				"unsent_count": len(unsent),
				"unsent":       unsentMaps,
			},
		})
		return
	}

	if len(unsent) == 0 {
		fmt.Println("[i] No unsent messages")
		return
	}

	fmt.Printf("[i] %d unsent message(s):\n", len(unsent))
	for _, u := range unsent {
		fmt.Printf("  %s → %s (%s)\n", u.Message.ID, u.Message.To, u.Message.Timestamp[:16])
	}
}

func handleDMConfig() {
	dataDir := getDataDir()
	privatePath, publicPath := policy.DefaultPaths(dataDir)

	// Load all policies and filter to DM-related ones
	allPolicies, err := policy.LoadPolicies(privatePath, publicPath)
	if err != nil {
		exitError("Load policies: %v", err)
	}

	// Separate private vs public DM rules
	privateDM := filterDMPolicies(privatePath)
	publicDM := filterDMPolicies(publicPath)

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "dm config",
			"data": map[string]interface{}{
				"private_rules": policySliceToMaps(privateDM),
				"public_rules":  policySliceToMaps(publicDM),
				"total_rules":   len(allPolicies),
			},
		})
		return
	}

	fmt.Println("DM Acceptance Policy")
	fmt.Println()

	if len(privateDM) > 0 {
		fmt.Printf("Private rules (.polis/policies/rules.jsonl):\n")
		for _, p := range privateDM {
			active := "  "
			if !p.Active {
				active = "# "
			}
			fmt.Printf("  %s%s\n", active, p.Rule)
		}
	} else {
		fmt.Println("Private rules: (none)")
	}

	fmt.Println()
	if len(publicDM) > 0 {
		fmt.Printf("Public rules (policies/rules.jsonl):\n")
		for _, p := range publicDM {
			active := "  "
			if !p.Active {
				active = "# "
			}
			fmt.Printf("  %s%s\n", active, p.Rule)
		}
	} else {
		fmt.Println("Public rules: (none)")
	}

	if len(privateDM) == 0 && len(publicDM) == 0 {
		fmt.Println()
		fmt.Println("[i] No DM policies configured. DMs are allowed from everyone by default.")
		fmt.Println("    Add rules to .polis/policies/rules.jsonl, e.g.:")
		fmt.Println(`    {"active":true,"policy":"allow pub.polis.dm from following"}`)
		fmt.Println(`    {"active":true,"policy":"deny pub.polis.dm from all"}`)
	}
}

// filterDMPolicies loads policies from a single file and returns only DM-related ones.
func filterDMPolicies(path string) []policy.Policy {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	var dmPolicies []policy.Policy
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var p policy.Policy
		if err := json.Unmarshal(line, &p); err != nil {
			continue
		}
		if strings.Contains(p.Rule, "pub.polis.dm") {
			dmPolicies = append(dmPolicies, p)
		}
	}
	return dmPolicies
}

func policySliceToMaps(policies []policy.Policy) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(policies))
	for _, p := range policies {
		result = append(result, map[string]interface{}{
			"active": p.Active,
			"policy": p.Rule,
		})
	}
	return result
}
