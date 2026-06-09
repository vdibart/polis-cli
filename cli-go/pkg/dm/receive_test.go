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

// testLogger captures emitted events as "action k=v ..." strings for assertion.
type testLogger struct {
	events []string
}

func (l *testLogger) Event(action string, fields map[string]any) {
	parts := []string{action}
	for k, v := range fields {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	l.events = append(l.events, strings.Join(parts, " "))
}

func testReceiver(t *testing.T) (*Receiver, []byte, []byte, []byte, []byte) {
	t.Helper()
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis", "policies"), 0700)

	// Generate receiver keypair
	privPEM, pubSSH, _ := signing.GenerateKeypair()

	// Generate sender keypair
	senderPriv, senderPub, _ := signing.GenerateKeypair()

	rl := NewRateLimiter(10, 100)
	rcv := NewReceiver(privPEM, pubSSH, "bob.example.com", siteDir, rl)

	// DM-1: inject a fake sender-key fetcher so box_pub authentication passes for the
	// canonical test sender (epoch 0, key derived from senderPriv — matching buildEnvelope).
	// Tests exercising the failure path override this.
	rcv.FetchSenderKeys = fakeSenderKeys(t, senderPriv, senderPub, 0)

	// Provision the receiver's DM keyring (bootstrap epoch) so deliveries can validate
	// recipient_epoch and there's a messages key to seal to.
	kr := &Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		t.Fatalf("bootstrap epoch: %v", err)
	}
	if err := kr.Save(DMDir(siteDir)); err != nil {
		t.Fatalf("save keyring: %v", err)
	}

	return rcv, senderPriv, senderPub, privPEM, pubSSH
}

// fakeSenderKeys returns a Receiver.FetchSenderKeys stub that advertises, for the given
// sender_epoch, the X25519 messages key derived from senderPrivPEM (matching buildEnvelope's
// box_pub), signed by the sender's identity key — i.e. an authentic published block.
func fakeSenderKeys(t *testing.T, senderPrivPEM, senderPubSSH []byte, epoch int) func(string) ([]byte, *MessagesKeyBlock, error) {
	t.Helper()
	senderSK, err := signing.Ed25519PrivateKeyToX25519(senderPrivPEM)
	if err != nil {
		t.Fatalf("sender SK: %v", err)
	}
	senderPK, err := publicFromDEK(senderSK[:])
	if err != nil {
		t.Fatalf("sender PK: %v", err)
	}
	keyB64 := base64.StdEncoding.EncodeToString(senderPK[:])
	sig, err := signing.SignContent(canonicalMessagesKey(epoch, keyB64), senderPrivPEM)
	if err != nil {
		t.Fatalf("sign messages key: %v", err)
	}
	block := &MessagesKeyBlock{Current: MessagesKeyEntry{Epoch: epoch, Key: keyB64, Sig: sig}}
	return func(string) ([]byte, *MessagesKeyBlock, error) { return senderPubSSH, block, nil }
}

// buildEnvelope creates a valid wire envelope for testing: the inner payload is box-sealed
// to the receiver's current epoch messages key (from its keyring), and the envelope carries
// box_pub (the sender's X25519 pubkey) + recipient_epoch, matching the phase-2 wire model.
func buildEnvelope(t *testing.T, rcv *Receiver, senderPrivPEM, senderPubSSH []byte, senderDomain, receiverDomain, content string) []byte {
	t.Helper()

	senderSK, err := signing.Ed25519PrivateKeyToX25519(senderPrivPEM)
	if err != nil {
		t.Fatalf("sender SK: %v", err)
	}
	senderPK, err := publicFromDEK(senderSK[:])
	if err != nil {
		t.Fatalf("sender PK: %v", err)
	}

	// Seal to the receiver's current epoch messages key.
	kr, err := LoadKeyring(DMDir(rcv.SiteDir))
	if err != nil {
		t.Fatalf("load receiver keyring: %v", err)
	}
	cur, err := kr.CurrentEpoch()
	if err != nil {
		t.Fatal(err)
	}
	recvPKBytes, _ := base64.StdEncoding.DecodeString(cur.PublicKeyMessages)
	var receiverPK [32]byte
	copy(receiverPK[:], recvPKBytes)

	ciphertext, nonce, err := Encrypt([]byte(content), &receiverPK, &senderSK)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	envelope := MessageEnvelope{
		Version:          MessageEnvelopeVersion,
		SenderDomain:     senderDomain,
		RecipientDomain:  receiverDomain,
		EncryptedContent: base64.StdEncoding.EncodeToString(ciphertext),
		Nonce:            base64.StdEncoding.EncodeToString(nonce[:]),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		BoxPub:           base64.StdEncoding.EncodeToString(senderPK[:]),
		SenderEpoch:      0,
		RecipientEpoch:   cur.ID,
	}

	data, _ := json.Marshal(envelope)
	return data
}

// TestReceiveMessage_RejectsUnauthenticatedBoxPub is the DM-1 regression: an envelope whose
// box_pub is NOT the sender's published messages key (the impersonation primitive — a third
// party replays a valid signed deliver header but attaches an attacker-generated box_pub and
// a box sealed under it) must be rejected at receive time, before storage.
func TestReceiveMessage_RejectsUnauthenticatedBoxPub(t *testing.T) {
	rcv, senderPriv, senderPub, _, _ := testReceiver(t)
	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(`{"active":true,"policy":"allow pub.polis.dm from all"}`+"\n"), 0600)

	// Forge: attacker mints their own keypair and seals a chosen message to the receiver's
	// key, then sets box_pub to the attacker key (NOT alice's published key).
	attackerPK, attackerSK, err := NewEpochKeypair()
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := LoadKeyring(DMDir(rcv.SiteDir))
	cur, _ := kr.CurrentEpoch()
	recvPKBytes, _ := base64.StdEncoding.DecodeString(cur.PublicKeyMessages)
	var receiverPK [32]byte
	copy(receiverPK[:], recvPKBytes)
	ct, nonce, err := Encrypt([]byte("forged: trust me, this is alice"), &receiverPK, &attackerSK)
	if err != nil {
		t.Fatal(err)
	}
	env := MessageEnvelope{
		Version:          MessageEnvelopeVersion,
		SenderDomain:     "alice.example.com",
		RecipientDomain:  "bob.example.com",
		EncryptedContent: base64.StdEncoding.EncodeToString(ct),
		Nonce:            base64.StdEncoding.EncodeToString(nonce[:]),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		BoxPub:           base64.StdEncoding.EncodeToString(attackerPK[:]),
		SenderEpoch:      0,
		RecipientEpoch:   cur.ID,
	}
	body, _ := json.Marshal(env)

	// FetchSenderKeys returns alice's authentic block (epoch 0); the forged box_pub won't match.
	rcv.FetchSenderKeys = fakeSenderKeys(t, senderPriv, senderPub, 0)

	if _, err := rcv.ReceiveMessage("alice.example.com", body, nil); err == nil {
		t.Fatal("expected rejection of unauthenticated box_pub, got nil error")
	} else if !strings.Contains(err.Error(), "box_pub") {
		t.Fatalf("expected box_pub authentication error, got: %v", err)
	}

	// And nothing was stored.
	msgs, _ := NewMailbox(DMDir(rcv.SiteDir)).LoadMessages("alice.example.com")
	if len(msgs) != 0 {
		t.Fatalf("forged message must not be stored, found %d", len(msgs))
	}
}

func TestReceiveMessage_Success(t *testing.T) {
	rcv, senderPriv, senderPub, _, _ := testReceiver(t)
	logger := &testLogger{}
	rcv.Logger = logger

	// Write policy allowing DMs from all
	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(`{"active":true,"policy":"allow pub.polis.dm from all"}`+"\n"), 0600)

	envelope := buildEnvelope(t, rcv, senderPriv, senderPub, "alice.example.com", "bob.example.com", "Hello Bob!")
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
	if len(logger.events) < 2 {
		t.Errorf("expected at least 2 info logs (received + accepted), got %d", len(logger.events))
	}
	foundReceived := false
	foundAccepted := false
	for _, log := range logger.events {
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

// TestReceiveMessage_StaleEpochRejected ensures a delivery sealed to a non-current epoch
// (a stale cached messages key) is rejected for retry, not stored unreadable — the guard
// that makes clearing the bootstrap server_dek safe.
func TestReceiveMessage_StaleEpochRejected(t *testing.T) {
	rcv, senderPriv, senderPub, _, _ := testReceiver(t)
	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(`{"active":true,"policy":"allow pub.polis.dm from all"}`+"\n"), 0600)

	envelope := buildEnvelope(t, rcv, senderPriv, senderPub, "alice.example.com", "bob.example.com", "stale")
	// Rewrite recipient_epoch to a non-current value.
	var env MessageEnvelope
	json.Unmarshal(envelope, &env)
	env.RecipientEpoch = 999
	patched, _ := json.Marshal(env)

	_, err := rcv.ReceiveMessage("alice.example.com", patched, nil)
	if err == nil {
		t.Fatal("expected rejection of a stale recipient_epoch")
	}
	if !strings.Contains(err.Error(), "stale recipient_epoch") {
		t.Errorf("error = %q, want a stale-epoch refetch message", err.Error())
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
	for _, log := range logger.events {
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
	for _, log := range logger.events {
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
	for _, log := range logger.events {
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
	for _, log := range logger.events {
		if strings.Contains(log, "dm.deliver.size_rejected") {
			found = true
		}
	}
	if !found {
		t.Error("missing dm.deliver.size_rejected log")
	}
}

func TestReceiveMessage_PolicyAllowFollowing(t *testing.T) {
	rcv, senderPriv, senderPub, _, _ := testReceiver(t)

	// Default DM policy: allow from following, deny from all
	policyPath := filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl")
	os.WriteFile(policyPath, []byte(
		`{"active":true,"policy":"allow pub.polis.dm from following"}`+"\n"+
			`{"active":true,"policy":"deny pub.polis.dm from all"}`+"\n",
	), 0600)

	followingDomains := map[string]bool{"alice.example.com": true}

	envelope := buildEnvelope(t, rcv, senderPriv, senderPub, "alice.example.com", "bob.example.com", "From a friend")
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
	canonicalJSON, _ := MakeDeliverAuthCanonicalJSON("deliver", "alice.example.com", "bob.example.com", timestamp, bodyDigest(nil))
	sig, err := signing.SignContent(canonicalJSON, privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	compactSig := strings.ReplaceAll(sig, "\n", "")

	req := httptest.NewRequest(http.MethodPost, "/v1/content/dm/actions/deliver", nil)
	req.Header.Set("X-Polis-Domain", "alice.example.com")
	req.Header.Set("X-Polis-Signature", compactSig)
	req.Header.Set("X-Polis-Timestamp", timestamp)

	domain, err := VerifySignedRequestWithLogger(req, "bob.example.com", nil, func(d string) ([]byte, error) {
		return pubSSH, nil
	}, nil)
	if err != nil {
		t.Fatalf("verify should succeed: %v", err)
	}
	if domain != "alice.example.com" {
		t.Errorf("domain = %q, want alice.example.com", domain)
	}
}

// TestVerifySignedRequest_BindsRecipientAndBody is the DM-2 regression: a signature is
// valid only for the exact recipient domain + body it was signed over. Replaying the same
// headers against a different recipient, or with a swapped body, must fail.
func TestVerifySignedRequest_BindsRecipientAndBody(t *testing.T) {
	privPEM, pubSSH, _ := signing.GenerateKeypair()
	fetch := func(string) ([]byte, error) { return pubSSH, nil }

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	body := []byte(`{"envelope":"contents"}`)
	// Signed for recipient bob.example.com over `body`.
	canonical, _ := MakeDeliverAuthCanonicalJSON("deliver", "alice.example.com", "bob.example.com", timestamp, bodyDigest(body))
	sig, err := signing.SignContent(canonical, privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	compactSig := strings.ReplaceAll(sig, "\n", "")
	mkReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/v1/content/dm/actions/deliver", nil)
		req.Header.Set("X-Polis-Domain", "alice.example.com")
		req.Header.Set("X-Polis-Signature", compactSig)
		req.Header.Set("X-Polis-Timestamp", timestamp)
		return req
	}

	// Correct recipient + body → accepted.
	if _, err := VerifySignedRequestForAction(mkReq(), "deliver", "bob.example.com", body, fetch, nil); err != nil {
		t.Fatalf("authentic request should verify: %v", err)
	}
	// Replayed against a different recipient → rejected (recipient binding).
	if _, err := VerifySignedRequestForAction(mkReq(), "deliver", "carol.example.com", body, fetch, nil); err == nil {
		t.Fatal("replay to a different recipient must fail")
	}
	// Same recipient, swapped body → rejected (body binding).
	if _, err := VerifySignedRequestForAction(mkReq(), "deliver", "bob.example.com", []byte(`{"envelope":"TAMPERED"}`), fetch, nil); err == nil {
		t.Fatal("swapped body must fail")
	}
}

func TestVerifySignedRequest_ExpiredTimestamp(t *testing.T) {
	logger := &testLogger{}
	timestamp := time.Now().Add(-10 * time.Minute).UTC().Format("2006-01-02T15:04:05Z")

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Polis-Domain", "alice.example.com")
	req.Header.Set("X-Polis-Signature", "dummy")
	req.Header.Set("X-Polis-Timestamp", timestamp)

	_, err := VerifySignedRequestWithLogger(req, "bob.example.com", nil, func(d string) ([]byte, error) {
		return nil, nil
	}, logger)
	if err == nil {
		t.Fatal("expected timestamp rejection")
	}
	if !strings.Contains(err.Error(), "freshness window") {
		t.Errorf("error should mention timestamp window: %v", err)
	}

	found := false
	for _, log := range logger.events {
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
	_, err := VerifySignedRequestWithLogger(req, "bob.example.com", nil, nil, nil)
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

	_, err := VerifySignedRequestWithLogger(req, "bob.example.com", nil, func(d string) ([]byte, error) {
		return nil, fmt.Errorf("connection refused")
	}, logger)
	if err == nil {
		t.Fatal("expected key fetch error")
	}

	found := false
	for _, log := range logger.events {
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
	canonicalJSON, _ := MakeDeliverAuthCanonicalJSON("deliver", "alice.example.com", "bob.example.com", timestamp, bodyDigest(nil))
	// Sign with wrong key
	sig, _ := signing.SignContent(canonicalJSON, otherPriv)
	compactSig := strings.ReplaceAll(sig, "\n", "")

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Polis-Domain", "alice.example.com")
	req.Header.Set("X-Polis-Signature", compactSig)
	req.Header.Set("X-Polis-Timestamp", timestamp)

	_, err := VerifySignedRequestWithLogger(req, "bob.example.com", nil, func(d string) ([]byte, error) {
		return pubSSH, nil
	}, logger)
	if err == nil {
		t.Fatal("expected signature failure")
	}

	found := false
	for _, log := range logger.events {
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

func TestReceiveMessage_CaseInsensitiveDomains(t *testing.T) {
	rcv, senderPriv, senderPub, _, _ := testReceiver(t)

	// Write permissive DM policy
	os.MkdirAll(filepath.Join(rcv.SiteDir, ".polis", "policies"), 0700)
	os.WriteFile(
		filepath.Join(rcv.SiteDir, ".polis", "policies", "rules.jsonl"),
		[]byte("{\"active\":true,\"policy\":\"allow pub.polis.dm from all\"}\n"),
		0600,
	)

	// Build envelope with mixed-case domains (simulates david.polis.pub's bug)
	envelope := buildEnvelope(t, rcv, senderPriv, senderPub, "Alice.Example.COM", // sender uses uppercase
		"Bob.Example.COM", // recipient uses uppercase
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
	canonical, _ := MakeDeliverAuthCanonicalJSON("deliver", "Alice.Example.COM", "bob.example.com", timestamp, bodyDigest(nil))
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
	domain, err := VerifySignedRequestWithLogger(req, "bob.example.com", nil, func(d string) ([]byte, error) {
		fetchedDomain = d
		return pubSSH, nil
	}, nil)
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
