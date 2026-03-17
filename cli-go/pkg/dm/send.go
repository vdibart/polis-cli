package dm

import (
	"bytes"
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
	Store         *Store
	Logger        Logger

	myX25519SK [32]byte
	initOnce   sync.Once
	initErr    error

	keyCache     map[string]*cachedKey
	keyCacheMu   sync.Mutex
	httpClient   *http.Client
}

type cachedKey struct {
	publicKeySSH []byte
	x25519PK    [32]byte
	fetchedAt   time.Time
}

const keyCacheTTL = 1 * time.Hour

// NewSender creates a Sender for delivering DMs.
// The domain is normalized to lowercase (DNS hostnames are case-insensitive per RFC 1123).
func NewSender(privateKeyPEM, publicKeySSH []byte, domain string, store *Store) *Sender {
	return &Sender{
		PrivateKeyPEM: privateKeyPEM,
		PublicKeySSH:  publicKeySSH,
		Domain:        strings.ToLower(domain),
		Store:         store,
		Logger:        nopLogger{},
		keyCache:      make(map[string]*cachedKey),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ensureKeys derives the X25519 secret key once.
func (s *Sender) ensureKeys() error {
	s.initOnce.Do(func() {
		s.myX25519SK, s.initErr = signing.Ed25519PrivateKeyToX25519(s.PrivateKeyPEM)
	})
	return s.initErr
}

// SendMessage composes, encrypts, and delivers a DM to a remote instance.
// On delivery failure, the message is saved locally as "unsent".
func (s *Sender) SendMessage(recipientURL, content, replyToID string) (*Message, error) {
	if err := s.ensureKeys(); err != nil {
		return nil, fmt.Errorf("init sender keys: %w", err)
	}

	recipientDomain := extractDomain(recipientURL)
	if recipientDomain == "" {
		return nil, fmt.Errorf("invalid recipient URL: %s", recipientURL)
	}

	s.Logger.Info("event=dm.send.initiated recipient_domain=%s content_length=%d",
		recipientDomain, len(content))

	// Fetch recipient's public key
	peerPKSSH, peerX25519PK, err := s.fetchRecipientKey(recipientURL)
	if err != nil {
		s.Logger.Warn("event=dm.send.key_fetch_failed recipient_domain=%s error=%q",
			recipientDomain, err.Error())
		return nil, fmt.Errorf("fetch recipient key: %w", err)
	}
	_ = peerPKSSH // used for inner payload verification

	// Build inner payload
	inner := InnerPayload{
		Content:         content,
		ReplyToID:       replyToID,
		SenderPublicKey: string(s.PublicKeySSH),
	}
	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return nil, fmt.Errorf("marshal inner payload: %w", err)
	}

	// Encrypt with NaCl box
	ciphertext, nonce, err := Encrypt(innerJSON, &peerX25519PK, &s.myX25519SK)
	if err != nil {
		return nil, fmt.Errorf("encrypt message: %w", err)
	}

	// Build envelope
	now := time.Now().UTC().Format(time.RFC3339)
	envelope := MessageEnvelope{
		Version:          1,
		SenderDomain:     s.Domain,
		RecipientDomain:  recipientDomain,
		EncryptedContent: base64.StdEncoding.EncodeToString(ciphertext),
		Nonce:            base64.StdEncoding.EncodeToString(nonce[:]),
		Timestamp:        now,
	}

	// Compute conversation ID
	convID := ComputeConversationID(s.Domain, recipientDomain)

	// Try to deliver
	deliverErr := s.deliver(recipientURL, envelope)

	status := "sent"
	if deliverErr != nil {
		status = "unsent"
	}

	// Store message locally
	msg, err := s.Store.AppendMessage(convID, recipientDomain, recipientURL,
		s.Domain, recipientDomain, content, replyToID, status, nonce)
	if err != nil {
		return nil, fmt.Errorf("store message: %w", err)
	}

	if deliverErr != nil {
		s.Logger.Warn("event=dm.send.failed recipient_domain=%s error=%q status=unsent",
			recipientDomain, deliverErr.Error())
		return msg, fmt.Errorf("message saved as unsent: %w", deliverErr)
	}

	s.Logger.Info("event=dm.send.delivered recipient_domain=%s message_id=%s conversation_id=%s",
		recipientDomain, msg.ID, convID)
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

	// Add signed request headers for instance-to-instance auth
	if err := s.addSignedHeaders(req, "deliver"); err != nil {
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

// addSignedHeaders adds X-Polis-Domain, X-Polis-Signature, X-Polis-Timestamp
// headers for instance-to-instance authentication.
func (s *Sender) addSignedHeaders(req *http.Request, action string) error {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	canonicalJSON, err := MakeDeliverAuthCanonicalJSON(action, s.Domain, timestamp)
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

// MakeDeliverAuthCanonicalJSON creates the canonical JSON for deliver action auth.
// Field order must match the receiver's reconstruction.
func MakeDeliverAuthCanonicalJSON(action, domain, timestamp string) ([]byte, error) {
	return json.Marshal(struct {
		Action    string `json:"action"`
		Domain    string `json:"domain"`
		Timestamp string `json:"timestamp"`
	}{
		Action:    action,
		Domain:    domain,
		Timestamp: timestamp,
	})
}

// fetchRecipientKey fetches and caches a recipient's public key from .well-known/polis.
func (s *Sender) fetchRecipientKey(recipientURL string) ([]byte, [32]byte, error) {
	domain := extractDomain(recipientURL) // already lowercased by extractDomain

	s.keyCacheMu.Lock()
	if cached, ok := s.keyCache[domain]; ok && time.Since(cached.fetchedAt) < keyCacheTTL {
		s.keyCacheMu.Unlock()
		return cached.publicKeySSH, cached.x25519PK, nil
	}
	s.keyCacheMu.Unlock()

	// Fetch .well-known/polis
	wellKnownURL := strings.TrimRight(recipientURL, "/") + "/.well-known/polis"
	resp, err := s.httpClient.Get(wellKnownURL)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("fetch %s: %w", wellKnownURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, [32]byte{}, fmt.Errorf("fetch %s: HTTP %d", wellKnownURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWellKnownSize))
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("read response: %w", err)
	}

	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(body, &wk); err != nil {
		return nil, [32]byte{}, fmt.Errorf("parse .well-known/polis: %w", err)
	}
	if wk.PublicKey == "" {
		return nil, [32]byte{}, fmt.Errorf("no public_key in .well-known/polis")
	}

	publicKeySSH := []byte(wk.PublicKey)
	x25519PK, err := signing.Ed25519PublicKeyToX25519(publicKeySSH)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("convert public key to X25519: %w", err)
	}

	// Cache
	s.keyCacheMu.Lock()
	s.keyCache[domain] = &cachedKey{
		publicKeySSH: publicKeySSH,
		x25519PK:    x25519PK,
		fetchedAt:   time.Now(),
	}
	s.keyCacheMu.Unlock()

	return publicKeySSH, x25519PK, nil
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
