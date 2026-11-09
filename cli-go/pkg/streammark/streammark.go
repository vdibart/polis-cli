// Package streammark holds the owner's stream marks: rules that pick out posts
// in the owner's stream and mark them, for example a post by
// discover.polis.pub that mentions "platform", whose timeline dot then shows
// a "!".
//
// ⚠️ EXPERIMENTAL. Part of an exploratory feature: the owner's AI changing
// their site through the instance's API. This package's API may change or be
// removed without notice; do not depend on it.
//
// ⭐ BEHAVIOUR AS DATA. A rule is data the owner (or their AI, through the
// API) writes; the instance evaluates it and polis-shipped code draws the
// mark. Nothing a rule says is ever executed, and a rule can only choose a
// mark from a closed set. This is the first item of the declared vocabulary
// of effects through which customisations reach a site.
//
// ⛔ PLAIN WORDS, NEVER PATTERNS. Keywords match case-insensitively on word
// boundaries; there is no regex, so a crafted rule cannot make matching slow.
// The number and size of rules are capped.
package streammark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Key is where the rules live: private, never a public file.
const Key = ".polis/webapp/stream-marks.json"

// Marks are the marks a rule may set. The drawing of each is polis's.
const MarkAlert = "alert"

var marks = map[string]bool{MarkAlert: true}

// Limits, so a rule file stays small and matching stays cheap.
const (
	MaxRules       = 20
	MaxKeywords    = 10
	MaxKeywordLen  = 40
	maxRuleIDLen   = 40
	maxDomainLen   = 253
	defaultMarkFor = MarkAlert
)

// Rule marks the posts by one author that mention any of its keywords.
type Rule struct {
	ID           string   `json:"id"`
	AuthorDomain string   `json:"author_domain"`
	AnyOf        []string `json:"any_of"`
	Mark         string   `json:"mark"`
}

// File is the rules file.
type File struct {
	Rules []Rule `json:"rules"`
}

var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// Normalize validates the rules and returns them in canonical form: domains
// and keywords lower-cased and trimmed, a missing mark set to alert, a missing
// id derived from the position. Every problem is named; none is guessed at.
func Normalize(f File) (File, error) {
	if len(f.Rules) > MaxRules {
		return File{}, fmt.Errorf("at most %d rules, got %d", MaxRules, len(f.Rules))
	}
	out := File{Rules: make([]Rule, 0, len(f.Rules))}
	seen := map[string]bool{}
	for i, r := range f.Rules {
		n := i + 1
		r.AuthorDomain = strings.ToLower(strings.TrimSpace(r.AuthorDomain))
		if r.AuthorDomain == "" || len(r.AuthorDomain) > maxDomainLen || !domainRe.MatchString(r.AuthorDomain) {
			return File{}, fmt.Errorf("rule %d: author_domain must be a site's domain, such as discover.polis.pub", n)
		}
		if len(r.AnyOf) == 0 || len(r.AnyOf) > MaxKeywords {
			return File{}, fmt.Errorf("rule %d: any_of needs 1 to %d keywords", n, MaxKeywords)
		}
		kws := make([]string, 0, len(r.AnyOf))
		for _, k := range r.AnyOf {
			k = strings.ToLower(strings.TrimSpace(k))
			if k == "" || len(k) > MaxKeywordLen {
				return File{}, fmt.Errorf("rule %d: each keyword is 1 to %d characters", n, MaxKeywordLen)
			}
			if !plain(k) {
				return File{}, fmt.Errorf("rule %d: keyword %q must be plain words (letters, digits, spaces, hyphens, apostrophes): patterns are not supported", n, k)
			}
			kws = append(kws, k)
		}
		r.AnyOf = kws
		if r.Mark == "" {
			r.Mark = defaultMarkFor
		}
		if !marks[r.Mark] {
			return File{}, fmt.Errorf("rule %d: mark must be %q", n, MarkAlert)
		}
		r.ID = strings.TrimSpace(r.ID)
		if r.ID == "" {
			r.ID = fmt.Sprintf("rule-%d", n)
		}
		if len(r.ID) > maxRuleIDLen || seen[r.ID] {
			return File{}, fmt.Errorf("rule %d: id must be unique and at most %d characters", n, maxRuleIDLen)
		}
		seen[r.ID] = true
		out.Rules = append(out.Rules, r)
	}
	return out, nil
}

func plain(k string) bool {
	for _, c := range k {
		if !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == ' ' || c == '-' || c == '\'') {
			return false
		}
	}
	return true
}

// Load reads the rules. A site with no file has none.
func Load(site storage.Site) (File, error) {
	data, err := site.Store().Get(context.Background(), Key)
	if errors.Is(err, fs.ErrNotExist) {
		return File{Rules: []Rule{}}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read stream marks: %w", err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("parse stream marks: %w", err)
	}
	// ⛔ What was written is checked again on the way in: only Save writes the
	// file today, but a file that did not come through Save (hand-edited, an
	// older build, a restored backup) must not reach a page unchecked.
	n, err := Normalize(f)
	if err != nil {
		return File{}, fmt.Errorf("stream marks on disk are not valid: %w", err)
	}
	if n.Rules == nil {
		n.Rules = []Rule{}
	}
	return n, nil
}

// Save validates and writes the rules (mode 600), replacing what was there.
// An empty list removes every mark.
func Save(site storage.Site, f File) (File, error) {
	n, err := Normalize(f)
	if err != nil {
		return File{}, err
	}
	b, _ := json.MarshalIndent(n, "", "  ")
	if err := site.Store().Put(context.Background(), Key, append(b, '\n'), storage.WithMode(0o600)); err != nil {
		return File{}, fmt.Errorf("write stream marks: %w", err)
	}
	return n, nil
}

// Match returns the mark the first matching rule sets for a post by author
// whose text is texts, or "" when none matches.
func (f File) Match(author string, texts ...string) string {
	author = strings.ToLower(strings.TrimSpace(author))
	if author == "" || len(f.Rules) == 0 {
		return ""
	}
	var lowered string
	for _, r := range f.Rules {
		if r.AuthorDomain != author {
			continue
		}
		if lowered == "" {
			lowered = strings.ToLower(strings.Join(texts, "\n"))
		}
		for _, k := range r.AnyOf {
			if containsWord(lowered, k) {
				return r.Mark
			}
		}
	}
	return ""
}

// containsWord reports whether word occurs in text with no letter or digit
// immediately before or after it. Both are already lower-cased.
func containsWord(text, word string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], word)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(word)
		if !wordRune(lastRune(text[:start])) && !wordRune(firstRune(text[end:])) {
			return true
		}
		from = start + 1
	}
}

func wordRune(r rune) bool { return r != 0 && (unicode.IsLetter(r) || unicode.IsDigit(r)) }

func lastRune(s string) rune {
	if s == "" {
		return 0
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}

func firstRune(s string) rune {
	if s == "" {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(s)
	return r
}
