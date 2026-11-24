package ops

import "errors"

// ErrNotFound marks "the caller named something that does not exist", for
// handlers to wrap with %w.
//
// ⚠️ The HTTP layer maps a dispatch error to a status by looking for words in
// the message, and it tests for "invalid" before "not found" — so a theme
// called `invalid-theme` answered 400. A sentinel cannot be fooled by the name
// of the thing that was not found.
var ErrNotFound = errors.New("not found")

// RecipientError is a message that could not go to its recipient for a
// reason about the recipient, said in plain words that name the site. Its
// detail is logged where it happens, never answered.
type RecipientError struct {
	Site        string
	Unreachable bool // the site did not answer (else: it can't receive messages)
}

func (e *RecipientError) Error() string {
	if e.Unreachable {
		return e.Site + " could not be reached just now, so the message wasn't sent; try again later"
	}
	return e.Site + " can't receive messages from this site yet: it publishes no messages key"
}
