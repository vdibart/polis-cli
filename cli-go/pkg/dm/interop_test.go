package dm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// provisionSite creates a tenant dir with a keypair and a bootstrap keyring.
func provisionSite(t *testing.T) (siteDir string, privPEM, pubSSH []byte) {
	t.Helper()
	siteDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(siteDir, ".polis", "policies"), 0o700); err != nil {
		t.Fatal(err)
	}
	privPEM, pubSSH, _ = signing.GenerateKeypair()
	kr := &Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	if err := kr.Save(DMDir(siteDir)); err != nil {
		t.Fatal(err)
	}
	return siteDir, privPEM, pubSSH
}

// TestInteropTwoInstances is the phase-2 exit milestone, automated: site A sends a DM to
// site B over the point-to-point wire — A fetches + verifies B's signed public_key_messages
// block, seals the bare content FROM A's bootstrap DEK TO B's messages key, and delivers it.
// B stores the wire box WITHOUT decrypting (no re-seal), then opens it with its own
// bootstrap DEK on read. No discovery service, no shared secret. Both sides' stored copies
// decrypt to the identical bare content.
func TestInteropTwoInstances(t *testing.T) {
	const content = "hello over the wire"

	siteA, aPriv, aPub := provisionSite(t)
	siteB, bPriv, bPub := provisionSite(t)

	// B advertises its identity key + signed messages-key block, and accepts DMs from all.
	bKeyring, err := LoadKeyring(DMDir(siteB))
	if err != nil {
		t.Fatal(err)
	}
	bBlock, err := BuildMessagesKeyBlock(bKeyring, bPriv)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(siteB, ".polis", "policies", "rules.jsonl"),
		[]byte(`{"active":true,"policy":"allow pub.polis.dm from all"}`+"\n"), 0o600)

	// B's receiver, behind its deliver endpoint. The test's deliver handler skips the
	// signed-request header check (covered elsewhere) and exercises the crypto/storage path.
	bReceiver := NewReceiver(bPriv, bPub, "b.example.com", siteB, NewRateLimiter(1000, 1000))

	// DM-1: B authenticates A's box_pub against A's published messages block. Return A's
	// real signed block (built from A's keyring) so the authentic end-to-end path is exercised.
	aKeyring, err := LoadKeyring(DMDir(siteA))
	if err != nil {
		t.Fatal(err)
	}
	aBlock, err := BuildMessagesKeyBlock(aKeyring, aPriv)
	if err != nil {
		t.Fatal(err)
	}
	bReceiver.FetchSenderKeys = func(string) ([]byte, *MessagesKeyBlock, error) { return aPub, aBlock, nil }

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"public_key":          string(bPub),
				"public_key_messages": bBlock,
			})
		case "/v1/content/dm/actions/deliver":
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			// senderDomain comes from the (verified, in production) X-Polis-Domain header;
			// in the test we read it from the envelope.
			var env MessageEnvelope
			json.Unmarshal(body, &env)
			bReceiver.Domain = ExtractDomainFromURL(srv.URL) // match envelope.RecipientDomain
			if _, err := bReceiver.ReceiveMessage(env.SenderDomain, body, map[string]bool{"a.example.com": true}); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// A sends.
	sender := NewSender(aPriv, aPub, "a.example.com", siteA)
	sent, err := sender.SendMessage(srv.URL, content, "")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if sent.Status != "sent" {
		t.Fatalf("status = %q, want sent", sent.Status)
	}

	// B opens the delivered box with its own bootstrap DEK.
	bDEKs, err := LoadAvailableDEKs(siteB)
	if err != nil {
		t.Fatal(err)
	}
	bMsgs, err := NewMailbox(DMDir(siteB)).ReadConversation("a.example.com", bDEKs)
	if err != nil {
		t.Fatalf("B read: %v", err)
	}
	if len(bMsgs) != 1 {
		t.Fatalf("B expected 1 received message, got %d", len(bMsgs))
	}
	if bMsgs[0].Locked {
		t.Fatal("B should open the box with its bootstrap DEK")
	}
	if bMsgs[0].Plaintext != content {
		t.Errorf("B decrypted %q, want %q", bMsgs[0].Plaintext, content)
	}
	if bMsgs[0].Dir != DirIn {
		t.Errorf("B message dir = %q, want in", bMsgs[0].Dir)
	}

	// A re-reads its own sent copy.
	aDEKs, _ := LoadAvailableDEKs(siteA)
	aMsgs, err := NewMailbox(DMDir(siteA)).ReadConversation(ExtractDomainFromURL(srv.URL), aDEKs)
	if err != nil {
		t.Fatalf("A read: %v", err)
	}
	if len(aMsgs) != 1 || aMsgs[0].Locked || aMsgs[0].Plaintext != content {
		t.Errorf("A sent copy = %+v, want one readable %q", aMsgs, content)
	}
}
