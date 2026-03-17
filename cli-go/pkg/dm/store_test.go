package dm

import (
	"os"
	"path/filepath"
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

func TestStore_UpdateMessageStatus(t *testing.T) {
	store := testStore(t)

	var nonce [24]byte
	copy(nonce[:], []byte("nonce-1-24bytes-testing!"))

	msg, _ := store.AppendMessage("conv1", "bob.example.com", "https://bob.example.com",
		"alice.example.com", "bob.example.com", "Retrying", "", "unsent", nonce)

	if err := store.UpdateMessageStatus("conv1", msg.ID, "sent"); err != nil {
		t.Fatalf("UpdateMessageStatus: %v", err)
	}

	conv, _ := store.LoadConversation("conv1")
	if conv.Messages[0].Status != "sent" {
		t.Errorf("expected sent, got %s", conv.Messages[0].Status)
	}
}
