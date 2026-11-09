package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/csp"
)

// ⭐ Vercel as a Platform: the provisioner, scoped to the user's team, plus
// what a deploy command needs from it without naming it.

func init() { registerPlatform("vercel", openVercelPlatform) }

// vercelAppSuffix is the address Vercel gives a project from its name.
const vercelAppSuffix = ".vercel.app"

// vercelTeam is a team as the API names it (id) and as its OIDC tokens name it
// (slug).
type vercelTeam struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

type vercelPlatform struct {
	*Vercel
	team vercelTeam
}

// openVercelPlatform resolves the user's team, then returns a provisioner
// scoped to it.
//
// ⚠️ The team is resolved first because the configuration needs both of its
// names: the API's teamId takes the id, and the instance's OIDC issuer and
// subject name the slug. A user may write either.
func openVercelPlatform(ctx context.Context, opts PlatformOptions) (Platform, error) {
	if strings.TrimSpace(opts.Team) == "" {
		return nil, fmt.Errorf("provision: deploying to vercel needs the team")
	}
	// ⛔ The one exposure: consumed by the two clients, never kept as a string.
	token := opts.Token.Expose()
	unscoped, err := NewVercel(VercelConfig{Token: token, BaseURL: opts.APIBase, HTTPClient: opts.HTTPClient})
	if err != nil {
		return nil, err
	}
	team, err := unscoped.resolveTeam(ctx, strings.TrimSpace(opts.Team))
	if err != nil {
		return nil, err
	}
	v, err := NewVercel(VercelConfig{Token: token, TeamID: team.ID, BaseURL: opts.APIBase, HTTPClient: opts.HTTPClient})
	if err != nil {
		return nil, err
	}
	return &vercelPlatform{Vercel: v, team: team}, nil
}

// resolveTeam finds the team named by id or slug among the teams the token
// can see. ⛔ Not found refuses, naming what was asked for.
func (v *Vercel) resolveTeam(ctx context.Context, name string) (vercelTeam, error) {
	var out struct {
		Teams []vercelTeam `json:"teams"`
	}
	if err := v.call(ctx, http.MethodGet, "/v2/teams?limit=100", nil, &out); err != nil {
		return vercelTeam{}, fmt.Errorf("provision: listing the token's teams: %w", err)
	}
	for _, t := range out.Teams {
		if t.ID != "" && t.Slug != "" && (t.ID == name || strings.EqualFold(t.Slug, name)) {
			return t, nil
		}
	}
	return vercelTeam{}, fmt.Errorf("provision: the token can see no team %q; name the team by its id or its slug", name)
}

// vercelProjectName is a name Vercel accepts for a project and gives an
// address from: one DNS label.
var vercelProjectName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,98}[a-z0-9])?$`)

func (p *vercelPlatform) InstanceNameFor(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	name, ok := strings.CutSuffix(host, vercelAppSuffix)
	if !ok || !vercelProjectName.MatchString(name) {
		return "", false
	}
	return name, true
}

func (p *vercelPlatform) InstanceConfig(inst Instance, spec InstanceSpec) (Config, error) {
	store := vercelStoreID(inst)
	if store == "" {
		return Config{}, fmt.Errorf("provision: the instance's private store is not known, so its environment cannot name it")
	}
	return VercelInstanceEnv(VercelInstanceSpec{
		Project:          inst.Name,
		StoreID:          store,
		TeamID:           p.team.ID,
		TeamSlug:         p.team.Slug,
		BaseURL:          spec.BaseURL,
		FunctionManifest: spec.CodeManifest,
	})
}

// vercelStoreID is the id of the instance's one private store, as Create
// recorded it.
func vercelStoreID(inst Instance) string { return inst.Store }

// Handover is the deploy-token handover (VercelDeployTokenHandover, the
// default lifetime): a project-only token, set write-only before the first
// deployment, which the instance adopts.
//
// ⚠️ It runs against this platform's own provisioner, never the one a flow
// hands it: a caller may wrap the platform (a deploy command checks the
// address in Create), and the handover needs the Vercel client itself.
func (p *vercelPlatform) Handover() (func(ctx context.Context, _ Provisioner, inst Instance) error, error) {
	issue := VercelDeployTokenHandover(0)
	return func(ctx context.Context, _ Provisioner, inst Instance) error {
		return issue(ctx, p.Vercel, inst)
	}, nil
}

// Code is a local build output (config.json and the function), with the
// instance's routing, or the released build of src.Version.
//
// ⭐ A local build is a development build: its function manifest goes to the
// instance inline. A released build carries its routing already, and the
// instance fetches its manifest by version.
func (p *vercelPlatform) Code(ctx context.Context, src CodeSource) (PreparedCode, error) {
	if dir := strings.TrimSpace(src.BuildDir); dir != "" {
		code, err := ArchiveFunctionBuild(dir)
		if err != nil {
			return PreparedCode{}, err
		}
		code.Routing = &InstanceRouting{PageCSP: csp.TenantContent()}
		version := "dev-" + time.Now().UTC().Format("20060102T150405Z")
		m, err := FunctionManifestFor(code, version)
		if err != nil {
			return PreparedCode{}, err
		}
		mj, err := json.Marshal(m)
		if err != nil {
			return PreparedCode{}, err
		}
		return PreparedCode{Code: code, Manifest: string(mj), Version: version}, nil
	}
	code, err := ReleasedFunctionCode(ctx, src.HTTPClient, src.Version)
	if err != nil {
		return PreparedCode{}, err
	}
	return PreparedCode{Code: code, Version: src.Version}, nil
}

var _ Platform = (*vercelPlatform)(nil)
