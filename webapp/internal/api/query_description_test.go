package api

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/pql"
)

// TestTheQueryDescriptionStatesTheGrammar: query_pql's description (the one
// text an agent is sure to read) carries the grammar generated from the
// parser's own tables, every type in it, the one-type rule and what search
// matches. pql's TestVocabularyDescribesWhatParseAccepts keeps the generated
// text in step with what Parse accepts, so neither can drift.
func TestTheQueryDescriptionStatesTheGrammar(t *testing.T) {
	d := queryOperation.Description
	if !strings.Contains(d, pql.Vocabulary()) {
		t.Fatal("the query description does not carry pql.Vocabulary()")
	}
	for _, typ := range []string{"posts", "comments", "profiles", "messages", "drafts", "activity"} {
		if !strings.Contains(d, typ) {
			t.Errorf("the description omits the type %q", typ)
		}
	}
	for _, want := range []string{"One type per sentence", "activity covers posts and comments", "never a post's body", "title or excerpt"} {
		if !strings.Contains(d, want) {
			t.Errorf("the description does not say %q", want)
		}
	}
}
