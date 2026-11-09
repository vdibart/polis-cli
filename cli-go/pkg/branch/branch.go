// Package branch turns an exported polis site into a new site at a new
// address, with its identity key already rotated: `polis branch`, the local
// half.
//
// ⭐ EVERY IDENTITY STEP HAPPENS HERE, ON THE OWNER'S MACHINE, AND IT IS THE
// SAME FOR EVERY DESTINATION. The command takes an export (content, the private
// key and the private residue) and a target address, makes a new key, signs the
// handover from the exported key under the ORIGIN's domain, and writes a
// complete site. Putting that site somewhere is a separate step, so nothing in
// this package knows about any host.
//
// ⛔ What a branch never does:
//   - change its inputs, or any existing site: the source stays active and
//     unaffected, publishing under its own key;
//   - call a discovery service, or make any other network call: re-announcing
//     the content under the new key is optional and later;
//   - re-sign a signed file, except the follow list and the blessing list,
//     which carry no signing time and so verify only against the current key —
//     and only when they verify under the exported key, as a rotation does;
//   - carry the origin's discovery-service registration, its DM keyring or
//     conversations, its retired or staged keys, or its logs;
//   - keep the exported private key once it has signed the handover.
//
// ⛔ A BRANCH ALWAYS NEEDS THE SOURCE'S PRIVATE KEY. Without it the handover
// cannot be signed, and the result would be a mirror (a copy of public content
// with no claim to the identity), never a branch.
package branch

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// Options is one branch.
type Options struct {
	// Source is the export: a zip archive, or a directory holding an unpacked
	// one. ⭐ The input is a slot, not a format: a live URL and an existing
	// site directory are later sources, and nothing here rules them out.
	Source string

	// To is the new address, an http(s) URL with a host and no path.
	To string

	// Out is the directory to write. It must not exist, or be empty.
	// Archive, instead, writes a zip archive at that path, which must not
	// exist. Exactly one is set.
	Out     string
	Archive string

	// FromDomain is the origin's domain, which goes inside the handover
	// signature. Empty reads it from the export's .well-known/did.json.
	FromDomain string

	// WithoutPrivate proceeds without the private residue (policies, settings,
	// drafts). ⛔ Without it a branch whose export holds no residue refuses,
	// because the reset is a choice the owner has to make, knowingly.
	WithoutPrivate bool

	// CLIThemesDir is the renderer's fallback themes directory, as for
	// `polis render`. Optional.
	CLIThemesDir string

	// Custody decides which private key the owner keeps (see ownerKeys).
	// Empty is ServerSigned.
	Custody Custody

	// RecoveryKey is where the owner's private key is written. Empty is
	// DefaultRecoveryKeyPath of the new domain. ⛔ It must lie outside the
	// input and the output, and nothing may exist there.
	RecoveryKey string
}

// ResidueItem is one part of the private residue: what it is, and what the
// new site gets in its place when it is not carried.
type ResidueItem struct {
	Key     string `json:"key"`
	What    string `json:"what"`
	Default string `json:"default"`
}

// Residue is the private residue, POLICIES FIRST: the private policies decide
// who may interact with the site, so they head any list of what is reset.
var Residue = []ResidueItem{
	{policy.PrivateRulesKey, "private policies (who may comment, follow and message)", "the default private policy"},
	{bundle.RegistryKey, "active theme and shape", "the default theme and shape"},
	{".polis/webapp/config.json", "web app settings", "default settings"},
	{".polis/webapp/hooks", "post-action hooks", "no hooks"},
	{".polis/ds", "discovery-service preferences (notifications, feed)", "default preferences"},
	{".polis/bundles/pub.polis.core/posts/drafts", "post drafts", "none: drafts are not carried"},
	{".polis/bundles/pub.polis.core/comments/drafts", "comment drafts", "none: drafts are not carried"},
	{".polis/bundles/pub.polis.core/comments/pending", "comments awaiting a blessing", "none"},
	{".polis/bundles/pub.polis.core/comments/denied", "denied comments", "none"},
}

// Result is what a branch did.
type Result struct {
	Output     string `json:"output"`
	IsArchive  bool   `json:"archive"`
	FromDomain string `json:"from_domain"`
	ToDomain   string `json:"to_domain"`
	BaseURL    string `json:"base_url"`

	NewPublicKey string `json:"new_public_key"`
	Epoch        int    `json:"key_history_epoch"`
	// Annotated is how many earlier handovers were given the origin as their
	// signing_domain.
	Annotated int `json:"annotated_entries"`

	FollowResign  signing.ResignOutcome `json:"following_resign"`
	BlessedResign signing.ResignOutcome `json:"blessed_resign"`
	ResignErrors  []string              `json:"resign_errors,omitempty"`

	MessagesKeyRemoved bool `json:"messages_key_removed"`
	// LeftBehind lists, by kind, what the export held that the branch did not
	// carry.
	LeftBehind []string `json:"left_behind"`
	// ResidueReset lists what was reset to defaults, policies first. Empty when
	// the residue was carried.
	ResidueReset []ResidueItem `json:"residue_reset,omitempty"`

	PostsRendered    int `json:"posts_rendered"`
	CommentsRendered int `json:"comments_rendered"`

	// RecoveryKeyPath is where the owner's recovery private key was written,
	// outside the output. RecoveryPublicKey is its public half, which the
	// output carries at owner.RecoveryPublicKeyKey for the deploy step.
	RecoveryKeyPath   string `json:"recovery_key_path"`
	RecoveryPublicKey string `json:"recovery_public_key"`

	// Validation is the local site check over the output.
	Validation *sitecheck.Report `json:"-"`
	// InheritedFailures are checks that fail on the export too: problems the
	// branch carried, not ones it made.
	InheritedFailures []string `json:"inherited_failures,omitempty"`
}

// Refusals a caller can tell apart.
var (
	ErrNoPrivateKey   = errors.New("the export holds no private key")
	ErrKeyNotHead     = errors.New("the export's private key is not the key its .well-known/polis publishes")
	ErrNoOriginDomain = errors.New("the origin's domain is unknown")
	ErrNoResidue      = errors.New("the export holds no private residue")
	ErrOutputExists   = errors.New("the output already exists")
	ErrBranchBroke    = errors.New("the branched site fails a check the export passes")
)

// rotationTime is the handover's valid_from; empty means now. A variable only
// so a test can place the rotation after artifacts its fixture signed in the
// same second (a key's window starts AT valid_from).
var rotationTime = func() string { return "" }

// Run makes the branch. On any error nothing is written at the output.
func Run(opts Options) (*Result, error) {
	ctx := context.Background()

	baseURL, toDomain, err := targetAddress(opts.To)
	if err != nil {
		return nil, err
	}
	if (opts.Out == "") == (opts.Archive == "") {
		return nil, errors.New("name exactly one output: a directory, or an archive")
	}
	target := opts.Out
	if opts.Archive != "" {
		target = opts.Archive
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	if err := refuseExistingOutput(target, opts.Archive != ""); err != nil {
		return nil, err
	}
	if opts.Custody == ClientSigned {
		return nil, ErrClientSignedNotBuilt
	}
	if opts.Custody != "" && opts.Custody != ServerSigned {
		return nil, fmt.Errorf("unknown custody %q: use %q", opts.Custody, ServerSigned)
	}
	recoveryPath := opts.RecoveryKey
	if recoveryPath == "" {
		if recoveryPath, err = DefaultRecoveryKeyPath(toDomain); err != nil {
			return nil, err
		}
	}
	if recoveryPath, err = checkRecoveryKeyPath(recoveryPath, target); err != nil {
		return nil, err
	}

	// ⭐ Stage beside the output, so the finished directory lands by rename on
	// the same filesystem. Removed on every exit, which is what removes the
	// unpacked copy of the exported key.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("create the output's parent directory: %w", err)
	}
	removeStaleStaging(filepath.Dir(target))
	stageRoot, err := os.MkdirTemp(filepath.Dir(target), stagingPrefix)
	if err != nil {
		return nil, fmt.Errorf("create a staging directory: %w", err)
	}
	defer os.RemoveAll(stageRoot)
	// ⛔ The staging directory holds the unpacked export's private key and
	// the new site's, and a deferred removal does not run when the process
	// is interrupted. So an interrupt removes it before exiting, and the next
	// run sweeps up any that a harder kill left behind.
	stopCleanup := removeOnInterrupt(stageRoot)
	defer stopCleanup()

	in, err := openSource(opts.Source, filepath.Join(stageRoot, "in"))
	if err != nil {
		return nil, err
	}
	if within(target, in) || within(in, target) {
		return nil, errors.New("the output and the export must not contain one another")
	}
	srcAbs, _ := filepath.Abs(opts.Source)
	if _, err := checkRecoveryKeyPath(recoveryPath, in, srcAbs); err != nil {
		return nil, err
	}
	if err := checkSource(ctx, in); err != nil {
		return nil, err
	}

	fromDomain, err := originDomain(in, opts.FromDomain)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(fromDomain, toDomain) {
		return nil, fmt.Errorf("the new address %s is the origin's own: a branch goes to a different address", toDomain)
	}

	residuePresent := presentResidue(in)
	if len(residuePresent) == 0 && !opts.WithoutPrivate {
		return nil, fmt.Errorf("%w (%s). Branching without it resets every one of these to defaults:\n%s"+
			"Pass --without-private to branch anyway", ErrNoResidue, "policies, settings and drafts",
			describeResidue())
	}

	res := &Result{FromDomain: fromDomain, ToDomain: toDomain, BaseURL: baseURL, IsArchive: opts.Archive != ""}

	// ---- copy, minus what never crosses ----
	out := filepath.Join(stageRoot, "out")
	left, err := copySite(in, out, !opts.WithoutPrivate)
	if err != nil {
		return nil, err
	}
	res.LeftBehind = left
	if opts.WithoutPrivate {
		res.ResidueReset = append([]ResidueItem(nil), Residue...)
	}
	s := storage.At(out)
	if err := ensurePublicKeyFile(ctx, s); err != nil {
		return nil, err
	}
	if err := writeResidueDefaults(ctx, s); err != nil {
		return nil, err
	}

	// ---- rotate offline, naming the ORIGIN ----
	// Earlier handovers first: each must keep meaning the origin once the
	// chain is read at the new address.
	if res.Annotated, err = site.AnnotateSigningDomains(s, fromDomain); err != nil {
		return nil, fmt.Errorf("annotate the key history with the origin: %w", err)
	}
	// The DM keyring stays behind, so the block advertising its keys goes.
	if res.MessagesKeyRemoved, err = site.RemoveMessagesKey(s); err != nil {
		return nil, fmt.Errorf("remove the messages-key block: %w", err)
	}
	keys, err := site.PrepareRotation(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyNotHead, err)
	}
	rot, err := site.CompleteRotation(ctx, s, keys, site.RotationOptions{
		SigningDomain: fromDomain,
		DIDHost:       toDomain,
		Timestamp:     rotationTime(),
	})
	if err != nil {
		return nil, fmt.Errorf("rotate the key: %w", err)
	}
	if rot.FinishErr != nil {
		return nil, fmt.Errorf("remove the exported key after the handover: %w", rot.FinishErr)
	}
	if !rot.DIDPublished {
		return nil, fmt.Errorf("publish did.json for %s: %v", toDomain, rot.DIDErr)
	}
	res.NewPublicKey, res.Epoch = rot.NewPublicKey, rot.Epoch
	res.FollowResign, res.BlessedResign = rot.FollowResign, rot.BlessedResign
	for _, e := range []error{rot.FollowErr, rot.BlessedErr} {
		if e != nil {
			res.ResignErrors = append(res.ResignErrors, e.Error())
		}
	}

	// ---- the owner's key: which one is withheld, and its public half ----
	identity, err := s.Store().Get(ctx, site.PrivateKeyKey)
	if err != nil {
		return nil, fmt.Errorf("read the new identity key: %w", err)
	}
	withheld, recoveryPub, _, err := ownerKeys(opts.Custody, identity)
	signing.ZeroKey(identity)
	if err != nil {
		return nil, err
	}
	defer signing.ZeroKey(withheld)
	if err := s.Store().Put(ctx, owner.RecoveryPublicKeyKey, recoveryPub, storage.WithMode(0o644)); err != nil {
		return nil, fmt.Errorf("write the recovery public key: %w", err)
	}
	res.RecoveryPublicKey = strings.TrimSpace(string(recoveryPub))

	// ---- the new address ----
	if err := setBaseURL(ctx, s, baseURL); err != nil {
		return nil, err
	}

	// ---- regenerate the derived output ----
	stats, err := renderSite(s, out, baseURL, opts.CLIThemesDir)
	if err != nil {
		return nil, err
	}
	res.PostsRendered, res.CommentsRendered = stats.PostsRendered, stats.CommentsRendered

	// ---- check: only what the export already failed may fail here ----
	// No discovery-service keys are fetched: witnesses are left unchecked,
	// and reported as such, rather than making a network call.
	before := sitecheck.RunLocalWithThemes(in, nil, opts.CLIThemesDir)
	after := sitecheck.RunLocalWithThemes(out, nil, opts.CLIThemesDir)
	res.Validation = after
	if after.FatalError != "" {
		return nil, fmt.Errorf("%w: %s", ErrBranchBroke, after.FatalError)
	}
	failedBefore := map[string]bool{}
	for _, c := range before.Checks {
		if c.Outcome == sitecheck.OutcomeFailed {
			failedBefore[c.ID] = true
		}
	}
	var broke []string
	for _, c := range after.Checks {
		if c.Outcome != sitecheck.OutcomeFailed {
			continue
		}
		if failedBefore[c.ID] {
			res.InheritedFailures = append(res.InheritedFailures, c.ID)
			continue
		}
		broke = append(broke, fmt.Sprintf("%s: %s %s", c.ID, c.Detail, strings.Join(c.Findings, "; ")))
	}
	if len(broke) > 0 {
		return nil, fmt.Errorf("%w; nothing was written:\n  %s", ErrBranchBroke, strings.Join(broke, "\n  "))
	}

	// ---- write ----
	// ⭐ The owner's key first, outside the output, so a branch never lands
	// without it; and removed again if the output then fails, so a failed
	// branch leaves no orphan key behind.
	if err := writeRecoveryKey(recoveryPath, withheld); err != nil {
		return nil, err
	}
	if err := finishOutput(out, target, opts.Archive != ""); err != nil {
		removeRecoveryKey(recoveryPath)
		return nil, err
	}
	res.RecoveryKeyPath = recoveryPath
	res.Output = target
	return res, nil
}

// finishOutput writes the staged site at target: a zip archive, or the
// directory itself, moved into place. A variable only so a test can make it
// fail after the recovery key is written.
var finishOutput = func(out, target string, archive bool) error {
	if archive {
		return writeArchive(out, target)
	}
	// An empty directory the caller made is replaced by the staged one.
	if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("replace the empty output directory: %w", err)
	}
	if err := os.Rename(out, target); err != nil {
		return fmt.Errorf("move the branched site into place: %w", err)
	}
	return nil
}

// targetAddress parses the new address into a base URL and its domain.
func targetAddress(to string) (string, string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return "", "", errors.New("no target address: name the URL the branched site will be served at")
	}
	u, err := url.Parse(to)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", "", fmt.Errorf("the target address %q is not an http(s) URL with a host", to)
	}
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", "", fmt.Errorf("the target address %q must be a site root, with no path, query or credentials", to)
	}
	base := u.Scheme + "://" + u.Host
	return base, polisurl.ExtractDomain(base), nil
}

// refuseExistingOutput refuses to write over anything: an archive path that
// exists, or a directory that is not empty.
func refuseExistingOutput(target string, archive bool) error {
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check the output %s: %w", target, err)
	}
	if archive || !info.IsDir() {
		return fmt.Errorf("%w: %s", ErrOutputExists, target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return fmt.Errorf("check the output %s: %w", target, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%w and is not empty: %s", ErrOutputExists, target)
	}
	return nil
}

// within reports whether p is inside (or is) dir.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// openSource returns the root of the export: the directory itself, or the
// archive unpacked into stage. ⛔ The source is only ever read.
func openSource(src, stage string) (string, error) {
	if src == "" {
		return "", errors.New("no export: name the export archive or directory to branch")
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("read the export: %w", err)
	}
	root := abs
	if !info.IsDir() {
		if err := unzip(abs, stage); err != nil {
			return "", fmt.Errorf("unpack the export %s: %w", src, err)
		}
		root = stage
	}
	return siteRoot(root)
}

// siteRoot finds the site in an export: its root, or the one directory
// directly inside it that holds .well-known/polis (an archive made of a
// folder).
func siteRoot(dir string) (string, error) {
	if fileExists(filepath.Join(dir, ".well-known", "polis")) {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() && fileExists(filepath.Join(dir, e.Name(), ".well-known", "polis")) {
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", errors.New("the export is not a polis site: it holds no .well-known/polis")
}

// checkSource refuses an export with no private key, or whose key is not the
// one its .well-known/polis publishes. ⛔ The messages name files, never key
// material.
func checkSource(ctx context.Context, in string) error {
	s := storage.At(in)
	priv, err := s.Store().Get(ctx, site.PrivateKeyKey)
	if errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("%w (%s): without it the handover to a new key cannot be signed, "+
			"and the result would be a mirror of the content, not a branch of the site", ErrNoPrivateKey, site.PrivateKeyKey)
	}
	if err != nil {
		return fmt.Errorf("read the export's private key: %w", err)
	}
	pub, err := signing.PublicKeyFromPrivate(priv)
	if err != nil {
		return fmt.Errorf("%w: %s does not parse as a private key", ErrKeyNotHead, site.PrivateKeyKey)
	}
	wk, err := site.LoadWellKnown(s)
	if err != nil {
		return fmt.Errorf("read the export's .well-known/polis: %w", err)
	}
	if !site.SameSSHKey(string(pub), wk.PublicKey) {
		return fmt.Errorf("%w. A handover signed by any other key would break the site's key history for good. "+
			"Export the site again, or run 'polis validate' on the export to see which key it publishes", ErrKeyNotHead)
	}
	return nil
}

// originDomain is --from-domain, or the host the export's did.json names.
func originDomain(in, flag string) (string, error) {
	if d := strings.TrimSpace(flag); d != "" {
		return polisurl.ExtractDomain(d), nil
	}
	doc, err := storage.At(in).Store().Get(context.Background(), site.DIDDocumentKey)
	if err == nil {
		host := sitecheck.DomainFromDIDDocument(doc)
		// did:web encodes a port's colon as %3A.
		host = strings.ReplaceAll(strings.ReplaceAll(host, "%3A", ":"), "%3a", ":")
		if host != "" {
			return host, nil
		}
	}
	return "", fmt.Errorf("%w: the export has no .well-known/did.json naming it, and the domain goes inside the "+
		"handover signature. Pass --from-domain with the address the site is served at", ErrNoOriginDomain)
}

// presentResidue lists the residue items the export holds.
func presentResidue(in string) []string {
	var got []string
	for _, r := range Residue {
		if r.Key == ".polis/ds" {
			// Present on any site that ever registered; only config/ under
			// it is residue, and that is never the only residue a site has.
			continue
		}
		if _, err := os.Stat(filepath.Join(in, filepath.FromSlash(r.Key))); err == nil {
			got = append(got, r.Key)
		}
	}
	return got
}

func describeResidue() string {
	var b strings.Builder
	for _, r := range Residue {
		fmt.Fprintf(&b, "  - %s (%s): %s\n", r.What, r.Key, r.Default)
	}
	return b.String()
}

// The copy rules. Paths are slash-separated, relative to the site root.
var (
	// Only these two key files cross; the exported private key is needed in
	// the output to sign the handover, and the rotation removes it.
	keepKeys = map[string]bool{site.PrivateKeyKey: true, site.PublicKeyKey: true}

	// What never crosses, by prefix, and the kind it is reported as.
	leaveBehind = []struct{ prefix, kind string }{
		{dm.DMDirKey + "/", "DM keyring and conversations"},
		{".polis/dm/", "DM keyring and conversations"},
		{".polis/logs/", "logs"},
		{"logs/", "logs"},
		{".polis/owner/", "the origin instance's owner sign-in enrolments"},
		{".polis/deploy/", "the origin instance's deploy credentials"},
		// ⛔ Credentials the origin's owner gave to OTHER parties stay behind:
		// a key minted for an agent or integration was granted for the origin,
		// never for a site that does not exist yet, and the v0 key is a third
		// party's credential the owner did not choose to move. The MCP call log
		// is the origin's history of what those keys did.
		{".polis/api-keys.json", "API keys the origin's owner issued"},
		{".polis/kay/", "the origin's AI connection (its v0 key)"},
		{".polis/mcp/", "the origin's record of API-key calls"},
		{site.InitPendingKey, "an unfinished init marker"},
	}
)

// copySite copies the export into out, minus what never crosses, and returns
// what it left behind by kind. withResidue false leaves the residue too.
//
// ⛔ Regular files only: a symlink in an export could point anywhere,
// including at another site's key.
func copySite(in, out string, withResidue bool) ([]string, error) {
	left := map[string]bool{}
	// NOT the storage interface for the walk: the export is a directory on
	// this machine that is not (yet) a site we own, and the copy must see
	// every file in it, including the ones it refuses.
	err := filepath.WalkDir(in, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(in, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		key := filepath.ToSlash(rel)
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			left["links and other non-regular files"] = true
			return nil
		}
		if kind, skip := leftBehind(key, withResidue); skip {
			left[kind] = true
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s from the export: %w", key, err)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if strings.HasPrefix(key, ".polis/") {
			mode &^= 0o077
		}
		if err := storage.At(out).Store().Put(context.Background(), key, data, storage.WithMode(mode)); err != nil {
			return fmt.Errorf("write %s: %w", key, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	kinds := make([]string, 0, len(left))
	for k := range left {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds, nil
}

// leftBehind says whether key stays behind, and as what.
func leftBehind(key string, withResidue bool) (string, bool) {
	if strings.HasPrefix(key, ".polis/keys/") && !keepKeys[key] {
		return "retired and staged keys", true
	}
	for _, l := range leaveBehind {
		if key == l.prefix || strings.HasPrefix(key, l.prefix) {
			return l.kind, true
		}
	}
	// The origin's discovery-service state: its registration, and everything
	// derived (state/, cursors). Only the user's preferences (config/) cross.
	if strings.HasPrefix(key, ".polis/ds/") {
		parts := strings.Split(key, "/")
		if len(parts) < 5 || parts[4] != "config" {
			return "the origin's discovery-service registration and state", true
		}
		if !withResidue {
			return "private residue", true
		}
		return "", false
	}
	if !withResidue {
		for _, r := range Residue {
			if key == r.Key || strings.HasPrefix(key, r.Key+"/") {
				return "private residue", true
			}
		}
	}
	return "", false
}

// ensurePublicKeyFile writes the public half beside the private key when the
// export lacks it. It is derived from the private key, so it states nothing new.
func ensurePublicKeyFile(ctx context.Context, s storage.Site) error {
	if ok, err := s.Store().Exists(ctx, site.PublicKeyKey); err != nil || ok {
		return err
	}
	priv, err := s.Store().Get(ctx, site.PrivateKeyKey)
	if err != nil {
		return err
	}
	pub, err := signing.PublicKeyFromPrivate(priv)
	if err != nil {
		return err
	}
	return s.Store().Put(ctx, site.PublicKeyKey, pub, storage.WithMode(0o644))
}

// writeResidueDefaults gives the new site the defaults init would, for the
// residue files it needs and does not have.
func writeResidueDefaults(ctx context.Context, s storage.Site) error {
	defaults := []struct {
		key  string
		body string
	}{
		{policy.PrivateRulesKey, policy.DefaultPrivatePolicyContent()},
		{".polis/webapp/config.json", "{\n  \"show_frontmatter\": false\n}\n"},
	}
	for _, d := range defaults {
		ok, err := s.Store().Exists(ctx, d.key)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		if err := s.Store().Put(ctx, d.key, []byte(d.body), storage.WithMode(0o600)); err != nil {
			return fmt.Errorf("write the default %s: %w", d.key, err)
		}
	}
	return nil
}

// setBaseURL writes POLIS_BASE_URL into the output's .env, keeping every other
// line the export carried.
func setBaseURL(ctx context.Context, s storage.Site, baseURL string) error {
	const key = ".env"
	data, err := s.Store().Get(ctx, key)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("read .env: %w", err)
	}
	line := "POLIS_BASE_URL=" + baseURL
	var lines []string
	found := false
	if len(data) > 0 {
		for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "POLIS_BASE_URL=") || strings.HasPrefix(t, "export POLIS_BASE_URL=") {
				if found {
					continue
				}
				l, found = line, true
			}
			lines = append(lines, l)
		}
	} else {
		lines = []string{"# polis configuration"}
	}
	if !found {
		lines = append(lines, line)
	}
	return s.Store().Put(ctx, key, []byte(strings.Join(lines, "\n")+"\n"), storage.WithMode(0o644))
}

// renderSite regenerates every rendered page for the new address, through the
// same renderer `polis render` uses, offline.
func renderSite(s storage.Site, dir, baseURL, themesDir string) (*render.RenderStats, error) {
	if err := bundle.EnsureReferencePayload(s, "pub.polis.core"); err != nil {
		return nil, fmt.Errorf("install the bundle payload: %w", err)
	}
	core, err := bundle.LoadBundle(filepath.Join(dir, "content", "pub.polis.core", "bundle.json"))
	if err != nil {
		core = bundle.DefaultCoreBundle()
	}
	postsSource, _ := core.ContentDir("pub.polis.post")
	postsMount, _ := core.MountDir("pub.polis.post")
	commentsSource, _ := core.ContentDir("pub.polis.comment")
	commentsMount, _ := core.MountDir("pub.polis.comment")
	r, err := render.NewPageRenderer(render.PageConfig{
		Site:              s,
		CLIThemesDir:      themesDir,
		BaseURL:           baseURL,
		PostsSourceDir:    postsSource,
		PostsMountDir:     postsMount,
		CommentsSourceDir: commentsSource,
		CommentsMountDir:  commentsMount,
		Offline:           true,
	})
	if err != nil {
		return nil, fmt.Errorf("create the renderer: %w", err)
	}
	stats, err := r.RenderAll(true)
	if err != nil {
		return nil, fmt.Errorf("render the site for its new address: %w", err)
	}
	return stats, nil
}

// stagingPrefix names a branch's staging directory, beside the output.
const stagingPrefix = ".polis-branch-"

// staleStagingAge is how old a staging directory must be before a later run
// treats it as abandoned. ⭐ Far longer than any branch takes, so a run in
// progress beside this one is never touched.
const staleStagingAge = time.Hour

// removeStaleStaging removes abandoned staging directories in dir.
func removeStaleStaging(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, stagingPrefix+"*"))
	for _, m := range matches {
		if info, err := os.Lstat(m); err == nil && info.IsDir() && time.Since(info.ModTime()) > staleStagingAge {
			_ = os.RemoveAll(m)
		}
	}
}

// removeOnInterrupt removes dir and exits if the process is interrupted
// before the returned stop is called.
func removeOnInterrupt(dir string) (stop func()) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigs:
			_ = os.RemoveAll(dir)
			fmt.Fprintln(os.Stderr, "interrupted: removed the staging directory, which held the keys")
			os.Exit(130)
		case <-done:
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(sigs)
			close(done)
		})
	}
}

// maxExportBytes caps what unzip writes: an export is a site, not a disk.
var maxExportBytes int64 = 1 << 30 // a var so a test can lower it

// unzip unpacks an export archive into dir. ⛔ Every entry must land inside
// dir: an absolute name, a `..` segment or a link is refused, not skipped,
// because an export that tries either is not one to branch.
func unzip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	// ⛔ Counted from the bytes actually written, never the archive's own
	// size fields, which a crafted archive can state as anything.
	var written int64
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		clean := path.Clean("/" + name)[1:]
		if name == "" || strings.HasPrefix(name, "/") || clean != strings.TrimSuffix(name, "/") || strings.HasPrefix(clean, "..") {
			return fmt.Errorf("the archive entry %q would land outside the export", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return fmt.Errorf("the archive entry %q is not a regular file", f.Name)
		}
		dst := filepath.Join(dir, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			rc.Close()
			return err
		}
		n, cerr := io.Copy(w, io.LimitReader(rc, maxExportBytes-written+1))
		rc.Close()
		written += n
		if cerr == nil && written > maxExportBytes {
			cerr = fmt.Errorf("the export unpacks to more than %d bytes; refusing it", maxExportBytes)
		}
		if err := w.Close(); cerr == nil {
			cerr = err
		}
		if cerr != nil {
			return cerr
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		if err := os.Chmod(dst, mode); err != nil {
			return err
		}
	}
	return nil
}

// writeArchive zips dir into a new file at target, through a temporary file
// beside it, so a failure leaves nothing at target.
func writeArchive(dir, target string) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".polis-branch-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	zw := zip.NewWriter(tmp)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		h.Method = zip.Deflate
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
	if err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err != nil {
		return fmt.Errorf("write the archive: %w", err)
	}
	if err := os.Link(tmp.Name(), target); err != nil {
		return fmt.Errorf("write the archive: %w", err)
	}
	return nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
