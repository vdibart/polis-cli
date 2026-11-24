package secret

import (
	"errors"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// The secrets polis actually holds, and which modes need them.
//
// ⛔⛔ READ THIS BEFORE ADDING A ROW. Nearly everything here is CUSTODIED-ONLY,
// and that is a fact about where the product has been deployed — not a default
// for other modes to inherit. polis.pub is a multi-tenant operator with
// accounts, sessions and signup mail. A remote instance is ONE PERSON'S SITE
// with none of those.
//
//	                        local   remote-client   remote-server   custodied
//	projected key            no        no             no              yes (actors)
//	RESEND_API_KEY           no        no             no              yes
//	DATABASE_URL             no        no             no              yes
//	POLIS_AGENT_SIGNIN_SECRET no       no             no          only with the switch
//	POLIS_DEPLOY_TOKEN       no        no          optional, handed    no
//	                                               over on Vercel
//
// ⭐⭐⭐ SO A remote INSTANCE NEEDS NO PLATFORM SECRET TO START. Its identity key
// (remote-server-signed) is generated into the instance's own private store
// by site.EnsureIdentityKey, and the store is reached with the deployment's
// OIDC token: writing a key into a project's environment would need a
// project-editing token beside the signing key. This was once planned as a
// projected secret in the platform's store; it is not one.
//
// ⚠️ The one exception is a HANDOVER, not a holding: on Vercel a server-signed
// instance publishes by deploying, which needs a project-only deploy token,
// and the provisioner cannot reach the private store to put it there. So the
// provisioner sets it as a write-only project setting, and the instance moves
// it into its private store at its first deploy and deletes the setting. It is
// optional (an instance without it starts, and its site waits, behind), and it
// carries no identity, so it is a reference secret, not a projected one.
//
// ⚠️ A ROW WITH A MODE IT HAS NO USE FOR IS WORSE THAN A MISSING ROW. It makes
// an instance refuse to boot over a value it would never read.

// Environment variable names, in one place so a typo is a compile error at the
// call site rather than a silent empty read.
const (
	EnvResendAPIKey = "RESEND_API_KEY" //nolint:gosec // G101: a setting's or path's name, not a credential
	EnvDatabaseURL  = "DATABASE_URL"

	// EnvAgentSignInSecret signs the tokens polis.pub's sign-in server for
	// agents issues. Required only when that server is switched on
	// (POLIS_AGENT_SIGNIN=1); the boot checks it then, not here.
	EnvAgentSignInSecret = "POLIS_AGENT_SIGNIN_SECRET" //nolint:gosec // G101: a setting's or path's name, not a credential

	// EnvDeployToken hands a server-signed Vercel instance its project-only
	// deploy token, once. See RemoteSet.
	EnvDeployToken = "POLIS_DEPLOY_TOKEN" //nolint:gosec // G101: a setting's or path's name, not a credential
)

// CustodiedSet is what a multi-tenant operator deployment reads.
//
// ⚠️ ACTOR KEYS ARE NOT IN THIS SET, deliberately. They are per-handle
// (POLIS_ACTOR_KEY_<HANDLE>, discovered from POLIS_ACTOR_KEYS at boot), so the
// set is not known until the environment is read — a shape this table cannot
// express. ⭐ They are projected secrets and keep their own discipline in
// actor.ProjectKey, which is the reference implementation this package was
// modelled on, not an exception to it. See ActorKeyDeclaration.
func CustodiedSet() Set {
	return Set{
		{
			Env:        EnvDatabaseURL,
			Kind:       Reference,
			RequiredIn: []mode.Mode{mode.Custodied},
			Validate:   ValidatePostgresDSN,
			Why:        "accounts and sessions live in Postgres",
		},
		{
			Env:        EnvResendAPIKey,
			Kind:       Reference,
			RequiredIn: []mode.Mode{mode.Custodied},
			Validate:   nonEmptyOneLine,
			Why:        "tenant sign-in sends a magic link",
		},
		{
			// ⭐ Optional in every mode: the sign-in server for agents is dark
			// until it is switched on, and an instance must not refuse to boot
			// over a value it would never read.
			Env:      EnvAgentSignInSecret,
			Kind:     Reference,
			Validate: atLeast32,
			Why:      "the sign-in server for agents signs its tokens with it; it must persist across restarts and machines",
		},
	}
}

// atLeast32 is a signing secret's shape: one line, at least 32 bytes.
func atLeast32(v string) error {
	if err := nonEmptyOneLine(v); err != nil {
		return err
	}
	if len(v) < 32 {
		return errors.New("is shorter than 32 bytes; generate one with: openssl rand -hex 32")
	}
	return nil
}

// RemoteSet is what a hosted-anywhere instance reads.
//
// ⭐ NOTHING IN IT IS REQUIRED, AND THAT IS THE FINDING, not an omission. A
// remote instance needs no database (it has one owner and no signup; whatever
// owner auth persists is per-site state and belongs behind pkg/storage) and no
// mail provider (the email magic link was dropped in favour of passkey, so the
// only thing the mail client does is the mechanism that was removed).
//
// ⭐ Its one entry is the deploy-token HANDOVER (see the package table): optional
// in every mode, read only to move it into the private store. Listed so a
// provisioner stores it write-only (IsSecretName), whatever its caller says.
//
// ⛔ The identity key a server-signing instance holds is NOT here, because it
// is generated into the instance's own store rather than read at boot.
func RemoteSet() Set {
	return Set{
		{
			Env:      EnvDeployToken,
			Kind:     Reference,
			Validate: nonEmptyOneLine,
			Why:      "a server-signed instance on Vercel publishes by deploying, with a project-only token",
		},
	}
}

// ActorKeyDeclaration describes one projected identity key.
//
// ⭐ It is built per handle rather than listed, because the set of actors is
// discovered from the environment at boot. ⛔ RESOLVING IT IS NOT WRITING IT:
// a projected secret's never-overwrite rule and its stale check need to know
// what the site publishes, which only the owning package can do. Use this to
// state the requirement; use actor.ProjectKey to act on it.
func ActorKeyDeclaration(env string, requiredIn ...mode.Mode) Declaration {
	return Declaration{
		Env:        env,
		Kind:       Projected,
		RequiredIn: requiredIn,
		Validate:   looksLikePrivateKey,
		Why:        "the instance signs with this identity and must not generate a new one",
	}
}

// ValidatePostgresDSN checks that a database URL is a Postgres one.
//
// ⭐ PRESENCE IS NOT ENOUGH. A truncated or wrong-service DSN should fail at
// boot rather than reach the driver, where the error arrives later and reads as
// a connectivity problem.
//
// ⛔ IT NEVER QUOTES THE VALUE. A production DSN contains the password
// (confirmed 2026-10-03), so echoing a rejected one would write a credential
// into the logs at exactly the moment someone is pasting output into a ticket.
func ValidatePostgresDSN(v string) error {
	if !strings.HasPrefix(v, "postgres://") && !strings.HasPrefix(v, "postgresql://") {
		return errors.New("not a Postgres DSN: it must begin postgres:// or postgresql://. " +
			"The value is not shown because it may hold a password")
	}
	return nil
}

// nonEmptyOneLine rejects a value carrying a newline.
//
// ⚠️ A trailing newline is the commonest way a secret arrives wrong — a copy
// and paste, or a store that round-trips values with one. For a bearer token
// that means a header the service rejects for reasons that look nothing like
// the cause. ⛔ It does not quote the value.
func nonEmptyOneLine(v string) error {
	if strings.ContainsAny(v, "\r\n") {
		return errors.New("contains a newline, which usually means a stray line break was copied in")
	}
	return nil
}

// looksLikePrivateKey rejects a value that is obviously not a private key.
//
// ⚠️ THIS IS A SHAPE CHECK, NOT A VALIDATION. Real parsing belongs in the
// signing package, which the owning projection path already calls. Checking
// here is worth it anyway: it turns "an empty or truncated secret" into a
// refusal that names the variable, at boot, before anything signs.
//
// ⛔ It does not quote the value, and it does not report the length either —
// a length is a fingerprint.
func looksLikePrivateKey(v string) error {
	if !strings.Contains(v, "PRIVATE KEY") {
		return errors.New("does not look like a private key: expected a PEM block. " +
			"The value is not shown")
	}
	return nil
}

// actorKeyPrefix is the name every projected actor key shares.
const actorKeyPrefix = "POLIS_ACTOR_KEY_"

// IsSecretName reports whether the seam declares env a secret: a member of any
// mode's set, or a projected actor key.
//
// ⭐ A provisioner uses it so a secret the seam knows about can never be stored
// as readable configuration, whatever the caller declared.
func IsSecretName(env string) bool {
	if strings.HasPrefix(env, actorKeyPrefix) {
		return true
	}
	for _, set := range []Set{CustodiedSet(), RemoteSet()} {
		for _, d := range set {
			if d.Env == env {
				return true
			}
		}
	}
	return false
}
