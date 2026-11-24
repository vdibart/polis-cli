package server

import (
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
