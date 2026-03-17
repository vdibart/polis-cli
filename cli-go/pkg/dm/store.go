package dm

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	storageSaltFile   = "storage-salt"
	storageInfoDM     = "polis-dm-storage-v1"
	conversationsFile = "conversations.json"
	convDir           = "conv"
)

// Store manages DM conversation storage on disk.
type Store struct {
	baseDir    string   // .polis/content/pub.polis.core/dm
	siteDir    string   // site root
	storageKey [32]byte // derived from private key + salt
}

// NewStore creates a DM store, deriving the storage key from the private key seed
// and the site-wide salt. Creates the storage directories if needed.
func NewStore(siteDir string, privateKeySeed []byte) (*Store, error) {
	baseDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm")
	if err := os.MkdirAll(filepath.Join(baseDir, convDir), 0700); err != nil {
		return nil, fmt.Errorf("create dm dir: %w", err)
	}

	salt, err := loadOrCreateSalt(siteDir)
	if err != nil {
		return nil, fmt.Errorf("storage salt: %w", err)
	}

	storageKey, err := DeriveStorageKey(privateKeySeed, salt, storageInfoDM)
	// Zero the seed now that we've derived the storage key.
	// Callers must not reuse the seed after calling NewStore.
	for i := range privateKeySeed {
		privateKeySeed[i] = 0
	}
	if err != nil {
		return nil, fmt.Errorf("derive storage key: %w", err)
	}

	return &Store{
		baseDir:    baseDir,
		siteDir:    siteDir,
		storageKey: storageKey,
	}, nil
}

// loadOrCreateSalt loads the site-wide storage salt, creating it if it doesn't exist.
func loadOrCreateSalt(siteDir string) ([]byte, error) {
	saltPath := filepath.Join(siteDir, ".polis", storageSaltFile)

	data, err := os.ReadFile(saltPath)
	if err == nil {
		salt, err := hex.DecodeString(string(data))
		if err != nil || len(salt) != 32 {
			return nil, fmt.Errorf("corrupt storage-salt file")
		}
		return salt, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	// Generate new 32-byte random salt
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(saltPath), 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(saltPath, []byte(hex.EncodeToString(salt)), 0600); err != nil {
		return nil, fmt.Errorf("write salt: %w", err)
	}

	return salt, nil
}

// EnsureSalt ensures the site-wide storage salt exists, creating it if needed.
func EnsureSalt(siteDir string) error {
	_, err := loadOrCreateSalt(siteDir)
	return err
}

// LoadIndex reads the conversations index.
func (s *Store) LoadIndex() (*ConversationIndex, error) {
	path := filepath.Join(s.baseDir, conversationsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ConversationIndex{}, nil
		}
		return nil, err
	}

	var idx ConversationIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse conversations index: %w", err)
	}
	return &idx, nil
}

// SaveIndex writes the conversations index to disk.
func (s *Store) SaveIndex(idx *ConversationIndex) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(s.baseDir, conversationsFile), data, 0600)
}

// LoadConversation reads a full conversation from disk.
func (s *Store) LoadConversation(convID string) (*Conversation, error) {
	path := filepath.Join(s.baseDir, convDir, convID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("conversation not found: %s", convID)
		}
		return nil, err
	}

	var conv Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, fmt.Errorf("parse conversation: %w", err)
	}
	return &conv, nil
}

// SaveConversation writes a conversation to disk.
func (s *Store) SaveConversation(convID string, conv *Conversation) error {
	data, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(s.baseDir, convDir, convID+".json"), data, 0600)
}

// DeleteConversation removes a conversation file and its index entry.
func (s *Store) DeleteConversation(convID string) error {
	// Remove conversation file
	convPath := filepath.Join(s.baseDir, convDir, convID+".json")
	if err := os.Remove(convPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove conversation: %w", err)
	}

	// Update index
	idx, err := s.LoadIndex()
	if err != nil {
		return err
	}
	filtered := idx.Conversations[:0]
	for _, c := range idx.Conversations {
		if c.ID != convID {
			filtered = append(filtered, c)
		}
	}
	idx.Conversations = filtered
	return s.SaveIndex(idx)
}

// AppendMessage adds a message to a conversation, encrypting the content for storage.
// Creates the conversation if it doesn't exist. Updates the index.
func (s *Store) AppendMessage(convID, peerDomain, peerURL, from, to, plaintext, replyToID, status string, transportNonce [24]byte) (*Message, error) {
	// Encrypt content for storage
	ciphertext, storageNonce, err := EncryptForStorage([]byte(plaintext), &s.storageKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt for storage: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	msg := Message{
		ID:               MessageIDFromNonce(transportNonce),
		From:             from,
		To:               to,
		EncryptedContent: base64.StdEncoding.EncodeToString(ciphertext),
		StorageNonce:     base64.StdEncoding.EncodeToString(storageNonce[:]),
		ReplyToID:        replyToID,
		Timestamp:        now,
		Status:           status,
	}

	// Load or create conversation
	conv, err := s.LoadConversation(convID)
	if err != nil {
		conv = &Conversation{
			PeerDomain: peerDomain,
			PeerURL:    peerURL,
		}
	}
	conv.Messages = append(conv.Messages, msg)

	if err := s.SaveConversation(convID, conv); err != nil {
		return nil, err
	}

	// Update index
	preview := plaintext
	if len(preview) > 100 {
		preview = preview[:100]
	}
	if err := s.updateIndex(convID, peerDomain, peerURL, now, preview, status == "received"); err != nil {
		return nil, fmt.Errorf("update index: %w", err)
	}

	return &msg, nil
}

// DecryptMessage decrypts a stored message's content.
func (s *Store) DecryptMessage(msg *Message) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(msg.EncryptedContent)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	nonceBytes, err := base64.StdEncoding.DecodeString(msg.StorageNonce)
	if err != nil {
		return "", fmt.Errorf("decode nonce: %w", err)
	}
	if len(nonceBytes) != 24 {
		return "", fmt.Errorf("invalid nonce length: %d", len(nonceBytes))
	}
	var nonce [24]byte
	copy(nonce[:], nonceBytes)

	plaintext, err := DecryptFromStorage(ciphertext, &nonce, &s.storageKey)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// MarkRead marks messages in a conversation as read.
func (s *Store) MarkRead(convID string) error {
	conv, err := s.LoadConversation(convID)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	changed := false
	for i := range conv.Messages {
		if conv.Messages[i].ReadAt == "" && conv.Messages[i].Status == "received" {
			conv.Messages[i].ReadAt = now
			changed = true
		}
	}

	if !changed {
		return nil
	}

	if err := s.SaveConversation(convID, conv); err != nil {
		return err
	}

	// Update unread count in index
	idx, err := s.LoadIndex()
	if err != nil {
		return err
	}
	for i := range idx.Conversations {
		if idx.Conversations[i].ID == convID {
			idx.Conversations[i].UnreadCount = 0
			break
		}
	}
	return s.SaveIndex(idx)
}

// GetUnsentMessages returns all messages with status "unsent" across all conversations.
func (s *Store) GetUnsentMessages() ([]UnsentMessage, error) {
	idx, err := s.LoadIndex()
	if err != nil {
		return nil, err
	}

	var unsent []UnsentMessage
	for _, summary := range idx.Conversations {
		conv, err := s.LoadConversation(summary.ID)
		if err != nil {
			continue
		}
		for _, msg := range conv.Messages {
			if msg.Status == "unsent" {
				unsent = append(unsent, UnsentMessage{
					ConvID:  summary.ID,
					Message: msg,
				})
			}
		}
	}

	return unsent, nil
}

// UnsentMessage pairs a message with its conversation ID.
type UnsentMessage struct {
	ConvID  string
	Message Message
}

// UpdateMessageStatus changes the status of a specific message.
func (s *Store) UpdateMessageStatus(convID, msgID, newStatus string) error {
	conv, err := s.LoadConversation(convID)
	if err != nil {
		return err
	}

	for i := range conv.Messages {
		if conv.Messages[i].ID == msgID {
			conv.Messages[i].Status = newStatus
			return s.SaveConversation(convID, conv)
		}
	}

	return fmt.Errorf("message %s not found in conversation %s", msgID, convID)
}

// updateIndex updates the conversations index with new message info.
func (s *Store) updateIndex(convID, peerDomain, peerURL, timestamp, preview string, isIncoming bool) error {
	idx, err := s.LoadIndex()
	if err != nil {
		idx = &ConversationIndex{}
	}

	found := false
	for i := range idx.Conversations {
		if idx.Conversations[i].ID == convID {
			idx.Conversations[i].LastMessageAt = timestamp
			idx.Conversations[i].LastPreview = preview
			if isIncoming {
				idx.Conversations[i].UnreadCount++
			}
			found = true
			break
		}
	}

	if !found {
		unread := 0
		if isIncoming {
			unread = 1
		}
		idx.Conversations = append(idx.Conversations, ConversationSummary{
			ID:            convID,
			PeerDomain:    peerDomain,
			PeerURL:       peerURL,
			LastMessageAt: timestamp,
			UnreadCount:   unread,
			LastPreview:   preview,
		})
	}

	return s.SaveIndex(idx)
}
