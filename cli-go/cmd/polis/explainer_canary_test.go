package main

// The live half of the Signet explainer's provenance checks.
//
// Three excerpts on polis.pub/for/trust are marked *Live* and quote bytes from
// a real site. The offline tests in explainer_provenance_test.go cannot touch
// those — they can only check the four excerpts attributed to a specification
// in this repo. This is the check that can, and it is the only thing standing
// between "Live" and a claim that quietly stopped being true.
//
// ⛔ SKIPPED BY DEFAULT. It walks the public internet, so it is not a CI test:
//
//	POLIS_EXPLAINER_CANARY=1 go test ./cmd/polis/ -run TestExplainerLiveExcerpts -v
//
// ⭐ IT READS THE PAGE'S OWN LINKS RATHER THAN A LIST OF URLS. Each receipt on
// the page carries a provenance line, and since those lines link their sources
// the check can ask the page where to look. So it cannot drift from the page
// the way a hardcoded copy of the same three URLs would, and a new live excerpt
// is covered the moment it is added.
//
// ⛔ IT REPORTS, AND NEVER REPAIRS. A failure means a person decides whether to
// re-quote the new bytes, re-date the page, or demote the excerpt to
// spec-sourced. Rewriting the page from what was fetched would re-assert
// provenance nobody checked, which is the failure this whole area exists to
// prevent.

import (
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	receiptRe = regexp.MustCompile(`(?s)<div class="receipt">\s*<pre>(.*?)</pre>\s*<p class="provenance">(.*?)</p>`)
	hrefRe    = regexp.MustCompile(`<a href="([^"]+)"`)
	tagRe     = regexp.MustCompile(`<[^>]+>`)
	wsRe      = regexp.MustCompile(`\s+`)
)

// flatten strips markup, unescapes entities and collapses whitespace, so a
// fragment the page pretty-prints can be compared with the same bytes as the
// server serves them.
func flatten(s string) string {
	return strings.TrimSpace(wsRe.ReplaceAllString(html.UnescapeString(tagRe.ReplaceAllString(s, "")), " "))
}

// substantial rejects fragments too slight to prove anything — a lone brace, a
// closing bracket. Finding "}" in a JSON document is not evidence.
func substantial(fragment string) bool {
	n := 0
	for _, r := range fragment {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			n++
		}
	}
	return n >= 8
}

func TestExplainerLiveExcerptsStillMatch(t *testing.T) {
	if os.Getenv("POLIS_EXPLAINER_CANARY") == "" {
		t.Skip("set POLIS_EXPLAINER_CANARY=1 to check the explainer's live excerpts against the sites they quote")
	}

	page := readExplainer(t)
	blocks := receiptRe.FindAllStringSubmatch(page, -1)
	if len(blocks) == 0 {
		t.Fatal("found no receipt blocks on the page — the markup changed and this check is now blind")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	checked := 0

	for _, b := range blocks {
		pre, provenance := b[1], b[2]
		if !strings.Contains(flatten(provenance), "Live ·") {
			continue // attributed to a spec; the offline tests cover those
		}

		m := hrefRe.FindStringSubmatch(provenance)
		if m == nil {
			t.Errorf("a Live excerpt names no linkable source: %q", truncate(flatten(provenance), 90))
			continue
		}
		src := m[1]

		body, err := fetch(client, src)
		if err != nil {
			t.Errorf("%s: could not be fetched: %v", src, err)
			continue
		}
		checked++
		haystack := flatten(body)

		for _, line := range strings.Split(pre, "\n") {
			// The page abbreviates long keys and signatures with an ellipsis;
			// only the part before it was ever claimed to be real.
			fragment := flatten(strings.SplitN(line, "…", 2)[0])
			if !substantial(fragment) {
				continue
			}
			if !strings.Contains(haystack, fragment) {
				t.Errorf("DRIFT — the page quotes this as Live, and %s no longer contains it:\n    %s",
					src, truncate(fragment, 120))
			}
		}
	}

	if checked == 0 {
		t.Error("no Live excerpt was checked — either they all lost their links, or none could be fetched")
	}
	t.Logf("checked %d live excerpt(s)", checked)
}

func fetch(client *http.Client, url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "polis-explainer-canary")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", &statusError{url: url, code: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return string(data), err
}

type statusError struct {
	url  string
	code int
}

func (e *statusError) Error() string {
	return http.StatusText(e.code) + " (" + itoa(e.code) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
