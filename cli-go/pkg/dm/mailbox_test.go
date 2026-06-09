package dm

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// epochKP returns a fresh (pub, dek) pair for tests.
func epochKP(t *testing.T) (pub [32]byte, dek [32]byte) {
	t.Helper()
	p, d, err := NewEpochKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return p, d
}

func TestMailboxSentRoundTrip(t *testing.T) {
	mb := NewMailbox(t.TempDir())
	pub, dek := epochKP(t)

	msg, err := mb.AppendSent("Maya.Polis.PUB", "me.polis.pub", "hey, thursday?", 1, pub, dek, "", "sent")
	if err != nil {
		t.Fatalf("AppendSent: %v", err)
	}
	if msg.Dir != DirOut || msg.BoxPub == "" {
		t.Errorf("bad sent message: %+v", msg)
	}
	// Handle is lowercased on disk.
	if _, err := os.Stat(filepath.Join(mb.dmDir, "conversations", "maya.polis.pub", "messages.jsonl")); err != nil {
		t.Fatalf("expected lowercased conversation dir: %v", err)
	}

	got, err := mb.ReadConversation("maya.polis.pub", map[int][32]byte{1: dek})
	if err != nil {
		t.Fatalf("ReadConversation: %v", err)
	}
	if len(got) != 1 || got[0].Locked || got[0].Plaintext != "hey, thursday?" {
		t.Fatalf("sent round-trip failed: %+v", got)
	}
}

func TestMailboxReceivedRoundTrip(t *testing.T) {
	mb := NewMailbox(t.TempDir())
	myPub, myDEK := epochKP(t)
	senderPub, senderDEK := epochKP(t)

	// Sender seals to my epoch pubkey (the wire box).
	ct, nonce, err := Encrypt([]byte("welcome aboard"), &myPub, &senderDEK)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mb.AppendReceived("dev.polis.pub", "dev.polis.pub", 1, ct, nonce, senderPub, ""); err != nil {
		t.Fatalf("AppendReceived: %v", err)
	}
	got, err := mb.ReadConversation("dev.polis.pub", map[int][32]byte{1: myDEK})
	if err != nil {
		t.Fatalf("ReadConversation: %v", err)
	}
	if len(got) != 1 || got[0].Dir != DirIn || got[0].Plaintext != "welcome aboard" {
		t.Fatalf("received round-trip failed: %+v", got)
	}
}

// TestMailboxReceivedDedupesOnMessageID is the DM-3 regression: a replayed wire envelope
// (same transport nonce → same message ID) must be stored exactly once. Re-ports the dedup
// guarantee lost when the legacy Store was removed (was R20-C-F7).
func TestMailboxReceivedDedupesOnMessageID(t *testing.T) {
	mb := NewMailbox(t.TempDir())
	myPub, myDEK := epochKP(t)
	senderPub, senderDEK := epochKP(t)
	ct, nonce, err := Encrypt([]byte("replay me"), &myPub, &senderDEK)
	if err != nil {
		t.Fatal(err)
	}

	m1, err := mb.AppendReceived("dev.polis.pub", "dev.polis.pub", 1, ct, nonce, senderPub, "")
	if err != nil {
		t.Fatalf("first AppendReceived: %v", err)
	}
	m2, err := mb.AppendReceived("dev.polis.pub", "dev.polis.pub", 1, ct, nonce, senderPub, "")
	if err != nil {
		t.Fatalf("replayed AppendReceived: %v", err)
	}
	if m1.ID != m2.ID {
		t.Fatalf("expected identical IDs, got %s and %s", m1.ID, m2.ID)
	}

	got, err := mb.ReadConversation("dev.polis.pub", map[int][32]byte{1: myDEK})
	if err != nil {
		t.Fatalf("ReadConversation: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("replay must store exactly one message, got %d", len(got))
	}
	// Meta must not have double-counted the replay.
	meta, err := mb.LoadMeta("dev.polis.pub")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.MessageCount != 1 {
		t.Fatalf("message_count = %d, want 1 (replay must not bump it)", meta.MessageCount)
	}
}

// TestMailboxReadResilientToCorruptBox is the DM-6 regression: a single undecryptable box
// (DEK present, but the tag fails) is flagged per-message and the rest of the conversation
// still reads — it must not error the whole batch (a thread-level DoS).
func TestMailboxReadResilientToCorruptBox(t *testing.T) {
	mb := NewMailbox(t.TempDir())
	myPub, myDEK := epochKP(t)
	senderPub, senderDEK := epochKP(t)

	// A good message.
	goodCT, goodNonce, err := Encrypt([]byte("legit"), &myPub, &senderDEK)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mb.AppendReceived("peer.example.com", "peer.example.com", 1, goodCT, goodNonce, senderPub, ""); err != nil {
		t.Fatal(err)
	}
	// A garbage box under the same epoch (tag will fail to open with myDEK).
	var badNonce [24]byte
	badNonce[0] = 0x01
	if _, err := mb.AppendReceived("peer.example.com", "peer.example.com", 1, []byte("not a real box"), badNonce, senderPub, ""); err != nil {
		t.Fatal(err)
	}

	got, err := mb.ReadConversation("peer.example.com", map[int][32]byte{1: myDEK})
	if err != nil {
		t.Fatalf("ReadConversation must not error on a corrupt box: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both messages returned, got %d", len(got))
	}
	if got[0].Plaintext != "legit" || got[0].Undecryptable {
		t.Errorf("good message should decrypt: %+v", got[0])
	}
	if !got[1].Undecryptable || got[1].Plaintext != "" {
		t.Errorf("corrupt message should be flagged undecryptable with no plaintext: %+v", got[1])
	}
}

// TestMailboxConcurrentReplayDedup is the DM-3 + DM-14 regression under contention: many
// concurrent appends of the SAME wire envelope (same nonce → same ID) must still land
// exactly one message, with no race (run with -race).
func TestMailboxConcurrentReplayDedup(t *testing.T) {
	mb := NewMailbox(t.TempDir())
	myPub, myDEK := epochKP(t)
	senderPub, senderDEK := epochKP(t)
	ct, nonce, err := Encrypt([]byte("concurrent replay"), &myPub, &senderDEK)
	if err != nil {
		t.Fatal(err)
	}

	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, _ = mb.AppendReceived("peer.example.com", "peer.example.com", 1, ct, nonce, senderPub, "")
		}()
	}
	wg.Wait()

	got, err := mb.ReadConversation("peer.example.com", map[int][32]byte{1: myDEK})
	if err != nil {
		t.Fatalf("ReadConversation: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("concurrent replay must store exactly one message, got %d", len(got))
	}
}

func TestMailboxLockedWhenEpochDEKMissing(t *testing.T) {
	mb := NewMailbox(t.TempDir())
	pub, dek := epochKP(t)
	if _, err := mb.AppendSent("a.polis.pub", "me", "secret from epoch 2", 2, pub, dek, "", "sent"); err != nil {
		t.Fatal(err)
	}
	// Read with only epoch 1's DEK → message under epoch 2 is locked.
	_, otherDEK := epochKP(t)
	got, err := mb.ReadConversation("a.polis.pub", map[int][32]byte{1: otherDEK})
	if err != nil {
		t.Fatalf("ReadConversation should not error on locked msgs: %v", err)
	}
	if len(got) != 1 || !got[0].Locked || got[0].Plaintext != "" {
		t.Fatalf("expected locked message, got %+v", got)
	}
	if got[0].KeyEpoch != 2 {
		t.Errorf("locked message should report its epoch (2), got %d", got[0].KeyEpoch)
	}
}

func TestMailboxAppendOnlyAndMeta(t *testing.T) {
	mb := NewMailbox(t.TempDir())
	pub, dek := epochKP(t)
	for i := 0; i < 3; i++ {
		if _, err := mb.AppendSent("p.polis.pub", "me", "msg", 1, pub, dek, "", "sent"); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := mb.LoadMessages("p.polis.pub")
	if err != nil || len(msgs) != 3 {
		t.Fatalf("append-only: want 3 lines, got %d (err %v)", len(msgs), err)
	}
	meta, err := mb.LoadMeta("p.polis.pub")
	if err != nil {
		t.Fatal(err)
	}
	if meta.MessageCount != 3 || meta.LastMessageAt == "" || meta.CreatedAt == "" {
		t.Fatalf("conversation meta wrong: %+v", meta)
	}
}

func TestMailboxMarkReadAndInbox(t *testing.T) {
	mb := NewMailbox(t.TempDir())
	myPub, myDEK := epochKP(t)
	sPub, sDEK := epochKP(t)

	// Two incoming messages.
	for _, body := range []string{"one", "two"} {
		ct, nonce, _ := Encrypt([]byte(body), &myPub, &sDEK)
		if _, err := mb.AppendReceived("bob.polis.pub", "bob.polis.pub", 1, ct, nonce, sPub, ""); err != nil {
			t.Fatal(err)
		}
	}
	_ = myDEK

	entries, err := mb.RebuildInbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Peer != "bob.polis.pub" || entries[0].Unread != 2 {
		t.Fatalf("inbox before read: %+v", entries)
	}

	if err := mb.MarkRead("bob.polis.pub"); err != nil {
		t.Fatal(err)
	}
	entries, err = mb.RebuildInbox()
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Unread != 0 {
		t.Errorf("after MarkRead, unread = %d, want 0", entries[0].Unread)
	}

	// inbox.json was written.
	if _, err := os.Stat(filepath.Join(mb.dmDir, inboxFile)); err != nil {
		t.Errorf("inbox.json not written: %v", err)
	}
}

// TestReencryptBootstrapForward verifies the set-password "close the bootstrap window"
// step: bootstrap-epoch messages are re-sealed forward to the new password epoch so they
// become readable only under that epoch's DEK (and no longer under the bootstrap DEK), with
// plaintext preserved and message IDs unchanged.
func TestReencryptBootstrapForward(t *testing.T) {
	dir := t.TempDir()
	const peer = "alice.example.com"

	k := &Keyring{}
	bootDEK, err := k.AddBootstrapEpoch()
	if err != nil {
		t.Fatal(err)
	}
	bootPub, err := publicFromDEK(bootDEK[:])
	if err != nil {
		t.Fatal(err)
	}
	newPub, newDEK, err := NewEpochKeypair()
	if err != nil {
		t.Fatal(err)
	}

	mb := NewMailbox(dir)
	// A received bootstrap-epoch message (sealed to our bootstrap pubkey by a peer).
	_, sSK := epochKP(t)
	sPub, _ := publicFromDEK(sSK[:])
	ct, nonce, _ := Encrypt([]byte("sent during the bootstrap window"), &bootPub, &sSK)
	recvMsg, err := mb.AppendReceived(peer, peer, 0, ct, nonce, sPub, "")
	if err != nil {
		t.Fatal(err)
	}
	// A sent bootstrap-epoch copy (box-to-self).
	sentMsg, err := mb.AppendSent(peer, "me.example.com", "my reply in the window", 0, bootPub, bootDEK, "", "sent")
	if err != nil {
		t.Fatal(err)
	}

	n, err := mb.ReencryptBootstrapForward(0, bootDEK, 1, newPub)
	if err != nil {
		t.Fatalf("ReencryptBootstrapForward: %v", err)
	}
	if n != 2 {
		t.Fatalf("re-sealed %d messages, want 2", n)
	}

	// Readable under the NEW epoch DEK, tagged epoch 1, plaintext preserved, IDs unchanged.
	withNew, err := mb.ReadConversation(peer, map[int][32]byte{1: newDEK})
	if err != nil {
		t.Fatal(err)
	}
	if len(withNew) != 2 {
		t.Fatalf("want 2 messages, got %d", len(withNew))
	}
	byID := map[string]DecryptedMessage{}
	for _, m := range withNew {
		if m.Locked || m.KeyEpoch != 1 {
			t.Errorf("message %s: want unlocked epoch-1, got locked=%v epoch=%d", m.ID, m.Locked, m.KeyEpoch)
		}
		byID[m.ID] = m
	}
	if got := byID[recvMsg.ID].Plaintext; got != "sent during the bootstrap window" {
		t.Errorf("received msg plaintext = %q", got)
	}
	if got := byID[sentMsg.ID].Plaintext; got != "my reply in the window" {
		t.Errorf("sent msg plaintext = %q", got)
	}

	// The bootstrap DEK alone no longer opens them (re-sealed away from epoch 0).
	withBoot, err := mb.ReadConversation(peer, map[int][32]byte{0: bootDEK})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range withBoot {
		if !m.Locked {
			t.Errorf("after re-seal, bootstrap DEK must not open message %s", m.ID)
		}
	}
}
