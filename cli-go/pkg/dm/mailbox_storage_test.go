package dm

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// A site with no conversations has no conversations directory, and every
// listing reads that as "nothing there". An unreadable directory is a failed
// read, and every listing must say so rather than report an empty mailbox.
func TestMailboxListingsTellAbsenceFromAFailedRead(t *testing.T) {
	dir := t.TempDir()
	mb := NewMailbox(storage.At(dir))
	pub, dek := epochKP(t)

	peers, err := mb.Peers()
	if err != nil || len(peers) != 0 {
		t.Fatalf("Peers on an empty site = %v, %v; want none, nil", peers, err)
	}
	inbox, err := mb.RebuildInbox()
	if err != nil || len(inbox) != 0 {
		t.Fatalf("RebuildInbox on an empty site = %v, %v; want none, nil", inbox, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis/bundles/pub.polis.core/dm", inboxFile)); err != nil {
		t.Errorf("RebuildInbox on an empty site wrote no inbox: %v", err)
	}
	n, err := mb.ReencryptBootstrapForward(0, dek, 1, pub)
	if err != nil || n != 0 {
		t.Fatalf("ReencryptBootstrapForward on an empty site = %d, %v; want 0, nil", n, err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root lists a mode-000 directory; the failed-read half needs a non-root user")
	}
	if _, err := mb.AppendSent("maya.polis.pub", "me.polis.pub", "hi", 0, pub, dek, "", "sent"); err != nil {
		t.Fatal(err)
	}
	convs := filepath.Join(dir, filepath.FromSlash(conversationsKey))
	if err := os.Chmod(convs, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(convs, 0o700) })

	if _, err := mb.Peers(); err == nil {
		t.Error("Peers on an unreadable directory returned nil; it could not look and must say so")
	}
	if _, err := mb.RebuildInbox(); err == nil {
		t.Error("RebuildInbox on an unreadable directory returned nil; it could not look and must say so")
	}
	if _, err := mb.ReencryptBootstrapForward(0, dek, 1, pub); err == nil {
		t.Error("ReencryptBootstrapForward on an unreadable directory returned nil; it could not look and must say so")
	}
}

// A conversation directory is created by the append, through the site's store,
// and comes out exactly 0700 whatever the umask.
func TestMailboxDirectoriesArePrivate(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	mb := NewMailbox(storage.At(dir))
	pub, dek := epochKP(t)
	if _, err := mb.AppendSent("maya.polis.pub", "me.polis.pub", "hi", 1, pub, dek, "", "sent"); err != nil {
		t.Fatal(err)
	}
	if _, err := mb.RebuildInbox(); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		".polis/bundles/pub.polis.core/dm",
		".polis/bundles/pub.polis.core/dm/conversations",
		".polis/bundles/pub.polis.core/dm/conversations/maya.polis.pub",
	} {
		fi, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o700 {
			t.Errorf("%s mode = %o, want 700", rel, got)
		}
	}
	fi, err := os.Stat(filepath.Join(dir, ".polis/bundles/pub.polis.core/dm/conversations/maya.polis.pub", messagesFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("messages.jsonl mode = %o, want 600", got)
	}
}

// The mailbox works on a store with no filesystem: append, list, read, rebuild
// the inbox and delete, all by key.
func TestMailboxOnAnObjectStore(t *testing.T) {
	mb := NewMailbox(storage.On("/nonexistent-site-root", storage.NewObjectStore("")))
	pub, dek := epochKP(t)
	if _, err := mb.AppendSent("Maya.polis.pub", "me.polis.pub", "hi", 1, pub, dek, "", "sent"); err != nil {
		t.Fatal(err)
	}
	if _, err := mb.AppendSent("maya.polis.pub", "me.polis.pub", "again", 1, pub, dek, "", "sent"); err != nil {
		t.Fatal(err)
	}
	peers, err := mb.Peers()
	if err != nil || len(peers) != 1 || peers[0] != "maya.polis.pub" {
		t.Fatalf("Peers = %v, %v; want [maya.polis.pub]", peers, err)
	}
	got, err := mb.ReadConversation("maya.polis.pub", map[int][32]byte{1: dek})
	if err != nil || len(got) != 2 || got[1].Plaintext != "again" {
		t.Fatalf("ReadConversation = %+v, %v", got, err)
	}
	inbox, err := mb.RebuildInbox()
	if err != nil || len(inbox) != 1 {
		t.Fatalf("RebuildInbox = %v, %v", inbox, err)
	}
	if err := mb.DeleteConversation("maya.polis.pub"); err != nil {
		t.Fatal(err)
	}
	if peers, err := mb.Peers(); err != nil || len(peers) != 0 {
		t.Fatalf("Peers after delete = %v, %v; want none", peers, err)
	}
}
