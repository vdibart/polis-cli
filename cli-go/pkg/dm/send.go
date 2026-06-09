package dm

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// maxWellKnownSize is the maximum size of a .well-known/polis response.
const maxWellKnownSize = 1 << 20 // 1MB

// Sender handles composing and delivering DMs to remote instances.
type Sender struct {
	PrivateKeyPEM []byte
	PublicKeySSH  []byte
	Domain        string
	SiteDir       string // tenant site dir (locates the keyring + mailbox)
	Logger        Logger

	keyCache   map[string]*cachedKey
	keyCacheMu sync.Mutex
	httpClient *http.Client
}

type cachedKey struct {
	publicKeySSH []byte
	x25519PK     [32]byte
	epoch        int // recipient's current messages epoch (stamped as recipient_epoch on the wire)
	fetchedAt    time.Time
}

const keyCacheTTL = 1 * time.Hour

// maxKeyCacheEntries caps the sender's recipient-key cache (DM-12). The cache is keyed by
// recipient domain (caller-chosen); the TTL only gates reuse, not eviction, so without a cap
// a long-lived process that DMs many distinct domains grows it unbounded. On insert past the
// cap we drop expired entries, then evict the oldest. Owned by R23-5.
const maxKeyCacheEntries = 2048

// NewSender creates a Sender for delivering DMs from the given tenant site dir.
// The domain is normalized to lowercase (DNS hostnames are case-insensitive per RFC 1123).
func NewSender(privateKeyPEM, publicKeySSH []byte, domain, siteDir string) *Sender {
	return &Sender{
		PrivateKeyPEM: privateKeyPEM,
		PublicKeySSH:  publicKeySSH,
		Domain:        strings.ToLower(domain),
		SiteDir:       siteDir,
		Logger:        nopLogger{},
		keyCache:      make(map[string]*cachedKey),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewSenderWithHTTP creates a Sender using a shared HTTP client for connection pooling.
func NewSenderWithHTTP(privateKeyPEM, publicKeySSH []byte, domain, siteDir string, hc *http.Client) *Sender {
	if hc == nil {
		return NewSender(privateKeyPEM, publicKeySSH, domain, siteDir)
	}
	return &Sender{
		PrivateKeyPEM: privateKeyPEM,
		PublicKeySSH:  publicKeySSH,
		Domain:        strings.ToLower(domain),
		SiteDir:       siteDir,
		Logger:        nopLogger{},
		keyCache:      make(map[string]*cachedKey),
		httpClient:    hc,
	}
}

// SendMessage composes, encrypts, and delivers a DM to a remote instance, storing the
// sender's own copy via the mailbox (sealed to the sender's current epoch key — no
// seed-derived re-seal). On delivery failure the local copy is saved with status
// "unsent". The box is sealed FROM the sender's current-epoch DEK TO the recipient's
// verified messages key; the envelope carries box_pub/sender_epoch/recipient_epoch so the
// recipient can store and (client-side) open it. Returns ErrMessagesLocked if the sender's
// current epoch is password-protected and not unlocked (awaits the phase-4 SPA unlock).
func (s *Sender) SendMessage(recipientURL, content, replyToID string) (*MailboxMessage, error) {
	recipientDomain := extractDomain(recipientURL)
	if recipientDomain == "" {
		return nil, fmt.Errorf("invalid recipient URL: %s", recipientURL)
	}

	s.Logger.Event("dm.send.initiated", map[string]any{
		"recipient_domain": recipientDomain, "content_length": len(content)})

	// Recipient's verified messages key + the epoch it belongs to.
	_, peerX25519PK, recipientEpoch, err := s.fetchRecipientKey(recipientURL)
	if err != nil {
		s.Logger.Event("dm.send.key_fetch_failed", map[string]any{
			"recipient_domain": recipientDomain, "error": err.Error()})
		return nil, fmt.Errorf("fetch recipient key: %w", err)
	}

	// Sender's current-epoch sealing material (bootstrap → server_dek; password → locked).
	siteDir := s.SiteDir
	seal, err := loadCurrentSealing(siteDir)
	if err != nil {
		return nil, fmt.Errorf("sender sealing key: %w", err)
	}

	// Box-seal the bare content FROM the sender's epoch DEK TO the recipient's messages
	// key. The sender's pubkey travels as box_pub and reply_to as envelope metadata, so
	// the stored sent + received copies hold the identical bare-content box.
	ciphertext, nonce, err := Encrypt([]byte(content), &peerX25519PK, &seal.dek)
	if err != nil {
		return nil, fmt.Errorf("encrypt message: %w", err)
	}

	envelope := MessageEnvelope{
		Version:          MessageEnvelopeVersion,
		SenderDomain:     s.Domain,
		RecipientDomain:  recipientDomain,
		EncryptedContent: base64.StdEncoding.EncodeToString(ciphertext),
		Nonce:            base64.StdEncoding.EncodeToString(nonce[:]),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		BoxPub:           base64.StdEncoding.EncodeToString(seal.pub[:]),
		SenderEpoch:      seal.epoch,
		RecipientEpoch:   recipientEpoch,
		ReplyTo:          replyToID,
	}

	deliverErr := s.deliver(recipientURL, envelope)
	status := "sent"
	if deliverErr != nil {
		status = "unsent"
	}

	// Store our own copy, sealed to our own current-epoch key (mailbox).
	mb := NewMailbox(DMDir(siteDir))
	msg, err := mb.AppendSent(recipientDomain, s.Domain, content, seal.epoch, seal.pub, seal.dek, replyToID, status)
	if err != nil {
		return nil, fmt.Errorf("store sent message: %w", err)
	}

	convID := ComputeConversationID(s.Domain, recipientDomain)
	if deliverErr != nil {
		s.Logger.Event("dm.send.failed", map[string]any{
			"recipient_domain": recipientDomain, "error": deliverErr.Error(), "status": "unsent"})
		return msg, fmt.Errorf("message saved as unsent: %w", deliverErr)
	}
	s.Logger.Event("dm.send.delivered", map[string]any{
		"recipient_domain": recipientDomain, "message_id": msg.ID, "conversation_id": convID})
	return msg, nil
}

// ResolveRecipientKey returns the recipient's VERIFIED X25519 messages public
// key (base64) and the recipient epoch it belongs to, so the SPA can seal a box
// to it in-browser (phase 4.6 browser-seal send). It wraps fetchRecipientKey,
// which fetches the recipient's published public_key_messages block and verifies
// its identity-key signature (phase 2.2) — so the server still vouches for the
// key the browser seals to; the browser just never holds the sender's DEK.
func (s *Sender) ResolveRecipientKey(recipientURL string) (x25519PubB64 string, recipientEpoch int, err error) {
	_, peerX25519PK, epoch, err := s.fetchRecipientKey(recipientURL)
	if err != nil {
		return "", 0, err
	}
	return base64.StdEncoding.EncodeToString(peerX25519PK[:]), epoch, nil
}

// SealedBox is a NaCl box sealed in the browser: base64 ciphertext + nonce.
type SealedBox struct {
	Ciphertext string
	Nonce      string
}

// SendSealed delivers a message whose boxes were sealed in the BROWSER from the
// sender's unlocked password-epoch DEK — which never reaches the server (phase
// 4.6). The server seals nothing: it only builds the wire envelope from the
// browser's delivery box, signs the instance-to-instance deliver request with
// the identity key, and stores the sender's pre-sealed box-to-self copy.
//
//   - delivery: box sealed to the RECIPIENT's messages key (goes on the wire).
//   - sent:     box sealed to the sender's OWN epoch key (stored for re-reading).
//   - boxPubB64: the sender's epoch pubkey — the envelope BoxPub the recipient
//     opens the delivery box with, and the stored sent copy's BoxPub.
//
// Mirrors SendMessage's envelope/deliver/store flow, minus the server-side seal.
func (s *Sender) SendSealed(recipientURL string, senderEpoch, recipientEpoch int, boxPubB64 string, delivery, sent SealedBox, replyToID string) (*MailboxMessage, error) {
	recipientDomain := extractDomain(recipientURL)
	if recipientDomain == "" {
		return nil, fmt.Errorf("invalid recipient URL: %s", recipientURL)
	}
	if pub, err := base64.StdEncoding.DecodeString(boxPubB64); err != nil || len(pub) != 32 {
		return nil, fmt.Errorf("invalid box_pub (want 32 bytes base64)")
	}
	if nb, err := base64.StdEncoding.DecodeString(delivery.Nonce); err != nil || len(nb) != 24 {
		return nil, fmt.Errorf("invalid delivery nonce (want 24 bytes base64)")
	}
	if _, err := base64.StdEncoding.DecodeString(delivery.Ciphertext); err != nil {
		return nil, fmt.Errorf("invalid delivery ciphertext base64")
	}

	s.Logger.Event("dm.send_sealed.initiated", map[string]any{
		"recipient_domain": recipientDomain, "sender_epoch": senderEpoch, "recipient_epoch": recipientEpoch})

	envelope := MessageEnvelope{
		Version:          MessageEnvelopeVersion,
		SenderDomain:     s.Domain,
		RecipientDomain:  recipientDomain,
		EncryptedContent: delivery.Ciphertext,
		Nonce:            delivery.Nonce,
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		BoxPub:           boxPubB64,
		SenderEpoch:      senderEpoch,
		RecipientEpoch:   recipientEpoch,
		ReplyTo:          replyToID,
	}

	deliverErr := s.deliver(recipientURL, envelope)
	status := "sent"
	if deliverErr != nil {
		status = "unsent"
	}

	// Store the browser's pre-sealed box-to-self copy (no re-seal — the server
	// has no password-epoch DEK).
	mb := NewMailbox(DMDir(s.SiteDir))
	msg, err := mb.AppendSentRaw(recipientDomain, senderEpoch, sent.Ciphertext, sent.Nonce, boxPubB64, replyToID, status)
	if err != nil {
		return nil, fmt.Errorf("store sent message: %w", err)
	}

	if deliverErr != nil {
		s.Logger.Event("dm.send_sealed.failed", map[string]any{
			"recipient_domain": recipientDomain, "error": deliverErr.Error(), "status": "unsent"})
		return msg, fmt.Errorf("message saved as unsent: %w", deliverErr)
	}
	s.Logger.Event("dm.send_sealed.delivered", map[string]any{
		"recipient_domain": recipientDomain, "message_id": msg.ID})
	return msg, nil
}

// deliver POSTs an encrypted envelope to the recipient's DM deliver endpoint.
func (s *Sender) deliver(recipientURL string, envelope MessageEnvelope) error {
	deliverURL := strings.TrimRight(recipientURL, "/") + "/v1/content/dm/actions/deliver"

	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, deliverURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Add signed request headers for instance-to-instance auth. The signature binds the
	// recipient domain + a digest of THIS envelope body (DM-2), so a captured header can't
	// be replayed against a different recipient or with a swapped body.
	if err := s.addSignedHeaders(req, "deliver", extractDomain(recipientURL), body); err != nil {
		return fmt.Errorf("sign request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delivery request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("delivery rejected (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}

// FetchProtectionStatus asks a recipient site whether it has secured its messages (set a
// password), to drive the "this person hasn't set a password — be careful what you send"
// warning. It signs a protection_status request with the sender's identity key, POSTs it,
// and verifies the signed answer against the recipient's identity public key (so an
// operator in the path can't spoof a reassuring "protected: true"). A caller outside the
// recipient's follow relationship is refused (gate #3) → non-nil error.
func (s *Sender) FetchProtectionStatus(recipientURL string) (*ProtectionStatus, error) {
	statusURL := strings.TrimRight(recipientURL, "/") + "/v1/content/dm/actions/protection_status"

	req, err := http.NewRequest(http.MethodPost, statusURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	// protection_status carries no body; the signature still binds the recipient domain.
	if err := s.addSignedHeaders(req, "protection_status", extractDomain(recipientURL), nil); err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("protection_status request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("protection_status rejected (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var ps ProtectionStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxWellKnownSize)).Decode(&ps); err != nil {
		return nil, fmt.Errorf("decode protection_status: %w", err)
	}

	// Verify the answer is signed by the recipient's identity key. fetchRecipientKey
	// returns that identity key (its first result) and requires the recipient to have
	// published public_key_messages — which every provisioned site has.
	idPubSSH, _, _, err := s.fetchRecipientKey(recipientURL)
	if err != nil {
		return nil, fmt.Errorf("fetch recipient identity key: %w", err)
	}
	ok, err := VerifyProtectionStatus(ps, idPubSSH)
	if err != nil {
		return nil, fmt.Errorf("verify protection_status signature: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("protection_status signature invalid for %s", extractDomain(recipientURL))
	}
	return &ps, nil
}

// addSignedHeaders adds X-Polis-Domain, X-Polis-Signature, X-Polis-Timestamp
// headers for instance-to-instance authentication. recipientDomain + body are folded into
// the signed canonical (DM-2) but NOT sent on the wire — the receiver re-derives the
// recipient from its own configured domain and the body digest from the bytes it received,
// so neither is attacker-spoofable.
func (s *Sender) addSignedHeaders(req *http.Request, action, recipientDomain string, body []byte) error {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	canonicalJSON, err := MakeDeliverAuthCanonicalJSON(action, s.Domain, recipientDomain, timestamp, bodyDigest(body))
	if err != nil {
		return fmt.Errorf("build canonical JSON: %w", err)
	}

	signature, err := signing.SignContent(canonicalJSON, s.PrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("sign auth payload: %w", err)
	}

	// Strip newlines from PEM signature for HTTP header
	compactSig := strings.ReplaceAll(signature, "\n", "")

	req.Header.Set("X-Polis-Domain", s.Domain)
	req.Header.Set("X-Polis-Signature", compactSig)
	req.Header.Set("X-Polis-Timestamp", timestamp)

	return nil
}

// MakeDeliverAuthCanonicalJSON creates the canonical signed-request JSON (envelope v2).
// It binds the action, the SENDER domain, the RECIPIENT domain, the timestamp, and a
// base64 SHA-256 digest of the request body — so a captured signature can't be replayed
// against a different recipient or with a swapped body (DM-2 / R20-C-F5). The byte form is
// frozen: field order is exactly action, body_sha256, domain, recipient, timestamp. Do not
// reorder or reformat (PROTOCOL.md §"Canonical signing form").
func MakeDeliverAuthCanonicalJSON(action, domain, recipient, timestamp, bodySHA256 string) ([]byte, error) {
	return json.Marshal(struct {
		Action     string `json:"action"`
		BodySHA256 string `json:"body_sha256"`
		Domain     string `json:"domain"`
		Recipient  string `json:"recipient"`
		Timestamp  string `json:"timestamp"`
	}{
		Action:     action,
		BodySHA256: bodySHA256,
		Domain:     domain,
		Recipient:  recipient,
		Timestamp:  timestamp,
	})
}

// bodyDigest is the base64 SHA-256 of a request body, used in the signed canonical so the
// signature commits to the exact bytes delivered. An empty/nil body (e.g. protection_status)
// hashes to the digest of the empty string — still a fixed, agreed value.
func bodyDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// fetchRecipientKey fetches and caches a recipient's public key from .well-known/polis.
func (s *Sender) fetchRecipientKey(recipientURL string) ([]byte, [32]byte, int, error) {
	domain := extractDomain(recipientURL) // already lowercased by extractDomain

	s.keyCacheMu.Lock()
	if cached, ok := s.keyCache[domain]; ok && time.Since(cached.fetchedAt) < keyCacheTTL {
		pubSSH := make([]byte, len(cached.publicKeySSH))
		copy(pubSSH, cached.publicKeySSH)
		x25519PK := cached.x25519PK // [32]byte is a value type, copied on assignment
		epoch := cached.epoch
		s.keyCacheMu.Unlock()
		return pubSSH, x25519PK, epoch, nil
	}
	s.keyCacheMu.Unlock()

	// Fetch .well-known/polis
	wellKnownURL := strings.TrimRight(recipientURL, "/") + "/.well-known/polis"
	resp, err := s.httpClient.Get(wellKnownURL)
	if err != nil {
		return nil, [32]byte{}, 0, fmt.Errorf("fetch %s: %w", wellKnownURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, [32]byte{}, 0, fmt.Errorf("fetch %s: HTTP %d", wellKnownURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWellKnownSize))
	if err != nil {
		return nil, [32]byte{}, 0, fmt.Errorf("read response: %w", err)
	}

	var wk struct {
		PublicKey         string            `json:"public_key"`
		PublicKeyMessages *MessagesKeyBlock `json:"public_key_messages"`
	}
	if err := json.Unmarshal(body, &wk); err != nil {
		return nil, [32]byte{}, 0, fmt.Errorf("parse .well-known/polis: %w", err)
	}
	if wk.PublicKey == "" {
		return nil, [32]byte{}, 0, fmt.Errorf("no public_key in .well-known/polis")
	}
	publicKeySSH := []byte(wk.PublicKey)
	if err := signing.ValidatePublicKey(publicKeySSH); err != nil {
		return nil, [32]byte{}, 0, fmt.Errorf("invalid public key from %s: %w", wellKnownURL, err)
	}

	// Phase 2 (decision (a)): require the identity-signed public_key_messages block and
	// encrypt to the verified messages key — never the identity-derived key. A site
	// without it is pre-DM-encryption: refuse (the UI surfaces this as "can't send"). A
	// present-but-invalid signature is a key-swap attack: also refuse — never silently
	// fall back, or stripping/forging the block would downgrade the recipient.
	if wk.PublicKeyMessages == nil {
		return nil, [32]byte{}, 0, fmt.Errorf("recipient %s has not published public_key_messages (pre-DM-encryption site)", domain)
	}
	ok, err := VerifyMessagesKeyEntry(wk.PublicKeyMessages.Current, publicKeySSH)
	if err != nil {
		return nil, [32]byte{}, 0, fmt.Errorf("verify public_key_messages for %s: %w", domain, err)
	}
	if !ok {
		return nil, [32]byte{}, 0, fmt.Errorf("public_key_messages signature invalid for %s (key-swap?)", domain)
	}
	x25519PK, err := wk.PublicKeyMessages.Current.X25519()
	if err != nil {
		return nil, [32]byte{}, 0, fmt.Errorf("decode messages key for %s: %w", domain, err)
	}

	epoch := wk.PublicKeyMessages.Current.Epoch

	// Cache (bounded — DM-12).
	s.keyCacheMu.Lock()
	if _, exists := s.keyCache[domain]; !exists && len(s.keyCache) >= maxKeyCacheEntries {
		s.evictKeyCacheLocked()
	}
	s.keyCache[domain] = &cachedKey{
		publicKeySSH: publicKeySSH,
		x25519PK:     x25519PK,
		epoch:        epoch,
		fetchedAt:    time.Now(),
	}
	s.keyCacheMu.Unlock()

	return publicKeySSH, x25519PK, epoch, nil
}

// evictKeyCacheLocked frees room in the key cache (DM-12): it first drops every expired
// entry, and if that freed nothing, evicts the single oldest entry. Caller holds keyCacheMu.
func (s *Sender) evictKeyCacheLocked() {
	now := time.Now()
	freed := false
	for d, c := range s.keyCache {
		if now.Sub(c.fetchedAt) >= keyCacheTTL {
			delete(s.keyCache, d)
			freed = true
		}
	}
	if freed {
		return
	}
	var oldestDomain string
	var oldestAt time.Time
	for d, c := range s.keyCache {
		if oldestDomain == "" || c.fetchedAt.Before(oldestAt) {
			oldestDomain, oldestAt = d, c.fetchedAt
		}
	}
	if oldestDomain != "" {
		delete(s.keyCache, oldestDomain)
	}
}

// ExtractDomainFromURL extracts the hostname from a URL.
func ExtractDomainFromURL(rawURL string) string {
	return extractDomain(rawURL)
}

// extractDomain extracts the hostname from a URL, normalized to lowercase.
// DNS hostnames are case-insensitive (RFC 1123), so we always lowercase
// to prevent case-mismatch failures in domain comparisons and conversation IDs.
func extractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
