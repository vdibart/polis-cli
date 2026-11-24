package apidesc

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// The description publishes the whole grantable vocabulary, so the owner app's
// token maker renders the same scopes, in the same words, as the consent page
// (which reads Scopes). An app with its own list offered 7 of the 12.
func TestTheDescriptionPublishesTheScopeVocabulary(t *testing.T) {
	raw, ok := describeFor(t, mode.Custodied)["x-polis-scopes"].([]any)
	if !ok {
		t.Fatalf("x-polis-scopes missing or the wrong shape")
	}
	got := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		e, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("x-polis-scopes entry is not an object: %v", r)
		}
		got = append(got, e)
	}
	if len(got) != len(Scopes) {
		t.Fatalf("x-polis-scopes has %d entries, Scopes %d", len(got), len(Scopes))
	}
	for i, s := range Scopes {
		e := got[i]
		if e["name"] != s.Name || e["sentence"] != s.Sentence {
			t.Errorf("entry %d = %v, want %s: %q", i, e, s.Name, s.Sentence)
		}
		if e["agent"] != AgentGrantable(s.Name) {
			t.Errorf("%s: agent = %v, want %v", s.Name, e["agent"], AgentGrantable(s.Name))
		}
		_, warns := e["warning"]
		if warns != (s.Name == ScopePolicyWrite) {
			t.Errorf("%s: warning present = %v", s.Name, warns)
		}
	}
	// What the ruling fixed: tag is never an agent's; follow is.
	for _, e := range got {
		switch e["name"] {
		case ScopeTag:
			if e["agent"] != false {
				t.Error("tag is published as agent-grantable")
			}
		case ScopeFollow:
			if e["agent"] != true {
				t.Error("follow is published as not agent-grantable")
			}
		}
	}
}
