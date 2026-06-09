package dm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

const (
	// maxDMPayloadSize is the maximum encrypted message size (64KB).
	maxDMPayloadSize = 64 * 1024
	// timestampWindow bounds signed-request freshness (DM-11). Tightened from 5m to 2m now
	// that the signature binds recipient + body (DM-2) and receipt dedups on message ID
	// (DM-3): the only thing the window must tolerate is honest clock skew between sites,
	// not a generous replay budget. ±2m via Abs below.
	timestampWindow = 2 * time.Minute
)

// Receiver handles incoming DM deliveries from remote instances.
type Receiver struct {
	PrivateKeyPEM  []byte
	PublicKeySSH   []byte
	Domain         string
	SiteDir        string
	RateLimiter    *RateLimiter
	Logger         Logger
	MaxMessageSize int // max encrypted envelope size in bytes (default: 64KB)

	// FetchSenderKeys returns a sender domain's identity public key (OpenSSH) and its
	// signed public_key_messages block, used to authenticate an incoming box_pub against
	// the sender's published keys (DM-1). Nil → a default .well-known/polis fetch. Tests
	// inject a fake.
	FetchSenderKeys func(domain string) (identityPubSSH []byte, block *MessagesKeyBlock, err error)
}

// Logger receives structured DM operation events. Implementations route them to
// the host's structured event sink (action=<name>, source=...), so DM signals
// (signature_invalid, rate-limited, policy_denied, send/deliver outcomes) are
// queryable in Axiom rather than discarded or buried in logfmt text. Previously
// this was a printf Info/Warn logger AND was never wired in production
// (nopLogger), so DM events reached neither disk nor Axiom.
type Logger interface {
	Event(action string, fields map[string]any)
}

// EventFunc adapts a plain emit function (e.g. the webapp's Server.LogEvent) to a
// dm.Logger.
type EventFunc func(action string, fields map[string]any)

func (f EventFunc) Event(action string, fields map[string]any) {
	if f != nil {
		f(action, fields)
	}
}

// nopLogger discards events.
type nopLogger struct{}

func (nopLogger) Event(string, map[string]any) {}

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
func NewReceiver(privateKeyPEM, publicKeySSH []byte, domain, siteDir string, rl *RateLimiter) *Receiver {
	return &Receiver{
		PrivateKeyPEM:  privateKeyPEM,
		PublicKeySSH:   publicKeySSH,
		Domain:         strings.ToLower(domain),
		SiteDir:        siteDir,
		RateLimiter:    rl,
		Logger:         nopLogger{},
		MaxMessageSize: maxDMPayloadSize,
	}
}

// VerifySignedRequestWithLogger verifies a `deliver`-action signed request, binding the
// recipient domain + body digest (DM-2). recipientDomain is the verifier's OWN configured
// domain (never the request Host — that's attacker-controlled); body is the raw request body.
func VerifySignedRequestWithLogger(r *http.Request, recipientDomain string, body []byte, fetchPublicKey func(domain string) ([]byte, error), logger Logger) (string, error) {
	return VerifySignedRequestForAction(r, "deliver", recipientDomain, body, fetchPublicKey, logger)
}

// VerifySignedRequestForAction verifies a signed site-to-site request for a specific
// action (e.g. "deliver", "protection_status"). The caller signs the canonical auth JSON
// {action, domain, recipient, timestamp, body_sha256} with its identity key; we reconstruct
// it — binding the verifier's OWN recipient domain and the digest of the received body — and
// verify against the caller's identity public key. Returns the verified (lowercased) caller
// domain. recipientDomain MUST be the verifier's own configured domain (NOT the request Host,
// which the caller controls); body is the raw request body (nil for bodyless actions).
func VerifySignedRequestForAction(r *http.Request, action, recipientDomain string, body []byte, fetchPublicKey func(domain string) ([]byte, error), logger Logger) (string, error) {
	if logger == nil {
		logger = nopLogger{}
	}

	rawDomain := r.Header.Get("X-Polis-Domain")
	signature := r.Header.Get("X-Polis-Signature")
	timestamp := r.Header.Get("X-Polis-Timestamp")

	if rawDomain == "" || signature == "" || timestamp == "" {
		return "", fmt.Errorf("missing signed request headers")
	}

	// Use original case for signature verification (caller signed with their case),
	// but return lowercase domain for all downstream use.
	domain := strings.ToLower(rawDomain)

	// Layer 2: Timestamp check (near-zero cost — replay protection)
	ts, err := time.Parse("2006-01-02T15:04:05Z", timestamp)
	if err != nil {
		return "", fmt.Errorf("invalid timestamp format: %w", err)
	}
	delta := time.Since(ts)
	if delta.Abs() > timestampWindow {
		logger.Event("dm."+action+".timestamp_rejected", map[string]any{
			"sender_domain": domain, "timestamp_delta_seconds": delta.Seconds()})
		return "", fmt.Errorf("timestamp outside freshness window")
	}

	// Reconstruct canonical JSON using original domain case (caller signed with their case),
	// binding our own recipient domain + the received body digest (DM-2). A header replayed
	// against a different recipient (recipientDomain mismatch) or with a swapped body
	// (digest mismatch) fails signature verification here.
	canonicalJSON, err := MakeDeliverAuthCanonicalJSON(action, rawDomain, strings.ToLower(recipientDomain), timestamp, bodyDigest(body))
	if err != nil {
		return "", fmt.Errorf("build canonical JSON: %w", err)
	}

	// Layer 4: Fetch caller's public key (medium cost — remote fetch, cached)
	publicKeySSH, err := fetchPublicKey(domain)
	if err != nil {
		logger.Event("dm."+action+".key_fetch_failed", map[string]any{
			"sender_domain": domain, "error": err.Error(), "url": "https://" + domain + "/.well-known/polis"})
		return "", fmt.Errorf("fetch sender public key: %w", err)
	}

	// The signature comes as a compact (newline-stripped) PEM.
	// Restore PEM format for verification.
	fullSig := restorePEMSignature(signature)

	valid, err := signing.VerifySignature(canonicalJSON, publicKeySSH, fullSig)
	if err != nil {
		logger.Event("dm."+action+".signature_invalid", map[string]any{"sender_domain": domain, "error": err.Error()})
		return "", fmt.Errorf("verify signature: %w", err)
	}
	if !valid {
		logger.Event("dm."+action+".signature_invalid", map[string]any{"sender_domain": domain, "error": "signature verification failed"})
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

// ReceiveMessage processes an incoming DM delivery. The signature (timestamp + sig) was
// verified upstream (VerifySignedRequestForAction). This applies the remaining checks and
// stores the sender's wire box **as-is** via the mailbox — it does NOT decrypt: a hosted
// recipient on a password epoch holds no DEK, and not opening the box is the point. The
// stored key_epoch comes from the wire (recipient_epoch), validated against the keyring.
//
//  1. Policy check  3. Global rate limit  5. Per-sender rate limit  6. Size check
//  7. Validate recipient_epoch + store wire box (no decrypt)
func (rcv *Receiver) ReceiveMessage(senderDomain string, envelopeBody []byte, followingDomains map[string]bool) (*MailboxMessage, error) {
	// Normalize domain to lowercase — DNS hostnames are case-insensitive (RFC 1123)
	senderDomain = strings.ToLower(senderDomain)

	// SSRF protection: reject IPs, localhost, internal hostnames
	if err := ValidateDomain(senderDomain); err != nil {
		return nil, fmt.Errorf("invalid sender domain: %w", err)
	}

	// Log receipt
	rcv.Logger.Event("dm.deliver.received", map[string]any{
		"sender_domain": senderDomain, "recipient_domain": rcv.Domain, "content_length": len(envelopeBody)})

	// Layer 1: Policy check (near-zero cost — in-memory lookup)
	if err := rcv.checkPolicy(senderDomain, followingDomains); err != nil {
		rcv.Logger.Event("dm.deliver.policy_denied", map[string]any{
			"sender_domain": senderDomain, "recipient_domain": rcv.Domain, "reason": err.Error()})
		return nil, fmt.Errorf("policy denied: %w", err)
	}

	// Layer 3: Global rate limit (near-zero cost — in-memory counter)
	if rcv.RateLimiter != nil && !rcv.RateLimiter.AllowGlobal() {
		count, limit := rcv.RateLimiter.GlobalStatus()
		rcv.Logger.Event("dm.deliver.global_rate_limited", map[string]any{
			"sender_domain": senderDomain, "recipient_domain": rcv.Domain, "current_count": count, "limit": limit})
		return nil, fmt.Errorf("global rate limit exceeded")
	}

	// Layer 5: Per-sender rate limit (near-zero cost — in-memory counter)
	if rcv.RateLimiter != nil && !rcv.RateLimiter.AllowSender(senderDomain) {
		count, limit := rcv.RateLimiter.SenderStatus(senderDomain)
		rcv.Logger.Event("dm.deliver.sender_rate_limited", map[string]any{
			"sender_domain": senderDomain, "recipient_domain": rcv.Domain, "sender_count": count, "limit": limit})
		return nil, fmt.Errorf("per-sender rate limit exceeded")
	}

	// Layer 6: Size check (near-zero cost)
	if len(envelopeBody) > rcv.MaxMessageSize {
		rcv.Logger.Event("dm.deliver.size_rejected", map[string]any{
			"sender_domain": senderDomain, "recipient_domain": rcv.Domain, "content_length": len(envelopeBody), "limit": rcv.MaxMessageSize})
		return nil, fmt.Errorf("message too large: %d bytes (max %d)", len(envelopeBody), rcv.MaxMessageSize)
	}

	// Parse envelope
	var envelope MessageEnvelope
	if err := json.Unmarshal(envelopeBody, &envelope); err != nil {
		return nil, fmt.Errorf("invalid envelope: %w", err)
	}

	if envelope.Version != MessageEnvelopeVersion {
		return nil, fmt.Errorf("unsupported envelope version: %d (want %d)", envelope.Version, MessageEnvelopeVersion)
	}
	if !strings.EqualFold(envelope.SenderDomain, senderDomain) {
		return nil, fmt.Errorf("sender domain mismatch: header=%s envelope=%s", senderDomain, envelope.SenderDomain)
	}
	if !strings.EqualFold(envelope.RecipientDomain, rcv.Domain) {
		return nil, fmt.Errorf("recipient domain mismatch: expected=%s got=%s", rcv.Domain, envelope.RecipientDomain)
	}

	// Decode the wire box: ciphertext, nonce, and the sender's box public key (needed to
	// open it later, client-side). The server never opens it here.
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

	if envelope.BoxPub == "" {
		return nil, fmt.Errorf("envelope missing box_pub")
	}
	boxPubBytes, err := base64.StdEncoding.DecodeString(envelope.BoxPub)
	if err != nil || len(boxPubBytes) != 32 {
		return nil, fmt.Errorf("invalid box_pub")
	}
	var senderPub [32]byte
	copy(senderPub[:], boxPubBytes)

	// Validate recipient_epoch against our keyring: the sender must seal to our CURRENT
	// epoch (the only one published as `.current`). A non-current epoch means a stale
	// cached messages key — reject so the sender refetches and re-seals. This is also what
	// makes clearing the bootstrap `server_dek` safe: after a password is set, a late
	// message still addressed to the (now non-current, key-cleared) bootstrap epoch is
	// rejected for retry rather than stored as ciphertext nobody can ever open.
	kr, err := LoadKeyring(DMDir(rcv.SiteDir))
	if err != nil {
		return nil, fmt.Errorf("load keyring (site not provisioned for DMs?): %w", err)
	}
	if envelope.RecipientEpoch != kr.Current {
		rcv.Logger.Event("dm.deliver.stale_epoch", map[string]any{
			"sender_domain": senderDomain, "recipient_domain": rcv.Domain, "recipient_epoch": envelope.RecipientEpoch, "current_epoch": kr.Current})
		return nil, fmt.Errorf("stale recipient_epoch %d (current is %d — refetch messages key and retry)", envelope.RecipientEpoch, kr.Current)
	}

	// DM-1: authenticate box_pub against the sender's published messages keys. The deliver
	// signature covers only {action, domain, timestamp}, not the box — so without this a
	// party who can present a valid signed header for senderDomain (e.g. by replaying one
	// observed within the timestamp window) could attach an attacker-generated box_pub and a
	// box sealed under it, and the recipient would decrypt attacker-chosen plaintext
	// attributed to senderDomain. Requiring box_pub to equal the sender's identity-signed
	// messages key for sender_epoch binds the box-opening key to the DS-attested identity.
	// Done here (receive time, incoming only) — never at read time, where a stored box_pub
	// may legitimately be an unpublished ephemeral key (bootstrap forward re-seal) or the
	// recipient's own key (box-to-self).
	fetch := rcv.FetchSenderKeys
	if fetch == nil {
		fetch = fetchSenderKeysDefault
	}
	idPub, block, ferr := fetch(senderDomain)
	if ferr != nil {
		rcv.Logger.Event("dm.deliver.sender_key_fetch_failed", map[string]any{
			"sender_domain": senderDomain, "recipient_domain": rcv.Domain, "error": ferr.Error()})
		return nil, fmt.Errorf("authenticate sender box_pub: fetch sender keys: %w", ferr)
	}
	if verr := VerifyBoxPub(idPub, block, envelope.SenderEpoch, senderPub); verr != nil {
		rcv.Logger.Event("dm.deliver.box_pub_unauthenticated", map[string]any{
			"sender_domain": senderDomain, "recipient_domain": rcv.Domain, "sender_epoch": envelope.SenderEpoch, "reason": verr.Error()})
		return nil, fmt.Errorf("box_pub failed sender authentication: %w", verr)
	}

	// Store the wire box as-is via the mailbox (key_epoch = the recipient epoch it was
	// sealed to). No re-seal, no decrypt. reply_to is envelope metadata; the message body
	// stays sealed and opens client-side on read.
	mb := NewMailbox(DMDir(rcv.SiteDir))
	msg, err := mb.AppendReceived(senderDomain, senderDomain, envelope.RecipientEpoch, ciphertext, nonce, senderPub, envelope.ReplyTo)
	if err != nil {
		return nil, fmt.Errorf("store received message: %w", err)
	}

	convID := ComputeConversationID(rcv.Domain, senderDomain)
	rcv.Logger.Event("dm.deliver.accepted", map[string]any{
		"sender_domain": senderDomain, "recipient_domain": rcv.Domain, "conversation_id": convID, "message_id": msg.ID, "key_epoch": envelope.RecipientEpoch})

	return msg, nil
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

// fetchSenderKeysDefault fetches a sender domain's identity public key (OpenSSH) and its
// signed public_key_messages block from .well-known/polis — the default for
// Receiver.FetchSenderKeys (DM-1 box_pub authentication). Domain is SSRF-validated.
func fetchSenderKeysDefault(domain string) ([]byte, *MessagesKeyBlock, error) {
	if err := ValidateDomain(domain); err != nil {
		return nil, nil, fmt.Errorf("invalid domain: %w", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	wellKnownURL := "https://" + domain + "/.well-known/polis"
	resp, err := client.Get(wellKnownURL)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch %s: %w", wellKnownURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("fetch %s: HTTP %d", wellKnownURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWellKnownSize))
	if err != nil {
		return nil, nil, err
	}
	var wk struct {
		PublicKey         string            `json:"public_key"`
		PublicKeyMessages *MessagesKeyBlock `json:"public_key_messages"`
	}
	if err := json.Unmarshal(body, &wk); err != nil {
		return nil, nil, fmt.Errorf("parse .well-known/polis: %w", err)
	}
	if wk.PublicKey == "" {
		return nil, nil, fmt.Errorf("no public_key in .well-known/polis")
	}
	if err := signing.ValidatePublicKey([]byte(wk.PublicKey)); err != nil {
		return nil, nil, fmt.Errorf("invalid public key from %s: %w", domain, err)
	}
	if wk.PublicKeyMessages == nil {
		return nil, nil, fmt.Errorf("sender %s published no public_key_messages", domain)
	}
	return []byte(wk.PublicKey), wk.PublicKeyMessages, nil
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
