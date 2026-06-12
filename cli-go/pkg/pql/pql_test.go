package pql

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Canonical shared files live at the repo root, outside the cli-go module.
// `go test` runs with CWD = this package dir (cli-go/pkg/pql), so the repo
// root is three levels up.
const (
	goldenPath = "../../../docs/general/reference/pql-golden.jsonl"
	vocabPath  = "../../../docs/general/reference/pql-vocabulary.json"
)

type goldenExpected struct {
	Qualifier string  `json:"qualifier"`
	Type      string  `json:"type"`
	Relation  string  `json:"relation"`
	Scope     string  `json:"scope"`
	Modifier  *string `json:"modifier"`
}

type goldenLine struct {
	Sentence     string          `json:"sentence"`
	URL          string          `json:"url"`
	DefaultScope string          `json:"defaultScope"`
	Expected     json.RawMessage `json:"expected"`
	Notes        string          `json:"notes"`
}

// TestGoldenCorpus is the cross-language contract: the Go parser must agree
// with the JS and TS parsers on every line of docs/general/reference/pql-golden.jsonl.
func TestGoldenCorpus(t *testing.T) {
	f, err := os.Open(goldenPath)
	if err != nil {
		t.Fatalf("open golden corpus %s: %v", goldenPath, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var gl goldenLine
		if err := json.Unmarshal([]byte(raw), &gl); err != nil {
			t.Fatalf("line %d: bad golden JSON: %v", lineNo, err)
		}

		var got *Filter
		if gl.DefaultScope != "" {
			got, err = ParseTenantRelative(gl.Sentence, gl.DefaultScope)
		} else {
			got, err = Parse(gl.Sentence)
		}

		if strings.TrimSpace(string(gl.Expected)) == "null" {
			if err == nil {
				t.Errorf("line %d %q: expected parse failure, got %+v", lineNo, gl.Sentence, got)
			}
			continue
		}

		if err != nil {
			t.Errorf("line %d %q: unexpected parse error: %v", lineNo, gl.Sentence, err)
			continue
		}

		var exp goldenExpected
		if err := json.Unmarshal(gl.Expected, &exp); err != nil {
			t.Fatalf("line %d: bad expected JSON: %v", lineNo, err)
		}
		wantMod := ""
		if exp.Modifier != nil {
			wantMod = *exp.Modifier
		}
		if got.Qualifier != exp.Qualifier || got.Type != exp.Type ||
			got.Relation != exp.Relation || got.Scope != exp.Scope || got.Modifier != wantMod {
			t.Errorf("line %d %q:\n  got  %+v\n  want {Qualifier:%s Type:%s Relation:%s Scope:%s Modifier:%s}",
				lineNo, gl.Sentence, *got, exp.Qualifier, exp.Type, exp.Relation, exp.Scope, wantMod)
			continue
		}

		// Round-trip: ComposeURL should reproduce the golden URL form.
		if gl.URL != "" {
			gotURL := ComposeURL(got, "", gl.DefaultScope)
			wantURL := "/pql/" + gl.URL
			if gotURL != wantURL {
				t.Errorf("line %d %q: ComposeURL = %q, want %q", lineNo, gl.Sentence, gotURL, wantURL)
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan golden corpus: %v", err)
	}
}

// vocabFile is the subset of pql-vocabulary.json the Go tables must match.
type vocabFile struct {
	Qualifiers []string          `json:"qualifiers"`
	Relations  []string          `json:"relations"`
	Types      map[string]string `json:"types"`
	ScopeAtoms []struct {
		Tokens []string `json:"tokens"`
		Value  string   `json:"value"`
	} `json:"scope_atoms"`
	FirstPersonScopes []string `json:"first_person_scopes"`
	Modifiers         []struct {
		Tokens []string `json:"tokens"`
		Value  string   `json:"value"`
	} `json:"modifiers"`
	HandlePattern    string `json:"handle_pattern"`
	EventTypePattern string `json:"event_type_pattern"`
}

// TestVocabularyMatchesCanonical closes the drift loop: the hand-mirrored Go
// tables must match docs/general/reference/pql-vocabulary.json exactly. Editing the JSON
// without updating pql.go (or vice-versa) fails here.
func TestVocabularyMatchesCanonical(t *testing.T) {
	data, err := os.ReadFile(vocabPath)
	if err != nil {
		t.Fatalf("read vocabulary %s: %v", vocabPath, err)
	}
	var v vocabFile
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parse vocabulary: %v", err)
	}

	for _, q := range v.Qualifiers {
		if !qualifiers[q] {
			t.Errorf("qualifier %q in JSON missing from Go tables", q)
		}
	}
	if len(v.Qualifiers) != len(qualifiers) {
		t.Errorf("qualifier count mismatch: JSON %d, Go %d", len(v.Qualifiers), len(qualifiers))
	}

	for _, r := range v.Relations {
		if !relations[r] {
			t.Errorf("relation %q in JSON missing from Go tables", r)
		}
	}
	if len(v.Relations) != len(relations) {
		t.Errorf("relation count mismatch: JSON %d, Go %d", len(v.Relations), len(relations))
	}

	for alias, internal := range v.Types {
		if typeToInternal[alias] != internal {
			t.Errorf("type %q: JSON->%q, Go->%q", alias, internal, typeToInternal[alias])
		}
	}
	if len(v.Types) != len(typeToInternal) {
		t.Errorf("type count mismatch: JSON %d, Go %d", len(v.Types), len(typeToInternal))
	}

	if len(v.ScopeAtoms) != len(scopeAtoms) {
		t.Fatalf("scope atom count mismatch: JSON %d, Go %d", len(v.ScopeAtoms), len(scopeAtoms))
	}
	for i, a := range v.ScopeAtoms {
		if scopeAtoms[i].value != a.Value || strings.Join(scopeAtoms[i].tokens, " ") != strings.Join(a.Tokens, " ") {
			t.Errorf("scope atom %d mismatch: JSON %+v, Go %+v", i, a, scopeAtoms[i])
		}
	}

	if len(v.Modifiers) != len(modifierClauses) {
		t.Fatalf("modifier count mismatch: JSON %d, Go %d", len(v.Modifiers), len(modifierClauses))
	}
	for i, m := range v.Modifiers {
		if modifierClauses[i].value != m.Value || strings.Join(modifierClauses[i].tokens, " ") != strings.Join(m.Tokens, " ") {
			t.Errorf("modifier %d mismatch: JSON %+v, Go %+v", i, m, modifierClauses[i])
		}
	}

	for _, s := range v.FirstPersonScopes {
		if !FirstPersonScopes[s] {
			t.Errorf("first-person scope %q in JSON missing from Go", s)
		}
	}
	if len(v.FirstPersonScopes) != len(FirstPersonScopes) {
		t.Errorf("first-person scope count mismatch: JSON %d, Go %d", len(v.FirstPersonScopes), len(FirstPersonScopes))
	}

	// The canonical JSON stores the pattern BODY; JS/TS apply case-insensitivity
	// via the /i flag, Go applies it inline with (?i). Compare modulo that prefix.
	if handlePattern.String() != "(?i)"+v.HandlePattern {
		t.Errorf("handle pattern mismatch:\n JSON %q\n Go   %q", "(?i)"+v.HandlePattern, handlePattern.String())
	}
	if v.EventTypePattern != eventTypePattern.String() {
		t.Errorf("event-type pattern mismatch:\n JSON %q\n Go   %q", v.EventTypePattern, eventTypePattern.String())
	}
}

func TestParseURL(t *testing.T) {
	cases := []struct {
		path         string
		defaultScope string
		wantType     string
		wantScope    string
		wantErr      bool
	}{
		{"/pql/all+posts+from+me+by+date", "", "posts", "me", false},
		{"/_/pql/all+activity+from+my+network", "", "activity", "my-network", false},
		{"/pql/all+posts+by+date", "@alice.polis.pub", "posts", "@alice.polis.pub", false},
		{"/pql/all+comments+about+alice.polis.pub", "", "comments", "@alice.polis.pub", false},
		{"/settings", "", "", "", true},
	}
	for _, c := range cases {
		f, err := ParseURL(c.path, c.defaultScope)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseURL(%q): expected error", c.path)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseURL(%q): %v", c.path, err)
			continue
		}
		if f.Type != c.wantType || f.Scope != c.wantScope {
			t.Errorf("ParseURL(%q) = {Type:%s Scope:%s}, want {Type:%s Scope:%s}", c.path, f.Type, f.Scope, c.wantType, c.wantScope)
		}
	}
}

func TestComposeURLTenantOmission(t *testing.T) {
	// from + default tenant scope -> clause omitted
	f := &Filter{Qualifier: "all", Type: "posts", Relation: "from", Scope: "@alice.polis.pub", Modifier: "by-date"}
	got := ComposeURL(f, "", "@alice.polis.pub")
	if got != "/pql/all+posts+by+date" {
		t.Errorf("tenant omission: got %q", got)
	}
	// about + same scope -> NOT omitted (about is semantically distinct)
	fa := &Filter{Qualifier: "all", Type: "activity", Relation: "about", Scope: "@alice.polis.pub"}
	gotA := ComposeURL(fa, "", "@alice.polis.pub")
	if gotA != "/pql/all+activity+about+alice.polis.pub" {
		t.Errorf("about not omitted: got %q", gotA)
	}
	// from non-default scope -> NOT omitted
	fn := &Filter{Qualifier: "all", Type: "posts", Relation: "from", Scope: "my-network"}
	gotN := ComposeURL(fn, "", "@alice.polis.pub")
	if gotN != "/pql/all+posts+from+my+network" {
		t.Errorf("non-default not preserved: got %q", gotN)
	}
}

// Sanity: ensure the golden file is actually present and non-trivial, so a
// missing-file scenario can't silently pass.
func TestGoldenFilePresent(t *testing.T) {
	abs, _ := filepath.Abs(goldenPath)
	info, err := os.Stat(goldenPath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("golden corpus missing/empty at %s (abs %s): %v", goldenPath, abs, err)
	}
}
