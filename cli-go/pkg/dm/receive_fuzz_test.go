package dm

import (
	"testing"
)

// A DM envelope is a POST body from any site on the network. ReceiveMessage
// must refuse what it cannot use without panicking, before decrypting, and
// never accept a fuzzed envelope as a message (it cannot carry a valid box to
// this receiver's key).
func FuzzReceiveEnvelope(f *testing.F) {
	for _, s := range []string{
		`{"version":1,"sender_domain":"alice.example.com","recipient_domain":"bob.example.com","encrypted_content":"AAAA","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","box_pub":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","recipient_epoch":0,"sender_epoch":0}`,
		`{"version":2}`,
		`{}`,
		`null`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		rcv, _, _, _, _ := testReceiver(t) // fresh: its rate limiter must not refuse before parsing
		msg, err := rcv.ReceiveMessage("alice.example.com", body, map[string]bool{"alice.example.com": true})
		if err == nil {
			t.Fatalf("a fuzzed envelope was accepted as a message: %+v\n%q", msg, body)
		}
	})
}
