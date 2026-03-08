// Package dm implements encrypted direct messaging between polis instances.
//
// Messages are encrypted in transit using NaCl box (X25519 + XSalsa20-Poly1305)
// and encrypted at rest using NaCl secretbox with an HKDF-derived symmetric key.
// The recipient's polis instance serves as the mailbox — senders POST encrypted
// messages to the recipient's /v1/content/dm/actions/deliver endpoint.
package dm

// Version is set at build time via -ldflags.
var Version = "dev"

// GetGenerator returns the generator string for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// Message represents a single DM, stored on disk with encrypted content.
type Message struct {
	ID               string `json:"id"`
	From             string `json:"from"`
	To               string `json:"to"`
	EncryptedContent string `json:"encrypted_content"` // base64, secretbox-encrypted
	StorageNonce     string `json:"storage_nonce"`      // base64, 24 bytes
	ReplyToID        string `json:"reply_to_id,omitempty"`
	Timestamp        string `json:"timestamp"`
	ReadAt           string `json:"read_at,omitempty"`
	Status           string `json:"status"` // "sent", "unsent", "received"
}

// ConversationSummary is a lightweight entry in the conversations index.
type ConversationSummary struct {
	ID            string `json:"id"`
	PeerDomain    string `json:"peer_domain"`
	PeerURL       string `json:"peer_url"`
	LastMessageAt string `json:"last_message_at"`
	UnreadCount   int    `json:"unread_count"`
	LastPreview   string `json:"last_preview"` // truncated plaintext for listing
}

// Conversation contains the full message history with a peer.
type Conversation struct {
	PeerDomain string    `json:"peer_domain"`
	PeerURL    string    `json:"peer_url"`
	Messages   []Message `json:"messages"`
}

// ConversationIndex is the top-level conversations list file.
type ConversationIndex struct {
	Conversations []ConversationSummary `json:"conversations"`
}

// MessageEnvelope is the wire format for DM delivery (cleartext metadata + encrypted payload).
type MessageEnvelope struct {
	Version          int    `json:"version"`
	SenderDomain     string `json:"sender_domain"`
	RecipientDomain  string `json:"recipient_domain"`
	EncryptedContent string `json:"encrypted_content"` // base64, NaCl box ciphertext
	Nonce            string `json:"nonce"`              // base64, 24 bytes
	Timestamp        string `json:"timestamp"`
}

// InnerPayload is the decrypted content inside a MessageEnvelope.
type InnerPayload struct {
	Content        string `json:"content"`
	ReplyToID      string `json:"reply_to_id,omitempty"`
	SenderPublicKey string `json:"sender_public_key"`
}
