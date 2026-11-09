package secret

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// Kind distinguishes the two disciplines. See the package comment.
type Kind int

const (
	// Reference is used in place and never written to disk.
	Reference Kind = iota

	// Projected is written to disk and carries identity.
	//
	// ⛔ A projected secret is NOT resolved by this package alone. Resolve
	// hands back the value; writing it is the owning package's job, because
	// never-overwrite and the stale check need to know what the site publishes
	// — see actor.ProjectKey, which is the reference implementation.
	Projected
)

func (k Kind) String() string {
	if k == Projected {
		return "projected"
	}
	return "reference"
}

// Declaration is one secret: where it comes from, who needs it, and what shape
// it must have.
//
// ⭐ IT IS DATA, NOT A BRANCH. The whole point is that "which secrets does this
// deployment need" is answered by reading a table rather than by tracing four
// different env reads through a boot sequence, which is what it replaced.
type Declaration struct {
	// Env is the environment variable holding the value.
	Env string

	// Kind is the discipline that applies.
	Kind Kind

	// RequiredIn lists the modes that cannot start without this secret.
	//
	// ⛔ EMPTY MEANS NO MODE REQUIRES IT, which is a real and useful state: the
	// secret is optional everywhere and absence is not an error. It does NOT
	// mean "required in all modes" — that reading would make every optional
	// secret fatal, and is the mistake this field's name is trying to prevent.
	RequiredIn []mode.Mode

	// Validate checks the shape of a supplied value. ⭐ It runs only when a
	// value is present, so it never has to handle the empty case.
	//
	// ⛔ ITS ERROR MUST NOT QUOTE THE VALUE. A DSN holds a password and a
	// private key is a private key; see the DSN validator below for the
	// wording that works — name the problem, not the input.
	Validate func(string) error

	// Why is one line on what the secret is for, used in the refusal message.
	// ⭐ An operator reading a failed boot should not need the source to learn
	// what is missing.
	Why string
}

// RequiredBy reports whether this secret is required in m.
func (d Declaration) RequiredBy(m mode.Mode) bool {
	for _, k := range d.RequiredIn {
		if k == m {
			return true
		}
	}
	return false
}

// Set is a deployment's declared secrets.
type Set []Declaration

// Resolved is what a successful resolution yields, keyed by environment name.
type Resolved map[string]Value

// Get returns a resolved secret. ⚠️ A secret that was optional and absent is
// present here as a zero Value, so callers test IsZero rather than existence.
func (r Resolved) Get(env string) Value { return r[env] }

// Resolve reads every declared secret for one mode and refuses if a required
// one is missing or malformed.
//
// ⛔ FATAL IS PER MODE, AND THIS IS THE DECISION, NOT AN OPTIMISATION. In local
// there is no database URL, no mail provider and no projected key, and that is
// correct rather than a fault. A global "every secret must be present" rule
// would refuse to start the laptop case the product is named for.
//
// ⭐ It gathers EVERY problem before returning, rather than failing on the
// first. An operator bringing up a new deployment should learn about three
// missing secrets in one go, not across three deploys.
func Resolve(m mode.Mode, set Set, getenv func(string) string) (Resolved, error) {
	if !mode.Valid(m) {
		return nil, fmt.Errorf("cannot resolve secrets for unknown mode %q", m)
	}
	out := make(Resolved, len(set))
	var problems []string

	for _, d := range set {
		raw := strings.TrimSpace(getenv(d.Env))

		if raw == "" {
			if d.RequiredBy(m) {
				problems = append(problems, fmt.Sprintf(
					"%s is not set, and %s mode requires it (%s)", d.Env, m, d.Why))
			}
			// ⭐ Absent and optional: a zero Value, not an omission. A caller
			// that ranges over the result sees every declared secret.
			out[d.Env] = Value{}
			continue
		}

		if d.Validate != nil {
			if err := d.Validate(raw); err != nil {
				// ⛔ err must not contain raw. Validators are written to that
				// rule; this wrapper cannot enforce it, which is why
				// TestValidatorsDoNotEchoTheValue exists.
				problems = append(problems, fmt.Sprintf("%s is set but not usable: %v", d.Env, err))
				out[d.Env] = Value{}
				continue
			}
		}
		out[d.Env] = New(raw)
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("this instance cannot start:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return out, nil
}

// Names returns the declared environment variable names, sorted. ⭐ For
// diagnostics that should say WHICH secrets a deployment reads without going
// anywhere near their values.
func (s Set) Names() []string {
	out := make([]string, 0, len(s))
	for _, d := range s {
		out = append(out, d.Env)
	}
	sort.Strings(out)
	return out
}

// RequiredNames returns the names required in m, sorted.
func (s Set) RequiredNames(m mode.Mode) []string {
	var out []string
	for _, d := range s {
		if d.RequiredBy(m) {
			out = append(out, d.Env)
		}
	}
	sort.Strings(out)
	return out
}
