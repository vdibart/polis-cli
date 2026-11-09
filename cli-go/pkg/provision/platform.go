package provision

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	secretpkg "github.com/vdibart/polis-cli/cli-go/pkg/secret"
)

// ⭐⭐ A PLATFORM IS A PROVISIONER PLUS WHAT A CALLER NEEDS TO DRIVE IT
// WITHOUT KNOWING WHICH ONE IT IS. A command that deploys a site names a
// platform by the word its user wrote ("vercel") and gets this back: the eight
// operations, how the platform turns an address into an instance name, the
// instance's environment, the credential handover, and the code. ⛔ So the
// caller holds no platform vocabulary at all: every name, endpoint and setting
// stays in that platform's own files.

// Platform is one hosting platform, opened with the user's credential.
type Platform interface {
	Provisioner

	// InstanceNameFor returns the instance name that the platform serves at
	// host by itself, when it gives each instance an address made from its
	// name. ⭐ ok is false when host is not such an address: the caller then
	// names the instance itself, and attaches host as a domain.
	//
	// ⚠️ The platform gives that address only when the name is free, so a
	// caller checks the address after Create (Status) rather than trusting it.
	InstanceNameFor(host string) (name string, ok bool)

	// InstanceConfig is the instance's environment on this platform: what it
	// needs to find its store, its address and its code. ⭐ Without the
	// owner's recovery key, which a seeded provision reads from the site and
	// merges in (OwnerConfig).
	InstanceConfig(inst Instance, spec InstanceSpec) (Config, error)

	// Handover is the credential handover a provision runs between SetConfig
	// and the first deployment, or nil when the platform needs none.
	Handover() (func(ctx context.Context, p Provisioner, inst Instance) error, error)

	// Code is the instance's code: a local build, or the released build
	// matching src.Version. ⛔ With neither it returns ErrNoReleasedCode.
	Code(ctx context.Context, src CodeSource) (PreparedCode, error)
}

// PlatformOptions is what the user supplies to open a platform.
type PlatformOptions struct {
	// Token is the user's credential on the platform. ⛔ A secret: it is
	// exposed once, inside the platform's own client, and never kept as a
	// string.
	Token secretpkg.Value

	// Team scopes every call to an account the user belongs to, in the
	// platform's own words (an id or a slug).
	Team string

	// APIBase and HTTPClient override the platform's API, for tests.
	APIBase    string
	HTTPClient *http.Client
}

// InstanceSpec is what an instance's environment is built from, beside the
// instance itself.
type InstanceSpec struct {
	// BaseURL is the address the instance serves its site at.
	BaseURL string

	// CodeManifest is what a development build's instance must be told about
	// its own code, inline (PreparedCode.Manifest). ⭐ Empty for a released
	// build, which looks its own up by version.
	CodeManifest string
}

// CodeSource says where an instance's code comes from.
type CodeSource struct {
	// BuildDir is a local build of the instance, when there is one.
	BuildDir string

	// Version is this program's version. ⭐ Without a BuildDir, the code is
	// the released build of this version.
	Version string

	// HTTPClient fetches a release, for tests.
	HTTPClient *http.Client
}

// PreparedCode is an instance's first release and what its environment must
// say about it.
type PreparedCode struct {
	Code Code

	// Manifest goes into InstanceSpec.CodeManifest.
	Manifest string

	// Version names the build, for a status line.
	Version string
}

// ErrNoReleasedCode means no local build was named and no released build
// matches the version: a development version has no release, and a release
// may not publish one.
var ErrNoReleasedCode = errors.New("provision: there is no released build of this version to deploy")

// platformOpeners are the platforms a user can name, by that name.
var platformOpeners = map[string]func(context.Context, PlatformOptions) (Platform, error){}

// registerPlatform adds a platform under the name a user writes.
func registerPlatform(name string, open func(context.Context, PlatformOptions) (Platform, error)) {
	platformOpeners[name] = open
}

// PlatformNames lists the platforms a user can name, sorted.
func PlatformNames() []string {
	var out []string
	for n := range platformOpeners {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// OpenPlatform opens the platform a user named, with their credential.
//
// ⛔ An unknown name is refused with the list of known ones, never guessed.
// ⛔ A missing credential is refused before any call.
func OpenPlatform(ctx context.Context, name string, opts PlatformOptions) (Platform, error) {
	open, ok := platformOpeners[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil, fmt.Errorf("provision: %q is not a platform this build deploys to (known: %s)", name, strings.Join(PlatformNames(), ", "))
	}
	if opts.Token.IsZero() {
		return nil, fmt.Errorf("provision: opening %s needs a token", name)
	}
	return open(ctx, opts)
}
