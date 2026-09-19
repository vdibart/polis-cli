package dm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// messages.jsonl is unsigned. The re-seal-forward rewrite re-encodes only the
// lines it re-seals — and those keep the members this build does not model —
// while every other line is written back as its original bytes.
func TestReencryptBootstrapForwardKeepsMembersAndUntouchedLinesBytes(t *testing.T) {
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
	_, sSK := epochKP(t)
	sPub, _ := publicFromDEK(sSK[:])
	ct, nonce, _ := Encrypt([]byte("sent during the bootstrap window"), &bootPub, &sSK)
	if _, err := mb.AppendReceived(peer, peer, 0, ct, nonce, sPub, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := mb.AppendSent(peer, "me.example.com", "already under a password", 1, newPub, newDEK, "", "sent"); err != nil {
		t.Fatal(err)
	}

	// Give both lines a member this build does not model; re-space the second
	// so a re-encode of it would show.
	path := filepath.Join(mb.convDir(peer), messagesFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}
	lines[0] = strings.TrimSuffix(lines[0], "}") + `,"future_msg":{"reactions":["+1"]}}`
	lines[1] = "{ " + strings.TrimPrefix(strings.ReplaceAll(lines[1], `","`, `", "`), "{") + " "
	lines[1] = strings.TrimSuffix(strings.TrimSpace(lines[1]), "}") + `, "future_msg": 2 }`
	untouched := lines[1]
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	n, err := mb.ReencryptBootstrapForward(0, bootDEK, 1, newPub)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("re-sealed %d messages, want 1", n)
	}

	data, _ = os.ReadFile(path)
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(got) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(got), data)
	}
	if got[1] != untouched {
		t.Errorf("an untouched line changed:\n want %s\n got  %s", untouched, got[1])
	}
	var resealed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got[0]), &resealed); err != nil {
		t.Fatal(err)
	}
	if string(resealed["key_epoch"]) != "1" {
		t.Fatalf("the re-seal itself was lost: %s", got[0])
	}
	if string(resealed["future_msg"]) != `{"reactions":["+1"]}` {
		t.Errorf("the re-sealed line lost an unmodelled member: %s", got[0])
	}

	// Both still open under the new epoch.
	msgs, err := mb.ReadConversation(peer, map[int][32]byte{1: newDEK})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.Locked || m.Undecryptable || m.Plaintext == "" {
			t.Fatalf("a message no longer opens: %+v", m)
		}
	}
}
