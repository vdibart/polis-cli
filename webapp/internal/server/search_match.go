package server

import (
	"strings"
	"unicode"
)

// searchMatches reports whether every word of query begins a word of text,
// case-insensitively: "go" finds "Go was designed" and "go-to", not "Hugo" or
// "ago". A word is a run of letters and digits; anything else separates.
// The discovery service's site search follows the same rule.
func searchMatches(text, query string) bool {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return true
	}
	lower := strings.ToLower(text)
	for _, w := range words {
		if !beginsAWord(lower, w) {
			return false
		}
	}
	return true
}

// beginsAWord reports whether w occurs in text at the start of a word.
func beginsAWord(text, w string) bool {
	for from := 0; ; {
		k := strings.Index(text[from:], w)
		if k < 0 {
			return false
		}
		at := from + k
		if at == 0 {
			return true
		}
		prev := []rune(text[:at])
		if r := prev[len(prev)-1]; !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return true
		}
		from = at + 1
	}
}

// siteHandle is the part of a domain a person is known by: without its
// top-level label, and for three labels or more without the last two
// (alice.polis.pub → alice, example.com → example). The discovery service's
// site search uses the same rule, so the shared hosting suffix never matches.
func siteHandle(domain string) string {
	labels := strings.Split(strings.ToLower(domain), ".")
	switch {
	case len(labels) >= 3:
		return strings.Join(labels[:len(labels)-2], ".")
	case len(labels) == 2:
		return labels[0]
	}
	return strings.ToLower(domain)
}

// profileMatches reports whether every word of query begins a word of the
// name or of the domain's handle, or begins the domain itself.
func profileMatches(name, domain, query string) bool {
	d := strings.ToLower(domain)
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if !beginsAWord(strings.ToLower(name), w) && !beginsAWord(siteHandle(d), w) && !strings.HasPrefix(d, w) {
			return false
		}
	}
	return true
}
