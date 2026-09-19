package signing

import (
	"fmt"
	"os"
	"strings"
)

// Refusing writers — the write side of epic 47.
//
// ⛔ TOLERANT READING IS NOT TOLERANT WRITING. Every Go writer of the six signed
// JSON types rebuilds its file from the declared struct, so a member this build
// does not model is dropped on the way in, absent on the way out — and then
// SIGNED WITH THE USER'S KEY. That rewrites the author's record and vouches for
// the result (epic 46 R6's harm, generalised).
//
// The rule, three outcomes and no fourth:
//
//	nothing unrecognised on disk   → write, signed
//	something unrecognised on disk → REFUSE, naming the members
//	the user explicitly asks       → rewrite UNSIGNED, dropping them, and say so
//
// ⛔ NEVER PRESERVE-AND-RE-SIGN. It would have the user's key assert bytes this
// build could not interpret, and it has no canonicalisation answer: the JSON
// family's signed field order is Go struct declaration order, and an unknown
// member has no place in that sequence. (.well-known/polis, which is UNSIGNED,
// takes the opposite answer and preserves — see site.WellKnown.Extra.)
//
// ⭐ GuardRewrite reads the file ON DISK, not the caller's struct. That is what
// makes the refusal a property of the write point rather than of the caller: a
// writer that built its value from scratch, copied entries out of a loaded one,
// or never loaded at all is refused exactly the same way.

// RewriteRefusedError is the refusal. Artifact names the type for a person
// ("blessed.json", "tag file"); Fields are the unrecognised member paths.
type RewriteRefusedError struct {
	Artifact string
	Path     string
	Fields   []string
}

func (e *RewriteRefusedError) Error() string {
	return fmt.Sprintf("refusing to rewrite %s at %s: it was written by a newer polis and carries %s this version does not understand (%s). "+
		"Rewriting it would drop them and sign what was left with your key. Upgrade polis to modify it — or, to rewrite it WITHOUT them "+
		"and UNSIGNED, run `polis site rewrite-unsigned %s`",
		e.Artifact, e.Path, plural(len(e.Fields), "a field", "fields"), strings.Join(e.Fields, ", "), e.Path)
}

// GuardRewrite refuses a rewrite of the file at path when it carries members
// shape does not declare. A missing file is a creation and always allowed.
//
// shape is the FULL FILE struct, as for UnrecognisedFields. A file that does
// not parse yields no findings — it has already failed elsewhere, and every
// load path refuses it first.
func GuardRewrite(artifact, path string, shape any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s before rewriting it: %w", artifact, err)
	}
	if fields := UnrecognisedFields(raw, shape); len(fields) > 0 {
		return &RewriteRefusedError{Artifact: artifact, Path: path, Fields: fields}
	}
	return nil
}

// RewrittenUnsignedNote is what the escape hatch says it did, for a caller to
// print. Empty when nothing was dropped.
func RewrittenUnsignedNote(artifact string, dropped []string) string {
	if len(dropped) == 0 {
		return ""
	}
	return fmt.Sprintf("%s rewritten UNSIGNED without %s: %s. Its signature has been removed.",
		artifact, plural(len(dropped), "a field this polis does not understand", "fields this polis does not understand"),
		strings.Join(dropped, ", "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
