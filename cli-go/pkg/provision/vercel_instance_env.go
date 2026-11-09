package provision

import (
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ ONE PLACE BUILDS A VERCEL INSTANCE'S ENVIRONMENT. Every path that stands
// up an instance on Vercel (a branch deploy, the Deploy button, the exit run)
// sets these values, and the deploying-by-hand table in the Vercel deploy
// README is held equal to this function by a golden test, so the list is
// written once.
//
// ⭐ Every value is plain configuration. Nothing secret is injected at
// creation: the instance reads its store with the deployment's own OIDC token,
// and its deploy token arrives through the write-only handover.

// The instance's own project and team, which its publish deployments are made
// to. ⭐ The instance reads them by these names.
const (
	VercelProjectEnv = "POLIS_VERCEL_PROJECT"
	VercelTeamEnv    = "POLIS_VERCEL_TEAM"
)

// baseURLEnv is the address the instance serves its site at.
const baseURLEnv = "POLIS_BASE_URL"

// VercelInstanceSpec is what a Vercel instance's environment is built from.
type VercelInstanceSpec struct {
	// Project is the project's name, as Vercel returned it.
	Project string

	// StoreID is the instance's one private Blob store.
	StoreID string

	// TeamID is the team's id, which the API is scoped with, and TeamSlug its
	// slug, which the deployment's OIDC token names (team issuer mode).
	TeamID, TeamSlug string

	// BaseURL is the production address the instance serves its site at.
	BaseURL string

	// FunctionManifest is a development build's function manifest, inline.
	// ⭐ Empty for a released build, which fetches its own from its release.
	FunctionManifest string
}

// VercelInstanceEnv returns a Vercel instance's environment, every key plain.
//
// ⛔ A value that is missing, or not one line, is refused by name: a variable
// set empty would boot an instance that cannot find its store or its address.
func VercelInstanceEnv(s VercelInstanceSpec) (Config, error) {
	need := map[string]string{
		"the project": s.Project, "the private store": s.StoreID,
		"the team id": s.TeamID, "the team slug": s.TeamSlug, "the address": s.BaseURL,
	}
	for _, what := range []string{"the project", "the private store", "the team id", "the team slug", "the address"} {
		v := need[what]
		if strings.TrimSpace(v) == "" {
			return Config{}, fmt.Errorf("provision: the instance's environment needs %s", what)
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return Config{}, fmt.Errorf("provision: %s is not one line", what)
		}
	}
	values := map[string]string{
		capability.ModeEnv:              string(mode.RemoteServerSigned),
		storage.EnvStorageKind:          storage.BackendVercelBlob,
		storage.EnvStorageSource:        s.StoreID,
		storage.EnvStorageTokenIssuer:   "https://oidc.vercel.com/" + s.TeamSlug,
		storage.EnvStorageTokenAudience: "https://vercel.com/" + s.TeamSlug,
		storage.EnvStorageTokenSubject:  "owner:" + s.TeamSlug + ":project:" + s.Project + ":environment:production",
		baseURLEnv:                      s.BaseURL,
		VercelProjectEnv:                s.Project,
		VercelTeamEnv:                   s.TeamID,
	}
	if m := strings.TrimSpace(s.FunctionManifest); m != "" {
		values[FunctionManifestEnv] = m
	}
	cfg := Config{Values: values}
	for k := range values {
		cfg.Plain = append(cfg.Plain, k)
	}
	return cfg, nil
}

// VercelInstanceEnvOrder is the order the deploy README lists the variables
// in, for its golden test and anyone printing them.
func VercelInstanceEnvOrder() []string {
	return []string{
		capability.ModeEnv, storage.EnvStorageKind, storage.EnvStorageSource,
		storage.EnvStorageTokenIssuer, storage.EnvStorageTokenAudience, storage.EnvStorageTokenSubject,
		baseURLEnv, VercelProjectEnv, VercelTeamEnv, FunctionManifestEnv,
	}
}
