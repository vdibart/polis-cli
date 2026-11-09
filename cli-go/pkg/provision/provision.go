// Package provision is the deploy seam: one interface for standing up a polis
// instance on a platform, with Vercel as one implementation.
//
// ⚠️ EXPERIMENTAL. Remote instances on Vercel are exploratory and not under
// active development. This package's API may change or be removed without
// notice, and it is not kept current with Vercel's API.
//
// ⭐⭐ IT EXISTS BECAUSE THE ORGANIZING RULE NAMED THIS SEAM AND NOTHING WAS
// BUILT FOR IT. The README protects the alternate-VPS deferral with "the
// storage seam with a filesystem backend, plus a DEPLOY SEAM with Vercel as one
// implementation" — and six of the seven seams had epics while this one did
// not, so the deploy button sat where the interface should be: an
// implementation standing in for the thing above it.
//
// ⛔⛔ THE OPERATIONS ARE DERIVED FROM VERCEL'S SHAPE AND SANITY-CHECKED
// AGAINST FLY AND A VPS — not designed abstractly and then bent to fit. That
// order matters, because there is exactly ONE implementation on the MVP path
// and generalising ahead of evidence is the failure this ticket was written to
// avoid. ⚠️ An operation only one backend could ever implement does not belong
// here; `platforms.md` records the three-column check that each one passed.
//
// ⛔ NOTHING HERE REQUIRES A GIT REPOSITORY. The A0 spike proved a deployment
// can be uploaded directly, which removes a whole class of repository plumbing
// from the MVP — so "no git" is a finding about what is possible, not a
// preference about what is tidy.
package provision

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Provisioner stands up and tears down one instance on one platform.
//
// ⭐ THE EIGHT OPERATIONS ARE THE SIX STEPS THAT ACTUALLY OCCUR, PLUS THE TWO
// THAT MAKE THEM OPERABLE. Create, PutCode, Seed, SetConfig, AttachDomain and
// Activate are the provisioning steps (Seed only when the instance starts from
// an existing site); Status and Destroy are what an operator needs afterwards,
// and without them a failed provision is unobservable and unrecoverable.
//
// ⚠️ EVERY METHOD TAKES A CONTEXT because every one of them is a network call
// on at least one platform, and a provisioning flow that cannot be cancelled
// strands a half-built instance.
type Provisioner interface {
	// Name identifies the platform, for logs and for an operator reading a
	// status line. ⛔ Never used to branch on: a caller that switches on this
	// has defeated the seam.
	Name() string

	// Create brings an instance into existence and returns its identity.
	//
	// ⚠️ ON SOME PLATFORMS THERE IS NOTHING TO DO. A VPS already exists when
	// provisioning starts, so its Create records the box rather than making
	// one. ⭐ That is why it returns an Instance rather than a bare error:
	// the caller needs a handle either way, and "already existed" is a normal
	// outcome rather than a conflict.
	Create(ctx context.Context, req CreateRequest) (Instance, error)

	// PutCode uploads the instance's code.
	//
	// ⛔ NO GIT. It takes BYTES — an archive of the build — because the A0
	// spike proved a deployment can be uploaded directly. A platform that
	// wants a repository can clone one itself; the interface must not require
	// a caller to have one.
	PutCode(ctx context.Context, inst Instance, code Code) (Release, error)

	// Seed puts an existing site into the instance's store before the
	// instance first starts. See Seeder.
	Seeder

	// SetConfig sets the instance's environment.
	//
	// ⭐ A USEFUL CONSTRAINT FROM A7, AND IT IS WORTH SAYING BECAUSE IT IS THE
	// KIND OF THING THAT QUIETLY STOPS BEING TRUE: nothing secret needs
	// injecting at creation time on the MVP path, so Values may legitimately
	// carry no secrets at all. ⚠️ Secret is still honoured, because the day
	// that changes the platform must already be told which values to protect
	// rather than being asked to guess.
	SetConfig(ctx context.Context, inst Instance, cfg Config) error

	// AttachDomain points a domain at the instance and arranges a certificate.
	//
	// ⛔⛔ THIS IS NOT OPTIONAL POLISH. THE DOMAIN IS THE IDENTITY —
	// signatures, `.well-known/polis` and every DS row are keyed on it — so an
	// instance reachable only at a platform hostname is not a polis site yet.
	// ⚠️ Certificate issuance is asynchronous on every platform, which is why
	// this returns a DomainStatus rather than nil: "requested, not yet valid"
	// is the normal case and a caller that treats it as done reports success
	// too early.
	AttachDomain(ctx context.Context, inst Instance, domain string) (DomainStatus, error)

	// Activate makes a release the one that serves traffic.
	//
	// ⚠️ IT IS SEPARATE FROM PutCode ON PURPOSE. Vercel builds a deployment
	// and then promotes it; a VPS copies files and then restarts the unit. In
	// both the upload and the cutover are distinct moments, and collapsing
	// them would make a half-uploaded instance live.
	Activate(ctx context.Context, inst Instance, rel Release) error

	// Status reports what the platform believes about the instance.
	//
	// ⛔ WITHOUT THIS A FAILED PROVISION IS UNOBSERVABLE. Every operation above
	// is asynchronous somewhere, so a caller needs one question it can ask
	// repeatedly without side effects.
	Status(ctx context.Context, inst Instance) (Status, error)

	// Destroy removes the instance.
	//
	// ⛔ A PROVISIONING SEAM WITHOUT TEARDOWN LEAKS. A flow that fails at step
	// four has to be able to undo steps one to three, and a user who leaves
	// must be able to take everything with them and have nothing left behind.
	Destroy(ctx context.Context, inst Instance) error
}

// Seeder puts an existing site (its files, its private residue and its
// identity key) into an instance's store before the instance first starts, so
// the instance adopts that identity instead of creating one.
//
// ⭐ IT IS HOW EVERY BRANCH, MOVE AND RESTORE REACHES A HOST. It comes after
// Create, because the store must exist, and before the first deployment,
// because the instance's first start is what would otherwise make a key.
//
// ⛔ AN INSTANCE THAT ALREADY HOLDS A KEY OR A SITE REFUSES (ErrSeedRefused),
// and says why. Every implementation writes through SeedStore, which fixes the
// order that keeps a half-finished seed from publishing an identity.
//
// ⚠️ It is its own interface as well as a Provisioner method because a
// directory can be seeded without being provisioned: the box already exists.
type Seeder interface {
	Seed(ctx context.Context, inst Instance, site storage.Site) error
}

// Instance identifies one provisioned instance.
//
// ⚠️ ID IS OPAQUE AND PLATFORM-SHAPED. A caller stores it and hands it back;
// it must never parse one, because its form differs per platform and a caller
// that reads it has coupled itself to a platform through a string.
type Instance struct {
	// ID is the platform's own identifier.
	ID string

	// Name is what a human asked for, kept so a status line can say it.
	Name string

	// Existing reports that the instance was already there and Create
	// recorded it rather than making it.
	//
	// ⭐ A VPS is always Existing. A platform where Create makes the instance
	// reports Existing when a re-run adopts one an earlier, half-finished
	// provision made. It is here so a caller can tell "I made this" from "I
	// adopted this" when deciding whether Destroy is its business.
	Existing bool

	// Store is the platform's id for the instance's private store, where
	// Create makes one beside the instance; empty where the instance's
	// storage is not a separate thing. ⭐ It is what the instance's storage
	// configuration names (storage.EnvStorageSource). ⚠️ Opaque, like ID.
	Store string
}

// CreateRequest is what an instance needs to exist.
type CreateRequest struct {
	// Name is the instance name. ⚠️ Platform naming rules differ, so an
	// implementation normalises and may return a different name on the
	// Instance than the one asked for.
	Name string

	// Region is where to run it, in the platform's own vocabulary.
	//
	// ⛔ NOT NORMALISED, DELIBERATELY. There is no honest mapping between
	// Vercel's regions, Fly's and a VPS's datacentre, and inventing one would
	// make a caller think it had chosen something it had not.
	Region string
}

// Code is the bytes of a build, and how to interpret them.
//
// ⛔ NO GIT: the A0 spike proved a deployment can be uploaded directly.
type Code struct {
	// Archive is the build, as a tarball or zip per Format.
	Archive []byte

	// Format says how to read Archive.
	Format CodeFormat

	// Entrypoint is the command or file the platform should run, where the
	// platform needs telling. ⚠️ Empty means the platform's own default. On
	// Vercel it names the function in the build ("api/index" by default).
	Entrypoint string

	// Routing, where set, is written into the build's routing before upload
	// (on Vercel, the header routes of VercelInstanceConfig). ⚠️ Nil uploads
	// the build's routing as built.
	Routing *InstanceRouting
}

// CodeFormat is how an archive is packed.
type CodeFormat string

const (
	FormatTarGz CodeFormat = "tar.gz"
	FormatZip   CodeFormat = "zip"
)

// Release identifies one uploaded version of the code.
//
// ⭐ It is separate from Instance because an instance outlives its releases,
// and Activate needs to name which one serves.
type Release struct {
	ID string

	// URL is where the platform serves this release before a domain is
	// attached. ⚠️ Useful for a smoke check and NOTHING ELSE — serving a
	// polis site from it would break identity, because the domain IS the
	// identity.
	URL string
}

// Config is the instance's environment.
type Config struct {
	// Values are the variables to set.
	Values map[string]string

	// Secret names the keys in Values that must be stored as secrets rather
	// than as plain configuration.
	//
	// ⛔ THE PLATFORM IS TOLD, NEVER LEFT TO GUESS. Vercel's env API takes a
	// type; Fly has separate secret and config commands; a file on a VPS wants
	// mode 0600. ⚠️ A platform that cannot distinguish them must REFUSE rather
	// than store a secret in the clear.
	Secret []string

	// Plain names the keys in Values that are readable configuration.
	//
	// ⛔ FAIL CLOSED: every key in Values must be in exactly one of Secret and
	// Plain, or SetConfig refuses before writing anything. A key nobody
	// classified used to go as plain, so forgetting to list a secret stored it
	// readable. And a key the secret seam declares a secret (secret.IsSecretName)
	// is refused as Plain, whatever the caller said.
	Plain []string
}

// Classify checks that every value is declared exactly once, and that nothing
// the secret seam names a secret is declared plain. It returns the set of
// secret keys. It names keys, never values.
func (c Config) Classify(isSecretName func(string) bool) (map[string]bool, error) {
	secret, plain := map[string]bool{}, map[string]bool{}
	for _, k := range c.Secret {
		secret[k] = true
	}
	for _, k := range c.Plain {
		plain[k] = true
	}
	var problems []string
	for k := range c.Values {
		switch {
		case secret[k] && plain[k]:
			problems = append(problems, k+" is declared both secret and plain")
		case !secret[k] && !plain[k]:
			problems = append(problems, k+" is declared neither secret nor plain")
		case plain[k] && isSecretName != nil && isSecretName(k):
			problems = append(problems, k+" is a secret and cannot be stored as plain configuration")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("provision: refusing to set config: %s", strings.Join(problems, "; "))
	}
	return secret, nil
}

// DomainStatus is what a platform says about a domain after AttachDomain.
type DomainStatus struct {
	Domain string

	// Attached reports the domain is pointed at the instance.
	Attached bool

	// CertificateReady reports a valid certificate is serving.
	//
	// ⚠️ FALSE IS THE NORMAL FIRST ANSWER. Issuance is asynchronous
	// everywhere, so a caller polls; treating false as failure reports a
	// working provision as broken.
	CertificateReady bool

	// NeedsDNS carries records the operator must create themselves, when the
	// platform cannot do it.
	//
	// ⚠️ On Fly this is where the ACME challenge CNAME goes — and a missing
	// one makes renewal fail SILENTLY months later, so it is surfaced rather
	// than logged.
	NeedsDNS []DNSRecord
}

// DNSRecord is one record an operator must create.
type DNSRecord struct {
	Type  string // "CNAME", "A", "TXT"
	Name  string
	Value string
}

// Status is what the platform believes about an instance.
type Status struct {
	// State is the instance's lifecycle state.
	State State

	// ActiveRelease is the release currently serving, if any.
	ActiveRelease string

	// Domains are the domains attached, with their certificate state.
	Domains []DomainStatus

	// Message is whatever the platform said, for an operator to read.
	//
	// ⚠️ NEVER PARSED. It is prose from a third party and its wording is not
	// a contract.
	Message string

	// CheckedAt is when this was observed, because a status is a snapshot and
	// a caller that caches one needs to know how old it is.
	CheckedAt time.Time
}

// State is an instance's lifecycle state.
type State string

const (
	// StateUnknown means the platform could not say. ⛔ Distinct from
	// StateGone: "I could not look" is not "it is not there", and conflating
	// them makes a transient API failure look like a destroyed instance.
	StateUnknown State = "unknown"

	StateCreating State = "creating"
	StateReady    State = "ready"
	StateFailed   State = "failed"
	StateGone     State = "gone"
)

// ErrUnsupported is returned by an operation a platform genuinely cannot
// perform.
//
// ⛔ IT IS NOT A SHRUG. An implementation returns it only where the operation
// has no meaning on that platform — and says so in the message — rather than
// silently succeeding. ⚠️ A caller that ignores it builds an instance that
// looks provisioned and is not.
var ErrUnsupported = errors.New("provision: unsupported on this platform")

// Unsupported builds an ErrUnsupported that names the platform and the reason.
func Unsupported(platform, op, why string) error {
	return fmt.Errorf("%w: %s cannot %s: %s", ErrUnsupported, platform, op, why)
}
