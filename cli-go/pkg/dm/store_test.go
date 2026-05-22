package dm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	siteDir := t.TempDir()

	// Create .polis directory
	os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700)

	// NewStore zeroes the seed, so each call needs a fresh copy
	seed := []byte("test-seed-32bytes-for-testing!!")
	store, err := NewStore(siteDir, seed) // seed is zeroed after this
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestStore_SeedZeroedAfterNewStore(t *testing.T) {
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700)

	seed := []byte("test-seed-32bytes-for-testing!!")
	_, err := NewStore(siteDir, seed)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Verify seed has been zeroed
	for i, b := range seed {
		if b != 0 {
			t.Fatalf("seed byte %d is %d, expected 0", i, b)
		}
	}
}

func TestStore_NewCreatesDirectories(t *testing.T) {
	store := testStore(t)

	// Check directories exist
	convPath := filepath.Join(store.baseDir, convDir)
	info, err := os.Stat(convPath)
	if err != nil {
		t.Fatalf("conv dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("conv should be a directory")
	}
}

func TestStore_SaltPersistence(t *testing.T) {
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700)
	seedVal := "test-seed-32bytes-for-testing!!"

	// First store creates salt (seed is zeroed by NewStore)
	seed1 := []byte(seedVal)
	store1, err := NewStore(siteDir, seed1)
	if err != nil {
		t.Fatalf("first NewStore: %v", err)
	}

	// Second store reads same salt, derives same key (fresh seed copy)
	seed2 := []byte(seedVal)
	store2, err := NewStore(siteDir, seed2)
	if err != nil {
		t.Fatalf("second NewStore: %v", err)
	}

	if store1.storageKey != store2.storageKey {
		t.Error("same seed + same salt should produce same storage key")
	}
}

func TestStore_AppendAndDecryptMessage(t *testing.T) {
	store := testStore(t)

	var nonce [24]byte
	copy(nonce[:], []byte("test-nonce-24-bytes!!!!"))

	msg, err := store.AppendMessage(
		"conv123", "bob.example.com", "https://bob.example.com",
		"alice.example.com", "bob.example.com",
		"Hello Bob!", "", "sent", nonce,
	)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	if msg.From != "alice.example.com" {
		t.Errorf("from should be alice, got %s", msg.From)
	}
	if msg.Status != "sent" {
		t.Errorf("status should be sent, got %s", msg.Status)
	}

	// Decrypt the stored message
	plaintext, err := store.DecryptMessage(msg)
	if err != nil {
		t.Fatalf("DecryptMessage: %v", err)
	}
	if plaintext != "Hello Bob!" {
		t.Errorf("decrypted content mismatch: %q", plaintext)
	}
}

func TestStore_ConversationCRUD(t *testing.T) {
	store := testStore(t)

	var nonce1, nonce2 [24]byte
	copy(nonce1[:], []byte("nonce-1-24bytes-testing!"))
	copy(nonce2[:], []byte("nonce-2-24bytes-testing!"))

	// Append two messages
	store.AppendMessage("conv1", "bob.example.com", "https://bob.example.com",
		"alice.example.com", "bob.example.com", "First message", "", "sent", nonce1)
	store.AppendMessage("conv1", "bob.example.com", "https://bob.example.com",
		"bob.example.com", "alice.example.com", "Reply!", "", "received", nonce2)

	// Load conversation
	conv, err := store.LoadConversation("conv1")
	if err != nil {
		t.Fatalf("LoadConversation: %v", err)
	}
	if len(conv.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(conv.Messages))
	}
	if conv.PeerDomain != "bob.example.com" {
		t.Errorf("peer domain mismatch: %s", conv.PeerDomain)
	}

	// Check index
	idx, err := store.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(idx.Conversations) != 1 {
		t.Fatalf("expected 1 conversation in index, got %d", len(idx.Conversations))
	}
	if idx.Conversations[0].UnreadCount != 1 {
		t.Errorf("expected 1 unread, got %d", idx.Conversations[0].UnreadCount)
	}
}

func TestStore_MarkRead(t *testing.T) {
	store := testStore(t)

	var nonce [24]byte
	copy(nonce[:], []byte("nonce-1-24bytes-testing!"))

	store.AppendMessage("conv1", "bob.example.com", "https://bob.example.com",
		"bob.example.com", "alice.example.com", "Incoming!", "", "received", nonce)

	// Mark read
	if err := store.MarkRead("conv1"); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	// Verify
	conv, _ := store.LoadConversation("conv1")
	if conv.Messages[0].ReadAt == "" {
		t.Error("message should be marked as read")
	}

	idx, _ := store.LoadIndex()
	if idx.Conversations[0].UnreadCount != 0 {
		t.Errorf("unread count should be 0, got %d", idx.Conversations[0].UnreadCount)
	}
}

func TestStore_DeleteConversation(t *testing.T) {
	store := testStore(t)

	var nonce [24]byte
	copy(nonce[:], []byte("nonce-1-24bytes-testing!"))

	store.AppendMessage("conv1", "bob.example.com", "https://bob.example.com",
		"alice.example.com", "bob.example.com", "Message", "", "sent", nonce)

	if err := store.DeleteConversation("conv1"); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}

	// Conversation file should be gone
	_, err := store.LoadConversation("conv1")
	if err == nil {
		t.Error("conversation should be deleted")
	}

	// Index should be empty
	idx, _ := store.LoadIndex()
	if len(idx.Conversations) != 0 {
		t.Errorf("index should be empty, got %d", len(idx.Conversations))
	}
}

func TestStore_GetUnsentMessages(t *testing.T) {
	store := testStore(t)

	var nonce1, nonce2 [24]byte
	copy(nonce1[:], []byte("nonce-1-24bytes-testing!"))
	copy(nonce2[:], []byte("nonce-2-24bytes-testing!"))

	store.AppendMessage("conv1", "bob.example.com", "https://bob.example.com",
		"alice.example.com", "bob.example.com", "Delivered", "", "sent", nonce1)
	store.AppendMessage("conv1", "bob.example.com", "https://bob.example.com",
		"alice.example.com", "bob.example.com", "Failed", "", "unsent", nonce2)

	unsent, err := store.GetUnsentMessages()
	if err != nil {
		t.Fatalf("GetUnsentMessages: %v", err)
	}
	if len(unsent) != 1 {
		t.Errorf("expected 1 unsent message, got %d", len(unsent))
	}
	if unsent[0].Message.Status != "unsent" {
		t.Errorf("expected status unsent, got %s", unsent[0].Message.Status)
	}
}

func TestEncryptPreview_RoundTrip(t *testing.T) {
	store := testStore(t)

	original := "Hello, this is a preview message!"
	encrypted, err := store.encryptPreview(original)
	if err != nil {
		t.Fatalf("encryptPreview: %v", err)
	}

	if !strings.HasPrefix(encrypted, "enc:") {
		t.Errorf("encrypted preview should start with enc: prefix, got %q", encrypted)
	}
	if !strings.Contains(encrypted, "$") {
		t.Error("encrypted preview should contain $ separator")
	}

	decrypted := store.DecryptPreview(encrypted)
	if decrypted != original {
		t.Errorf("round-trip failed: got %q, want %q", decrypted, original)
	}
}

func TestDecryptPreview_LegacyPlaintext(t *testing.T) {
	store := testStore(t)

	plaintext := "This is a legacy plaintext preview"
	result := store.DecryptPreview(plaintext)
	if result != plaintext {
		t.Errorf("legacy plaintext should pass through unchanged, got %q", result)
	}
}

func TestDecryptPreview_EmptyString(t *testing.T) {
	store := testStore(t)

	result := store.DecryptPreview("")
	if result != "" {
		t.Errorf("empty string should return empty, got %q", result)
	}
}

func TestEncryptPreview_EmptyString(t *testing.T) {
	store := testStore(t)

	result, err := store.encryptPreview("")
	if err != nil {
		t.Fatalf("encryptPreview empty: %v", err)
	}
	if result != "" {
		t.Errorf("encrypting empty string should return empty, got %q", result)
	}
}

func TestDecryptPreview_CorruptData(t *testing.T) {
	store := testStore(t)

	// Corrupt format - missing $
	result := store.DecryptPreview("enc:notvalid")
	if result != "[preview unavailable]" {
		t.Errorf("corrupt data should return fallback, got %q", result)
	}

	// Corrupt format - bad base64
	result = store.DecryptPreview("enc:!!!$!!!")
	if result != "[preview unavailable]" {
		t.Errorf("bad base64 should return fallback, got %q", result)
	}

	// Corrupt format - valid base64 but wrong nonce length
	result = store.DecryptPreview("enc:dGVzdA==$dGVzdA==")
	if result != "[preview unavailable]" {
		t.Errorf("wrong nonce length should return fallback, got %q", result)
	}
}

func TestIsEncryptedPreview(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"enc:abc$def", true},
		{"enc:", true},
		{"Hello world", false},
		{"encrypted but not prefixed", false},
		{"Enc:wrong-case", false},
	}

	for _, tt := range tests {
		got := IsEncryptedPreview(tt.input)
		if got != tt.want {
			t.Errorf("IsEncryptedPreview(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestAppendMessage_EncryptsPreview(t *testing.T) {
	store := testStore(t)

	var nonce [24]byte
	copy(nonce[:], []byte("nonce-preview-test-24by!"))

	_, err := store.AppendMessage(
		"conv1", "bob.example.com", "https://bob.example.com",
		"alice.example.com", "bob.example.com",
		"This message preview should be encrypted at rest", "", "sent", nonce,
	)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	idx, err := store.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	preview := idx.Conversations[0].LastPreview
	if !IsEncryptedPreview(preview) {
		t.Errorf("preview should be encrypted, got plaintext: %q", preview)
	}
	if !strings.HasPrefix(preview, "enc:") {
		t.Errorf("preview should have enc: prefix, got %q", preview)
	}

	// Decrypt and verify content
	decrypted := store.DecryptPreview(preview)
	if decrypted != "This message preview should be encrypted at rest" {
		t.Errorf("decrypted preview mismatch: %q", decrypted)
	}
}

func TestDecryptIndexPreviews(t *testing.T) {
	store := testStore(t)

	enc1, _ := store.encryptPreview("Hello")
	enc2, _ := store.encryptPreview("World")

	idx := &ConversationIndex{
		Conversations: []ConversationSummary{
			{ID: "conv1", LastPreview: enc1},
			{ID: "conv2", LastPreview: enc2},
			{ID: "conv3", LastPreview: "legacy plaintext"},
			{ID: "conv4", LastPreview: ""},
		},
	}

	store.DecryptIndexPreviews(idx)

	if idx.Conversations[0].LastPreview != "Hello" {
		t.Errorf("conv1 preview: got %q, want %q", idx.Conversations[0].LastPreview, "Hello")
	}
	if idx.Conversations[1].LastPreview != "World" {
		t.Errorf("conv2 preview: got %q, want %q", idx.Conversations[1].LastPreview, "World")
	}
	if idx.Conversations[2].LastPreview != "legacy plaintext" {
		t.Errorf("conv3 legacy preview should pass through: got %q", idx.Conversations[2].LastPreview)
	}
	if idx.Conversations[3].LastPreview != "" {
		t.Errorf("conv4 empty preview should stay empty: got %q", idx.Conversations[3].LastPreview)
	}
}

func TestEncryptPlaintextPreviews(t *testing.T) {
	store := testStore(t)

	// Create index with mixed plaintext and encrypted previews
	enc, _ := store.encryptPreview("already encrypted")
	idx := &ConversationIndex{
		Conversations: []ConversationSummary{
			{ID: "conv1", PeerDomain: "a.com", LastPreview: "plaintext one"},
			{ID: "conv2", PeerDomain: "b.com", LastPreview: enc},
			{ID: "conv3", PeerDomain: "c.com", LastPreview: "plaintext two"},
			{ID: "conv4", PeerDomain: "d.com", LastPreview: ""},
		},
	}
	if err := store.SaveIndex(idx); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}

	if err := store.EncryptPlaintextPreviews(); err != nil {
		t.Fatalf("EncryptPlaintextPreviews: %v", err)
	}

	// Reload and verify
	idx2, err := store.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	for i, c := range idx2.Conversations {
		if c.LastPreview != "" && !IsEncryptedPreview(c.LastPreview) {
			t.Errorf("conversation %d (%s) still has plaintext preview", i, c.ID)
		}
	}

	// Verify decrypted content
	store.DecryptIndexPreviews(idx2)
	if idx2.Conversations[0].LastPreview != "plaintext one" {
		t.Errorf("conv1 decrypted: got %q, want %q", idx2.Conversations[0].LastPreview, "plaintext one")
	}
	if idx2.Conversations[1].LastPreview != "already encrypted" {
		t.Errorf("conv2 decrypted: got %q, want %q", idx2.Conversations[1].LastPreview, "already encrypted")
	}
	if idx2.Conversations[2].LastPreview != "plaintext two" {
		t.Errorf("conv3 decrypted: got %q, want %q", idx2.Conversations[2].LastPreview, "plaintext two")
	}
	if idx2.Conversations[3].LastPreview != "" {
		t.Errorf("conv4 should remain empty: got %q", idx2.Conversations[3].LastPreview)
	}
}

func TestEncryptPlaintextPreviews_NoChangesWhenAllEncrypted(t *testing.T) {
	store := testStore(t)

	enc, _ := store.encryptPreview("encrypted")
	idx := &ConversationIndex{
		Conversations: []ConversationSummary{
			{ID: "conv1", LastPreview: enc},
			{ID: "conv2", LastPreview: ""},
		},
	}
	if err := store.SaveIndex(idx); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}

	// Get file content before
	idxPath := filepath.Join(store.baseDir, conversationsFile)
	before, _ := os.ReadFile(idxPath)

	if err := store.EncryptPlaintextPreviews(); err != nil {
		t.Fatalf("EncryptPlaintextPreviews: %v", err)
	}

	// File should be unchanged (no write when nothing to migrate)
	after, _ := os.ReadFile(idxPath)
	if string(before) != string(after) {
		t.Error("file should not be rewritten when all previews are already encrypted")
	}
}

func TestAppendMessage_PreviewNotVisibleInRawJSON(t *testing.T) {
	store := testStore(t)

	var nonce [24]byte
	copy(nonce[:], []byte("nonce-rawjson-test-24by!"))

	secret := "my secret DM content that should be encrypted"
	store.AppendMessage(
		"conv1", "bob.example.com", "https://bob.example.com",
		"alice.example.com", "bob.example.com",
		secret, "", "sent", nonce,
	)

	// Read raw conversations.json and verify the plaintext is NOT present
	idxPath := filepath.Join(store.baseDir, conversationsFile)
	raw, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Error("plaintext message content should not appear in raw conversations.json")
	}

	// Also verify it deserializes to an encrypted form
	var idx ConversationIndex
	json.Unmarshal(raw, &idx)
	if !IsEncryptedPreview(idx.Conversations[0].LastPreview) {
		t.Errorf("stored preview should be encrypted, got: %q", idx.Conversations[0].LastPreview)
	}
}
