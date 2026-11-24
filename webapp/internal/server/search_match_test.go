package server

import "testing"

func TestSearchMatchesAtTheStartOfAWord(t *testing.T) {
	cases := []struct {
		text, query string
		want        bool
	}{
		{"Go was designed at Google", "go", true},
		{"a go-to recipe", "go", true},
		{"Hugo, ago, cargo", "go", false},
		{"Hugo, and then: go!", "go", true},
		{"Notes on Postgres replication", "postgres repl", true},
		{"Notes on Postgres replication", "postgres sql", false},
		{"Écrire en français", "fran", true},
		{"Écrire en français", "çais", false},
		{"anything", "", true},
		{"(parenthetical) aside", "paren", true},
		{"version2 release", "2", false},
	}
	for _, c := range cases {
		if got := searchMatches(c.text, c.query); got != c.want {
			t.Errorf("searchMatches(%q, %q) = %v, want %v", c.text, c.query, got, c.want)
		}
	}
}

func TestProfileSearchMatchesTheHandleNotTheSharedSuffix(t *testing.T) {
	cases := []struct {
		name, domain, query string
		want                bool
	}{
		{"", "alice.polis.pub", "ali", true},
		{"", "alice.polis.pub", "polis", false},
		{"", "alice.polis.pub", "pub", false},
		{"", "polisfan.polis.pub", "polis", true},
		{"", "alice.polis.pub", "alice.polis.pub", true},
		{"Hugo", "hugo.polis.pub", "go", false},
		{"", "carol-go.polis.pub", "go", true},
		{"Alice Gopher", "alice.polis.pub", "gopher", true},
		{"", "example.com", "example", true},
		{"", "example.com", "com", false},
	}
	for _, c := range cases {
		if got := profileMatches(c.name, c.domain, c.query); got != c.want {
			t.Errorf("profileMatches(%q, %q, %q) = %v, want %v", c.name, c.domain, c.query, got, c.want)
		}
	}
}
