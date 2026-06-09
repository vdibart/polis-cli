package dm

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
)

// Per-dmDir mutation lock (DM-14): the mailbox does load→mutate→save on messages.jsonl,
// conversation.json, and inbox.json with no in-file locking. A Mailbox is created fresh per
// request (NewMailbox(DMDir(...))), so the lock can't live on the struct — it must be keyed
// by dmDir at package scope. In hosted mode the remote `deliver` path and the local `send`
// path run concurrently; without this, two simultaneous appends (or an append racing the
// dedup scan, DM-3) can drop a line or double-store. Mirrors the package-level serialization
// used for the reply-context cache (R19-1) and blessed.json (R22-1). Owned by R23-4.
var (
	mailboxLocksMu sync.Mutex
	mailboxLocks   = map[string]*sync.Mutex{}
)

func lockDMDir(dmDir string) func() {
	mailboxLocksMu.Lock()
	mu, ok := mailboxLocks[dmDir]
	if !ok {
		mu = &sync.Mutex{}
		mailboxLocks[dmDir] = mu
	}
	mailboxLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// Mailbox is the new DM storage layer: per-conversation directories under the tenant
// DM dir, holding append-only message logs + a metadata record, plus a derived inbox
// index. It is crypto-agnostic about epoch *kind* — callers supply per-epoch DEKs; each
// stored message carries the counterparty public key needed to box-open it, so the
// store is self-contained for offline decryption (export / `polis dm decrypt`).
//
//	dm/
//	  inbox.json                          (derived, rebuildable)
//	  conversations/<peer-handle>/        (handle lowercased)
//	    messages.jsonl                    (append-only)
//	    conversation.json                 (metadata + read cursor)
type Mailbox struct{ dmDir string }

// NewMailbox returns a Mailbox rooted at a tenant DM dir
// (.polis/bundles/pub.polis.core/dm).
func NewMailbox(dmDir string) *Mailbox { return &Mailbox{dmDir: dmDir} }

const (
	conversationsSubdir = "conversations"
	messagesFile        = "messages.jsonl"
	conversationFile    = "conversation.json"
	inboxFile           = "inbox.json"

	DirIn  = "in"
	DirOut = "out"
	selfID = "me"
)

// MailboxMessage is one line of messages.jsonl. No plaintext, no preview — only the
// box ciphertext + the data needed to open it given the epoch DEK.
type MailboxMessage struct {
	ID         string `json:"id"`
	KeyEpoch   int    `json:"key_epoch"`
	Dir        string `json:"dir"` // DirIn | DirOut
	From       string `json:"from"`
	To         string `json:"to"`
	At         string `json:"at"`         // RFC3339 (UTC)
	Nonce      string `json:"nonce"`      // base64, 24 bytes
	Ciphertext string `json:"ciphertext"` // base64, NaCl box output
	BoxPub     string `json:"box_pub"`    // base64 X25519 counterparty pubkey for box-open
	ReplyTo    string `json:"reply_to,omitempty"`
	Status     string `json:"status,omitempty"`
}

// ConversationMeta is conversation.json: thread metadata + the read cursor. Mutable
// in place; kept separate from the append-only message log.
type ConversationMeta struct {
	Peer           string `json:"peer"`
	ConversationID string `json:"conversation_id"`
	CreatedAt      string `json:"created_at"`
	LastMessageAt  string `json:"last_message_at"`
	ReadAt         string `json:"read_at"`
	MessageCount   int    `json:"message_count"`
}

// InboxEntry is one row of inbox.json — metadata only (no preview; the server can't
// decrypt password epochs).
type InboxEntry struct {
	Peer           string `json:"peer"`
	ConversationID string `json:"conversation_id"`
	LastMessageAt  string `json:"last_message_at"`
	Unread         int    `json:"unread"`
}

// DecryptedMessage is a MailboxMessage with its opened plaintext, or Locked=true when
// the epoch's DEK was not available this session, or Undecryptable=true when the DEK WAS
// available but the box failed to open (corruption, or — pre-DM-1 — an injected garbage
// box). Undecryptable is per-message: one bad box must not fail the whole conversation
// read (DM-6), which would be a DoS on the thread.
type DecryptedMessage struct {
	MailboxMessage
	Plaintext     string
	Locked        bool
	Undecryptable bool
}

func (m *Mailbox) convDir(peer string) string {
	return filepath.Join(m.dmDir, conversationsSubdir, strings.ToLower(peer))
}

// AppendSent seals plaintext to the sender's *own* epoch key (box to self) and appends
// it to the conversation as an outgoing message — so the sender can re-read their own
// side later, and the operator cannot. myDomain labels the "to" side.
func (m *Mailbox) AppendSent(peer, myDomain, plaintext string, keyEpoch int, myEpochPub, myEpochDEK [32]byte, replyTo, status string) (*MailboxMessage, error) {
	ct, nonce, err := Encrypt([]byte(plaintext), &myEpochPub, &myEpochDEK)
	if err != nil {
		return nil, fmt.Errorf("seal sent message: %w", err)
	}
	if status == "" {
		status = "sent"
	}
	msg := MailboxMessage{
		ID:         MessageIDFromNonce(nonce),
		KeyEpoch:   keyEpoch,
		Dir:        DirOut,
		From:       selfID,
		To:         strings.ToLower(peer),
		At:         nowRFC3339(),
		Nonce:      base64.StdEncoding.EncodeToString(nonce[:]),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
		BoxPub:     base64.StdEncoding.EncodeToString(myEpochPub[:]),
		ReplyTo:    replyTo,
		Status:     status,
	}
	if err := m.appendMessage(peer, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// AppendSentRaw stores a PRE-SEALED outgoing box (sealed in the browser for a
// password epoch — phase 4.6) without re-sealing. It mirrors AppendReceived for
// the outbound side: the server holds no password-epoch DEK, so the SPA seals
// the sender's box-to-self copy and hands it here as base64 ciphertext/nonce +
// the sender's epoch pubkey (boxPub). On read the sender opens it with their own
// epoch DEK, exactly like a server-sealed AppendSent copy.
func (m *Mailbox) AppendSentRaw(peer string, keyEpoch int, ciphertextB64, nonceB64, boxPubB64, replyTo, status string) (*MailboxMessage, error) {
	nonceRaw, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil || len(nonceRaw) != 24 {
		return nil, fmt.Errorf("sent box: bad nonce (want 24 bytes base64)")
	}
	if _, err := base64.StdEncoding.DecodeString(ciphertextB64); err != nil {
		return nil, fmt.Errorf("sent box: bad ciphertext base64")
	}
	if pub, err := base64.StdEncoding.DecodeString(boxPubB64); err != nil || len(pub) != 32 {
		return nil, fmt.Errorf("sent box: bad box_pub (want 32 bytes base64)")
	}
	var nonce [24]byte
	copy(nonce[:], nonceRaw)
	if status == "" {
		status = "sent"
	}
	msg := MailboxMessage{
		ID:         MessageIDFromNonce(nonce),
		KeyEpoch:   keyEpoch,
		Dir:        DirOut,
		From:       selfID,
		To:         strings.ToLower(peer),
		At:         nowRFC3339(),
		Nonce:      nonceB64,
		Ciphertext: ciphertextB64,
		BoxPub:     boxPubB64,
		ReplyTo:    replyTo,
		Status:     status,
	}
	if err := m.appendMessage(peer, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// AppendReceived stores an incoming message as the sender's wire box ciphertext, as
// received — the server has no key to open it. senderPub is the sender's messages
// public key (needed to box-open with the recipient's epoch DEK on read).
func (m *Mailbox) AppendReceived(peer, fromDomain string, keyEpoch int, wireCiphertext []byte, nonce [24]byte, senderPub [32]byte, replyTo string) (*MailboxMessage, error) {
	msg := MailboxMessage{
		ID:         MessageIDFromNonce(nonce),
		KeyEpoch:   keyEpoch,
		Dir:        DirIn,
		From:       strings.ToLower(fromDomain),
		To:         selfID,
		At:         nowRFC3339(),
		Nonce:      base64.StdEncoding.EncodeToString(nonce[:]),
		Ciphertext: base64.StdEncoding.EncodeToString(wireCiphertext),
		BoxPub:     base64.StdEncoding.EncodeToString(senderPub[:]),
		ReplyTo:    replyTo,
		Status:     "received",
	}
	if err := m.appendMessage(peer, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// writeMessages atomically overwrites a conversation's messages.jsonl with the given
// messages (used by the re-seal-forward path, which rewrites rather than appends).
func (m *Mailbox) writeMessages(peer string, msgs []MailboxMessage) error {
	dir := m.convDir(peer)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create conversation dir: %w", err)
	}
	var out []byte
	for i := range msgs {
		line, err := json.Marshal(&msgs[i])
		if err != nil {
			return fmt.Errorf("marshal message: %w", err)
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return atomicfile.WriteFile(filepath.Join(dir, messagesFile), out, 0o600)
}

// ReencryptBootstrapForward re-seals every stored bootstrap-epoch (epoch-0) message
// FORWARD to the current password epoch, so that once the server-held bootstrap DEK is
// cleared the messages are still readable — by the user, under their password — and no
// longer readable by the operator. The server can do this because at set-password time it
// still holds the bootstrap DEK (`bootstrapDEK`): it opens each epoch-0 box, then re-seals
// the plaintext to the new epoch's public key (`currentEpochPub`) under a fresh ephemeral
// sender key (the server does NOT hold the password-epoch DEK, and does not need it to seal
// TO that epoch's pubkey). Each message keeps its original ID; only the box (ciphertext,
// nonce, box_pub) and `key_epoch` change. Returns the number of messages re-sealed.
//
// This must run BEFORE ClearBootstrapServerDEK; it is the "re-encrypt bootstrap messages
// forward" step of the set-password upgrade. It never writes plaintext to disk.
func (m *Mailbox) ReencryptBootstrapForward(bootstrapEpoch int, bootstrapDEK [32]byte, currentEpoch int, currentEpochPub [32]byte) (int, error) {
	unlock := lockDMDir(m.dmDir) // DM-14: atomic load→re-seal→rewrite across the corpus
	defer unlock()
	base := filepath.Join(m.dmDir, conversationsSubdir)
	dirs, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("list conversations: %w", err)
	}
	total := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		peer := d.Name()
		msgs, err := m.LoadMessages(peer)
		if err != nil {
			return total, fmt.Errorf("load %s: %w", peer, err)
		}
		changed := false
		for i := range msgs {
			if msgs[i].KeyEpoch != bootstrapEpoch {
				continue
			}
			ct, err := base64.StdEncoding.DecodeString(msgs[i].Ciphertext)
			if err != nil {
				return total, fmt.Errorf("decode ciphertext %s: %w", msgs[i].ID, err)
			}
			nb, err := base64.StdEncoding.DecodeString(msgs[i].Nonce)
			if err != nil || len(nb) != 24 {
				return total, fmt.Errorf("bad nonce %s", msgs[i].ID)
			}
			pb, err := base64.StdEncoding.DecodeString(msgs[i].BoxPub)
			if err != nil || len(pb) != 32 {
				return total, fmt.Errorf("bad box_pub %s", msgs[i].ID)
			}
			var nonce [24]byte
			copy(nonce[:], nb)
			var storedPub [32]byte
			copy(storedPub[:], pb)

			// Open with the bootstrap DEK (the server still holds it here).
			plaintext, err := Decrypt(ct, &nonce, &storedPub, &bootstrapDEK)
			if err != nil {
				return total, fmt.Errorf("open bootstrap message %s: %w", msgs[i].ID, err)
			}

			// Re-seal forward to the new epoch's pubkey under a fresh ephemeral sender key.
			ephPub, ephSK, err := NewEpochKeypair()
			if err != nil {
				return total, err
			}
			newCT, newNonce, err := Encrypt(plaintext, &currentEpochPub, &ephSK)
			if err != nil {
				return total, fmt.Errorf("re-seal message %s: %w", msgs[i].ID, err)
			}
			msgs[i].KeyEpoch = currentEpoch
			msgs[i].Ciphertext = base64.StdEncoding.EncodeToString(newCT)
			msgs[i].Nonce = base64.StdEncoding.EncodeToString(newNonce[:])
			msgs[i].BoxPub = base64.StdEncoding.EncodeToString(ephPub[:])
			changed = true
			total++
		}
		if changed {
			if err := m.writeMessages(peer, msgs); err != nil {
				return total, fmt.Errorf("rewrite %s: %w", peer, err)
			}
		}
	}
	return total, nil
}

func (m *Mailbox) appendMessage(peer string, msg *MailboxMessage) error {
	// DM-14: serialize the dedup-scan → append → touchMeta sequence per tenant so a
	// concurrent same-ID delivery (DM-3) or a parallel append can't drop/double a line.
	unlock := lockDMDir(m.dmDir)
	defer unlock()

	dir := m.convDir(peer)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create conversation dir: %w", err)
	}
	// Replay dedup (DM-3): a message ID is sha256(transport nonce), so a replayed wire
	// envelope reuses the nonce → same ID. Skip the append if this ID is already stored —
	// no second line, no meta touch, no unread bump. Restores the idempotency that lived in
	// the pre-refactor Store.AppendMessage (R20-C-F7), lost in the Store→Mailbox refactor
	// (e536f6ea). Pairs with DM-1/DM-2 (which block the *forged* replay; this blocks the
	// *duplicate* replay).
	existing, err := m.LoadMessages(peer)
	if err != nil {
		return fmt.Errorf("load existing messages for dedup: %w", err)
	}
	for i := range existing {
		if existing[i].ID == msg.ID {
			return nil
		}
	}
	line, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, messagesFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open messages.jsonl: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append message: %w", err)
	}
	return m.touchMeta(peer, msg.At)
}

// touchMeta updates (or creates) conversation.json after an append: bumps
// last_message_at + message_count.
func (m *Mailbox) touchMeta(peer, at string) error {
	meta, err := m.LoadMeta(peer)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		meta = &ConversationMeta{Peer: strings.ToLower(peer), CreatedAt: at}
	}
	meta.LastMessageAt = at
	meta.MessageCount++
	return m.saveMeta(peer, meta)
}

// LoadMeta reads a conversation's conversation.json. Missing → os.IsNotExist.
func (m *Mailbox) LoadMeta(peer string) (*ConversationMeta, error) {
	data, err := os.ReadFile(filepath.Join(m.convDir(peer), conversationFile))
	if err != nil {
		return nil, err
	}
	var meta ConversationMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse conversation.json: %w", err)
	}
	return &meta, nil
}

func (m *Mailbox) saveMeta(peer string, meta *ConversationMeta) error {
	dir := m.convDir(peer)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal conversation.json: %w", err)
	}
	return atomicfile.WriteFile(filepath.Join(dir, conversationFile), data, 0o600)
}

// LoadMessages reads the raw (still-encrypted) message lines for a conversation.
func (m *Mailbox) LoadMessages(peer string) ([]MailboxMessage, error) {
	f, err := os.Open(filepath.Join(m.convDir(peer), messagesFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var msgs []MailboxMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg MailboxMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("parse message line: %w", err)
		}
		msgs = append(msgs, msg)
	}
	return msgs, sc.Err()
}

// ReadConversation opens each message with the supplied per-epoch DEKs. A message whose
// epoch DEK is absent is returned with Locked=true and no plaintext (its key_epoch tells
// the UI which epoch to recover). A present-but-failing open is an error (corruption /
// wrong key material), not a lock.
func (m *Mailbox) ReadConversation(peer string, deks map[int][32]byte) ([]DecryptedMessage, error) {
	raw, err := m.LoadMessages(peer)
	if err != nil {
		return nil, err
	}
	out := make([]DecryptedMessage, 0, len(raw))
	for _, msg := range raw {
		dm := DecryptedMessage{MailboxMessage: msg}
		dek, ok := deks[msg.KeyEpoch]
		if !ok {
			dm.Locked = true
			out = append(out, dm)
			continue
		}
		// DM-6: a corrupt/garbage box (bad base64, wrong-length nonce/box_pub, or a failed
		// Poly1305 tag) is flagged on that one message and skipped — never an error that
		// aborts the whole conversation read. The DEK is present here (absent → Locked above),
		// so a failure means the stored box is bad, not locked.
		ct, err := base64.StdEncoding.DecodeString(msg.Ciphertext)
		if err != nil {
			dm.Undecryptable = true
			out = append(out, dm)
			continue
		}
		nb, err := base64.StdEncoding.DecodeString(msg.Nonce)
		if err != nil || len(nb) != 24 {
			dm.Undecryptable = true
			out = append(out, dm)
			continue
		}
		pb, err := base64.StdEncoding.DecodeString(msg.BoxPub)
		if err != nil || len(pb) != 32 {
			dm.Undecryptable = true
			out = append(out, dm)
			continue
		}
		var nonce [24]byte
		var boxPub [32]byte
		copy(nonce[:], nb)
		copy(boxPub[:], pb)
		pt, err := Decrypt(ct, &nonce, &boxPub, &dek)
		if err != nil {
			dm.Undecryptable = true
			out = append(out, dm)
			continue
		}
		dm.Plaintext = string(pt)
		out = append(out, dm)
	}
	return out, nil
}

// MarkRead sets the conversation's read cursor to now.
func (m *Mailbox) MarkRead(peer string) error {
	unlock := lockDMDir(m.dmDir) // DM-14
	defer unlock()
	meta, err := m.LoadMeta(peer)
	if err != nil {
		return err
	}
	meta.ReadAt = nowRFC3339()
	return m.saveMeta(peer, meta)
}

// unreadCount returns the number of incoming messages newer than the read cursor.
// RFC3339-UTC timestamps compare correctly as strings.
func unreadCount(msgs []MailboxMessage, readAt string) int {
	n := 0
	for _, msg := range msgs {
		if msg.Dir == DirIn && msg.At > readAt {
			n++
		}
	}
	return n
}

// RebuildInbox scans the conversation dirs and writes inbox.json — the derived index.
// Source of truth is the per-conversation dirs; this just rebuilds the fast list.
func (m *Mailbox) RebuildInbox() ([]InboxEntry, error) {
	unlock := lockDMDir(m.dmDir) // DM-14
	defer unlock()
	base := filepath.Join(m.dmDir, conversationsSubdir)
	dirs, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, m.writeInbox(nil)
		}
		return nil, err
	}
	var entries []InboxEntry
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		peer := d.Name()
		meta, err := m.LoadMeta(peer)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		msgs, err := m.LoadMessages(peer)
		if err != nil {
			return nil, err
		}
		entries = append(entries, InboxEntry{
			Peer:           peer,
			ConversationID: meta.ConversationID,
			LastMessageAt:  meta.LastMessageAt,
			Unread:         unreadCount(msgs, meta.ReadAt),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].LastMessageAt > entries[j].LastMessageAt })
	return entries, m.writeInbox(entries)
}

func (m *Mailbox) writeInbox(entries []InboxEntry) error {
	if err := os.MkdirAll(m.dmDir, 0o700); err != nil {
		return err
	}
	if entries == nil {
		entries = []InboxEntry{} // marshal an empty inbox as [] (a non-nil slice), not null
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal inbox.json: %w", err)
	}
	return atomicfile.WriteFile(filepath.Join(m.dmDir, inboxFile), data, 0o600)
}
