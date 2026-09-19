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
