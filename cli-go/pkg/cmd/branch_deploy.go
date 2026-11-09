package cmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/branch"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
	"github.com/vdibart/polis-cli/cli-go/pkg/secret"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// ⛔ NOTHING IN THIS FILE NAMES A PLATFORM. The platform is a word the user
// writes, and everything it means (its API, its settings, how it turns an
// address into an instance name) is behind provision.OpenPlatform.

const branchDeployUsage = `Usage: polis branch deploy <branch-output> [--config <file>]
                           [--platform <name>] [--token-file <path>] [--team <team>]
                           [--region <region>] [--project <name>] [--build <dir>] [--wait <duration>]

Put a site that 'polis branch' wrote live on a host, at the address it was
branched to. It creates the instance and its private store, seeds the site
into the store (the identity key first), sets the instance's configuration
(your recovery public key included), hands over the instance's deploy token,
and deploys the instance's code. The instance then deploys the site's pages
itself. It finishes with the address and how to sign in.

The address comes from the branch output (POLIS_BASE_URL in its .env). On a
platform that makes an address from an instance's name, the name comes from
the address: https://alice.vercel.app makes the instance "alice". For any
other address, name the instance with --project.

What the branch cannot know comes from a config file or flags (a flag wins
over the file), and a missing value is asked for at a terminal:

  platform    where to deploy (vercel)
  token-file  a file holding your API token for the platform (mode 600).
              The token is read once and never saved, printed or logged
  team        the team (account) to create the instance in, by id or slug
  region      where the instance's store lives, in the platform's own words
  project     the instance's name, only for an address the platform does
              not make from a name

The config file holds those keys and nothing else, one per line:

  # deploy.toml
  platform   = "vercel"
  token-file = "~/.config/polis/vercel-token"
  team       = "my-team"
  region     = "iad1"

The code is the released build matching this polis version, or a local build
output with --build. Each instance is set up once: after that it deploys
itself every time you publish, and this command never redeploys it.

Refuses, having changed nothing, when:
  - <branch-output> is not a directory 'polis branch' wrote (it needs
    .well-known/polis, the site key, .polis/deploy/recovery.pub and
    POLIS_BASE_URL in .env); unpack an --archive branch first
  - a value is missing and there is no terminal to ask at (each is named)
  - the config file holds any other key, or a token instead of a token file
  - the token file is readable by anyone but you, or is empty
  - there is no released build of this version and no --build
Refuses, and removes the empty instance it made, when:
  - the platform did not give the instance the branch's address (the name
    is taken): branch again with another --to
Refuses, and leaves the instance as it was, when:
  - the instance already holds a site (a re-run)

Example:
  polis branch export.zip --to https://alice.vercel.app --out ./alice
  polis branch deploy ./alice --config deploy.toml
  polis recover sign --domain alice.vercel.app --challenge challenge.txt

Flags may come before or after the branch output.

`

// The keys a deploy config may hold. ⛔ Nothing else: the branch knows the
// rest, and a token belongs in a file the config names.
var deployConfigKeys = []string{"platform", "token-file", "team", "region", "project"}

// deploySettings is what a deploy needs that the branch output does not say.
type deploySettings struct {
	Platform, TokenFile, Team, Region, Project string
}

func (s *deploySettings) field(key string) *string {
	switch key {
	case "platform":
		return &s.Platform
	case "token-file":
		return &s.TokenFile
	case "team":
		return &s.Team
	case "region":
		return &s.Region
	case "project":
		return &s.Project
	}
	return nil
}

// branchDeployOptions are the command's inputs.
type branchDeployOptions struct {
	Dir        string
	ConfigFile string
	Flags      deploySettings
	BuildDir   string
	Wait       time.Duration
}

// deployDeps are what the command reaches outside itself, replaced in tests.
type deployDeps struct {
	open        func(context.Context, string, provision.PlatformOptions) (provision.Platform, error)
	interactive bool
	in          io.Reader
	prompts     io.Writer
	version     string
	httpClient  *http.Client
	sleep       func(time.Duration)
	now         func() time.Time
}

// branchDeployResult is what a deploy did. ⛔ No field carries a secret.
type branchDeployResult struct {
	Address       string                `json:"address"`
	Platform      string                `json:"platform"`
	Instance      string                `json:"instance"`
	Release       string                `json:"release"`
	CodeVersion   string                `json:"code_version"`
	PagesDeployed bool                  `json:"pages_deployed"`
	PagesWaited   bool                  `json:"pages_waited"`
	NeedsDNS      []provision.DNSRecord `json:"needs_dns,omitempty"`
	RecoveryKey   string                `json:"recovery_key"`
	SignIn        string                `json:"sign_in"`
	Notes         []string              `json:"notes,omitempty"`
}

func handleBranchDeploy(args []string) {
	fs := flag.NewFlagSet("branch deploy", flag.ExitOnError)
	config := fs.String("config", "", "A file of the deploy settings below (key = value lines); a flag wins over it")
	var flags deploySettings
	fs.StringVar(&flags.Platform, "platform", "", "Where to deploy: "+strings.Join(provision.PlatformNames(), ", "))
	fs.StringVar(&flags.TokenFile, "token-file", "", "A file holding your API token for the platform, mode 600 (read once, never saved or printed)")
	fs.StringVar(&flags.Team, "team", "", "The team (account) to create the instance in, by id or slug")
	fs.StringVar(&flags.Region, "region", "", "Where the instance's store lives, in the platform's own words")
	fs.StringVar(&flags.Project, "project", "", "The instance's name; only for an address the platform does not make from a name")
	build := fs.String("build", "", "Deploy this local build output instead of the released build of this version")
	wait := fs.Duration("wait", 5*time.Minute, "How long to wait for the instance to deploy the site's pages (0: do not wait)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), branchDeployUsage)
		fs.PrintDefaults()
	}
	rest, err := parseFlagsInterspersed(fs, args)
	if err != nil {
		exitError("%v", err)
	}
	if len(rest) != 1 {
		exitError("Usage: polis branch deploy <branch-output> [--config <file>] (polis branch deploy --help for every flag)")
	}
	interactive := isInteractive() && !jsonOutput
	res, err := runBranchDeploy(context.Background(), branchDeployOptions{
		Dir: rest[0], ConfigFile: *config, Flags: flags, BuildDir: *build, Wait: *wait,
	}, deployDeps{
		open:        provision.OpenPlatform,
		interactive: interactive,
		in:          os.Stdin,
		prompts:     os.Stderr,
		version:     Version,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		sleep:       time.Sleep,
		now:         time.Now,
	})
	if err != nil {
		exitError("Not deployed: %v", err)
	}
	logCLIAction("branch deploy", map[string]interface{}{
		"address":  res.Address,
		"platform": res.Platform,
		"instance": res.Instance,
		"pages":    res.PagesDeployed,
	})
	if jsonOutput {
		outputJSON(map[string]interface{}{"status": "success", "command": "branch deploy", "data": res})
		return
	}
	printBranchDeploy(res)
}

func printBranchDeploy(res *branchDeployResult) {
	fmt.Println("[✓] Deployed")
	fmt.Printf("  Address:  %s\n", res.Address)
	fmt.Printf("  Instance: %s on %s (release %s, code %s)\n", res.Instance, res.Platform, res.Release, res.CodeVersion)
	switch {
	case res.PagesDeployed:
		fmt.Println("[i] The instance has deployed the site's pages")
	case res.PagesWaited:
		fmt.Println("[!] The instance has not deployed the site's pages yet. Open the address: its first visit starts")
		fmt.Println("    the instance, which then deploys them. Until it does, pages show the app instead of the post.")
	default:
		fmt.Println("[i] The instance deploys the site's pages when it is first visited")
	}
	for _, r := range res.NeedsDNS {
		fmt.Printf("[!] Create this DNS record: %s %s %s\n", r.Type, r.Name, r.Value)
	}
	for _, n := range res.Notes {
		fmt.Printf("[i] %s\n", n)
	}
	fmt.Println()
	fmt.Println("Next: open the address and sign in. The setup screen shows a challenge: save its \"Copy message only\" text as challenge.txt, then sign it with your recovery key:")
	fmt.Printf("  %s\n", res.SignIn)
	fmt.Printf("  (your recovery key: %s)\n", res.RecoveryKey)
	// ⚠️ The only prompt to announce the branch, and an easy one to miss (see
	// handleBranchAnnounce): optional, and only possible once registered.
	fmt.Println("Optional, once the site is registered: `polis branch announce` tells the network where this site came from.")
}

// branchOutput is what the command reads from a branch output.
type branchOutput struct {
	site    storage.Site
	baseURL string
	host    string
}

// readBranchOutput checks dir is a site 'polis branch' wrote, and reads its
// address. ⛔ It reads, never writes.
//
// NOT the storage interface for the first check: whether the path the user
// named is a directory at all is a question about their machine, asked
// before it is opened as a site.
func readBranchOutput(dir string) (branchOutput, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return branchOutput{}, fmt.Errorf("cannot read the branch output %s: %w", dir, err)
	}
	if !info.IsDir() {
		return branchOutput{}, fmt.Errorf("%s is not a directory: name the directory 'polis branch' wrote (unpack an --archive branch first)", dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return branchOutput{}, err
	}
	s := storage.At(abs)
	ctx := context.Background()
	for _, need := range []struct{ key, what string }{
		{site.WellKnownKey, "the site's identity"},
		{site.PrivateKeyKey, "the site key"},
		{owner.RecoveryPublicKeyKey, "your recovery public key"},
	} {
		ok, err := s.Store().Exists(ctx, need.key)
		if err != nil {
			return branchOutput{}, fmt.Errorf("cannot check %s for %s: %w", dir, need.key, err)
		}
		if !ok {
			return branchOutput{}, fmt.Errorf("%s is not a branch output: it has no %s (%s); make one with 'polis branch'", dir, need.key, need.what)
		}
	}
	env, err := s.Store().Get(ctx, ".env")
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return branchOutput{}, fmt.Errorf("cannot read %s/.env: %w", dir, err)
	}
	base := envFileValue(string(env), "POLIS_BASE_URL")
	if base == "" {
		return branchOutput{}, fmt.Errorf("%s is not a branch output: its .env sets no POLIS_BASE_URL", dir)
	}
	if !strings.HasPrefix(base, "https://") {
		return branchOutput{}, fmt.Errorf("the branch's address %s is not https; branch again with an https --to", base)
	}
	host := strings.ToLower(polisurl.ExtractDomain(base))
	if host == "" || strings.ContainsAny(host, "/:@ ") {
		return branchOutput{}, fmt.Errorf("the branch's address %s names no host", base)
	}
	return branchOutput{site: s, baseURL: strings.TrimSuffix(base, "/"), host: host}, nil
}

// envFileValue is the first value of key in a .env file's text.
func envFileValue(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		k, v, ok := strings.Cut(t, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		return unquote(strings.TrimSpace(v))
	}
	return ""
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// readDeployConfig reads a config file of key = value lines (a TOML subset:
// strings, quoted or bare, and # comments).
//
// NOT the storage interface: the config is a file on the user's machine,
// outside every site.
//
// ⛔ Only the deploy keys. A key named for a token itself is refused with the
// reason, and so is anything else, so a mistyped key is never ignored.
func readDeployConfig(path string) (deploySettings, error) {
	var s deploySettings
	b, err := os.ReadFile(expandHome(path))
	if err != nil {
		return s, fmt.Errorf("cannot read the config %s: %w", path, err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			return s, fmt.Errorf("%s line %d is not key = value", path, i+1)
		}
		key := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(k)), "_", "-")
		val := unquote(strings.TrimSpace(v))
		switch {
		case key == "token" || key == "api-token" || key == "secret":
			// ⛔ The line number, never the value.
			return s, fmt.Errorf("%s line %d holds a token: the config names the token's FILE (token-file), never the token", path, i+1)
		case s.field(key) == nil:
			return s, fmt.Errorf("%s line %d sets %q; the config holds only %s", path, i+1, strings.TrimSpace(k), strings.Join(deployConfigKeys, ", "))
		}
		*s.field(key) = val
	}
	return s, nil
}

// resolveDeploySettings is the file, then the flags, then a prompt for what is
// still missing. ⛔ Without a terminal every missing value is refused by name,
// all at once.
func resolveDeploySettings(o branchDeployOptions, d deployDeps, required []string) (deploySettings, error) {
	var s deploySettings
	if o.ConfigFile != "" {
		var err error
		if s, err = readDeployConfig(o.ConfigFile); err != nil {
			return s, err
		}
	}
	for _, k := range deployConfigKeys {
		if v := strings.TrimSpace(*o.Flags.field(k)); v != "" {
			*s.field(k) = v
		}
	}
	return s, askMissing(&s, d, required)
}

// askMissing prompts for each required key that is empty, or refuses naming
// them.
func askMissing(s *deploySettings, d deployDeps, required []string) error {
	var missing []string
	for _, k := range required {
		if strings.TrimSpace(*s.field(k)) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if !d.interactive {
		flags := make([]string, len(missing))
		for i, k := range missing {
			flags[i] = "--" + k
		}
		return fmt.Errorf("missing %s: set them in --config or pass %s", strings.Join(missing, ", "), strings.Join(flags, " "))
	}
	r := bufio.NewReader(d.in)
	for _, k := range missing {
		hint := ""
		if k == "platform" {
			hint = " (" + strings.Join(provision.PlatformNames(), ", ") + ")"
		}
		fmt.Fprintf(d.prompts, "%s%s: ", k, hint)
		line, err := r.ReadString('\n')
		v := strings.TrimSpace(line)
		if v == "" {
			if err != nil {
				return fmt.Errorf("missing %s", k)
			}
			return fmt.Errorf("missing %s: nothing was entered", k)
		}
		*s.field(k) = v
	}
	return nil
}

// readTokenFile reads the platform token, once.
//
// NOT the storage interface: the token file is on the user's machine,
// outside every site, and its mode is checked as ssh checks a key's.
//
// ⛔ As ssh does with a key: a token file others can read is refused, not
// used. ⛔ Messages name the file, never its contents.
func readTokenFile(path string) (secret.Value, error) {
	p := expandHome(path)
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return secret.Value{}, fmt.Errorf("no token file at %s", path)
	}
	if err != nil {
		return secret.Value{}, fmt.Errorf("cannot read the token file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return secret.Value{}, fmt.Errorf("the token file %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return secret.Value{}, fmt.Errorf("the token file %s is readable by others (mode %o): run chmod 600 %s, then deploy again", path, info.Mode().Perm(), path)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return secret.Value{}, fmt.Errorf("cannot read the token file %s: %w", path, err)
	}
	defer signing.ZeroKey(b)
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return secret.Value{}, fmt.Errorf("the token file %s is empty", path)
	}
	if strings.ContainsAny(tok, "\r\n") {
		return secret.Value{}, fmt.Errorf("the token file %s holds more than one line: it must hold the token alone", path)
	}
	return secret.New(tok), nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// errAddressNotGiven is the platform giving the instance another address than
// the branch's.
var errAddressNotGiven = errors.New("the platform did not give the instance the branch's address")

// addressChecked is a platform whose Create checks the address before
// anything is seeded.
//
// ⭐ The check sits between Create and Seed, and lives here rather than in
// the provisioning flow, because only a branch knows its address in advance.
// ⛔ On a mismatch the instance is empty (nothing is seeded before Create
// returns), so it is destroyed, unless it was there before.
type addressChecked struct {
	provision.Platform
	host        string
	fromAddress bool
	dns         []provision.DNSRecord
}

func (a *addressChecked) Create(ctx context.Context, req provision.CreateRequest) (provision.Instance, error) {
	inst, err := a.Platform.Create(ctx, req)
	if err != nil {
		return inst, err
	}
	refuse := func(why error) (provision.Instance, error) {
		if inst.Existing {
			return provision.Instance{}, fmt.Errorf("%w; the instance was already there, so it was left as it was", why)
		}
		if derr := a.Platform.Destroy(ctx, inst); derr != nil {
			return provision.Instance{}, fmt.Errorf("%w; and removing the empty instance %s failed, so remove it by hand: %v", why, inst.Name, derr)
		}
		return provision.Instance{}, fmt.Errorf("%w (nothing was seeded, and the empty instance was removed)", why)
	}
	if !a.fromAddress {
		// A domain the user named: point it at the instance.
		st, err := a.Platform.AttachDomain(ctx, inst, a.host)
		if err != nil {
			return refuse(fmt.Errorf("attaching %s: %w", a.host, err))
		}
		a.dns = st.NeedsDNS
		return inst, nil
	}
	st, err := a.Platform.Status(ctx, inst)
	if err != nil {
		return refuse(fmt.Errorf("could not read the instance's address, so it was not checked: %w", err))
	}
	var got []string
	for _, d := range st.Domains {
		if strings.EqualFold(strings.TrimSuffix(d.Domain, "."), a.host) {
			return inst, nil
		}
		got = append(got, d.Domain)
	}
	sort.Strings(got)
	gave := "no address"
	if len(got) > 0 {
		gave = strings.Join(got, ", ")
	}
	return refuse(fmt.Errorf("%w: it serves %s, not %s, so the name is taken. Branch again with another --to", errAddressNotGiven, gave, a.host))
}

// runBranchDeploy is the command, without its terminal.
func runBranchDeploy(ctx context.Context, o branchDeployOptions, d deployDeps) (*branchDeployResult, error) {
	out, err := readBranchOutput(o.Dir)
	if err != nil {
		return nil, err
	}
	set, err := resolveDeploySettings(o, d, []string{"platform", "token-file", "team", "region"})
	if err != nil {
		return nil, err
	}
	token, err := readTokenFile(set.TokenFile)
	if err != nil {
		return nil, err
	}
	plat, err := d.open(ctx, set.Platform, provision.PlatformOptions{Token: token, Team: set.Team})
	if err != nil {
		return nil, err
	}

	name, fromAddress := plat.InstanceNameFor(out.host)
	switch {
	case fromAddress && set.Project != "" && !strings.EqualFold(set.Project, name):
		return nil, fmt.Errorf("the address %s makes the instance %q, and project is %q: leave project unset, or branch to the address you want", out.host, name, set.Project)
	case !fromAddress:
		if err := askMissing(&set, d, []string{"project"}); err != nil {
			return nil, fmt.Errorf("%v (%s is not an address %s makes from a name, so name the instance)", err, out.host, plat.Name())
		}
		name = set.Project
	}

	code, err := plat.Code(ctx, provision.CodeSource{BuildDir: o.BuildDir, Version: d.version, HTTPClient: d.httpClient})
	if errors.Is(err, provision.ErrNoReleasedCode) {
		return nil, fmt.Errorf("%w; build the instance and pass its build output with --build <dir>", err)
	}
	if err != nil {
		return nil, err
	}
	handover, err := plat.Handover()
	if err != nil {
		return nil, err
	}

	checked := &addressChecked{Platform: plat, host: out.host, fromAddress: fromAddress}
	inst, rel, err := provision.ProvisionSeeded(ctx, checked, provision.SeededProvision{
		Create: provision.CreateRequest{Name: name, Region: set.Region},
		Site:   out.site,
		ConfigFor: func(inst provision.Instance) (provision.Config, error) {
			return plat.InstanceConfig(inst, provision.InstanceSpec{BaseURL: out.baseURL, CodeManifest: code.Manifest})
		},
		Handover: handover,
		Code:     code.Code,
	})
	if errors.Is(err, provision.ErrSeedRefused) {
		return nil, fmt.Errorf("%w. This command sets an instance up once; after that the instance deploys itself on every publish, so nothing here redeploys it", err)
	}
	if err != nil {
		return nil, err
	}

	res := &branchDeployResult{
		Address:     out.baseURL,
		Platform:    plat.Name(),
		Instance:    inst.Name,
		Release:     rel.ID,
		CodeVersion: code.Version,
		NeedsDNS:    checked.dns,
	}
	if keyPath, err := branch.DefaultRecoveryKeyPath(out.host); err == nil {
		res.RecoveryKey = keyPath
	}
	res.SignIn = "polis recover sign --domain " + out.host + " --challenge challenge.txt"
	res.Notes = append(res.Notes, "Nothing was announced to a discovery service.")
	if fromAddress && o.Wait > 0 {
		res.PagesWaited = true
		res.PagesDeployed = waitForPages(ctx, plat, inst, rel, out.baseURL, o.Wait, d)
	}
	return res, nil
}

// waitForPages visits the address, which starts the instance, and waits until
// the instance has made a deployment of its own: its first one carries the
// site's pages. ⭐ The visit is what triggers it, since an instance starts on
// its first request.
func waitForPages(ctx context.Context, plat provision.Platform, inst provision.Instance, rel provision.Release, baseURL string, wait time.Duration, d deployDeps) bool {
	deadline := d.now().Add(wait)
	for {
		if req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/", nil); err == nil && d.httpClient != nil {
			if resp, err := d.httpClient.Do(req); err == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
				resp.Body.Close()
			}
		}
		if st, err := plat.Status(ctx, inst); err == nil && st.ActiveRelease != "" && st.ActiveRelease != rel.ID {
			return true
		}
		if !d.now().Before(deadline) || ctx.Err() != nil {
			return false
		}
		d.sleep(5 * time.Second)
	}
}
