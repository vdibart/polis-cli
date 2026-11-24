package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The owner's own changes to their rules (v5 spec 1.2, 2.4): the owner may
// replace either rule file with any rules that are valid, and put the
// defaults back at any time. ⛔ Never an agent's: an agent only tightens
// (TightenPrivate, RemoveAdded), and the operations built on these are
// owner-only.

// InvalidRulesError is a list of rules refused because some do not parse:
// each by its place in the list, the rule, and why.
type InvalidRulesError struct {
	Problems []RuleProblem
}

// RuleProblem is one rule that does not parse.
type RuleProblem struct {
	Index int    `json:"index"`
	Rule  string `json:"rule"`
	Why   string `json:"why"`
}

func (e *InvalidRulesError) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		parts = append(parts, fmt.Sprintf("rule %d %q: %s", p.Index+1, p.Rule, p.Why))
	}
	return "invalid rules, nothing changed: " + strings.Join(parts, "; ")
}

// SetRules replaces one of the site's rule files with rules, in order, each
// active. Every rule is checked first, and one that does not parse refuses
// the whole list (*InvalidRulesError), changing nothing. Empty lines are
// dropped. ⚠️ Replacing the private file also removes any rule an agent added
// there: the owner's act, in full.
func SetRules(site storage.Site, f File, rules []string) error {
	var kept []string
	var bad []RuleProblem
	for i, r := range rules {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if _, err := Parse(r); err != nil {
			bad = append(bad, RuleProblem{Index: i, Rule: r, Why: err.Error()})
			continue
		}
		kept = append(kept, r)
	}
	if len(bad) > 0 {
		return &InvalidRulesError{Problems: bad}
	}
	header, err := json.Marshal(map[string]interface{}{"version": 2, "generator": GetGenerator()})
	if err != nil {
		return err
	}
	lines := []string{string(header)}
	for _, r := range kept {
		line, err := json.Marshal(Policy{Active: true, Rule: r})
		if err != nil {
			return err
		}
		lines = append(lines, string(line))
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	return writeFile(site, f, strings.Join(lines, "\n")+"\n")
}

// ResetDefaults puts both rule files back to the defaults this build writes.
func ResetDefaults(site storage.Site) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := writeFile(site, PublicFile, DefaultPublicPolicyContent()); err != nil {
		return err
	}
	return writeFile(site, PrivateFile, DefaultPrivatePolicyContent())
}

func writeFile(site storage.Site, f File, body string) error {
	opts := []storage.PutOption{}
	if f == PrivateFile {
		opts = append(opts, storage.WithMode(0o600))
	}
	if err := site.Store().Put(context.Background(), f.RelPath(), []byte(body), opts...); err != nil {
		return fmt.Errorf("write %s rules: %w", f, err)
	}
	return nil
}
