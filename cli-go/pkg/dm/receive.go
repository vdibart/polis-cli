package dm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

const (
	// maxDMPayloadSize is the maximum encrypted message size (64KB).
	maxDMPayloadSize = 64 * 1024
	// maxPlaintextSize is the maximum decrypted content size (32KB).
	maxPlaintextSize = 32 * 1024
	// timestampWindow is the maximum age of a signed request (5 minutes).
	timestampWindow = 5 * time.Minute
)

// validHexID matches 16-char hex strings for reply_to_id validation.
var validHexID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Receiver handles incoming DM deliveries from remote instances.
type Receiver struct {
	PrivateKeyPEM  []byte
	PublicKeySSH   []byte
	Domain         string
	SiteDir        string
	Store          *Store
	RateLimiter    *RateLimiter
	Logger         Logger
	MaxMessageSize int // max encrypted envelope size in bytes (default: 64KB)

	myX25519SK [32]byte
	initOnce   sync.Once
	initErr    error

	// Remote public key cache
	keyCache   map[string]*cachedKey
	keyCacheMu sync.Mutex
	httpClient *http.Client
}

// Logger is the interface for DM operation logging.
// Implementations should support structured key=value fields in the format string.
type Logger interface {
	Info(format string, args ...interface{})
	Warn(format string, args ...interface{})
}

// nopLogger discards log messages.
type nopLogger struct{}

func (nopLogger) Info(string, ...interface{}) {}
func (nopLogger) Warn(string, ...interface{}) {}

// LogEvent is a structured log entry for the DM delivery pipeline.
type LogEvent struct {
	Event           string `json:"event"`
	Level           string `json:"level"` // "INFO" or "WARN"
	SenderDomain    string `json:"sender_domain,omitempty"`
	RecipientDomain string `json:"recipient_domain,omitempty"`
	ConversationID  string `json:"conversation_id,omitempty"`
	MessageID       string `json:"message_id,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Error           string `json:"error,omitempty"`
	ContentLength   int    `json:"content_length,omitempty"`
	CurrentCount    int    `json:"current_count,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	StatusCode      int    `json:"status_code,omitempty"`
}

// NewReceiver creates a Receiver for handling incoming DM deliveries.
// The domain is normalized to lowercase (DNS hostnames are case-insensitive per RFC 1123).
func NewReceiver(privateKeyPEM, publicKeySSH []byte, domain, siteDir string, store *Store, rl *RateLimiter) *Receiver {
	return &Receiver{
		PrivateKeyPEM:  privateKeyPEM,
		PublicKeySSH:   publicKeySSH,
		Domain:         strings.ToLower(domain),
		SiteDir:        siteDir,
		Store:          store,
		RateLimiter:    rl,
		Logger:         nopLogger{},
		MaxMessageSize: maxDMPayloadSize,
		keyCache:       make(map[string]*cachedKey),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ensureKeys derives the X25519 secret key once.
func (rcv *Receiver) ensureKeys() error {
	rcv.initOnce.Do(func() {
		rcv.myX25519SK, rcv.initErr = signing.Ed25519PrivateKeyToX25519(rcv.PrivateKeyPEM)
	})
	return rcv.initErr
}

// VerifySignedRequestWithLogger is like VerifySignedRequest but accepts a Logger
// for structured pipeline logging of timestamp rejection, key fetch failures,
// and signature verification failures.
func VerifySignedRequestWithLogger(r *http.Request, fetchPublicKey func(domain string) ([]byte, error), logger Logger) (string, error) {
	if logger == nil {
		logger = nopLogger{}
	}

	rawDomain := r.Header.Get("X-Polis-Domain")
	signature := r.Header.Get("X-Polis-Signature")
	timestamp := r.Header.Get("X-Polis-Timestamp")

	if rawDomain == "" || signature == "" || timestamp == "" {
		return "", fmt.Errorf("missing signed request headers")
	}

	// Use original case for signature verification (sender signed with their case),
	// but return lowercase domain for all downstream use.
	domain := strings.ToLower(rawDomain)

	// Layer 2: Timestamp check (near-zero cost — replay protection)
	ts, err := time.Parse("2006-01-02T15:04:05Z", timestamp)
	if err != nil {
		return "", fmt.Errorf("invalid timestamp format: %w", err)
	}
	delta := time.Since(ts)
	if delta.Abs() > timestampWindow {
		logger.Warn("event=dm.deliver.timestamp_rejected sender_domain=%s timestamp_delta_seconds=%.0f",
			domain, delta.Seconds())
		return "", fmt.Errorf("timestamp outside 5-minute window")
	}

	// Reconstruct canonical JSON using original domain case (sender signed with their case)
	canonicalJSON, err := MakeDeliverAuthCanonicalJSON("deliver", rawDomain, timestamp)
	if err != nil {
		return "", fmt.Errorf("build canonical JSON: %w", err)
	}

	// Layer 4: Fetch sender's public key (medium cost — remote fetch, cached)
	publicKeySSH, err := fetchPublicKey(domain)
	if err != nil {
		logger.Warn("event=dm.deliver.key_fetch_failed sender_domain=%s error=%q url=%q",
			domain, err.Error(), "https://"+domain+"/.well-known/polis")
		return "", fmt.Errorf("fetch sender public key: %w", err)
	}

	// The signature comes as a compact (newline-stripped) PEM.
	// Restore PEM format for verification.
	fullSig := restorePEMSignature(signature)

	valid, err := signing.VerifySignature(canonicalJSON, publicKeySSH, fullSig)
	if err != nil {
		logger.Warn("event=dm.deliver.signature_invalid sender_domain=%s error=%q", domain, err.Error())
		return "", fmt.Errorf("verify signature: %w", err)
	}
	if !valid {
		logger.Warn("event=dm.deliver.signature_invalid sender_domain=%s error=%q", domain, "signature verification failed")
		return "", fmt.Errorf("invalid signature")
	}

	return domain, nil
}

// restorePEMSignature restores a compact SSH signature back to PEM format.
func restorePEMSignature(compact string) string {
	// Strip header/footer markers if present in compact form
	compact = strings.TrimPrefix(compact, "-----BEGIN SSH SIGNATURE-----")
	compact = strings.TrimSuffix(compact, "-----END SSH SIGNATURE-----")
	compact = strings.TrimSpace(compact)

	// Re-wrap at 76 chars
	var lines []string
	lines = append(lines, "-----BEGIN SSH SIGNATURE-----")
	for len(compact) > 0 {
		end := 76
		if end > len(compact) {
			end = len(compact)
		}
		lines = append(lines, compact[:end])
		compact = compact[end:]
	}
	lines = append(lines, "-----END SSH SIGNATURE-----")
	lines = append(lines, "")

	return strings.Join(lines, "\n")
}

// ReceiveMessage processes an incoming DM delivery.
// It performs the full 7-layer defense pipeline:
// 1. Policy check, 2. Timestamp (handled by VerifySignedRequest),
// 3. Global rate limit, 4. Signature verify (handled by VerifySignedRequest),
// 5. Per-sender rate limit, 6. Size check, 7. Decrypt + store.
//
// Each step produces a structured log event for abuse detection and debugging.
func (rcv *Receiver) ReceiveMessage(senderDomain string, envelopeBody []byte, followingDomains map[string]bool) (*Message, error) {
	// Normalize domain to lowercase — DNS hostnames are case-insensitive (RFC 1123)
	senderDomain = strings.ToLower(senderDomain)

	// SSRF protection: reject IPs, localhost, internal hostnames
	if err := ValidateDomain(senderDomain); err != nil {
		return nil, fmt.Errorf("invalid sender domain: %w", err)
	}

	if err := rcv.ensureKeys(); err != nil {
		return nil, fmt.Errorf("init receiver keys: %w", err)
	}

	// Log receipt
	rcv.Logger.Info("event=dm.deliver.received sender_domain=%s recipient_domain=%s content_length=%d",
		senderDomain, rcv.Domain, len(envelopeBody))

	// Layer 1: Policy check (near-zero cost — in-memory lookup)
	if err := rcv.checkPolicy(senderDomain, followingDomains); err != nil {
		rcv.Logger.Warn("event=dm.deliver.policy_denied sender_domain=%s recipient_domain=%s reason=%q",
			senderDomain, rcv.Domain, err.Error())
		return nil, fmt.Errorf("policy denied: %w", err)
	}

	// Layer 3: Global rate limit (near-zero cost — in-memory counter)
	if rcv.RateLimiter != nil && !rcv.RateLimiter.AllowGlobal() {
		count, limit := rcv.RateLimiter.GlobalStatus()
		rcv.Logger.Warn("event=dm.deliver.global_rate_limited sender_domain=%s recipient_domain=%s current_count=%d limit=%d",
			senderDomain, rcv.Domain, count, limit)
		return nil, fmt.Errorf("global rate limit exceeded")
	}

	// Layer 5: Per-sender rate limit (near-zero cost — in-memory counter)
	if rcv.RateLimiter != nil && !rcv.RateLimiter.AllowSender(senderDomain) {
		count, limit := rcv.RateLimiter.SenderStatus(senderDomain)
		rcv.Logger.Warn("event=dm.deliver.sender_rate_limited sender_domain=%s recipient_domain=%s sender_count=%d limit=%d",
			senderDomain, rcv.Domain, count, limit)
		return nil, fmt.Errorf("per-sender rate limit exceeded")
	}

	// Layer 6: Size check (near-zero cost)
	if len(envelopeBody) > rcv.MaxMessageSize {
		rcv.Logger.Warn("event=dm.deliver.size_rejected sender_domain=%s recipient_domain=%s content_length=%d limit=%d",
			senderDomain, rcv.Domain, len(envelopeBody), rcv.MaxMessageSize)
		return nil, fmt.Errorf("message too large: %d bytes (max %d)", len(envelopeBody), rcv.MaxMessageSize)
	}

	// Parse envelope
	var envelope MessageEnvelope
	if err := json.Unmarshal(envelopeBody, &envelope); err != nil {
		return nil, fmt.Errorf("invalid envelope: %w", err)
	}

	if envelope.Version != 1 {
		return nil, fmt.Errorf("unsupported envelope version: %d", envelope.Version)
	}
	if !strings.EqualFold(envelope.SenderDomain, senderDomain) {
		return nil, fmt.Errorf("sender domain mismatch: header=%s envelope=%s", senderDomain, envelope.SenderDomain)
	}
	if !strings.EqualFold(envelope.RecipientDomain, rcv.Domain) {
		return nil, fmt.Errorf("recipient domain mismatch: expected=%s got=%s", rcv.Domain, envelope.RecipientDomain)
	}

	// Decode encrypted content and nonce
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.EncryptedContent)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	nonceBytes, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	if len(nonceBytes) != 24 {
		return nil, fmt.Errorf("invalid nonce length: %d", len(nonceBytes))
	}
	var nonce [24]byte
	copy(nonce[:], nonceBytes)

	// Layer 7: Decrypt (medium cost — remote fetch + crypto)
	senderX25519PK, err := rcv.fetchSenderX25519Key(senderDomain)
	if err != nil {
		rcv.Logger.Warn("event=dm.deliver.key_fetch_failed sender_domain=%s recipient_domain=%s error=%q url=%q",
			senderDomain, rcv.Domain, err.Error(), "https://"+senderDomain+"/.well-known/polis")
		return nil, fmt.Errorf("fetch sender key: %w", err)
	}

	plaintext, err := Decrypt(ciphertext, &nonce, &senderX25519PK, &rcv.myX25519SK)
	if err != nil {
		rcv.Logger.Warn("event=dm.deliver.decrypt_failed sender_domain=%s recipient_domain=%s error=%q",
			senderDomain, rcv.Domain, err.Error())
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	// Post-decryption validation
	inner, err := validateInnerPayload(plaintext, senderDomain, rcv)
	if err != nil {
		rcv.Logger.Warn("event=dm.deliver.validation_failed sender_domain=%s recipient_domain=%s error=%q",
			senderDomain, rcv.Domain, err.Error())
		return nil, fmt.Errorf("inner payload validation: %w", err)
	}

	// Determine sender URL for storage
	senderURL := "https://" + senderDomain

	// Store message (re-encrypted with storage key)
	convID := ComputeConversationID(rcv.Domain, senderDomain)
	msg, err := rcv.Store.AppendMessage(convID, senderDomain, senderURL,
		senderDomain, rcv.Domain, inner.Content, inner.ReplyToID, "received", nonce)
	if err != nil {
		return nil, fmt.Errorf("store message: %w", err)
	}

	rcv.Logger.Info("event=dm.deliver.accepted sender_domain=%s recipient_domain=%s conversation_id=%s message_id=%s",
		senderDomain, rcv.Domain, convID, msg.ID)

	return msg, nil
}

// validateInnerPayload validates the decrypted inner payload.
func validateInnerPayload(plaintext []byte, senderDomain string, rcv *Receiver) (*InnerPayload, error) {
	// Must be valid UTF-8
	if !utf8.Valid(plaintext) {
		return nil, fmt.Errorf("content is not valid UTF-8")
	}

	// Size check on plaintext
	if len(plaintext) > maxPlaintextSize {
		return nil, fmt.Errorf("plaintext too large: %d bytes (max %d)", len(plaintext), maxPlaintextSize)
	}

	var inner InnerPayload
	if err := json.Unmarshal(plaintext, &inner); err != nil {
		return nil, fmt.Errorf("invalid inner JSON: %w", err)
	}

	// Reject unknown fields by re-marshaling and comparing
	known, _ := json.Marshal(inner)
	var rawMap map[string]json.RawMessage
	json.Unmarshal(plaintext, &rawMap)
	var knownMap map[string]json.RawMessage
	json.Unmarshal(known, &knownMap)
	for key := range rawMap {
		if _, ok := knownMap[key]; !ok {
			return nil, fmt.Errorf("unknown field in inner payload: %s", key)
		}
	}

	// Content must not be empty
	if strings.TrimSpace(inner.Content) == "" {
		return nil, fmt.Errorf("empty content")
	}

	// Strip HTML tags from content (DM content is plaintext only)
	inner.Content = stripHTMLTags(inner.Content)

	// Validate reply_to_id format if present
	if inner.ReplyToID != "" && !validHexID.MatchString(inner.ReplyToID) {
		return nil, fmt.Errorf("invalid reply_to_id format")
	}

	// Verify sender public key matches the authenticated sender
	if inner.SenderPublicKey != "" {
		innerDomain, err := rcv.domainForPublicKey(inner.SenderPublicKey, senderDomain)
		if err != nil || innerDomain != senderDomain {
			return nil, fmt.Errorf("sender_public_key mismatch")
		}
	}

	return &inner, nil
}

// stripHTMLTags removes HTML tags from text.
func stripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' && inTag {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// checkPolicy evaluates DM acceptance policy for the sender domain.
func (rcv *Receiver) checkPolicy(senderDomain string, followingDomains map[string]bool) error {
	privatePath, publicPath := policy.DefaultPaths(rcv.SiteDir)
	policies, err := policy.LoadPolicies(privatePath, publicPath)
	if err != nil {
		return fmt.Errorf("load policies: %w", err)
	}

	// Normalize following domains to lowercase for consistent policy matching
	normalizedFollowing := make(map[string]bool, len(followingDomains))
	for d, v := range followingDomains {
		normalizedFollowing[strings.ToLower(d)] = v
	}

	evt := policy.Event{
		Type:        "pub.polis.dm",
		ActorDomain: strings.ToLower(senderDomain),
	}
	ctx := policy.EvalContext{
		FollowingDomains: normalizedFollowing,
	}

	decision := policy.Evaluate(policies, evt, ctx)
	if decision == policy.Deny {
		return fmt.Errorf("denied by policy")
	}
	return nil
}

// fetchSenderX25519Key fetches a sender's Ed25519 public key and converts to X25519.
func (rcv *Receiver) fetchSenderX25519Key(senderDomain string) ([32]byte, error) {
	if err := ValidateDomain(senderDomain); err != nil {
		return [32]byte{}, fmt.Errorf("invalid sender domain: %w", err)
	}
	cacheKey := strings.ToLower(senderDomain)
	rcv.keyCacheMu.Lock()
	if cached, ok := rcv.keyCache[cacheKey]; ok && time.Since(cached.fetchedAt) < keyCacheTTL {
		rcv.keyCacheMu.Unlock()
		return cached.x25519PK, nil
	}
	rcv.keyCacheMu.Unlock()

	wellKnownURL := "https://" + senderDomain + "/.well-known/polis"
	resp, err := rcv.httpClient.Get(wellKnownURL)
	if err != nil {
		return [32]byte{}, fmt.Errorf("fetch %s: %w", wellKnownURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return [32]byte{}, fmt.Errorf("fetch %s: HTTP %d", wellKnownURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWellKnownSize))
	if err != nil {
		return [32]byte{}, err
	}

	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(body, &wk); err != nil {
		return [32]byte{}, fmt.Errorf("parse .well-known/polis: %w", err)
	}
	if wk.PublicKey == "" {
		return [32]byte{}, fmt.Errorf("no public_key in .well-known/polis")
	}
	if err := signing.ValidatePublicKey([]byte(wk.PublicKey)); err != nil {
		return [32]byte{}, fmt.Errorf("invalid public key from %s: %w", senderDomain, err)
	}

	x25519PK, err := signing.Ed25519PublicKeyToX25519([]byte(wk.PublicKey))
	if err != nil {
		return [32]byte{}, fmt.Errorf("convert to X25519: %w", err)
	}

	rcv.keyCacheMu.Lock()
	rcv.keyCache[cacheKey] = &cachedKey{
		publicKeySSH: []byte(wk.PublicKey),
		x25519PK:    x25519PK,
		fetchedAt:   time.Now(),
	}
	rcv.keyCacheMu.Unlock()

	return x25519PK, nil
}

// FetchPublicKey fetches the Ed25519 public key for a domain from .well-known/polis.
// Used by the webapp middleware for signed-request verification.
func FetchPublicKey(domain string) ([]byte, error) {
	if err := ValidateDomain(domain); err != nil {
		return nil, fmt.Errorf("invalid domain: %w", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	wellKnownURL := "https://" + domain + "/.well-known/polis"
	resp, err := client.Get(wellKnownURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", wellKnownURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", wellKnownURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWellKnownSize))
	if err != nil {
		return nil, err
	}

	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(body, &wk); err != nil {
		return nil, fmt.Errorf("parse .well-known/polis: %w", err)
	}
	if wk.PublicKey == "" {
		return nil, fmt.Errorf("no public_key in .well-known/polis")
	}
	if err := signing.ValidatePublicKey([]byte(wk.PublicKey)); err != nil {
		return nil, fmt.Errorf("invalid public key from %s: %w", domain, err)
	}

	return []byte(wk.PublicKey), nil
}

// domainForPublicKey checks if the given public key matches the cached key for a domain.
func (rcv *Receiver) domainForPublicKey(pubKeyStr, expectedDomain string) (string, error) {
	rcv.keyCacheMu.Lock()
	cached, ok := rcv.keyCache[strings.ToLower(expectedDomain)]
	var cachedPubKey string
	if ok {
		cachedPubKey = string(cached.publicKeySSH) // string() copies the bytes, safe after unlock
	}
	rcv.keyCacheMu.Unlock()

	if !ok {
		return "", fmt.Errorf("no cached key for domain %s", expectedDomain)
	}

	if strings.TrimSpace(cachedPubKey) == strings.TrimSpace(pubKeyStr) {
		return expectedDomain, nil
	}
	return "", fmt.Errorf("public key does not match domain %s", expectedDomain)
}
