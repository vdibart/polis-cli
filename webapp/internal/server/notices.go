package server

import "time"

// Notices tells a site's owner, outside the app, that who can act on the site
// or how it is reached has changed: an agent connected, a passkey added or
// removed. The host supplies it; polis.pub's hosted service sends each as an
// email to the account's address. ⭐ nil on a self-hosted instance, where
// there is no mail and nothing is sent.
//
// ⛔ A notice carries facts, never a credential: no token, grant or key goes
// into one. The implementation must not block the request (send in the
// background) and must never log a mail body.
type Notices interface {
	AgentConnected(AgentConnectedNotice)
	PasskeyChanged(PasskeyNotice)
}

// AgentConnectedNotice is a new connection: its name, where it says it is
// from, how it was made (by agent sign-in, or by a token made for it), and
// the scopes it holds.
type AgentConnectedNotice struct {
	Name, From, How string
	Scopes          []string
	When            time.Time
}

// PasskeyNotice is a passkey added (Added) or removed; Count is how many the
// site holds afterwards.
type PasskeyNotice struct {
	Added bool
	Count int
	When  time.Time
}

// HowToken is how a connection made by minting a token for an agent is
// described in its notice.
const HowToken = "a token was made for it in enhance › agents"

// notifyAgentConnected tells the host, if it listens, that a connection was
// made.
func (s *Server) notifyAgentConnected(n AgentConnectedNotice) {
	if s.Notices == nil {
		return
	}
	if n.When.IsZero() {
		n.When = time.Now()
	}
	s.Notices.AgentConnected(n)
}

// notifyPasskey tells the host, if it listens, that a passkey was added or
// removed.
func (s *Server) notifyPasskey(added bool, count int) {
	if s.Notices == nil {
		return
	}
	s.Notices.PasskeyChanged(PasskeyNotice{Added: added, Count: count, When: time.Now()})
}
