package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
)

// The owner-auth screens render from the registry, never per provider.
//
// ⛔⛔ THE SHORTCUT THIS GUARDS IS THE ONE MOST LIKELY TO BE TAKEN: a
// good-looking first release with a passkey section, a passkey button and an
// `if (name === 'passkey')`. Two providers are already deferred on non-design
// grounds, and each would need that undone. ⭐ So the rule mirrors the stub
// provider test on the server: a provider the SPA has never heard of must
// render with no change to the SPA, which is only true if the SPA names no
// provider at all. The single exception is the ceremony table in
// owner-auth.js, which is keyed by ceremony KIND and may mention a provider in
// its labels' prose.

const (
	ceremonyTableBegin = "CEREMONY-TABLE-BEGIN"
	ceremonyTableEnd   = "CEREMONY-TABLE-END"
)

var spaDir = filepath.Join("..", "webui", "www")

// providerLiteral matches a provider name used as code: inside a string
// literal on one line, as a whole token. Prose in comments is not code.
func providerLiteral(name string) *regexp.Regexp {
	const q = `'"` + "`"
	return regexp.MustCompile(`(?i)[` + q + `](?:[^` + q + `\n]*[^\w` + q + `\n-])?` +
		regexp.QuoteMeta(name) + `(?:[^\w` + q + `\n-][^` + q + `\n]*)?[` + q + `]`)
}

// providerNamesIn reports which registered provider names a source names as
// code.
func providerNamesIn(src string, names []string) []string {
	var found []string
	for _, n := range names {
		if providerLiteral(n).MatchString(src) {
			found = append(found, n)
		}
	}
	return found
}

func registeredNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, p := range owner.Registered() {
		names = append(names, p.Name())
	}
	// ⛔ Non-vacuity: a registry this test cannot see would pass everything.
	have := strings.Join(names, ",")
	for _, want := range []string{passkeyName, "loopback", "operator"} {
		if !strings.Contains(","+have+",", ","+want+",") {
			t.Fatalf("registry is missing %q (have %s); the scan would prove nothing", want, have)
		}
	}
	return names
}

func TestOwnerAuthUINamesNoProvider(t *testing.T) {
	names := registeredNames(t)

	entries, err := os.ReadDir(spaDir)
	if err != nil {
		t.Fatalf("reading the SPA: %v", err)
	}
	scanned, sawTable := 0, false
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".js" && ext != ".html") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(spaDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if e.Name() == "owner-auth.js" {
			a, b := strings.Index(src, ceremonyTableBegin), strings.Index(src, ceremonyTableEnd)
			if a < 0 || b < a || strings.Count(src, ceremonyTableBegin) != 1 {
				t.Fatal("owner-auth.js must hold exactly one ceremony table between its markers")
			}
			checkCeremonyTable(t, src[a:b], names)
			src = src[:a] + src[b:]
			sawTable = true
		}
		if got := providerNamesIn(src, names); len(got) > 0 {
			t.Errorf("%s names provider(s) %v outside the ceremony table; render from the registry "+
				"and branch on the ceremony kind instead", e.Name(), got)
		}
		scanned++
	}
	if !sawTable || scanned < 5 {
		t.Fatalf("scanned %d files (owner-auth.js seen: %v); the census is looking in the wrong place", scanned, sawTable)
	}
}

// checkCeremonyTable asserts the table is keyed by KIND: no key is a provider
// name, and the kind the passkey provider reports has an entry.
func checkCeremonyTable(t *testing.T, table string, names []string) {
	t.Helper()
	keys := regexp.MustCompile(`(?m)^\s+([A-Za-z0-9_-]+):\s*\{`).FindAllStringSubmatch(table, -1)
	seen := map[string]bool{}
	for _, k := range keys {
		seen[k[1]] = true
		for _, n := range names {
			if strings.EqualFold(k[1], n) {
				t.Errorf("the ceremony table has an entry keyed by provider %q; key it by ceremony kind", n)
			}
		}
	}
	if !seen[PasskeyCeremony] {
		t.Errorf("the ceremony table has no %q entry, so the passkey provider could not be set up", PasskeyCeremony)
	}
}

// ⭐ The scan must catch the shortcut it exists for, or a green run means
// nothing.
func TestProviderNameScanCatchesAPerProviderBranch(t *testing.T) {
	names := registeredNames(t)
	for _, bad := range []string{
		`if (p.name === 'passkey') {`,
		"fetch(`/api/enrol/passkey/begin`)",
		`<section id="x" data-provider="Passkey">`,
	} {
		if len(providerNamesIn(bad, names)) == 0 {
			t.Errorf("the scan missed a per-provider branch: %s", bad)
		}
	}
	for _, fine := range []string{
		`// the operator's login is the front door`,
		`const kind = p.kind;`,
		`'hasOperator'`,
	} {
		if got := providerNamesIn(fine, names); len(got) > 0 {
			t.Errorf("the scan flagged %v in innocent code: %s", got, fine)
		}
	}
}

// ⛔ Where an operator signs the owner in (polis.pub), Settings shows no list
// of ways to sign in: there is nothing there an owner could set up or remove.
// The screens skip it already; the Settings list must too.
func TestSettingsWaysToSignInSkipsAnOperatorInstance(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(spaDir, "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	call := strings.Index(src, "PolisOwnerAuth.renderProviderList(")
	if call < 0 {
		t.Fatal("app.js no longer renders the ways to sign in; update this test")
	}
	guard := strings.LastIndex(src[:call], "if (")
	if guard < 0 || !strings.Contains(src[guard:call], "!this.capabilities.hasOperator") {
		t.Error("the Settings list of ways to sign in must be guarded by !this.capabilities.hasOperator")
	}
}
