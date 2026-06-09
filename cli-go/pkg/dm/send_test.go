package dm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func testSender(t *testing.T) (*Sender, string) {
	t.Helper()
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700)

	privPEM, pubSSH, _ := signing.GenerateKeypair()

	// Provision the sender's own keyring (bootstrap epoch) so SendMessage can resolve its
	// current-epoch sealing key (server_dek) and store the sent copy in the mailbox.
	kr := &Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		t.Fatalf("bootstrap epoch: %v", err)
	}
	if err := kr.Save(DMDir(siteDir)); err != nil {
		t.Fatalf("save keyring: %v", err)
	}

	sender := NewSender(privPEM, pubSSH, "alice.example.com", siteDir)
	return sender, siteDir
}

// senderDEKs returns the sender's available epoch DEKs, for reading back its own sent
// copies from the mailbox in assertions.
func senderDEKs(t *testing.T, s *Sender) map[int][32]byte {
	t.Helper()
	kr, err := LoadKeyring(DMDir(s.SiteDir))
	if err != nil {
		t.Fatal(err)
	}
	deks, err := AvailableDEKs(kr)
	if err != nil {
		t.Fatal(err)
	}
	return deks
}

// recipientWellKnown returns a .well-known/polis map advertising a fresh recipient
// identity key plus a valid, identity-signed public_key_messages block. Phase 2 requires
// the block, and the sender verifies it before encrypting, so every test recipient must
// serve one.
func recipientWellKnown(t *testing.T) map[string]interface{} {
	t.Helper()
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	kr := &Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	block, err := BuildMessagesKeyBlock(kr, privPEM)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]interface{}{
		"public_key":          string(pubSSH),
		"public_key_messages": block,
	}
}

func TestSendMessage_DeliverySuccess(t *testing.T) {
	sender, _ := testSender(t)
	logger := &testLogger{}
	sender.Logger = logger

	// Start a test server that mimics a recipient's DM deliver endpoint
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(recipientWellKnown(t))
		case "/v1/content/dm/actions/deliver":
			// Verify headers are present
			if r.Header.Get("X-Polis-Domain") == "" {
				t.Error("missing X-Polis-Domain header")
			}
			if r.Header.Get("X-Polis-Signature") == "" {
				t.Error("missing X-Polis-Signature header")
			}
			if r.Header.Get("X-Polis-Timestamp") == "" {
				t.Error("missing X-Polis-Timestamp header")
			}

			// Verify envelope is valid JSON
			var envelope MessageEnvelope
			if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
				t.Errorf("invalid envelope: %v", err)
				http.Error(w, "bad envelope", 400)
				return
			}
			if envelope.Version != MessageEnvelopeVersion {
				t.Errorf("envelope version = %d, want %d", envelope.Version, MessageEnvelopeVersion)
			}
			if envelope.SenderDomain != "alice.example.com" {
				t.Errorf("sender_domain = %q", envelope.SenderDomain)
			}
			// New wire fields: box_pub (to open) + recipient_epoch (bootstrap = 0).
			if envelope.BoxPub == "" {
				t.Error("envelope missing box_pub")
			}
			if envelope.RecipientEpoch != 0 {
				t.Errorf("recipient_epoch = %d, want 0 (bootstrap)", envelope.RecipientEpoch)
			}

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	msg, err := sender.SendMessage(srv.URL, "Hello recipient!", "")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Status != "sent" {
		t.Errorf("status = %q, want sent", msg.Status)
	}

	// Verify the sent copy is stored in the mailbox and opens with our own epoch DEK.
	peer := ExtractDomainFromURL(srv.URL)
	conv, err := NewMailbox(DMDir(sender.SiteDir)).ReadConversation(peer, senderDEKs(t, sender))
	if err != nil {
		t.Fatalf("read conversation: %v", err)
	}
	if len(conv) != 1 {
		t.Fatalf("expected 1 stored message, got %d", len(conv))
	}
	if conv[0].Locked {
		t.Fatal("our own sent copy should be readable (sealed to our epoch key)")
	}
	if conv[0].Plaintext != "Hello recipient!" {
		t.Errorf("decrypted = %q, want 'Hello recipient!'", conv[0].Plaintext)
	}

	// Check structured send logs
	foundInitiated := false
	foundDelivered := false
	for _, log := range logger.events {
		if strings.Contains(log, "dm.send.initiated") {
			foundInitiated = true
		}
		if strings.Contains(log, "dm.send.delivered") {
			foundDelivered = true
		}
	}
	if !foundInitiated {
		t.Error("missing dm.send.initiated log")
	}
	if !foundDelivered {
		t.Error("missing dm.send.delivered log")
	}
}

func TestSendMessage_DeliveryFailure_SavesUnsent(t *testing.T) {
	sender, _ := testSender(t)
	logger := &testLogger{}
	sender.Logger = logger

	// Server that accepts .well-known but rejects delivery
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(recipientWellKnown(t))
		case "/v1/content/dm/actions/deliver":
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	msg, err := sender.SendMessage(srv.URL, "Will fail delivery", "")
	// Should return both a message AND an error
	if msg == nil {
		t.Fatal("expected message to be saved even on delivery failure")
	}
	if err == nil {
		t.Fatal("expected delivery error")
	}
	if msg.Status != "unsent" {
		t.Errorf("status = %q, want unsent", msg.Status)
	}

	// Verify it's in the mailbox as unsent
	unsent, _ := NewMailbox(DMDir(sender.SiteDir)).UnsentMessages()
	if len(unsent) != 1 {
		t.Errorf("expected 1 unsent message, got %d", len(unsent))
	}

	// Check structured log for failure
	found := false
	for _, log := range logger.events {
		if strings.Contains(log, "dm.send.failed") {
			found = true
		}
	}
	if !found {
		t.Error("missing dm.send.failed log")
	}
}

func TestSendMessage_KeyFetchFailure(t *testing.T) {
	sender, _ := testSender(t)
	logger := &testLogger{}
	sender.Logger = logger

	// Server that returns 404 for .well-known
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := sender.SendMessage(srv.URL, "Hello", "")
	if err == nil {
		t.Fatal("expected key fetch error")
	}

	found := false
	for _, log := range logger.events {
		if strings.Contains(log, "dm.send.key_fetch_failed") {
			found = true
		}
	}
	if !found {
		t.Error("missing dm.send.key_fetch_failed log")
	}
}

func TestSendMessage_InvalidRecipientURL(t *testing.T) {
	sender, _ := testSender(t)
	_, err := sender.SendMessage("not-a-url", "Hello", "")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestSendMessage_WithReplyToID(t *testing.T) {
	sender, _ := testSender(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(recipientWellKnown(t))
		case "/v1/content/dm/actions/deliver":
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	msg, err := sender.SendMessage(srv.URL, "Reply message", "abcdef0123456789")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if msg.ReplyTo != "abcdef0123456789" {
		t.Errorf("reply_to = %q, want abcdef0123456789", msg.ReplyTo)
	}

	peer := ExtractDomainFromURL(srv.URL)
	conv, _ := NewMailbox(DMDir(sender.SiteDir)).ReadConversation(peer, senderDEKs(t, sender))
	if len(conv) != 1 {
		t.Fatalf("expected 1 message, got %d", len(conv))
	}
	if conv[0].ReplyTo != "abcdef0123456789" {
		t.Errorf("stored reply_to = %q", conv[0].ReplyTo)
	}
}

func TestMakeDeliverAuthCanonicalJSON(t *testing.T) {
	data, err := MakeDeliverAuthCanonicalJSON("deliver", "alice.example.com", "bob.example.com", "2026-03-07T10:00:00Z", "BODYHASH=")
	if err != nil {
		t.Fatalf("MakeDeliverAuthCanonicalJSON: %v", err)
	}

	var parsed map[string]string
	json.Unmarshal(data, &parsed)

	if parsed["action"] != "deliver" {
		t.Errorf("action = %q", parsed["action"])
	}
	if parsed["domain"] != "alice.example.com" {
		t.Errorf("domain = %q", parsed["domain"])
	}
	if parsed["recipient"] != "bob.example.com" {
		t.Errorf("recipient = %q", parsed["recipient"])
	}
	if parsed["body_sha256"] != "BODYHASH=" {
		t.Errorf("body_sha256 = %q", parsed["body_sha256"])
	}
	if parsed["timestamp"] != "2026-03-07T10:00:00Z" {
		t.Errorf("timestamp = %q", parsed["timestamp"])
	}

	// Frozen byte order (DM-2): field order must be action, body_sha256, domain, recipient,
	// timestamp — cross-implementation interop depends on it.
	const want = `{"action":"deliver","body_sha256":"BODYHASH=","domain":"alice.example.com","recipient":"bob.example.com","timestamp":"2026-03-07T10:00:00Z"}`
	if string(data) != want {
		t.Errorf("canonical bytes = %s, want %s", data, want)
	}
}

func TestExtractDomainFromURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://alice.example.com", "alice.example.com"},
		{"https://alice.example.com/path", "alice.example.com"},
		{"https://alice.example.com:8080", "alice.example.com"},
		{"http://localhost", "localhost"},
		{"", ""},
	}

	for _, tt := range tests {
		got := ExtractDomainFromURL(tt.input)
		if got != tt.want {
			t.Errorf("ExtractDomainFromURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSender_SignedHeaders(t *testing.T) {
	sender, _ := testSender(t)

	req, _ := http.NewRequest(http.MethodPost, "https://bob.example.com/v1/content/dm/actions/deliver", nil)
	if err := sender.addSignedHeaders(req, "deliver", "bob.example.com", nil); err != nil {
		t.Fatalf("addSignedHeaders: %v", err)
	}

	if req.Header.Get("X-Polis-Domain") != "alice.example.com" {
		t.Errorf("X-Polis-Domain = %q", req.Header.Get("X-Polis-Domain"))
	}
	if req.Header.Get("X-Polis-Signature") == "" {
		t.Error("X-Polis-Signature should be set")
	}
	if req.Header.Get("X-Polis-Timestamp") == "" {
		t.Error("X-Polis-Timestamp should be set")
	}

	// Verify the signature is valid
	pubSSH := sender.PublicKeySSH
	timestamp := req.Header.Get("X-Polis-Timestamp")
	canonicalJSON, _ := MakeDeliverAuthCanonicalJSON("deliver", "alice.example.com", "bob.example.com", timestamp, bodyDigest(nil))

	compactSig := req.Header.Get("X-Polis-Signature")
	fullSig := restorePEMSignature(compactSig)
	valid, err := signing.VerifySignature(canonicalJSON, pubSSH, fullSig)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !valid {
		t.Error("signature should be valid")
	}
}

func TestSender_KeyCache(t *testing.T) {
	sender, _ := testSender(t)

	fetchCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			fetchCount++
			json.NewEncoder(w).Encode(recipientWellKnown(t))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// First fetch
	_, _, _, err := sender.fetchRecipientKey(srv.URL)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	// Second fetch should use cache
	_, _, _, err = sender.fetchRecipientKey(srv.URL)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	if fetchCount != 1 {
		t.Errorf("expected 1 HTTP fetch (cached), got %d", fetchCount)
	}
}

func TestSender_LoggerInterface(t *testing.T) {
	sender, _ := testSender(t)
	logger := &testLogger{}
	sender.Logger = logger

	// Just verify the logger is called — don't need a real server for this
	sender.Logger.Event("dm.send.initiated", map[string]any{"recipient_domain": "test.com", "content_length": 42})

	if len(logger.events) != 1 {
		t.Errorf("expected 1 event, got %d", len(logger.events))
	}
	if !strings.Contains(logger.events[0], "dm.send.initiated") {
		t.Errorf("unexpected event content: %s", logger.events[0])
	}
}

func TestSendMessage_EnvelopeStructure(t *testing.T) {
	sender, _ := testSender(t)

	var receivedEnvelope MessageEnvelope

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(recipientWellKnown(t))
		case "/v1/content/dm/actions/deliver":
			json.NewDecoder(r.Body).Decode(&receivedEnvelope)
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	_, err := sender.SendMessage(srv.URL, "Test content", "")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if receivedEnvelope.Version != MessageEnvelopeVersion {
		t.Errorf("version = %d, want %d", receivedEnvelope.Version, MessageEnvelopeVersion)
	}
	if receivedEnvelope.SenderDomain != "alice.example.com" {
		t.Errorf("sender_domain = %q", receivedEnvelope.SenderDomain)
	}
	if receivedEnvelope.EncryptedContent == "" {
		t.Error("encrypted_content should not be empty")
	}
	if receivedEnvelope.Nonce == "" {
		t.Error("nonce should not be empty")
	}
	if receivedEnvelope.Timestamp == "" {
		t.Error("timestamp should not be empty")
	}
	// RecipientDomain is extracted from the server URL hostname
	if receivedEnvelope.RecipientDomain == "" {
		// httptest URLs use 127.0.0.1, so this is expected
		fmt.Printf("Note: recipient_domain is %q (expected for httptest)\n", receivedEnvelope.RecipientDomain)
	}
}

// A recipient that publishes only an identity public_key (no public_key_messages) is a
// pre-DM-encryption site. Phase 2 (decision (a)) refuses to send — never silently falls
// back to the identity-derived key.
func TestSendMessage_RequiresPublicKeyMessages(t *testing.T) {
	sender, _ := testSender(t)
	_, pubSSH, _ := signing.GenerateKeypair()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			json.NewEncoder(w).Encode(map[string]string{"public_key": string(pubSSH)})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, err := sender.SendMessage(srv.URL, "hi", ""); err == nil {
		t.Fatal("expected refusal when recipient has no public_key_messages")
	}
}

// A public_key_messages block signed by a key other than the advertised identity key is a
// key-swap attempt. The sender must refuse rather than encrypt to the unverified key.
func TestSendMessage_RejectsInvalidMessagesKeySignature(t *testing.T) {
	sender, _ := testSender(t)

	// Identity key A is advertised, but the block is signed by an unrelated key B.
	_, pubSSHA, _ := signing.GenerateKeypair()
	privPEMB, _, _ := signing.GenerateKeypair()
	kr := &Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	block, err := BuildMessagesKeyBlock(kr, privPEMB)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"public_key":          string(pubSSHA),
				"public_key_messages": block,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, err := sender.SendMessage(srv.URL, "hi", ""); err == nil {
		t.Fatal("expected refusal when public_key_messages signature is invalid")
	}
}

func TestExtractDomainFromURL_LowercaseNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://Vdibart.polis.pub/", "vdibart.polis.pub"},
		{"https://Alice.Example.COM", "alice.example.com"},
		{"https://bob.example.com", "bob.example.com"},
		{"https://UPPER.CASE.NET/path", "upper.case.net"},
		{"", ""},
		{"not-a-url", ""},
	}

	for _, tc := range tests {
		got := ExtractDomainFromURL(tc.input)
		if got != tc.want {
			t.Errorf("ExtractDomainFromURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNewSenderWithHTTP_Nil(t *testing.T) {
	privPEM, pubSSH, _ := signing.GenerateKeypair()
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700)
	s := NewSenderWithHTTP(privPEM, pubSSH, "Test.Local", siteDir, nil)
	if s == nil {
		t.Fatal("returned nil")
	}
	if s.httpClient == nil {
		t.Error("expected fallback httpClient")
	}
	if s.Domain != "test.local" {
		t.Errorf("Domain = %q, want test.local (lowercased)", s.Domain)
	}
}

func TestNewSenderWithHTTP_Shared(t *testing.T) {
	privPEM, pubSSH, _ := signing.GenerateKeypair()
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700)
	shared := &http.Client{}
	s := NewSenderWithHTTP(privPEM, pubSSH, "test.local", siteDir, shared)
	if s.httpClient != shared {
		t.Error("expected shared httpClient")
	}
	if s.keyCache == nil {
		t.Error("expected keyCache to be initialized")
	}
}
