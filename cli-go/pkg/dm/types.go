// Package dm implements encrypted direct messaging between polis instances.
//
// Messages are end-to-end encrypted with NaCl box (X25519 + XSalsa20-Poly1305) to a
// per-epoch messages key and stored as the wire box via the mailbox (see mailbox.go /
// FORMAT.md). The recipient's polis instance serves as the mailbox — senders POST
// encrypted messages to the recipient's /v1/content/dm/actions/deliver endpoint.
package dm

// MessageEnvelopeVersion is the current DM wire-envelope schema version. v2 is the hardened
// protocol: box_pub is authenticated against the sender's published messages key at receipt
// (DM-1) and the signed-request canonical binds the recipient domain + body digest (DM-2).
// v1 (box_pub unverified, signature bound only {action,domain,timestamp}) is retired — there
// was no live cross-site traffic when v2 landed, so no dual-accept window is carried.
const MessageEnvelopeVersion = 2

// MessageEnvelope is the wire format for DM delivery (cleartext metadata + encrypted payload).
type MessageEnvelope struct {
	Version          int    `json:"version"`
	SenderDomain     string `json:"sender_domain"`
	RecipientDomain  string `json:"recipient_domain"`
	EncryptedContent string `json:"encrypted_content"` // base64, NaCl box ciphertext
	Nonce            string `json:"nonce"`             // base64, 24 bytes
	Timestamp        string `json:"timestamp"`

	// Phase 2 (messages-key model — see PROTOCOL.md §2).
	BoxPub         string `json:"box_pub,omitempty"`         // base64 x25519: sender's messages pubkey for SenderEpoch — required to open the box (also the inner SenderPublicKey)
	SenderEpoch    int    `json:"sender_epoch,omitempty"`    // sender's epoch that BoxPub belongs to (advisory cross-check vs the sender's published block)
	RecipientEpoch int    `json:"recipient_epoch,omitempty"` // recipient epoch the sender sealed to; the receiver stamps it as the stored key_epoch (no server-side decrypt needed)
	ReplyTo        string `json:"reply_to,omitempty"`        // optional message id this replies to. Cleartext metadata (not in the box) so sent + received copies decrypt to identical bare content; the message body stays end-to-end encrypted
}
