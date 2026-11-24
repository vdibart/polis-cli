package policy

import (
	"encoding/json"
	"strings"
)

// File names which of a site's two policy files a content belongs to.
type File string

const (
	PublicFile  File = "public"  // policies/rules.jsonl
	PrivateFile File = "private" // .polis/policies/rules.jsonl
)

// RelPath is the file's path relative to the site root.
func (f File) RelPath() string {
	if f == PrivateFile {
		return ".polis/policies/rules.jsonl"
	}
	return "policies/rules.jsonl"
}

// DefaultContent is the current default content for the file.
func (f File) DefaultContent() string {
	if f == PrivateFile {
		return DefaultPrivatePolicyContent()
	}
	return DefaultPublicPolicyContent()
}

// Class is what a policy file on disk is, to a writer deciding whether it may
// replace it.
//
// ⛔ A policy file is AUTHORED unless it is byte-for-byte (after ContentKey) a
// default polis itself wrote. A site's owner may edit either file, and the
// docs give recipes for doing so; only the current default and the defaults
// earlier builds wrote are ours to replace. Anything else is the site's own
// rules, and a maintenance tool that overwrote it would destroy the only copy.
type Class int

const (
	// Current is the current default. Nothing to do.
	Current Class = iota
	// PastDefault is exactly a default an earlier build wrote (see
	// PastDefaults). Replacing it with the current default is an upgrade:
	// the site never chose those rules, polis did.
	PastDefault
	// Authored parses and is not any default polis has written: the site's
	// own rules. Report it; never overwrite it.
	Authored
	// Unparseable does not parse. It may be corruption or an edit in
	// progress, and there is no telling which, so it is reported and never
	// overwritten either.
	Unparseable
)

func (c Class) String() string {
	switch c {
	case Current:
		return "current"
	case PastDefault:
		return "past-default"
	case Authored:
		return "authored"
	case Unparseable:
		return "unparseable"
	}
	return "unknown"
}

// PastDefaultContent is one exact policy file content an earlier build wrote
// on a user's behalf.
type PastDefaultContent struct {
	File    File
	Origin  string // which writer produced it, and in which release
	Content string
}

// PastDefaults is every policy file content, other than the current default,
// that polis has ever written without the site's owner choosing the rules.
// It is the ONLY list of what may be converged: a file matching none of these
// (or the current default) is the owner's own and is never overwritten.
//
// ⚠️ A content missing from this list is not dangerous, only noisy: that
// site's file is reported as authored on every run instead of being upgraded.
// When a default changes, add the outgoing content here in the same change.
//
// Compared by ContentKey, so the header's "generator" (which named whichever
// build wrote the file) does not matter; the format "version" does.
var PastDefaults = []PastDefaultContent{
	// ---- public: policies/rules.jsonl ----
	{
		File:   PublicFile,
		Origin: "0.56 Go init: the first default, before the file had a header",
		Content: `{"active":true,"policy":"allow pub.polis.comment.blessing from following"}
`,
	},
	{
		File:   PublicFile,
		Origin: "0.57 Go init, and the bash 0.56.0 -> 0.57.0 migration (fresh, or run over the 0.56 file)",
		Content: `{"version":1,"generator":"polis-cli-go/0.57.0"}
{"active":true,"policy":"emit pub.polis.comment.blessing from self"}
{"active":true,"policy":"emit pub.polis.comment.blessing from following"}
{"active":true,"policy":"emit pub.polis.comment.blessing from thread-blessed"}
`,
	},
	{
		File:   PublicFile,
		Origin: "0.61 Go init: DM rules and the catch-all moved into the public file",
		Content: `{"version":1,"generator":"polis-cli-go/0.61.0"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"emit pub.polis.comment.blessing from self"}
{"active":true,"policy":"emit pub.polis.comment.blessing from following"}
{"active":true,"policy":"emit pub.polis.comment.blessing from thread-blessed"}
{"active":true,"policy":"deny all from all"}
`,
	},

	// ---- private: .polis/policies/rules.jsonl ----
	{
		File:   PrivateFile,
		Origin: "0.56 DM rules appended by init and webapp start-up to a site with no private file, before the file had a header",
		Content: `{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
`,
	},
	{
		File:   PrivateFile,
		Origin: "the headerless DM-only file after Medic or Tailor appended the feed self-omit and catch-all rules (0.59-0.60)",
		Content: `{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"omit pub.polis.feed from self"}
{"active":true,"policy":"deny all from all"}
`,
	},
	{
		File:   PrivateFile,
		Origin: "0.57 Go init, and the bash 0.56.0 -> 0.57.0 migration on a site with no private file",
		Content: `{"version":1,"generator":"polis-cli-go/0.57.0"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"allow pub.polis.comment from all"}
{"active":true,"policy":"allow pub.polis.follow from all"}
{"active":true,"policy":"allow pub.polis.site from all"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"omit pub.polis.notification from self"}
`,
	},
	{
		File:   PrivateFile,
		Origin: "the bash 0.56.0 -> 0.57.0 migration run over the headerless DM-only file",
		Content: `{"version":1,"generator":"polis-cli/0.57.0"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"allow pub.polis.comment from all"}
{"active":true,"policy":"allow pub.polis.follow from all"}
{"active":true,"policy":"allow pub.polis.site from all"}
{"active":true,"policy":"omit pub.polis.notification from self"}
`,
	},
	{
		File:   PrivateFile,
		Origin: "0.59 Go init, which added the feed self-omit and catch-all rules; also Medic or Tailor appending them to the 0.57 default",
		Content: `{"version":1,"generator":"polis-cli-go/0.57.0"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"allow pub.polis.comment from all"}
{"active":true,"policy":"allow pub.polis.follow from all"}
{"active":true,"policy":"allow pub.polis.site from all"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"omit pub.polis.notification from self"}
{"active":true,"policy":"omit pub.polis.feed from self"}
{"active":true,"policy":"deny all from all"}
`,
	},
	{
		File:   PrivateFile,
		Origin: "Medic or Tailor appending the feed self-omit and catch-all rules to the bash-migrated file (0.59-0.60)",
		Content: `{"version":1,"generator":"polis-cli/0.57.0"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"allow pub.polis.comment from all"}
{"active":true,"policy":"allow pub.polis.follow from all"}
{"active":true,"policy":"allow pub.polis.site from all"}
{"active":true,"policy":"omit pub.polis.notification from self"}
{"active":true,"policy":"omit pub.polis.feed from self"}
{"active":true,"policy":"deny all from all"}
`,
	},
	{
		File:   PrivateFile,
		Origin: "0.61 Go init: the private file emptied to overrides only, still format v1",
		Content: `{"version":1,"generator":"polis-cli-go/0.61.0"}
`,
	},
}

// Classify says what a policy file's content is: the current default, a past
// default, the site's own rules, or something that does not parse. The
// current default and the past defaults are checked before parsing, because
// some past defaults hold rules today's parser rejects in a tenant file.
//
// ⭐ Deny rules added through TightenPrivate (marked added_by) are set aside
// first: a default with only such tightenings on top is still that default,
// so it keeps receiving upgrades (Upgraded puts the additions back) and is
// never reported as the site's own rules for them alone.
func Classify(content []byte, f File) Class {
	base, _ := SplitAdded(string(content))
	key := ContentKey(base)
	if key == ContentKey(f.DefaultContent()) {
		return Current
	}
	for _, pd := range PastDefaults {
		if pd.File == f && key == ContentKey(pd.Content) {
			return PastDefault
		}
	}
	if !Parses([]byte(base)) {
		return Unparseable
	}
	return Authored
}

// SplitAdded separates the rules TightenPrivate added (a deny rule marked
// added_by) from the rest of a rules file. base is the file without them, in
// its order; added are those lines exactly as written, in their order. A line
// marked added_by that is not a deny rule that parses stays in base: only a
// tightening is set aside, so a hand edit that loosened one is the site's own.
func SplitAdded(content string) (base string, added []string) {
	var kept []string
	for _, l := range strings.Split(content, "\n") {
		if isAddedTightening(l) {
			added = append(added, strings.TrimSpace(l))
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n"), added
}

func isAddedTightening(line string) bool {
	var entry struct {
		Policy  string `json:"policy"`
		AddedBy string `json:"added_by"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(line)), &entry) != nil || strings.TrimSpace(entry.AddedBy) == "" {
		return false
	}
	p, err := Parse(entry.Policy)
	return err == nil && p.Action == "deny"
}

// Upgraded is what a file Classify calls PastDefault becomes: the current
// default, with the rules TightenPrivate added kept, where TightenPrivate puts
// them (after the header, before the defaults, in their order).
func Upgraded(content []byte, f File) []byte {
	_, added := SplitAdded(string(content))
	def := f.DefaultContent()
	if len(added) == 0 {
		return []byte(def)
	}
	lines := strings.Split(strings.TrimRight(def, "\n"), "\n")
	at := 0
	if len(lines) > 0 && isHeader(lines[0]) {
		at = 1
	}
	out := append(append(append([]string(nil), lines[:at]...), added...), lines[at:]...)
	return []byte(strings.Join(out, "\n") + "\n")
}

// Parses reports whether every line of a rules.jsonl body is a JSON object and
// every rule in it parses. A header line (no "policy" member) is metadata, as
// the evaluator treats it. An empty file parses: it declares no rules.
func Parses(content []byte) bool {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &raw) != nil {
			return false
		}
		rawRule, isRule := raw["policy"]
		if !isRule {
			continue
		}
		var p Policy
		if json.Unmarshal([]byte(line), &p) != nil {
			return false
		}
		var rule string
		if json.Unmarshal(rawRule, &rule) != nil {
			return false
		}
		if _, err := Parse(rule); err != nil {
			return false
		}
	}
	return true
}

// ContentKey is a policy file's content for comparing it with a default: each
// line trimmed, blank lines dropped, and the header line's "generator"
// removed. The generator names the build that wrote the file and changes
// every release; comparing it would flag every default-policy site as drifted
// on each version bump. The header's format "version" still counts (it is how
// a v1 file is recognised for upgrade), as does every rule line.
func ContentKey(content string) string {
	var lines []string
	for _, l := range strings.Split(content, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 0 {
		var header map[string]json.RawMessage
		if json.Unmarshal([]byte(lines[0]), &header) == nil {
			_, isRule := header["policy"]
			if _, hasGen := header["generator"]; hasGen && !isRule {
				delete(header, "generator")
				if b, err := json.Marshal(header); err == nil {
					lines[0] = string(b)
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

// HeaderVersion is the format version in a policy file's header line, or 0
// when the file has no header.
func HeaderVersion(content []byte) int {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var h struct {
			Version int     `json:"version"`
			Policy  *string `json:"policy"`
		}
		if json.Unmarshal([]byte(line), &h) != nil || h.Policy != nil {
			return 0
		}
		return h.Version
	}
	return 0
}
