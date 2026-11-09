package provision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	secretpkg "github.com/vdibart/polis-cli/cli-go/pkg/secret"
)

const platformTestToken = "pt-secret-0123456789"

// teamsAPI answers the team listing, and records every Authorization header
// and query it saw.
func teamsAPI(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.Header.Get("Authorization") != "Bearer "+platformTestToken {
			http.Error(w, `{"error":{"code":"forbidden"}}`, http.StatusForbidden)
			return
		}
		if r.URL.Path != "/v2/teams" {
			http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"teams": []map[string]string{
			{"id": "team_other", "slug": "someone-else"},
			{"id": "team_abc", "slug": "my-projects"},
		}})
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func openTestVercel(t *testing.T, team string) (Platform, error) {
	srv, _ := teamsAPI(t)
	return OpenPlatform(context.Background(), "vercel", PlatformOptions{
		Token: secretpkg.New(platformTestToken), Team: team, APIBase: srv.URL, HTTPClient: srv.Client(),
	})
}

// ⭐ A user names a team by its id or its slug, and the platform learns both,
// because the API is scoped by the id and the instance's OIDC token names the
// slug.
func TestTheTeamIsResolvedFromItsIdOrItsSlug(t *testing.T) {
	for _, name := range []string{"team_abc", "my-projects", "My-Projects"} {
		p, err := openTestVercel(t, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		vp := p.(*vercelPlatform)
		if vp.team.ID != "team_abc" || vp.team.Slug != "my-projects" || vp.Vercel.teamID != "team_abc" {
			t.Errorf("%s resolved to %+v (scoped to %q)", name, vp.team, vp.Vercel.teamID)
		}
	}
	if _, err := openTestVercel(t, "nobody"); err == nil || !strings.Contains(err.Error(), `"nobody"`) {
		t.Errorf("an unknown team: %v, want a refusal naming it", err)
	}
	if _, err := openTestVercel(t, ""); err == nil || !strings.Contains(err.Error(), "team") {
		t.Errorf("no team: %v, want a refusal naming the team", err)
	}
}

// ⛔ A platform nobody built, and a missing token, are refused before any call.
func TestOpeningAPlatformRefusesAnUnknownNameAndAMissingToken(t *testing.T) {
	_, err := OpenPlatform(context.Background(), "nimbus", PlatformOptions{Token: secretpkg.New("x")})
	if err == nil || !strings.Contains(err.Error(), "vercel") {
		t.Errorf("unknown platform: %v, want a refusal listing the known ones", err)
	}
	if _, err := OpenPlatform(context.Background(), "vercel", PlatformOptions{Team: "t"}); err == nil {
		t.Error("a platform opened with no token")
	}
}

// ⭐ <name>.vercel.app names the project; anything else is a domain the user
// names a project for.
func TestTheProjectNameComesFromAVercelAppAddress(t *testing.T) {
	p, err := openTestVercel(t, "team_abc")
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]string{
		"alice.vercel.app": "alice", "Alice-Site.vercel.app": "alice-site", "alice.vercel.app.": "alice",
	} {
		if got, ok := p.InstanceNameFor(host); !ok || got != want {
			t.Errorf("%s: %q %v, want %q", host, got, ok, want)
		}
	}
	for _, host := range []string{"alice.example", "a.b.vercel.app", "-x.vercel.app", "vercel.app", ".vercel.app"} {
		if got, ok := p.InstanceNameFor(host); ok {
			t.Errorf("%s gave project %q, want none", host, got)
		}
	}
}

// ⭐ The instance's environment names the store Create recorded. ⚠️ Until
// Create records one, it refuses rather than setting an empty store.
func TestTheInstanceEnvironmentRefusesAnUnknownStore(t *testing.T) {
	p, err := openTestVercel(t, "team_abc")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.InstanceConfig(Instance{ID: "prj_1", Name: "alice"}, InstanceSpec{BaseURL: "https://alice.vercel.app"})
	if err == nil || !strings.Contains(err.Error(), "store") {
		t.Errorf("no store: %v, want a refusal naming the store", err)
	}
	cfg, err := p.InstanceConfig(Instance{ID: "prj_1", Name: "alice", Store: "store_1"}, InstanceSpec{BaseURL: "https://alice.vercel.app"})
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"POLIS_STORAGE_SOURCE":        "store_1",
		VercelProjectEnv:              "alice",
		VercelTeamEnv:                 "team_abc",
		"POLIS_STORAGE_TOKEN_SUBJECT": "owner:my-projects:project:alice:environment:production",
	} {
		if cfg.Values[k] != want {
			t.Errorf("%s = %q, want %q", k, cfg.Values[k], want)
		}
	}
}

// ⛔ The settings provisioning writes for itself (Create's marker, the
// handover's token ids) are not the instance's environment: setting them here
// would overwrite what Create and the handover recorded.
func TestTheInstanceEnvironmentLeavesProvisioningsOwnSettingsAlone(t *testing.T) {
	cfg, err := VercelInstanceEnv(VercelInstanceSpec{Project: "a", StoreID: "s", TeamID: "t", TeamSlug: "t", BaseURL: "https://a.vercel.app", FunctionManifest: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{provisionedMarker, deployTokenIDSetting, "POLIS_DEPLOY_TOKEN", blobTokenSetting} {
		if _, ok := cfg.Values[k]; ok {
			t.Errorf("the instance environment sets %s", k)
		}
	}
}

// ⭐ The handover runs against the platform's own client even when the flow
// passes it a wrapper (as the deploy command does).
func TestTheHandoverIgnoresAWrappedProvisioner(t *testing.T) {
	p, err := openTestVercel(t, "team_abc")
	if err != nil {
		t.Fatal(err)
	}
	h, err := p.Handover()
	if err != nil || h == nil {
		t.Fatalf("handover: %v", err)
	}
	// The test API answers no token mint, so the handover fails; what matters
	// is that it reached the API rather than refusing the wrapper's type.
	err = h(context.Background(), &fakePlatform{}, Instance{ID: "prj_1", Name: "alice"})
	if err == nil || strings.Contains(err.Error(), "the platform is") {
		t.Errorf("handover through a wrapper: %v, want an API failure, not a type refusal", err)
	}
}

// ⛔ Every value of the environment is required, one line, and plain.
func TestTheVercelInstanceEnvironment(t *testing.T) {
	full := VercelInstanceSpec{Project: "alice", StoreID: "store_1", TeamID: "team_abc", TeamSlug: "my-projects", BaseURL: "https://alice.vercel.app"}
	cfg, err := VercelInstanceEnv(full)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Classify(secretpkg.IsSecretName); err != nil {
		t.Fatalf("the environment does not classify: %v", err)
	}
	if len(cfg.Plain) != len(cfg.Values) || len(cfg.Secret) != 0 {
		t.Errorf("plain %d secret %d of %d values; want every value plain", len(cfg.Plain), len(cfg.Secret), len(cfg.Values))
	}
	if _, ok := cfg.Values[FunctionManifestEnv]; ok {
		t.Error("a released build's environment carries an inline manifest")
	}
	full.FunctionManifest = `{"v":"x"}`
	if cfg, _ = VercelInstanceEnv(full); cfg.Values[FunctionManifestEnv] != `{"v":"x"}` {
		t.Error("a development build's manifest is not inline")
	}
	for _, bad := range []func(*VercelInstanceSpec){
		func(s *VercelInstanceSpec) { s.Project = "" },
		func(s *VercelInstanceSpec) { s.StoreID = " " },
		func(s *VercelInstanceSpec) { s.TeamID = "" },
		func(s *VercelInstanceSpec) { s.TeamSlug = "" },
		func(s *VercelInstanceSpec) { s.BaseURL = "" },
		func(s *VercelInstanceSpec) { s.BaseURL = "https://a\nX=1" },
	} {
		s := full
		bad(&s)
		if _, err := VercelInstanceEnv(s); err == nil {
			t.Errorf("%+v was accepted", s)
		}
	}
}

// ⭐ A local build is a development build: packed with the instance routing,
// and its manifest goes to the instance inline. ⛔ With no build, a development
// version has nothing to deploy.
func TestTheCodeIsALocalBuildOrTheRelease(t *testing.T) {
	p, err := openTestVercel(t, "team_abc")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := writeTestBuildOutput(dir); err != nil {
		t.Fatal(err)
	}
	pc, err := p.Code(context.Background(), CodeSource{BuildDir: dir, Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if pc.Code.Routing == nil || pc.Code.Routing.PageCSP == "" {
		t.Error("a local build carries no instance routing")
	}
	m, err := ParseFunctionManifest([]byte(pc.Manifest))
	if err != nil || len(m.Files) == 0 {
		t.Errorf("a local build's manifest: %v", err)
	}
	if _, err := p.Code(context.Background(), CodeSource{Version: "dev"}); !errors.Is(err, ErrNoReleasedCode) {
		t.Errorf("no build, a development version: %v, want ErrNoReleasedCode", err)
	}
	if _, err := p.Code(context.Background(), CodeSource{BuildDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "config.json") {
		t.Errorf("an empty build directory: %v, want a refusal naming config.json", err)
	}
}

// ⭐ A released build is the one its manifest names; ⛔ a release without one,
// or with a different one, is refused.
func TestTheReleasedCodeIsTheBuildItsManifestNames(t *testing.T) {
	ctx := context.Background()
	code := testBuild(t, FormatTarGz, nil)
	m, err := FunctionManifestFor(code, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	mb, _ := json.Marshal(m)
	m4, _ := FunctionManifestFor(code, "1.2.4")
	m4.Files[0].SHA = strings.Repeat("0", 40)
	mb4, _ := json.Marshal(m4)
	served := map[string][]byte{
		"/" + ReleaseRepo + "/releases/download/v1.2.3/" + FunctionManifestAsset: mb,
		"/" + ReleaseRepo + "/releases/download/v1.2.3/" + FunctionBuildAsset:    code.Archive,
		"/" + ReleaseRepo + "/releases/download/v1.2.4/" + FunctionManifestAsset: mb4,
		"/" + ReleaseRepo + "/releases/download/v1.2.4/" + FunctionBuildAsset:    code.Archive,
		"/" + ReleaseRepo + "/releases/download/v1.2.5/" + FunctionManifestAsset: mb,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := served[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	old := releaseBase
	releaseBase = srv.URL
	defer func() { releaseBase = old }()

	got, err := ReleasedFunctionCode(ctx, srv.Client(), "1.2.3")
	if err != nil || len(got.Archive) == 0 || got.Routing != nil {
		t.Fatalf("the release of 1.2.3: %v", err)
	}
	if _, err := ReleasedFunctionCode(ctx, srv.Client(), "1.2.4"); err == nil {
		t.Error("a build that is not its manifest's was accepted")
	}
	for _, v := range []string{"1.2.5", "1.3.0", "dev"} {
		if _, err := ReleasedFunctionCode(ctx, srv.Client(), v); !errors.Is(err, ErrNoReleasedCode) {
			t.Errorf("%s: %v, want ErrNoReleasedCode", v, err)
		}
	}
}

// writeTestBuildOutput writes a minimal prebuilt build output: config.json
// with a filesystem phase, and the function.
func writeTestBuildOutput(dir string) error {
	for name, body := range map[string]string{
		"config.json": `{"version":3,"routes":[{"handle":"filesystem"},{"src":"/(.*)","dest":"/api/index"}]}`,
		"functions/api/index.func/.vc-config.json": `{"runtime":"executable","handler":"bootstrap"}`,
		"functions/api/index.func/bootstrap":       "binary",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, "bootstrap") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			return err
		}
	}
	return nil
}
