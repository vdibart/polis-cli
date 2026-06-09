package dm

import (
	"fmt"
	"os"
	"path/filepath"
)

// Peers lists the conversation peer handles present on disk (newest order not guaranteed).
func (m *Mailbox) Peers() ([]string, error) {
	dirs, err := os.ReadDir(filepath.Join(m.dmDir, conversationsSubdir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var peers []string
	for _, d := range dirs {
		if d.IsDir() {
			peers = append(peers, d.Name())
		}
	}
	return peers, nil
}

// PeerForConversationID maps an external conversation id back to a peer handle: it finds
// the peer whose conversation with selfDomain hashes to convID. ok=false if none matches.
// convID = ComputeConversationID(selfDomain, peer) is order-independent, so this is the
// inverse the convID-keyed ops/webapp API needs over the peer-keyed mailbox.
func (m *Mailbox) PeerForConversationID(selfDomain, convID string) (string, bool, error) {
	peers, err := m.Peers()
	if err != nil {
		return "", false, err
	}
	for _, peer := range peers {
		if ComputeConversationID(selfDomain, peer) == convID {
			return peer, true, nil
		}
	}
	return "", false, nil
}

// DeleteConversation removes a conversation's directory (messages + metadata). Idempotent.
func (m *Mailbox) DeleteConversation(peer string) error {
	if err := os.RemoveAll(m.convDir(peer)); err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	return nil
}

// UnsentMailboxMessage pairs an unsent outgoing message with its conversation peer.
type UnsentMailboxMessage struct {
	Peer    string
	Message MailboxMessage
}

// UnsentMessages returns outgoing messages still marked "unsent" across all conversations.
func (m *Mailbox) UnsentMessages() ([]UnsentMailboxMessage, error) {
	peers, err := m.Peers()
	if err != nil {
		return nil, err
	}
	var out []UnsentMailboxMessage
	for _, peer := range peers {
		msgs, err := m.LoadMessages(peer)
		if err != nil {
			return nil, err
		}
		for _, msg := range msgs {
			if msg.Dir == DirOut && msg.Status == "unsent" {
				out = append(out, UnsentMailboxMessage{Peer: peer, Message: msg})
			}
		}
	}
	return out, nil
}
