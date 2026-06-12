package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"golang.org/x/term"
)

// handleDMPublishKey re-publishes the site's DM messages key (the keyring's
// current epoch, identity-signed) into .well-known/polis. Idempotent operator
// repair for a tenant whose published public_key_messages was lost — e.g. a
// pre-fix profile edit that did a lossy typed SaveWellKnown. Uses the
// field-preserving raw write, so other well-known fields (author_name, avatar,
// custom strings) are untouched. No-op-safe to re-run.
func handleDMPublishKey() {
	dir := getDataDir()
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("cannot read identity private key: %v", err)
	}
	if err := site.PublishMessagesKey(dir, privKey); err != nil {
		exitError("publish messages key failed: %v", err)
	}
	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "dm publish-key",
			"data":    map[string]interface{}{"data_dir": dir},
		})
		return
	}
	fmt.Println("[✓] Published DM messages key (current epoch) into .well-known/polis")
}

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
	case "decrypt":
		handleDMDecrypt(subArgs)
	case "publish-key":
		handleDMPublishKey()
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
  decrypt [options]       Decrypt and print your messages from a local/exported site
  publish-key             Re-publish your DM messages key into .well-known/polis (repair)

Options for 'decrypt':
  --phrase                Unlock with your recovery phrase instead of your password
  --conversation <id>     Limit output to one conversation
  --json                  Machine-readable output

  Bootstrap-epoch messages decrypt with no prompt (their key is in the export);
  password epochs prompt for the password (or the recovery phrase with --phrase).
  Neither secret is ever stored or transmitted — decryption happens locally.
`)
}

// loadDMMailbox returns the tenant mailbox, the site's own domain (for convID mapping),
// and the epoch DEKs available without a password (bootstrap server_dek).
func loadDMMailbox() (*dm.Mailbox, string, map[int][32]byte) {
	dataDir := getDataDir()
	deks, err := dm.LoadAvailableDEKs(dataDir)
	if err != nil {
		exitError("Cannot load DM keys: %v", err)
	}
	self := dm.ExtractDomainFromURL(baseURL)
	return dm.NewMailbox(dm.DMDir(dataDir)), self, deks
}

func handleDMList() {
	mb, self, _ := loadDMMailbox()

	entries, err := mb.RebuildInbox()
	if err != nil {
		exitError("Load conversations: %v", err)
	}

	if jsonOutput {
		convMaps := make([]map[string]interface{}, 0, len(entries))
		for _, e := range entries {
			convMaps = append(convMaps, map[string]interface{}{
				"id":              dm.ComputeConversationID(self, e.Peer),
				"peer_domain":     e.Peer,
				"last_message_at": e.LastMessageAt,
				"unread_count":    e.Unread,
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

	if len(entries) == 0 {
		fmt.Println("[i] No DM conversations")
		return
	}

	for _, e := range entries {
		unread := ""
		if e.Unread > 0 {
			unread = fmt.Sprintf(" (%d unread)", e.Unread)
		}
		fmt.Printf("  %s  %s%s\n", dm.ComputeConversationID(self, e.Peer), e.Peer, unread)
	}
}

// handleDMDecrypt reads a local or exported .polis site and prints DM plaintext.
// It is the offline/durability read path documented in cli-go/pkg/dm/FORMAT.md:
// bootstrap-epoch messages open with the server-held key carried in the export (no
// prompt); password epochs are unwrapped from the user's password (or recovery
// phrase, with --phrase). Neither secret is stored or transmitted — all crypto
// runs locally with golang.org/x/crypto (Argon2id + NaCl), byte-for-byte the same
// wrap the browser produced. The DataDir is the (possibly read-only) export dir.
func handleDMDecrypt(args []string) {
	usePhrase := false
	convFilter := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--phrase", "--recovery":
			usePhrase = true
		case "--conversation", "-c":
			if i+1 >= len(args) {
				exitError("--conversation needs a conversation id")
			}
			convFilter = args[i+1]
			i++
		default:
			exitError("Unknown option for 'dm decrypt': %s", args[i])
		}
	}

	dataDir := getDataDir()
	dmDir := dm.DMDir(dataDir)
	kr, err := dm.LoadKeyring(dmDir)
	if err != nil {
		if os.IsNotExist(err) {
			exitError("No DM keyring at %s — is this an unzipped polis site export?", dmDir)
		}
		exitError("Load keyring: %v", err)
	}

	// Bootstrap (server-held) DEKs need no prompt; collect them first.
	deks, err := dm.LoadAvailableDEKs(dataDir)
	if err != nil {
		exitError("Load available keys: %v", err)
	}

	// Unlock every password epoch from the supplied secret.
	var pwEpochs []int
	for _, e := range kr.Epochs {
		if e.Kind == dm.EpochKindPassword {
			pwEpochs = append(pwEpochs, e.ID)
		}
	}
	if len(pwEpochs) > 0 {
		secret := promptDMSecret(usePhrase)
		if secret == "" {
			exitError("No %s entered.", secretLabel(usePhrase))
		}
		for _, id := range pwEpochs {
			var d []byte
			if usePhrase {
				d, err = kr.UnlockEpochWithPhrase(id, secret)
			} else {
				d, err = kr.UnlockEpochWithPassword(id, []byte(secret))
			}
			if err != nil {
				exitError("Could not unlock epoch %d — wrong %s? (%v)", id, secretLabel(usePhrase), err)
			}
			var dek [32]byte
			copy(dek[:], d)
			deks[id] = dek
		}
	}

	mb := dm.NewMailbox(dmDir)
	self := dm.ExtractDomainFromURL(baseURL)
	entries, err := mb.RebuildInbox()
	if err != nil {
		exitError("List conversations: %v", err)
	}

	type outMsg struct {
		ID       string `json:"id"`
		From     string `json:"from"`
		To       string `json:"to"`
		Content  string `json:"content"`
		At       string `json:"timestamp"`
		KeyEpoch int    `json:"key_epoch"`
		Locked   bool   `json:"locked"`
	}
	type outConv struct {
		ID       string   `json:"id"`
		Peer     string   `json:"peer_domain"`
		Messages []outMsg `json:"messages"`
	}

	var convs []outConv
	for _, e := range entries {
		id := dm.ComputeConversationID(self, e.Peer)
		if convFilter != "" && convFilter != id {
			continue
		}
		msgs, err := mb.ReadConversation(e.Peer, deks)
		if err != nil {
			exitError("Read conversation with %s: %v", e.Peer, err)
		}
		oc := outConv{ID: id, Peer: e.Peer}
		for _, m := range msgs {
			oc.Messages = append(oc.Messages, outMsg{
				ID: m.ID, From: m.From, To: m.To, Content: m.Plaintext,
				At: m.At, KeyEpoch: m.KeyEpoch, Locked: m.Locked,
			})
		}
		convs = append(convs, oc)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "dm decrypt",
			"data":    map[string]interface{}{"conversations": convs, "count": len(convs)},
		})
		return
	}

	if len(convs) == 0 {
		fmt.Println("[i] No conversations to decrypt.")
		return
	}
	for _, c := range convs {
		fmt.Printf("=== %s (%s) ===\n", c.Peer, c.ID)
		for _, m := range c.Messages {
			content := m.Content
			if m.Locked {
				content = "[locked — this message is sealed under a different key/epoch]"
			}
			ts := m.At
			if len(ts) >= 16 {
				ts = ts[:16]
			}
			fmt.Printf("  [%s] %s\n    %s\n", ts, m.From, content)
		}
		fmt.Println()
	}
}

func secretLabel(phrase bool) string {
	if phrase {
		return "recovery phrase"
	}
	return "password"
}

// promptDMSecret reads the password / recovery phrase. On a terminal it reads
// without echo; when stdin is piped it reads a single line (scripting). The secret
// is held only for the unwrap and never written anywhere.
func promptDMSecret(phrase bool) string {
	label := secretLabel(phrase)
	fmt.Fprintf(os.Stderr, "Enter your message %s: ", label)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			exitError("Read %s: %v", label, err)
		}
		return strings.TrimSpace(string(b))
	}
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text())
	}
	return ""
}

func handleDMRead(convID string) {
	mb, self, deks := loadDMMailbox()

	peer, ok, err := mb.PeerForConversationID(self, convID)
	if err != nil {
		exitError("Resolve conversation: %v", err)
	}
	if !ok {
		exitError("Conversation not found: %s", convID)
	}
	msgs, err := mb.ReadConversation(peer, deks)
	if err != nil {
		exitError("Load conversation: %v", err)
	}

	if jsonOutput {
		msgMaps := make([]map[string]interface{}, 0, len(msgs))
		for _, msg := range msgs {
			msgMaps = append(msgMaps, map[string]interface{}{
				"id":        msg.ID,
				"from":      msg.From,
				"to":        msg.To,
				"content":   msg.Plaintext,
				"timestamp": msg.At,
				"status":    msg.Status,
				"key_epoch": msg.KeyEpoch,
				"locked":    msg.Locked,
			})
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "dm read",
			"data": map[string]interface{}{
				"conversation_id": convID,
				"peer_domain":     peer,
				"messages":        msgMaps,
			},
		})
		return
	}

	fmt.Printf("Conversation with %s\n\n", peer)
	for _, msg := range msgs {
		content := msg.Plaintext
		if msg.Locked {
			content = "[locked — unlock with your password to read]"
		}
		status := ""
		if msg.Status == "unsent" {
			status = " [unsent]"
		}
		fmt.Printf("  [%s] %s%s\n", msg.At[:16], msg.From, status)
		fmt.Printf("    %s\n\n", content)
	}

	// Mark as read
	mb.MarkRead(peer)
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

	domain := baseURL
	if domain == "" {
		exitError("POLIS_BASE_URL must be set to send DMs")
	}

	sender := dm.NewSender(privPEM, pubSSH, dm.ExtractDomainFromURL(domain), dataDir)
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
	mb, self, _ := loadDMMailbox()

	unsent, err := mb.UnsentMessages()
	if err != nil {
		exitError("Get unsent messages: %v", err)
	}

	if convID != "" {
		// Filter to specific conversation
		var filtered []dm.UnsentMailboxMessage
		for _, u := range unsent {
			if dm.ComputeConversationID(self, u.Peer) == convID {
				filtered = append(filtered, u)
			}
		}
		unsent = filtered
	}

	if jsonOutput {
		unsentMaps := make([]map[string]interface{}, 0, len(unsent))
		for _, u := range unsent {
			unsentMaps = append(unsentMaps, map[string]interface{}{
				"conversation_id": dm.ComputeConversationID(self, u.Peer),
				"message_id":      u.Message.ID,
				"to":              u.Message.To,
				"timestamp":       u.Message.At,
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
		fmt.Printf("  %s → %s (%s)\n", u.Message.ID, u.Message.To, u.Message.At[:16])
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
