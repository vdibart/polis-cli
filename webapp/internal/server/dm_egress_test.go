package server

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// ⛔ The outbound address rule: away from the owner's own machine, a message
// to a private address is refused. (The old app's /api/dm/send, which once
// checked only where RestrictDMEgress was set, is gone; /v1 is the one send.)
func TestEverySendRefusesAPrivateAddressAwayFromTheOwnersMachine(t *testing.T) {
	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		t.Fatal(err)
	}
	s := newConfiguredServer(t)
	s.Mode = mode.RemoteServerSigned // set before the routes are built, as at start
	h := editorRoutes(s, webFS)
	owner := mint(t, s)
	body := `{"recipient_url":"http://127.0.0.1:9/","content":"hello"}`

	code, out := apiCall(t, h, "POST", "/v1/content/dm/actions/send", owner, body)
	if code < 400 || code >= 500 {
		t.Errorf("/v1 dm.send to a private address = %d %v, want refused", code, out)
	}
}

// ⛔ The same rule on the owner app's other message routes (the recipient's
// key, its protection status, a browser-sealed send), which used to check only
// on hosted: a remote instance refuses a private recipient too.
func TestTheMessageRoutesRefuseAPrivateAddressAwayFromTheOwnersMachine(t *testing.T) {
	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		t.Fatal(err)
	}
	s := newConfiguredServer(t)
	s.Mode = mode.RemoteServerSigned
	h := editorRoutes(s, webFS)
	owner := mint(t, s)
	private := "http%3A%2F%2F127.0.0.1%3A9%2F"
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/v1/messages/recipient-key?recipient_url=" + private, ""},
		{"GET", "/v1/messages/protection-status?recipient_url=" + private, ""},
		{"POST", "/v1/messages/send-sealed", `{"recipient_url":"http://127.0.0.1:9/","box_pub":"x",` +
			`"delivery":{"ciphertext":"x","nonce":"x"},"sent":{"ciphertext":"x","nonce":"x"}}`},
	} {
		code, out := apiCall(t, h, c.method, c.path, owner, c.body)
		if code != 400 || !strings.Contains(fmt.Sprint(out), "nvalid recipient URL") {
			t.Errorf("%s %s = %d %v, want refused by the outbound rule", c.method, c.path, code, out)
		}
	}
	// On the owner's own machine the rule lets a local recipient through.
	if s.allowPrivateDMRecipients() {
		t.Fatal("a remote instance allows private recipients")
	}
	s.Mode = mode.Local
	if !s.allowPrivateDMRecipients() || s.dmHTTPClient() != s.SharedHTTPClient {
		t.Error("local mode does not allow a private recipient")
	}
	s.RestrictDMEgress = true
	if s.allowPrivateDMRecipients() {
		t.Error("hosted's restriction did not hold in local mode")
	}
}
