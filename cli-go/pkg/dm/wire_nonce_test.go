package dm

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// recipientCapturing is a recipient that answers with its messages key and
// records the envelope it is handed; deliverStatus is what delivery answers.
func recipientCapturing(t *testing.T, deliverStatus int, got *MessageEnvelope) *httptest.Server {
	t.Helper()
	wk := recipientWellKnown(t)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			_ = json.NewEncoder(w).Encode(wk)
		case "/v1/content/dm/actions/deliver":
			_ = json.NewDecoder(r.Body).Decode(got)
			w.WriteHeader(deliverStatus)
		default:
			http.NotFound(w, r)
		}
	}))
}

func storedSent(t *testing.T, s *Sender, peer string) DecryptedMessage {
	t.Helper()
	conv, err := NewMailbox(s.Site).ReadConversation(peer, senderDEKs(t, s))
	if err != nil || len(conv) != 1 {
		t.Fatalf("stored conversation: %d messages, %v", len(conv), err)
	}
	return conv[0]
}

func recipientIDOf(t *testing.T, nonceB64 string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil || len(raw) != 24 {
		t.Fatalf("nonce %q: %v", nonceB64, err)
	}
	var n [24]byte
	copy(n[:], raw)
	return MessageIDFromNonce(n)
}

// ⭐ A sent message keeps the nonce of the box it delivered, so a later resend
// can reuse it and be stored once: the recipient's id for it is
// MessageIDFromNonce of that nonce. Kept whether or not delivery succeeded,
// since an unsent message is the one a resend is for.
func TestASentMessageKeepsItsTransportNonce(t *testing.T) {
	for name, status := range map[string]int{"delivered": http.StatusCreated, "unsent": http.StatusServiceUnavailable} {
		t.Run(name, func(t *testing.T) {
			sender, _ := testSender(t)
			var env MessageEnvelope
			srv := recipientCapturing(t, status, &env)
			defer srv.Close()
			_, _ = sender.SendMessage(srv.URL, "hello", "")

			m := storedSent(t, sender, ExtractDomainFromURL(srv.URL))
			if m.WireNonce == "" || m.WireNonce != env.Nonce {
				t.Fatalf("wire_nonce = %q, the delivered envelope's nonce = %q", m.WireNonce, env.Nonce)
			}
			if recipientIDOf(t, m.WireNonce) == m.ID {
				t.Error("the record's own id is the recipient's: the two nonces should differ")
			}
		})
	}
}

// The browser-sealed send keeps it too: the delivery box's nonce, not the
// sent copy's.
func TestABrowserSealedMessageKeepsItsTransportNonce(t *testing.T) {
	sender, _ := testSender(t)
	var env MessageEnvelope
	srv := recipientCapturing(t, http.StatusCreated, &env)
	defer srv.Close()
	box := func() SealedBox {
		var n [24]byte
		_, _ = rand.Read(n[:])
		return SealedBox{Ciphertext: base64.StdEncoding.EncodeToString([]byte("sealed in the browser")), Nonce: base64.StdEncoding.EncodeToString(n[:])}
	}
	delivery, sent := box(), box()
	var pub [32]byte
	_, _ = rand.Read(pub[:])
	msg, err := sender.SendSealed(srv.URL, 0, 0, base64.StdEncoding.EncodeToString(pub[:]), delivery, sent, "")
	if err != nil {
		t.Fatal(err)
	}
	if msg.WireNonce != delivery.Nonce || env.Nonce != delivery.Nonce {
		t.Fatalf("wire_nonce = %q, delivered %q, want the delivery box's %q", msg.WireNonce, env.Nonce, delivery.Nonce)
	}
	if msg.ID != recipientIDOf(t, sent.Nonce) {
		t.Error("the record's id should still come from the sent copy's nonce")
	}
}

// A message stored before the nonce was kept still loads, with none.
func TestAnOldSentMessageWithoutATransportNonceStillLoads(t *testing.T) {
	sender, _ := testSender(t)
	seal, err := loadCurrentSealing(sender.Site)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMailbox(sender.Site).AppendSent("bob.example", sender.Domain, "older", seal.epoch, seal.pub, seal.dek, "", "unsent"); err != nil {
		t.Fatal(err)
	}
	m := storedSent(t, sender, "bob.example")
	if m.Plaintext != "older" || m.WireNonce != "" || m.Status != "unsent" {
		t.Errorf("old record = %+v", m.MailboxMessage)
	}
}
