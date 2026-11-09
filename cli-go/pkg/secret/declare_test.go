package secret

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// ⛔ THE DECISION THIS PACKAGE TURNS ON. A global "every secret must be
// present" rule would refuse to start the laptop case the product is named for.
func TestFatalIsPerMode(t *testing.T) {
	set := CustodiedSet()

	// Nothing supplied at all.
	for _, m := range []mode.Mode{mode.Local, mode.RemoteClientSigned, mode.RemoteServerSigned} {
		if _, err := Resolve(m, set, env(nil)); err != nil {
			t.Errorf("%s mode refused to start with no secrets, and it must not: %v", m, err)
		}
	}

	if _, err := Resolve(mode.Custodied, set, env(nil)); err == nil {
		t.Error("custodied mode started with no DATABASE_URL and no RESEND_API_KEY")
	}

	ok := map[string]string{
		EnvDatabaseURL:  "postgres://h/db",
		EnvResendAPIKey: "re_abc123",
	}
	if _, err := Resolve(mode.Custodied, set, env(ok)); err != nil {
		t.Errorf("custodied mode refused a complete environment: %v", err)
	}
}

// ⭐ A remote instance requiring nothing at boot is the finding, not an
// omission, so it is asserted rather than left implicit. Its one entry is the
// optional deploy-token handover.
func TestRemoteNeedsNoSecretsAtBoot(t *testing.T) {
	for _, m := range mode.All() {
		if names := RemoteSet().RequiredNames(m); len(names) != 0 {
			t.Errorf("%s now requires %v at boot; a remote instance needs no boot secrets — "+
				"if that changed, the per-mode table in polis.go and the plan both need updating", m, names)
		}
	}
	if names := RemoteSet().Names(); len(names) != 1 || names[0] != EnvDeployToken {
		t.Errorf("RemoteSet() = %v; the deploy-token handover is its only entry", names)
	}
	if !IsSecretName(EnvDeployToken) {
		t.Error("the deploy token is not declared a secret, so a provisioner could store it readable")
	}
	for _, m := range mode.All() {
		if _, err := Resolve(m, RemoteSet(), env(nil)); err != nil {
			t.Errorf("empty set refused %s: %v", m, err)
		}
	}
}

// ⚠️ Neither custodied secret may be required anywhere else. This is the
// criterion "no mode is made to hold a secret it has no use for".
func TestCustodiedSecretsAreRequiredOnlyInCustodied(t *testing.T) {
	for _, d := range CustodiedSet() {
		for _, m := range mode.All() {
			if m == mode.Custodied {
				continue
			}
			if d.RequiredBy(m) {
				t.Errorf("%s is required in %s, but it is a custodied-only concern", d.Env, m)
			}
		}
	}
}

// ⭐ An operator bringing up a deployment should learn about every missing
// secret at once, not across three deploys.
func TestResolveGathersEveryProblem(t *testing.T) {
	_, err := Resolve(mode.Custodied, CustodiedSet(), env(nil))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	msg := err.Error()
	for _, want := range []string{EnvDatabaseURL, EnvResendAPIKey} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not mention %s: %s", want, msg)
		}
	}
}

// ⛔ A validator's error must name the problem, never the input. A production
// DSN holds the password, so a rejected value echoed into a refusal writes a
// credential into the logs at the moment someone pastes output into a ticket.
func TestValidatorsDoNotEchoTheValue(t *testing.T) {
	secrets := []string{
		"mysql://user:SUPERSECRET@host/db", // wrong scheme, holds a password
		"sk-live-LEAKME",
		"-----BEGIN NOTHING-----LEAKME",
		"token-with-newline\nLEAKME",
	}
	validators := map[string]func(string) error{
		"ValidatePostgresDSN": ValidatePostgresDSN,
		"nonEmptyOneLine":     nonEmptyOneLine,
		"looksLikePrivateKey": looksLikePrivateKey,
	}
	for name, fn := range validators {
		for _, v := range secrets {
			err := fn(v)
			if err == nil {
				continue
			}
			if strings.Contains(err.Error(), "LEAKME") || strings.Contains(err.Error(), "SUPERSECRET") {
				t.Errorf("%s echoed the rejected value: %v", name, err)
			}
		}
	}
}

// And the same for Resolve's wrapper, which reports a validation failure.
func TestResolveDoesNotEchoARejectedValue(t *testing.T) {
	bad := map[string]string{
		EnvDatabaseURL:  "mysql://user:SUPERSECRET@host/db",
		EnvResendAPIKey: "re_ok",
	}
	_, err := Resolve(mode.Custodied, CustodiedSet(), env(bad))
	if err == nil {
		t.Fatal("expected a refusal for a non-Postgres DSN")
	}
	if strings.Contains(err.Error(), "SUPERSECRET") {
		t.Errorf("Resolve echoed the rejected value: %v", err)
	}
}

func TestPostgresDSNValidation(t *testing.T) {
	for _, ok := range []string{"postgres://h/d", "postgresql://h/d"} {
		if err := ValidatePostgresDSN(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"mysql://h/d", "/data/hosted.db", "postgres:/h", "h/d"} {
		if err := ValidatePostgresDSN(bad); err == nil {
			t.Errorf("%q accepted, and it is not a Postgres DSN", bad)
		}
	}
}

// ⚠️ A trailing newline is the commonest way a secret arrives wrong, and the
// failure it causes looks nothing like its cause.
func TestWhitespaceIsTrimmedBeforeUse(t *testing.T) {
	got, err := Resolve(mode.Custodied, CustodiedSet(), env(map[string]string{
		EnvDatabaseURL:  "  postgres://h/d\n",
		EnvResendAPIKey: "re_abc\n",
	}))
	if err != nil {
		t.Fatalf("a value with surrounding whitespace was refused: %v", err)
	}
	if v := got.Get(EnvDatabaseURL).Expose(); v != "postgres://h/d" {
		t.Errorf("DSN not trimmed: %q", v)
	}
}

// ⛔ An unknown mode must refuse rather than resolve to whatever an empty
// required-set implies.
func TestUnknownModeRefuses(t *testing.T) {
	if _, err := Resolve(mode.Mode("hosted"), CustodiedSet(), env(nil)); err == nil {
		t.Error(`Resolve accepted mode "hosted", which is not one of the four`)
	}
}

// ⭐ A declaration with no RequiredIn is optional everywhere — NOT required
// everywhere. The field name invites the opposite reading.
func TestEmptyRequiredInMeansOptionalEverywhere(t *testing.T) {
	set := Set{{Env: "OPTIONAL_THING", Kind: Reference, Why: "testing"}}
	for _, m := range mode.All() {
		got, err := Resolve(m, set, env(nil))
		if err != nil {
			t.Fatalf("%s refused an optional secret: %v", m, err)
		}
		if !got.Get("OPTIONAL_THING").IsZero() {
			t.Error("an absent optional secret should resolve to a zero Value")
		}
	}
}

func TestProjectedDeclarationCarriesItsKind(t *testing.T) {
	d := ActorKeyDeclaration("POLIS_ACTOR_KEY_POLIS", mode.Custodied)
	if d.Kind != Projected {
		t.Errorf("an identity key declared as %s, want projected", d.Kind)
	}
	if !d.RequiredBy(mode.Custodied) || d.RequiredBy(mode.Local) {
		t.Error("actor key required-set is wrong")
	}
}
