package pql

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// PQL sentences arrive in URLs anyone can send (/pql/<sentence>, /_/pql/...).
// Parse must never panic, and whatever it accepts must survive Compose and parse
// back to the same filter, or a link shared between two readers means two
// different things. Seeded from the golden corpus the Go, JS and TS parsers share.
func FuzzPQLParseComposeRoundTrip(f *testing.F) {
	fh, err := os.Open("../../../docs/general/reference/pql-golden.jsonl")
	if err != nil {
		f.Fatal(err)
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var row struct {
			Sentence string `json:"sentence"`
			URL      string `json:"url"`
		}
		if json.Unmarshal(sc.Bytes(), &row) == nil && row.Sentence != "" {
			f.Add(row.Sentence)
			f.Add(row.URL)
		}
	}
	f.Fuzz(func(t *testing.T, sentence string) {
		_, _ = ParseURL(sentence, "me")
		_, _ = ParseTenantRelative(sentence, "me")
		filter, err := Parse(sentence)
		if err != nil {
			return
		}
		composed, err := Compose(filter)
		if err != nil {
			return // a filter Compose cannot express is refused, not misread
		}
		again, err := Parse(composed)
		if err != nil {
			t.Fatalf("Parse(%q) composes to %q, which no longer parses: %v", sentence, composed, err)
		}
		if !reflect.DeepEqual(filter, again) {
			t.Fatalf("round trip changed the filter:\n in: %q -> %+v\nout: %q -> %+v", sentence, filter, composed, again)
		}
	})
}
