package capability

import (
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// Resolving which mode a self-hosted instance is running in.
//
// ⛔⛔ THIS IS WHERE isDev WENT, and the replacement is deliberately not a
// like-for-like swap.
//
// `window.__POLIS_DEV` existed so a developer on localhost could exercise the
// direct-messaging UI, which was gated on `!(isHosted || isDev)`. That made it
// a SECOND HIDDEN FLAG FAKING REACHABILITY, sitting beside the mode and
// answering the same question differently — exactly the scattering the
// capability model removes. Keeping it would mean one capability could be
// switched on by something other than the mode, and "derive, never set" would
// be true of four capabilities and a lie about the fifth.
//
// ⭐ So a developer who wants an instance to report a public API now says WHICH
// MODE to be, through POLIS_MODE, and the real derivation does the rest. The
// convenience survives; the fake does not.
//
// ⚠️ IT IS A DEVELOPMENT OVERRIDE AND NOTHING ELSE. A hosted deployment does
// not consult it — see Custodied's comment in ForHosted.

// ModeEnv names the environment variable a self-hosted instance may use to
// report a mode other than local.
const ModeEnv = "POLIS_MODE"

// ResolveSelfHosted decides the mode for a self-hosted instance.
//
// ⭐ LOCAL IS THE DEFAULT AND AN UNSET VALUE IS NOT AN ERROR HERE, which is the
// opposite of mode.Parse and deliberately so: `polis serve` on a laptop is the
// overwhelmingly common case, and demanding a mode for it would make the
// product's simplest path the one with required configuration.
//
// ⛔ AN INVALID VALUE RETURNS A NOTE, AND THE SERVER REFUSES TO START ON IT
// (checkModeForBackend in the webapp). Falling back to local was meant for a
// typo in a developer's shell; the same typo on a VPS behind a reverse proxy
// granted loopback owner-trust to everyone the proxy forwarded. The returned
// mode is local only so a caller that does not stop has a defined value.
//
// ⭐ unset reports that POLIS_MODE was empty, as distinct from set to local.
// ⛔ The difference is load-bearing: an unset mode beside a setting only a
// remote instance has (a recovery key, Vercel's runtime) also stops the boot,
// because a deployer who forgot POLIS_MODE would otherwise run as local with
// loopback owner-trust behind their proxy. An explicit local is a choice and is
// honoured.
func ResolveSelfHosted(getenv func(string) string) (m mode.Mode, note string, unset bool) {
	raw := strings.TrimSpace(getenv(ModeEnv))
	if raw == "" {
		return mode.Local, "", true
	}
	m, err := mode.Parse(raw)
	if err != nil {
		return mode.Local, "ignoring " + ModEnvQuoted(raw) + ": " + err.Error(), false
	}
	// ⛔ A self-hosted instance may NOT claim to be custodied. Custodied means
	// somebody other than the owner runs it, with accounts, provisioning and a
	// login redirect — none of which exist in this binary. Letting the flag say
	// otherwise would publish a capability set the instance cannot honour.
	if m == mode.Custodied {
		return mode.Local, "ignoring " + ModEnvQuoted(raw) +
			": a self-hosted instance has no operator", false
	}
	return m, "", false
}

// ModEnvQuoted renders the variable and value for a log line.
func ModEnvQuoted(v string) string { return ModeEnv + "=" + v }

// ForHosted is the mode a multi-tenant operator deployment runs in.
//
// ⛔ IT IS A CONSTANT, NOT A LOOKUP, and that is the point: polis-hosted IS the
// custodied case. Reading it from the environment would let a misconfiguration
// publish a capability set the deployment does not match — claiming no
// operator while running one, which every consumer would then believe.
func ForHosted() mode.Mode { return mode.Custodied }
