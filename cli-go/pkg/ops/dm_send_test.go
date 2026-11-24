package ops

import (
	"errors"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ The outbound address rule, once: a private recipient is refused
// everywhere except on the owner's own machine (local mode), before anything
// is sealed, saved or sent.
func TestASendToAPrivateAddressIsRefusedExceptOnTheOwnersMachine(t *testing.T) {
	c := DMSender{Site: storage.At(t.TempDir()), BaseURL: "https://maya.example"}
	for _, addr := range []string{"http://127.0.0.1:8080", "https://localhost", "https://10.0.0.5", "http://alice.example"} {
		_, err := SendDM(c, addr, "hello", "")
		if err == nil || !strings.Contains(err.Error(), "invalid recipient_url") {
			t.Errorf("%s: err = %v, want refused by the outbound rule", addr, err)
		}
	}
	// On the owner's own machine the rule lets a local recipient through: the
	// send then fails later, for want of a reachable site, not at the rule.
	c.AllowPrivateRecipients = true
	if _, err := SendDM(c, "http://127.0.0.1:1", "hello", ""); err != nil && strings.Contains(err.Error(), "invalid recipient_url") {
		t.Errorf("local mode refused a local recipient at the rule: %v", err)
	}
}

func TestASendThatCannotBeWrittenIsRefused(t *testing.T) {
	c := DMSender{Site: storage.At(t.TempDir()), BaseURL: "https://maya.example"}
	if _, err := SendDM(c, "", "hi", ""); err == nil {
		t.Error("no recipient was accepted")
	}
	if _, err := SendDM(c, "https://alice.example", "  ", ""); err == nil {
		t.Error("an empty message was accepted")
	}
	c.BaseURL = ""
	if _, err := SendDM(c, "https://alice.example", "hi", ""); err == nil {
		t.Error("a site with no address sent a message")
	}
}

// A message saved but not delivered answers as the sealed send does: its id,
// status unsent, and a warning saying why.
func TestAnUndeliveredMessageAnswersWithAWarning(t *testing.T) {
	f := DMSent{MessageID: "m1", ConversationID: "c1", Status: "unsent", Reason: errors.New("recipient down")}.Fields()
	if f["message_id"] != "m1" || f["status"] != "unsent" || f["warning"] != "recipient down" {
		t.Errorf("fields = %v", f)
	}
	if _, ok := (DMSent{MessageID: "m2", Status: "sent", Delivered: true}).Fields()["warning"]; ok {
		t.Error("a delivered message carries a warning")
	}
}
