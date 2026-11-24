package ops

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ SENDING A SERVER-SEALED MESSAGE HAS ONE IMPLEMENTATION.
//
// The API's dm.send, the old browser app's send and polis dm send each built
// their own sender, and differed in the outbound address check, the HTTP
// client, whether the send's events were logged, and what a failed delivery
// answered. SendDM is the one send; the old app's route keeps its own until it
// retires.
//
// ⛔ THE OUTBOUND ADDRESS RULE, written once: the recipient's address must be
// https and a public host (dm.ValidateRecipientURL), and every connection is to a
// public address (dm.PublicOnly, checked on the resolved IP), EVERYWHERE EXCEPT an
// instance in local mode, the owner's own machine, where another instance on
// the same machine is a legitimate recipient. A site that can be reached from
// the network must never be made to send to a private address on someone
// else's behalf.

// DMSender is the site a message is sent from and how it sends.
type DMSender struct {
	Site       storage.Site
	PrivateKey []byte
	PublicKey  []byte
	BaseURL    string
	// HTTPClient is the host's shared client (nil: the dm package's own).
	HTTPClient *http.Client
	// AllowPrivateRecipients is true only in local mode (the rule above).
	AllowPrivateRecipients bool
	// Emit logs the send's lifecycle events (dm.send.initiated, .delivered,
	// .failed); nil logs nothing.
	Emit func(action string, fields map[string]any)
}

// DMSent is what a send came to. A message that could not be delivered is
// still saved, as unsent, with the reason; it can be retried.
type DMSent struct {
	MessageID      string
	ConversationID string
	Status         string // sent, or unsent
	Delivered      bool
	Reason         error // why it was not delivered
}

// SendDM seals and sends a message. The error is for a send that saved
// nothing (a bad request, a recipient that cannot receive); a message saved
// but not delivered is a DMSent with Delivered false and its Reason.
func SendDM(c DMSender, recipientURL, content, replyToID string) (DMSent, error) {
	if recipientURL == "" {
		return DMSent{}, fmt.Errorf("recipient_url required")
	}
	if strings.TrimSpace(content) == "" {
		return DMSent{}, fmt.Errorf("content required")
	}
	if !c.AllowPrivateRecipients {
		if err := dm.ValidateRecipientURL(recipientURL); err != nil {
			return DMSent{}, fmt.Errorf("invalid recipient_url: %w", err)
		}
	}
	domain := dm.ExtractDomainFromURL(c.BaseURL)
	if domain == "" {
		return DMSent{}, fmt.Errorf("site base URL not configured")
	}
	hc := c.HTTPClient
	if !c.AllowPrivateRecipients {
		// The name was checked; so is every address it leads to, on connect.
		hc = dm.PublicOnly(hc)
	}
	sender := dm.NewSenderWithHTTP(c.PrivateKey, c.PublicKey, domain, c.Site, hc)
	if c.Emit != nil {
		sender.Logger = dm.EventFunc(c.Emit)
	}
	msg, err := sender.SendMessage(recipientURL, content, replyToID)
	if err != nil && msg == nil {
		site := dm.ExtractDomainFromURL(recipientURL)
		switch {
		case errors.Is(err, dm.ErrRecipientUnreachable):
			return DMSent{}, &RecipientError{Site: site, Unreachable: true}
		case errors.Is(err, dm.ErrRecipientNoMessagesKey):
			return DMSent{}, &RecipientError{Site: site}
		}
		return DMSent{}, fmt.Errorf("send: %w", err)
	}
	return DMSent{
		MessageID:      msg.ID,
		ConversationID: dm.ComputeConversationID(sender.Domain, dm.ExtractDomainFromURL(recipientURL)),
		Status:         msg.Status,
		Delivered:      err == nil,
		Reason:         err,
	}, nil
}

// Fields is the send as every answer states it, delivered or not: the same
// keys the browser's sealed send answers with (a warning when it is saved but
// not delivered).
func (d DMSent) Fields() map[string]any {
	f := map[string]any{"message_id": d.MessageID, "conversation_id": d.ConversationID, "status": d.Status}
	if d.Reason != nil {
		f["warning"] = d.Reason.Error()
	}
	return f
}
