package dm

import (
	"bytes"
	"testing"
)

// TestPhase1EndToEnd exercises the whole phase-1 core locally, the way the
// local-validation gate calls for: provision a keyring (bootstrap → password),
// store a sent and a received message, read them back, change the password,
// recover via the phrase, reload from disk, and re-read. No network, no webapp.
func TestPhase1EndToEnd(t *testing.T) {
	dir := t.TempDir()
	const pw = "phase-1 end to end password"
	const peer = "david.polis.pub"

	// 1) Provision: bootstrap epoch (server-held DEK) → set a password (epoch 1).
	k := &Keyring{}
	bootDEK, err := k.AddBootstrapEpoch()
	if err != nil {
		t.Fatal(err)
	}
	phrase, dek1, err := k.SetPassword([]byte(pw))
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SaveCAS(dir, 0); err != nil { // first write
		t.Fatalf("save keyring: %v", err)
	}
	pub1, err := publicFromDEK(dek1[:])
	if err != nil {
		t.Fatal(err)
	}
	bootPub, err := publicFromDEK(bootDEK[:])
	if err != nil {
		t.Fatal(err)
	}

	// 2) Store messages: a sent (epoch 1, sealed to self) + a received under each epoch.
	mb := NewMailbox(dir)
	if _, err := mb.AppendSent(peer, "me.polis.pub", "picking this back up — got a new password now", 1, pub1, dek1, "", "sent"); err != nil {
		t.Fatal(err)
	}
	// Received under epoch 1: a peer seals to my epoch-1 pubkey.
	{
		_, sDEK := epochKP(t)
		ct, nonce, _ := Encrypt([]byte("no worries!"), &pub1, &sDEK)
		sPub, _ := publicFromDEK(sDEK[:])
		if _, err := mb.AppendReceived(peer, peer, 1, ct, nonce, sPub, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Received under epoch 0 (bootstrap, server-readable): sealed to the bootstrap pubkey.
	{
		_, sDEK := epochKP(t)
		ct, nonce, _ := Encrypt([]byte("an early, bootstrap-era message"), &bootPub, &sDEK)
		sPub, _ := publicFromDEK(sDEK[:])
		if _, err := mb.AppendReceived(peer, peer, 0, ct, nonce, sPub, ""); err != nil {
			t.Fatal(err)
		}
	}

	// 3) Read with both epoch DEKs in hand → everything decrypts.
	deks := map[int][32]byte{0: bootDEK, 1: dek1}
	msgs, err := mb.ReadConversation(peer, deks)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.Locked || m.Plaintext == "" {
			t.Errorf("message %s should have decrypted, got locked=%v", m.ID, m.Locked)
		}
	}

	// Without epoch 1's DEK, the epoch-1 messages lock; epoch-0 still reads.
	partial, err := mb.ReadConversation(peer, map[int][32]byte{0: bootDEK})
	if err != nil {
		t.Fatal(err)
	}
	lockedSeen, openSeen := false, false
	for _, m := range partial {
		if m.KeyEpoch == 1 && m.Locked {
			lockedSeen = true
		}
		if m.KeyEpoch == 0 && !m.Locked {
			openSeen = true
		}
	}
	if !lockedSeen || !openSeen {
		t.Errorf("expected epoch-1 locked and epoch-0 open with bootstrap DEK only")
	}

	// 4) Change password, then reload from disk and unlock with the NEW password.
	const newPw = "a fresh password entirely"
	reloaded, err := LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	baseRev := reloaded.Revision
	if err := reloaded.ChangePassword([]byte(pw), []byte(newPw)); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.SaveCAS(dir, baseRev); err != nil {
		t.Fatalf("CAS save after change-password: %v", err)
	}

	final, err := LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	got1, err := final.UnlockEpochWithPassword(1, []byte(newPw))
	if err != nil || !bytes.Equal(got1, dek1[:]) {
		t.Fatalf("new password should unlock epoch 1 after reload: err=%v equal=%v", err, bytes.Equal(got1, dek1[:]))
	}

	// 5) Recover via the phrase (still works after a password change).
	rec1, err := final.UnlockEpochWithPhrase(1, phrase)
	if err != nil || !bytes.Equal(rec1, dek1[:]) {
		t.Fatalf("recovery phrase should still unlock epoch 1: err=%v equal=%v", err, bytes.Equal(rec1, dek1[:]))
	}

	// 6) Re-read messages with DEKs re-derived from the reloaded keyring + bootstrap key.
	reDeks := map[int][32]byte{0: bootDEK}
	copy32(reDeks, 1, got1)
	msgs2, err := mb.ReadConversation(peer, reDeks)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs2 {
		if m.Locked || m.Plaintext == "" {
			t.Errorf("post-reload message %s should decrypt", m.ID)
		}
	}

	// 7) Inbox rebuilds.
	entries, err := mb.RebuildInbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Peer != peer {
		t.Fatalf("inbox: %+v", entries)
	}
}

func copy32(m map[int][32]byte, id int, b []byte) {
	var a [32]byte
	copy(a[:], b)
	m[id] = a
}
