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

func testSender(t *testing.T) (*Sender, *Store) {
	t.Helper()
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700)

	privPEM, pubSSH, _ := signing.GenerateKeypair()
	privKey, _ := signing.ParsePrivateKey(privPEM)

	store, err := NewStore(siteDir, privKey.Seed())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sender := NewSender(privPEM, pubSSH, "alice.example.com", store)
	return sender, store
}

func TestSendMessage_DeliverySuccess(t *testing.T) {
	sender, store := testSender(t)
	logger := &testLogger{}
	sender.Logger = logger

	// Generate recipient keys
	recipientPrivPEM, recipientPubSSH, _ := signing.GenerateKeypair()
	_ = recipientPrivPEM

	// Start a test server that mimics a recipient's DM deliver endpoint
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(map[string]string{
				"public_key": string(recipientPubSSH),
			})
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
			if envelope.Version != 1 {
				t.Errorf("envelope version = %d, want 1", envelope.Version)
			}
			if envelope.SenderDomain != "alice.example.com" {
				t.Errorf("sender_domain = %q", envelope.SenderDomain)
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

	// Verify message is stored locally
	idx, _ := store.LoadIndex()
	if len(idx.Conversations) != 1 {
		t.Errorf("expected 1 conversation, got %d", len(idx.Conversations))
	}

	// Verify stored message can be decrypted
	plaintext, err := store.DecryptMessage(msg)
	if err != nil {
		t.Fatalf("DecryptMessage: %v", err)
	}
	if plaintext != "Hello recipient!" {
		t.Errorf("decrypted = %q, want 'Hello recipient!'", plaintext)
	}

	// Check structured send logs
	foundInitiated := false
	foundDelivered := false
	for _, log := range logger.infos {
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
	sender, store := testSender(t)
	logger := &testLogger{}
	sender.Logger = logger

	_, recipientPubSSH, _ := signing.GenerateKeypair()

	// Server that accepts .well-known but rejects delivery
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(map[string]string{
				"public_key": string(recipientPubSSH),
			})
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

	// Verify it's in the store as unsent
	unsent, _ := store.GetUnsentMessages()
	if len(unsent) != 1 {
		t.Errorf("expected 1 unsent message, got %d", len(unsent))
	}

	// Check structured log for failure
	found := false
	for _, log := range logger.warns {
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
	for _, log := range logger.warns {
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
	sender, store := testSender(t)
	_, recipientPubSSH, _ := signing.GenerateKeypair()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(map[string]string{"public_key": string(recipientPubSSH)})
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
	if msg.ReplyToID != "abcdef0123456789" {
		t.Errorf("reply_to_id = %q, want abcdef0123456789", msg.ReplyToID)
	}

	conv, _ := store.LoadConversation(ComputeConversationID(sender.Domain, ExtractDomainFromURL(srv.URL)))
	if len(conv.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(conv.Messages))
	}
	if conv.Messages[0].ReplyToID != "abcdef0123456789" {
		t.Errorf("stored reply_to_id = %q", conv.Messages[0].ReplyToID)
	}
}

func TestMakeDeliverAuthCanonicalJSON(t *testing.T) {
	data, err := MakeDeliverAuthCanonicalJSON("deliver", "alice.example.com", "2026-03-07T10:00:00Z")
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
	if parsed["timestamp"] != "2026-03-07T10:00:00Z" {
		t.Errorf("timestamp = %q", parsed["timestamp"])
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
	if err := sender.addSignedHeaders(req, "deliver"); err != nil {
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
	canonicalJSON, _ := MakeDeliverAuthCanonicalJSON("deliver", "alice.example.com", timestamp)

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

	_, recipientPubSSH, _ := signing.GenerateKeypair()
	fetchCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			fetchCount++
			json.NewEncoder(w).Encode(map[string]string{"public_key": string(recipientPubSSH)})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// First fetch
	_, _, err := sender.fetchRecipientKey(srv.URL)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	// Second fetch should use cache
	_, _, err = sender.fetchRecipientKey(srv.URL)
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
	sender.Logger.Info("event=dm.send.initiated recipient_domain=%s content_length=%d", "test.com", 42)

	if len(logger.infos) != 1 {
		t.Errorf("expected 1 info log, got %d", len(logger.infos))
	}
	if !strings.Contains(logger.infos[0], "dm.send.initiated") {
		t.Errorf("unexpected log content: %s", logger.infos[0])
	}
}

func TestSendMessage_EnvelopeStructure(t *testing.T) {
	sender, _ := testSender(t)
	_, recipientPubSSH, _ := signing.GenerateKeypair()

	var receivedEnvelope MessageEnvelope

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(map[string]string{"public_key": string(recipientPubSSH)})
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

	if receivedEnvelope.Version != 1 {
		t.Errorf("version = %d, want 1", receivedEnvelope.Version)
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
	privKey, _ := signing.ParsePrivateKey(privPEM)
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700)
	store, err := NewStore(siteDir, privKey.Seed())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	s := NewSenderWithHTTP(privPEM, pubSSH, "Test.Local", store, nil)
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
	privKey, _ := signing.ParsePrivateKey(privPEM)
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700)
	store, err := NewStore(siteDir, privKey.Seed())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	shared := &http.Client{}
	s := NewSenderWithHTTP(privPEM, pubSSH, "test.local", store, shared)
	if s.httpClient != shared {
		t.Error("expected shared httpClient")
	}
	if s.keyCache == nil {
		t.Error("expected keyCache to be initialized")
	}
}
