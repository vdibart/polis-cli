package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const deployTestToken = "tok-NEVER-PRINTED-4f9a"

// fakeDeployPlatform is a platform whose instances get the address
// <name>.fake.app, unless the test says the name is taken.
type fakeDeployPlatform struct {
	calls      []string
	taken      bool          // Create gives another address
	existing   storage.Store // Create adopts an instance holding this store
	store      storage.Store
	config     provision.Config
	selfDeploy int // Status calls after Activate until the instance has deployed
	statusN    int
	active     string
	opts       provision.PlatformOptions
}

func (f *fakeDeployPlatform) Name() string { return "fake" }
func (f *fakeDeployPlatform) InstanceNameFor(host string) (string, bool) {
	n, ok := strings.CutSuffix(host, ".fake.app")
	return n, ok && n != ""
}
func (f *fakeDeployPlatform) Create(ctx context.Context, req provision.CreateRequest) (provision.Instance, error) {
	f.calls = append(f.calls, "Create:"+req.Name+":"+req.Region)
	if f.existing != nil {
		f.store = f.existing
		return provision.Instance{ID: "i-1", Name: req.Name, Store: "store_1", Existing: true}, nil
	}
	f.store = storage.NewObjectStore("")
	return provision.Instance{ID: "i-1", Name: req.Name, Store: "store_1"}, nil
}
func (f *fakeDeployPlatform) Status(ctx context.Context, inst provision.Instance) (provision.Status, error) {
	f.calls = append(f.calls, "Status")
	host := inst.Name + ".fake.app"
	if f.taken {
		host = inst.Name + "-x7q.fake.app"
	}
	if f.active != "" {
		f.statusN++
		if f.statusN > f.selfDeploy {
			f.active = "instance-deploy"
		}
	}
	return provision.Status{ActiveRelease: f.active, Domains: []provision.DomainStatus{{Domain: host, Attached: true}}}, nil
}
func (f *fakeDeployPlatform) Seed(ctx context.Context, inst provision.Instance, s storage.Site) error {
	f.calls = append(f.calls, "Seed")
	return provision.SeedStore(ctx, f.store, s)
}
func (f *fakeDeployPlatform) SetConfig(ctx context.Context, inst provision.Instance, cfg provision.Config) error {
	f.calls = append(f.calls, "SetConfig")
	if _, err := cfg.Classify(nil); err != nil {
		return err
	}
	f.config = cfg
	return nil
}
func (f *fakeDeployPlatform) PutCode(ctx context.Context, inst provision.Instance, code provision.Code) (provision.Release, error) {
	f.calls = append(f.calls, "PutCode")
	return provision.Release{ID: "rel-1"}, nil
}
func (f *fakeDeployPlatform) Activate(ctx context.Context, inst provision.Instance, rel provision.Release) error {
	f.calls = append(f.calls, "Activate")
	f.active = rel.ID
	return nil
}
func (f *fakeDeployPlatform) AttachDomain(ctx context.Context, inst provision.Instance, d string) (provision.DomainStatus, error) {
	f.calls = append(f.calls, "AttachDomain:"+d)
	return provision.DomainStatus{Domain: d, NeedsDNS: []provision.DNSRecord{{Type: "CNAME", Name: d, Value: "edge.fake"}}}, nil
}
func (f *fakeDeployPlatform) Destroy(ctx context.Context, inst provision.Instance) error {
	f.calls = append(f.calls, "Destroy")
	return nil
}
func (f *fakeDeployPlatform) InstanceConfig(inst provision.Instance, spec provision.InstanceSpec) (provision.Config, error) {
	return provision.Config{
		Values: map[string]string{"STORE": inst.Store, "BASE": spec.BaseURL, "CODE": spec.CodeManifest},
		Plain:  []string{"STORE", "BASE", "CODE"},
	}, nil
}
func (f *fakeDeployPlatform) Handover() (func(context.Context, provision.Provisioner, provision.Instance) error, error) {
	return func(context.Context, provision.Provisioner, provision.Instance) error {
		f.calls = append(f.calls, "Handover")
		return nil
	}, nil
}
func (f *fakeDeployPlatform) Code(ctx context.Context, src provision.CodeSource) (provision.PreparedCode, error) {
	if src.BuildDir == "" {
		return provision.PreparedCode{}, fmt.Errorf("%w: %q is a development version", provision.ErrNoReleasedCode, src.Version)
	}
	return provision.PreparedCode{Code: provision.Code{Archive: []byte("x"), Format: provision.FormatTarGz}, Manifest: "dev-manifest", Version: "dev-1"}, nil
}

// deployFixture is a branch output at https://alice.fake.app, a mode-600
// token file, and a config naming it.
type deployFixture struct {
	dir, tokenFile, config string
}

func newDeployFixture(t *testing.T, host string) deployFixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "branched")
	s := storage.At(dir)
	if _, err := site.Init(s, site.InitOptions{SiteTitle: "alice", Author: "alice"}); err != nil {
		t.Fatal(err)
	}
	_, recovery, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for k, v := range map[string]string{
		owner.RecoveryPublicKeyKey: string(recovery),
		".env":                     "# polis configuration\nPOLIS_BASE_URL=https://" + host + "\n",
	} {
		if err := s.Store().Put(ctx, k, []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(deployTestToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "deploy.toml")
	body := "# deploy\nplatform = \"fake\"\ntoken-file = \"" + tokenFile + "\"\nteam = my-team\nregion = \"r1\"\n"
	if err := os.WriteFile(config, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return deployFixture{dir: dir, tokenFile: tokenFile, config: config}
}

func deployWith(f *fakeDeployPlatform, prompts *bytes.Buffer) deployDeps {
	clock := time.Unix(1_800_000_000, 0)
	return deployDeps{
		open: func(_ context.Context, name string, opts provision.PlatformOptions) (provision.Platform, error) {
			if name != "fake" {
				return nil, fmt.Errorf("unknown platform %q", name)
			}
			f.opts = opts
			return f, nil
		},
		in:      strings.NewReader(""),
		prompts: prompts,
		version: "dev",
		sleep:   func(time.Duration) { clock = clock.Add(5 * time.Second) },
		now:     func() time.Time { return clock },
	}
}

// ⛔ The token never reaches an output or an error.
func assertNoToken(t *testing.T, where string, s string) {
	t.Helper()
	if strings.Contains(s, deployTestToken) {
		t.Errorf("%s contains the token", where)
	}
}

// ⭐⭐ THE WHOLE COMMAND OVER A BRANCH OUTPUT: the address read from the
// output, the instance named from it and checked after Create, then seed,
// configuration (the platform's and the owner's key, in one SetConfig), the
// handover and the first deployment, and the wait for the instance's own.
func TestBranchDeployEndToEnd(t *testing.T) {
	fx := newDeployFixture(t, "alice.fake.app")
	f := &fakeDeployPlatform{selfDeploy: 2}
	var prompts bytes.Buffer
	res, err := runBranchDeploy(context.Background(), branchDeployOptions{
		Dir: fx.dir, ConfigFile: fx.config, BuildDir: "/some/build", Wait: time.Minute,
	}, deployWith(f, &prompts))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Create:alice:r1", "Status", "Seed", "SetConfig", "Handover", "PutCode", "Activate"}
	if !reflect.DeepEqual(f.calls[:len(want)], want) {
		t.Fatalf("calls = %v, want %v then the wait", f.calls, want)
	}
	if f.opts.Team != "my-team" || f.opts.Token.IsZero() {
		t.Errorf("the platform was opened with team %q and a token present=%v", f.opts.Team, !f.opts.Token.IsZero())
	}
	cfg := f.config.Values
	if cfg["STORE"] != "store_1" || cfg["BASE"] != "https://alice.fake.app" || cfg["CODE"] != "dev-manifest" || cfg[owner.RecoveryKeyEnv] == "" {
		t.Errorf("SetConfig got %v", cfg)
	}
	if ok, _ := f.store.Exists(context.Background(), site.WellKnownKey); !ok {
		t.Error("the instance's store was not seeded")
	}
	if res.Address != "https://alice.fake.app" || res.Instance != "alice" || !res.PagesDeployed ||
		!strings.Contains(res.SignIn, "polis recover sign --domain alice.fake.app --challenge challenge.txt") {
		t.Errorf("result %+v", res)
	}
	j, _ := json.Marshal(res)
	assertNoToken(t, "the result", string(j))
	assertNoToken(t, "the prompts", prompts.String())
}

// ⛔ A taken name: the platform gave another address, so the empty instance
// is destroyed and nothing is seeded, configured or deployed.
func TestBranchDeployRefusesAnAddressThePlatformDidNotGive(t *testing.T) {
	fx := newDeployFixture(t, "alice.fake.app")
	f := &fakeDeployPlatform{taken: true}
	_, err := runBranchDeploy(context.Background(), branchDeployOptions{Dir: fx.dir, ConfigFile: fx.config, BuildDir: "/b"}, deployWith(f, &bytes.Buffer{}))
	if err == nil || !errors.Is(err, errAddressNotGiven) || !strings.Contains(err.Error(), "another --to") ||
		!strings.Contains(err.Error(), "alice-x7q.fake.app") {
		t.Fatalf("err = %v, want the address refusal naming both addresses and --to", err)
	}
	if want := []string{"Create:alice:r1", "Status", "Destroy"}; !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	if entries, _ := f.store.List(context.Background(), "", storage.Recursive()); len(entries) != 0 {
		t.Errorf("the store holds %d entries; nothing should be written", len(entries))
	}
	assertNoToken(t, "the error", err.Error())
}

// ⛔ A re-run against an instance that already holds a site refuses at the
// seed, leaves it as it was (never destroyed), and deploys nothing.
func TestBranchDeployRefusesAReRun(t *testing.T) {
	fx := newDeployFixture(t, "alice.fake.app")
	held := storage.NewObjectStore("")
	if err := provision.SeedStore(context.Background(), held, storage.At(fx.dir)); err != nil {
		t.Fatal(err)
	}
	f := &fakeDeployPlatform{existing: held}
	_, err := runBranchDeploy(context.Background(), branchDeployOptions{Dir: fx.dir, ConfigFile: fx.config, BuildDir: "/b"}, deployWith(f, &bytes.Buffer{}))
	if err == nil || !errors.Is(err, provision.ErrSeedRefused) || !strings.Contains(err.Error(), "deploys itself") {
		t.Fatalf("err = %v, want the re-run refusal", err)
	}
	for _, c := range f.calls {
		if c == "PutCode" || c == "Activate" || c == "Destroy" || c == "SetConfig" {
			t.Errorf("a re-run called %s (calls %v)", c, f.calls)
		}
	}
}

// ⛔ With no --build and no release of this version, it refuses naming
// --build, before any instance is made.
func TestBranchDeployWithNoCodeNamesBuild(t *testing.T) {
	fx := newDeployFixture(t, "alice.fake.app")
	f := &fakeDeployPlatform{}
	_, err := runBranchDeploy(context.Background(), branchDeployOptions{Dir: fx.dir, ConfigFile: fx.config}, deployWith(f, &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "--build") {
		t.Fatalf("err = %v, want a refusal naming --build", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("calls = %v; nothing should be made", f.calls)
	}
}

// ⭐ An address the platform does not make from a name needs the project, and
// the domain is attached to it, with the DNS records reported.
func TestBranchDeployToACustomDomainNeedsTheProject(t *testing.T) {
	fx := newDeployFixture(t, "alice.example")
	f := &fakeDeployPlatform{}
	_, err := runBranchDeploy(context.Background(), branchDeployOptions{Dir: fx.dir, ConfigFile: fx.config, BuildDir: "/b"}, deployWith(f, &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "--project") {
		t.Fatalf("no project: %v, want a refusal naming --project", err)
	}
	f = &fakeDeployPlatform{}
	res, err := runBranchDeploy(context.Background(), branchDeployOptions{
		Dir: fx.dir, ConfigFile: fx.config, BuildDir: "/b", Flags: deploySettings{Project: "alice-site"}, Wait: time.Minute,
	}, deployWith(f, &bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if f.calls[0] != "Create:alice-site:r1" || f.calls[1] != "AttachDomain:alice.example" {
		t.Errorf("calls = %v", f.calls)
	}
	if len(res.NeedsDNS) != 1 || res.PagesWaited {
		t.Errorf("result %+v: want the DNS record and no wait", res)
	}
}

// ⭐ The config holds only the five keys; flags win over it; a missing value is
// asked for at a terminal and refused by name otherwise.
func TestBranchDeploySettings(t *testing.T) {
	fx := newDeployFixture(t, "alice.fake.app")
	write := func(body string) string {
		p := filepath.Join(t.TempDir(), "c.toml")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for body, want := range map[string]string{
		"token = \"" + deployTestToken + "\"\n": "token-file",
		"api_token = abc\n":                     "token-file",
		"colour = blue\n":                       "holds only",
		"[deploy]\n":                            "key = value",
	} {
		_, err := readDeployConfig(write(body))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("config %q: %v, want a refusal mentioning %q", body, err, want)
			continue
		}
		assertNoToken(t, "a config error", err.Error())
	}

	// Flags win over the file.
	s, err := resolveDeploySettings(branchDeployOptions{ConfigFile: fx.config, Flags: deploySettings{Region: "r2"}}, deployDeps{}, deployConfigKeys[:4])
	if err != nil || s.Region != "r2" || s.Team != "my-team" {
		t.Errorf("settings %+v %v, want the flag's region and the file's team", s, err)
	}

	// No terminal: every missing value named at once.
	_, err = resolveDeploySettings(branchDeployOptions{}, deployDeps{}, deployConfigKeys[:4])
	for _, k := range []string{"platform", "token-file", "team", "region", "--token-file"} {
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("no settings, no terminal: %v, want %s named", err, k)
		}
	}

	// A terminal: asked for, in order.
	var prompts bytes.Buffer
	s, err = resolveDeploySettings(branchDeployOptions{}, deployDeps{
		interactive: true, in: strings.NewReader("fake\n" + fx.tokenFile + "\nteam-1\nr9\n"), prompts: &prompts,
	}, deployConfigKeys[:4])
	if err != nil || s.Platform != "fake" || s.TokenFile != fx.tokenFile || s.Team != "team-1" || s.Region != "r9" {
		t.Errorf("prompted settings %+v %v", s, err)
	}
	if !strings.Contains(prompts.String(), "token-file") {
		t.Errorf("prompts %q", prompts.String())
	}
}

// ⛔ The token file is read once, refused if others can read it, and never
// echoed, whatever is wrong with it.
func TestBranchDeployTokenFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte(deployTestToken), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTokenFile(p); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("a world-readable token file: %v", err)
	}
	if err := os.WriteFile(p, []byte(deployTestToken+"\nsecond-line"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readTokenFile(p)
	if err == nil {
		t.Error("a two-line token file was accepted")
	} else {
		assertNoToken(t, "a token-file error", err.Error())
	}
	if err := os.WriteFile(p, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTokenFile(p); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("an empty token file: %v", err)
	}
	if err := os.WriteFile(p, []byte(deployTestToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := readTokenFile(p)
	if err != nil || v.IsZero() {
		t.Fatalf("a good token file: %v", err)
	}
	if s := fmt.Sprintf("%v %s %+v", v, v, v); strings.Contains(s, deployTestToken) {
		t.Error("the token renders itself")
	}
}

// ⛔ Only a branch output is deployed.
func TestBranchDeployRefusesWhatIsNotABranchOutput(t *testing.T) {
	fx := newDeployFixture(t, "alice.fake.app")
	if err := os.Remove(filepath.Join(fx.dir, filepath.FromSlash(owner.RecoveryPublicKeyKey))); err != nil {
		t.Fatal(err)
	}
	if _, err := readBranchOutput(fx.dir); err == nil || !strings.Contains(err.Error(), "recovery") {
		t.Errorf("no recovery key: %v", err)
	}
	if _, err := readBranchOutput(fx.config); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("a file: %v", err)
	}
	fx = newDeployFixture(t, "alice.fake.app")
	if err := os.WriteFile(filepath.Join(fx.dir, ".env"), []byte("X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readBranchOutput(fx.dir); err == nil || !strings.Contains(err.Error(), "POLIS_BASE_URL") {
		t.Errorf("no address: %v", err)
	}
}

// ⭐ Both commands' help carries usage, every flag, an example, and what each
// refuses.
func TestBranchHelpIsComplete(t *testing.T) {
	for name, c := range map[string]struct {
		text  string
		flags []string
	}{
		"branch":        {branchUsage, []string{"--to", "--out", "--archive", "--from-domain", "--without-private", "--recovery-key", "--custody"}},
		"branch deploy": {branchDeployUsage, []string{"--config", "--platform", "--token-file", "--team", "--region", "--project", "--build", "--wait"}},
	} {
		for _, want := range append([]string{"Usage: polis " + name, "Example:", "Refuses", "polis branch deploy"}, c.flags...) {
			if !strings.Contains(c.text, want) {
				t.Errorf("polis %s --help lacks %q", name, want)
			}
		}
	}
}

// ⛔ Nothing in the command's CODE names a platform. Comments and the help
// text may (the help's example is a real address); identifiers, logic and
// every other string may not.
func TestBranchDeployNamesNoPlatform(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "branch_deploy.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok && len(vs.Names) == 1 && vs.Names[0].Name == "branchDeployUsage" {
			return false
		}
		var text string
		switch x := n.(type) {
		case *ast.Ident:
			text = x.Name
		case *ast.BasicLit:
			text = x.Value
		default:
			return true
		}
		if strings.Contains(strings.ToLower(text), "vercel") {
			t.Errorf("%s: %q names a platform", fset.Position(n.Pos()), text)
		}
		return true
	})
}
