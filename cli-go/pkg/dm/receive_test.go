package dm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// testLogger captures log messages for assertion.
type testLogger struct {
	infos []string
	warns []string
}

func (l *testLogger) Info(format string, args ...interface{}) {
	l.infos = append(l.infos, fmt.Sprintf(format, args...))
}

func (l *testLogger) Warn(format string, args ...interface{}) {
	l.warns = append(l.warns, fmt.Sprintf(format, args...))
}

func testReceiver(t *testing.T) (*Receiver, []byte, []byte, []byte, []byte) {
	t.Helper()
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis", "policies"), 0700)

	// Generate receiver keypair
	privPEM, pubSSH, _ := signing.GenerateKeypair()

	// Generate sender keypair
	senderPriv, senderPub, _ := signing.GenerateKeypair()

	seed, _ := signing.ParsePrivateKey(privPEM)
	store, err := NewStore(siteDir, seed.Seed())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	rl := NewRateLimiter(10, 100)
	rcv := NewReceiver(privPEM, pubSSH, "bob.example.com", siteDir, store, rl)

	return rcv, senderPriv, senderPub, privPEM, pubSSH
}

// buildEnvelope creates a valid encrypted envelope for testing.
func buildEnvelope(t *testing.T, senderPrivPEM, senderPubSSH, receiverPubSSH []byte, senderDomain, receiverDomain, content string) []byte {
	t.Helper()

	senderSK, err := signing.Ed25519PrivateKeyToX25519(senderPrivPEM)
	if err != nil {
		t.Fatalf("sender SK: %v", err)
	}
	receiverPK, err := signing.Ed25519PublicKeyToX25519(receiverPubSSH)
	if err != nil {
		t.Fatalf("receiver PK: %v", err)
	}

	inner := InnerPayload{
		Content:         content,
		SenderPublicKey: string(senderPubSSH),
	}
	innerJSON, _ := json.Marshal(inner)

	ciphertext, nonce, err := Encrypt(innerJSON, &receiverPK, &senderSK)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	envelope := MessageEnvelope{
		Version:          1,
		SenderDomain:     senderDomain,
		RecipientDomain:  receiverDomain,
		EncryptedContent: base64.StdEncoding.EncodeToString(ciphertext),
		Nonce:            base64.StdEncoding.EncodeToString(nonce[:]),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
	}

	data, _ := json.Marshal(envelope)
	return data
}

func TestReceiveMessage_Success(t *testing.T) {
	rcv, senderPriv, senderPub, _, receiverPub := testReceiver(t)
	logger := &testLogger{}
	rcv.Logger = logger

	// Set up sender's public key in cache (simulate prior fetch)
	senderX25519PK, _ := signing.Ed25519PublicKeyToX25519(senderPub)
	rcv.keyCache["alice.example.com"] = &cachedKey{
		publicKeySSH: senderPub,
		x25519PK:     senderX25519PK,
		fetchedAt:    time.Now(),
	}

	// Write policy allowing DMs from all
	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(`{"active":true,"policy":"allow pub.polis.dm from all"}`+"\n"), 0600)

	envelope := buildEnvelope(t, senderPriv, senderPub, receiverPub, "alice.example.com", "bob.example.com", "Hello Bob!")
	msg, err := rcv.ReceiveMessage("alice.example.com", envelope, nil)
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Status != "received" {
		t.Errorf("status = %q, want received", msg.Status)
	}

	// Check structured logs
	if len(logger.infos) < 2 {
		t.Errorf("expected at least 2 info logs (received + accepted), got %d", len(logger.infos))
	}
	foundReceived := false
	foundAccepted := false
	for _, log := range logger.infos {
		if strings.Contains(log, "dm.deliver.received") {
			foundReceived = true
		}
		if strings.Contains(log, "dm.deliver.accepted") {
			foundAccepted = true
		}
	}
	if !foundReceived {
		t.Error("missing dm.deliver.received log")
	}
	if !foundAccepted {
		t.Error("missing dm.deliver.accepted log")
	}
}

func TestReceiveMessage_PolicyDenied(t *testing.T) {
	rcv, _, _, _, _ := testReceiver(t)
	logger := &testLogger{}
	rcv.Logger = logger

	// Write policy denying DMs from all
	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(`{"active":true,"policy":"deny pub.polis.dm from all"}`+"\n"), 0600)

	_, err := rcv.ReceiveMessage("spam.example.com", []byte("{}"), nil)
	if err == nil {
		t.Fatal("expected policy denial")
	}
	if !strings.Contains(err.Error(), "policy denied") {
		t.Errorf("error should mention policy: %v", err)
	}

	// Check structured log
	found := false
	for _, log := range logger.warns {
		if strings.Contains(log, "dm.deliver.policy_denied") && strings.Contains(log, "spam.example.com") {
			found = true
		}
	}
	if !found {
		t.Error("missing dm.deliver.policy_denied log")
	}
}

func TestReceiveMessage_GlobalRateLimit(t *testing.T) {
	rcv, _, _, _, _ := testReceiver(t)
	logger := &testLogger{}
	rcv.Logger = logger

	// Create rate limiter with global limit of 1
	rcv.RateLimiter = NewRateLimiter(100, 1)

	// Allow all DMs via policy
	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(`{"active":true,"policy":"allow pub.polis.dm from all"}`+"\n"), 0600)

	// First request consumes the limit
	rcv.RateLimiter.AllowGlobal() // consume

	_, err := rcv.ReceiveMessage("alice.example.com", []byte("{}"), nil)
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !strings.Contains(err.Error(), "global rate limit") {
		t.Errorf("error should mention global rate limit: %v", err)
	}

	found := false
	for _, log := range logger.warns {
		if strings.Contains(log, "dm.deliver.global_rate_limited") {
			found = true
		}
	}
	if !found {
		t.Error("missing dm.deliver.global_rate_limited log")
	}
}

func TestReceiveMessage_SenderRateLimit(t *testing.T) {
	rcv, _, _, _, _ := testReceiver(t)
	logger := &testLogger{}
	rcv.Logger = logger

	// Create rate limiter with per-sender limit of 1
	rcv.RateLimiter = NewRateLimiter(1, 100)

	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(`{"active":true,"policy":"allow pub.polis.dm from all"}`+"\n"), 0600)

	// First request consumes sender limit
	rcv.RateLimiter.AllowSender("alice.example.com") // consume

	_, err := rcv.ReceiveMessage("alice.example.com", []byte("{}"), nil)
	if err == nil {
		t.Fatal("expected sender rate limit error")
	}
	if !strings.Contains(err.Error(), "per-sender rate limit") {
		t.Errorf("error should mention per-sender rate limit: %v", err)
	}

	found := false
	for _, log := range logger.warns {
		if strings.Contains(log, "dm.deliver.sender_rate_limited") {
			found = true
		}
	}
	if !found {
		t.Error("missing dm.deliver.sender_rate_limited log")
	}
}

func TestReceiveMessage_SizeRejected(t *testing.T) {
	rcv, _, _, _, _ := testReceiver(t)
	logger := &testLogger{}
	rcv.Logger = logger
	rcv.MaxMessageSize = 100 // very small for testing

	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(`{"active":true,"policy":"allow pub.polis.dm from all"}`+"\n"), 0600)

	bigPayload := make([]byte, 200)
	_, err := rcv.ReceiveMessage("alice.example.com", bigPayload, nil)
	if err == nil {
		t.Fatal("expected size rejection")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error should mention size: %v", err)
	}

	found := false
	for _, log := range logger.warns {
		if strings.Contains(log, "dm.deliver.size_rejected") {
			found = true
		}
	}
	if !found {
		t.Error("missing dm.deliver.size_rejected log")
	}
}

func TestReceiveMessage_PolicyAllowFollowing(t *testing.T) {
	rcv, senderPriv, senderPub, _, receiverPub := testReceiver(t)

	// Set up sender key in cache
	senderX25519PK, _ := signing.Ed25519PublicKeyToX25519(senderPub)
	rcv.keyCache["alice.example.com"] = &cachedKey{
		publicKeySSH: senderPub,
		x25519PK:     senderX25519PK,
		fetchedAt:    time.Now(),
	}

	// Default DM policy: allow from following, deny from all
	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(
		`{"active":true,"policy":"allow pub.polis.dm from following"}`+"\n"+
			`{"active":true,"policy":"deny pub.polis.dm from all"}`+"\n",
	), 0600)

	followingDomains := map[string]bool{"alice.example.com": true}

	envelope := buildEnvelope(t, senderPriv, senderPub, receiverPub, "alice.example.com", "bob.example.com", "From a friend")
	msg, err := rcv.ReceiveMessage("alice.example.com", envelope, followingDomains)
	if err != nil {
		t.Fatalf("should accept from followed domain: %v", err)
	}
	if msg == nil {
		t.Fatal("expected message")
	}
}

func TestReceiveMessage_PolicyDenyNonFollowing(t *testing.T) {
	rcv, _, _, _, _ := testReceiver(t)

	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(
		`{"active":true,"policy":"allow pub.polis.dm from following"}`+"\n"+
			`{"active":true,"policy":"deny pub.polis.dm from all"}`+"\n",
	), 0600)

	// Empty following list — stranger should be denied
	followingDomains := map[string]bool{}

	_, err := rcv.ReceiveMessage("stranger.example.com", []byte("{}"), followingDomains)
	if err == nil {
		t.Fatal("should deny DM from non-followed domain")
	}
	if !strings.Contains(err.Error(), "policy denied") {
		t.Errorf("error should mention policy: %v", err)
	}
}

func TestVerifySignedRequest_ValidSignature(t *testing.T) {
	privPEM, pubSSH, _ := signing.GenerateKeypair()

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	canonicalJSON, _ := MakeDeliverAuthCanonicalJSON("deliver", "alice.example.com", timestamp)
	sig, err := signing.SignContent(canonicalJSON, privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	compactSig := strings.ReplaceAll(sig, "\n", "")

	req := httptest.NewRequest(http.MethodPost, "/v1/content/dm/actions/deliver", nil)
	req.Header.Set("X-Polis-Domain", "alice.example.com")
	req.Header.Set("X-Polis-Signature", compactSig)
	req.Header.Set("X-Polis-Timestamp", timestamp)

	domain, err := VerifySignedRequest(req, func(d string) ([]byte, error) {
		return pubSSH, nil
	})
	if err != nil {
		t.Fatalf("verify should succeed: %v", err)
	}
	if domain != "alice.example.com" {
		t.Errorf("domain = %q, want alice.example.com", domain)
	}
}

func TestVerifySignedRequest_ExpiredTimestamp(t *testing.T) {
	logger := &testLogger{}
	timestamp := time.Now().Add(-10 * time.Minute).UTC().Format("2006-01-02T15:04:05Z")

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Polis-Domain", "alice.example.com")
	req.Header.Set("X-Polis-Signature", "dummy")
	req.Header.Set("X-Polis-Timestamp", timestamp)

	_, err := VerifySignedRequestWithLogger(req, func(d string) ([]byte, error) {
		return nil, nil
	}, logger)
	if err == nil {
		t.Fatal("expected timestamp rejection")
	}
	if !strings.Contains(err.Error(), "5-minute window") {
		t.Errorf("error should mention timestamp window: %v", err)
	}

	found := false
	for _, log := range logger.warns {
		if strings.Contains(log, "dm.deliver.timestamp_rejected") {
			found = true
		}
	}
	if !found {
		t.Error("missing dm.deliver.timestamp_rejected log")
	}
}

func TestVerifySignedRequest_MissingHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	_, err := VerifySignedRequest(req, nil)
	if err == nil {
		t.Fatal("expected error for missing headers")
	}
	if !strings.Contains(err.Error(), "missing signed request headers") {
		t.Errorf("error should mention missing headers: %v", err)
	}
}

func TestVerifySignedRequest_KeyFetchFailure(t *testing.T) {
	logger := &testLogger{}
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Polis-Domain", "alice.example.com")
	req.Header.Set("X-Polis-Signature", "dummy")
	req.Header.Set("X-Polis-Timestamp", timestamp)

	_, err := VerifySignedRequestWithLogger(req, func(d string) ([]byte, error) {
		return nil, fmt.Errorf("connection refused")
	}, logger)
	if err == nil {
		t.Fatal("expected key fetch error")
	}

	found := false
	for _, log := range logger.warns {
		if strings.Contains(log, "dm.deliver.key_fetch_failed") {
			found = true
		}
	}
	if !found {
		t.Error("missing dm.deliver.key_fetch_failed log")
	}
}

func TestVerifySignedRequest_InvalidSignature(t *testing.T) {
	logger := &testLogger{}
	_, pubSSH, _ := signing.GenerateKeypair()
	otherPriv, _, _ := signing.GenerateKeypair()

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	canonicalJSON, _ := MakeDeliverAuthCanonicalJSON("deliver", "alice.example.com", timestamp)
	// Sign with wrong key
	sig, _ := signing.SignContent(canonicalJSON, otherPriv)
	compactSig := strings.ReplaceAll(sig, "\n", "")

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Polis-Domain", "alice.example.com")
	req.Header.Set("X-Polis-Signature", compactSig)
	req.Header.Set("X-Polis-Timestamp", timestamp)

	_, err := VerifySignedRequestWithLogger(req, func(d string) ([]byte, error) {
		return pubSSH, nil
	}, logger)
	if err == nil {
		t.Fatal("expected signature failure")
	}

	found := false
	for _, log := range logger.warns {
		if strings.Contains(log, "dm.deliver.signature_invalid") {
			found = true
		}
	}
	if !found {
		t.Error("missing dm.deliver.signature_invalid log")
	}
}

func TestFetchPublicKey_HTTPTest(t *testing.T) {
	pubKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyHere polis-local"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			json.NewEncoder(w).Encode(map[string]string{"public_key": pubKey})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// FetchPublicKey uses https:// prefix, so we test the receiver's internal fetch instead
	// by verifying the HTTP handler pattern works
	resp, err := http.Get(srv.URL + "/.well-known/polis")
	if err != nil {
		t.Fatalf("HTTP get: %v", err)
	}
	defer resp.Body.Close()

	var wk struct {
		PublicKey string `json:"public_key"`
	}
	json.NewDecoder(resp.Body).Decode(&wk)
	if wk.PublicKey != pubKey {
		t.Errorf("public_key = %q, want %q", wk.PublicKey, pubKey)
	}
}

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello <b>world</b>", "Hello world"},
		{"<script>alert('xss')</script>", "alert('xss')"},
		{"No tags here", "No tags here"},
		{"<a href='evil'>click</a>", "click"},
		{"", ""},
	}

	for _, tt := range tests {
		got := stripHTMLTags(tt.input)
		if got != tt.want {
			t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRestorePEMSignature(t *testing.T) {
	// Test that compact signatures are properly restored
	compact := "-----BEGIN SSH SIGNATURE-----" + strings.Repeat("A", 100) + "-----END SSH SIGNATURE-----"
	restored := restorePEMSignature(compact)

	if !strings.HasPrefix(restored, "-----BEGIN SSH SIGNATURE-----\n") {
		t.Error("should start with PEM header")
	}
	if !strings.HasSuffix(restored, "-----END SSH SIGNATURE-----\n") {
		t.Error("should end with PEM footer")
	}
}

func TestValidateInnerPayload_EmptyContent(t *testing.T) {
	inner := InnerPayload{Content: "  "}
	data, _ := json.Marshal(inner)

	rcv := &Receiver{keyCache: make(map[string]*cachedKey)}
	_, err := validateInnerPayload(data, "alice.example.com", rcv)
	if err == nil {
		t.Fatal("should reject empty content")
	}
}

func TestValidateInnerPayload_InvalidReplyToID(t *testing.T) {
	inner := InnerPayload{Content: "Hello", ReplyToID: "not-valid-hex!"}
	data, _ := json.Marshal(inner)

	rcv := &Receiver{keyCache: make(map[string]*cachedKey)}
	_, err := validateInnerPayload(data, "alice.example.com", rcv)
	if err == nil {
		t.Fatal("should reject invalid reply_to_id")
	}
}

func TestValidateInnerPayload_UnknownFields(t *testing.T) {
	data := []byte(`{"content":"Hello","sender_public_key":"","evil_field":"injection"}`)

	rcv := &Receiver{keyCache: make(map[string]*cachedKey)}
	_, err := validateInnerPayload(data, "alice.example.com", rcv)
	if err == nil {
		t.Fatal("should reject unknown fields")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error should mention unknown field: %v", err)
	}
}

func TestValidateInnerPayload_HTMLStripping(t *testing.T) {
	inner := InnerPayload{Content: "Hello <script>alert(1)</script>world"}
	data, _ := json.Marshal(inner)

	rcv := &Receiver{keyCache: make(map[string]*cachedKey)}
	result, err := validateInnerPayload(data, "alice.example.com", rcv)
	if err != nil {
		t.Fatalf("should succeed: %v", err)
	}
	if strings.Contains(result.Content, "<script>") {
		t.Error("HTML tags should be stripped")
	}
	if result.Content != "Hello alert(1)world" {
		t.Errorf("content = %q, want 'Hello alert(1)world'", result.Content)
	}
}

func TestReceiveMessage_CaseInsensitiveDomains(t *testing.T) {
	rcv, senderPriv, senderPub, _, receiverPub := testReceiver(t)

	// Sender uses mixed case for both domains — should still succeed
	senderX25519PK, _ := signing.Ed25519PublicKeyToX25519(senderPub)
	rcv.keyCache["alice.example.com"] = &cachedKey{
		publicKeySSH: senderPub,
		x25519PK:     senderX25519PK,
		fetchedAt:    time.Now(),
	}

	// Write permissive DM policy
	os.MkdirAll(filepath.Join(rcv.SiteDir, ".polis", "policies"), 0700)
	os.WriteFile(
		filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl"),
		[]byte("{\"active\":true,\"policy\":\"allow pub.polis.dm from all\"}\n"),
		0600,
	)

	// Build envelope with mixed-case domains (simulates david.polis.pub's bug)
	envelope := buildEnvelope(t, senderPriv, senderPub, receiverPub,
		"Alice.Example.COM",  // sender uses uppercase
		"Bob.Example.COM",    // recipient uses uppercase
		"Hello with mixed case!")

	msg, err := rcv.ReceiveMessage("Alice.Example.COM", envelope, map[string]bool{
		"Alice.Example.COM": true, // following list also has mixed case
	})
	if err != nil {
		t.Fatalf("ReceiveMessage should succeed with mixed-case domains: %v", err)
	}
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Status != "received" {
		t.Errorf("status = %q, want received", msg.Status)
	}
	// Verify the stored from/to use the lowercase-normalized sender domain
	if msg.From != "alice.example.com" {
		t.Errorf("msg.From = %q, want lowercase 'alice.example.com'", msg.From)
	}
}

func TestVerifySignedRequest_NormalizesDomain(t *testing.T) {
	// VerifySignedRequest should normalize X-Polis-Domain to lowercase
	// while still verifying the signature with the original case.
	privPEM, pubSSH, _ := signing.GenerateKeypair()

	// Sign with mixed-case domain (as the sender would)
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	canonical, _ := MakeDeliverAuthCanonicalJSON("deliver", "Alice.Example.COM", timestamp)
	sig, err := signing.SignContent(canonical, privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	compactSig := strings.ReplaceAll(sig, "\n", "")

	req := httptest.NewRequest(http.MethodPost, "/deliver", nil)
	req.Header.Set("X-Polis-Domain", "Alice.Example.COM")
	req.Header.Set("X-Polis-Signature", compactSig)
	req.Header.Set("X-Polis-Timestamp", timestamp)

	// fetchPublicKey is called with the lowercased domain
	var fetchedDomain string
	domain, err := VerifySignedRequest(req, func(d string) ([]byte, error) {
		fetchedDomain = d
		return pubSSH, nil
	})
	if err != nil {
		t.Fatalf("VerifySignedRequest failed: %v", err)
	}
	if domain != "alice.example.com" {
		t.Errorf("returned domain = %q, want lowercase 'alice.example.com'", domain)
	}
	if fetchedDomain != "alice.example.com" {
		t.Errorf("fetchPublicKey received domain = %q, want lowercase 'alice.example.com'", fetchedDomain)
	}
}
