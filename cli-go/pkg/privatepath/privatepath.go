// Package privatepath is the one declared list of names a polis site must
// never serve, and the exceptions to it.
//
// Three consumers read it, so they cannot drift apart:
//
//   - the shared serve handler refuses a request whose path matches it;
//   - a deploy (a site on Vercel, whose public surface is a deployment) never
//     puts a matching key in the deployment's static files;
//   - the outside-in probe (Judge, and polis validate <url>) asks for exactly
//     the names the handler refuses, so whatever is denied is what is checked.
//
// A name is matched in EVERY path segment, not only the first, and without
// regard to case: a dot-directory one level down (a theme cloned with its .git,
// a nested .ssh) is as private as one at the root, and a case-insensitive
// filesystem serves /.GIT/config from the same file as /.git/config.
//
// What polis itself creates and keeps private:
//
//   - .polis/ holds the identity key (id_ed25519, and legacy id_ed25519.old
//     backups), the DM keyring and storage salt, sessions and owner-authentication state,
//     webapp configuration, logs, drafts, pending and denied comments,
//     discovery-service state, the bundle registry and hooks.
//   - .env at the site root holds the site's runtime configuration, which on a
//     remote instance can include a recovery key. .env.example is written
//     beside it by the bash CLI.
//
// What a static host must never serve whoever created it: version-control
// directories, credential stores, key material, and editor and backup files.
//
// This package imports nothing from polis, so that every package that needs
// the list, the storage layer included, can import it without a cycle.
package privatepath

import (
	"path"
	"strings"
)

// Kind says where a private name comes from.
type Kind string

const (
	// PolisCreated names something polis itself writes.
	PolisCreated Kind = "polis-created"
	// Conventional names a secret by convention, whoever created it.
	Conventional Kind = "conventional"
)

// Match says how an entry's Pattern is compared with one path segment.
type Match int

const (
	// Exact: the segment equals the pattern.
	Exact Match = iota
	// Prefix: the segment begins with the pattern.
	Prefix
	// Suffix: the segment ends with the pattern.
	Suffix
)

// Scope says which segments an exception applies to.
type Scope int

const (
	// AnyDepth applies to a segment wherever it appears.
	AnyDepth Scope = iota
	// RootOnly applies only to the first segment.
	RootOnly
)

// Entry is one row of the list. An entry is either a denial or, with
// Exception set, a stated exception to the hidden-name rule.
type Entry struct {
	Pattern   string
	Match     Match
	Kind      Kind
	Exception bool
	Scope     Scope // exceptions only
	Reason    string
	// Probe holds concrete request paths an outside-in probe asks for to
	// test this entry. Denials carry at least one; exceptions carry none.
	Probe []string
}

// hiddenPrefix is the pattern of the hidden-name rule: a segment that begins
// with a dot is private unless an exception names it.
const hiddenPrefix = "."

// Entries is the list. Matching is case-insensitive.
var Entries = []Entry{
	// The hidden-name rule. Everything below that begins with a dot is
	// already covered by it; those entries are listed so the denial is
	// stated, not implied, and so the probe asks for each one.
	{Pattern: hiddenPrefix, Match: Prefix, Kind: Conventional,
		Reason: "a name beginning with a dot is hidden by convention and is never public unless an exception names it",
		Probe:  []string{"/.hidden/index.html"}},

	// Stated exceptions.
	{Pattern: ".well-known", Match: Exact, Kind: PolisCreated, Exception: true, Scope: RootOnly,
		Reason: "the site's public identity and protocol documents (RFC 8615); public by protocol, and only at the root"},
	{Pattern: ".versions", Match: Exact, Kind: PolisCreated, Exception: true, Scope: AnyDepth,
		Reason: "a post's version history, kept beside the post; public by design"},

	// What polis creates.
	{Pattern: ".polis", Match: Exact, Kind: PolisCreated,
		Reason: "the identity key, the DM keyring, sessions, configuration, drafts and every other private state",
		Probe:  []string{"/.polis/keys/id_ed25519", "/.polis/webapp-config.json"}},
	{Pattern: ".env", Match: Exact, Kind: PolisCreated,
		Reason: "runtime configuration, which on a remote instance can include a recovery key",
		Probe:  []string{"/.env"}},
	{Pattern: ".env.", Match: Prefix, Kind: PolisCreated,
		Reason: "variants of .env (.env.local, .env.production) and the .env.example polis writes",
		Probe:  []string{"/.env.local"}},

	// Version control.
	{Pattern: ".git", Match: Exact, Kind: Conventional, Reason: "git repository internals, including remotes and history",
		Probe: []string{"/.git/config", "/.git/HEAD", "/content/.git/config", "/content/.git/HEAD"}},
	{Pattern: ".hg", Match: Exact, Kind: Conventional, Reason: "Mercurial repository internals", Probe: []string{"/.hg/hgrc"}},
	{Pattern: ".svn", Match: Exact, Kind: Conventional, Reason: "Subversion working-copy internals", Probe: []string{"/.svn/entries"}},

	// Credential stores.
	{Pattern: ".ssh", Match: Exact, Kind: Conventional, Reason: "SSH keys and configuration",
		Probe: []string{"/.ssh/id_ed25519", "/site/.ssh/id_ed25519"}},
	{Pattern: ".gnupg", Match: Exact, Kind: Conventional, Reason: "GnuPG keyrings", Probe: []string{"/.gnupg/pubring.kbx"}},
	{Pattern: ".aws", Match: Exact, Kind: Conventional, Reason: "AWS credentials", Probe: []string{"/.aws/credentials"}},
	{Pattern: ".npmrc", Match: Exact, Kind: Conventional, Reason: "npm registry tokens", Probe: []string{"/.npmrc"}},
	{Pattern: ".netrc", Match: Exact, Kind: Conventional, Reason: "stored login credentials", Probe: []string{"/.netrc"}},
	{Pattern: ".htpasswd", Match: Exact, Kind: Conventional, Reason: "web server password hashes", Probe: []string{"/.htpasswd"}},
	{Pattern: ".DS_Store", Match: Exact, Kind: Conventional, Reason: "macOS folder metadata, which lists a directory's file names", Probe: []string{"/.DS_Store"}},

	// Key material.
	{Pattern: "id_rsa", Match: Prefix, Kind: Conventional, Reason: "an RSA private key (or its public half beside it)",
		Probe: []string{"/id_rsa", "/site/id_rsa"}},
	{Pattern: "id_ed25519", Match: Prefix, Kind: Conventional, Reason: "an Ed25519 private key, polis's own key type",
		Probe: []string{"/id_ed25519"}},
	{Pattern: "id_ecdsa", Match: Prefix, Kind: Conventional, Reason: "an ECDSA private key", Probe: []string{"/id_ecdsa"}},
	{Pattern: ".pem", Match: Suffix, Kind: Conventional, Reason: "PEM key or certificate material", Probe: []string{"/server.pem"}},
	{Pattern: ".key", Match: Suffix, Kind: Conventional, Reason: "a private key file", Probe: []string{"/server.key"}},
	{Pattern: ".p12", Match: Suffix, Kind: Conventional, Reason: "a PKCS#12 key bundle", Probe: []string{"/cert.p12"}},

	// Editor and backup files, which are copies of whatever was being edited.
	{Pattern: ".swp", Match: Suffix, Kind: Conventional, Reason: "a vim swap file", Probe: []string{"/index.html.swp"}},
	{Pattern: "~", Match: Suffix, Kind: Conventional, Reason: "an editor backup copy", Probe: []string{"/index.html~"}},
	{Pattern: ".bak", Match: Suffix, Kind: Conventional, Reason: "a backup copy", Probe: []string{"/index.html.bak"}},
	{Pattern: ".orig", Match: Suffix, Kind: Conventional, Reason: "a merge or patch backup copy", Probe: []string{"/index.html.orig"}},
}

func (e Entry) matches(seg string) bool {
	p := strings.ToLower(e.Pattern)
	switch e.Match {
	case Prefix:
		return strings.HasPrefix(seg, p)
	case Suffix:
		return strings.HasSuffix(seg, p)
	default:
		return seg == p
	}
}

func excepted(seg string, depth int) bool {
	for _, e := range Entries {
		if !e.Exception || (e.Scope == RootOnly && depth != 0) {
			continue
		}
		if e.matches(seg) {
			return true
		}
	}
	return false
}

// Denied reports whether p names something that must never be served. p is a
// slash-separated path, with or without a leading slash; every segment is
// checked, case-insensitively. An exception only exempts its own segment from
// the hidden-name rule: a listed name below .versions is still denied.
func Denied(p string) bool {
	// ⛔ A backslash is a separator on Windows; treat it as one here too, so a
	// backslash-joined path cannot hide a private segment from this check.
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" {
		return false
	}
	for depth, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." {
			continue
		}
		seg = strings.ToLower(seg)
		hiddenExempt := excepted(seg, depth)
		for _, e := range Entries {
			if e.Exception || !e.matches(seg) {
				continue
			}
			if hiddenExempt && e.Pattern == hiddenPrefix {
				continue
			}
			return true
		}
	}
	return false
}

// KeyPath is where a site keeps its private identity key. It is the one probe
// target whose exposure is a compromised identity, so callers probing a single
// path per site use this one.
const KeyPath = "/.polis/keys/id_ed25519"

// ProbeTargets returns every request path an outside-in probe asks for: each
// denial's own probe paths, plus spellings of KeyPath that a handler checking
// only the literal string would let through.
func ProbeTargets() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, e := range Entries {
		for _, p := range e.Probe {
			add(p)
		}
	}
	for _, v := range keyPathVariants() {
		add(v)
	}
	return out
}

// keyPathVariants are encodings and casings of KeyPath. Each one, once a server
// has decoded and cleaned it, names the same file.
func keyPathVariants() []string {
	rest := strings.TrimPrefix(KeyPath, "/.polis")
	return []string{
		"/%2Epolis" + rest,
		"/%2epolis" + rest,
		"/.POLIS" + rest,
		"/./.polis" + rest,
		"/content/../.polis" + rest,
		path.Join("/content", "%2E%2E", ".polis") + rest,
	}
}
