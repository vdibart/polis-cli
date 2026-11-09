package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/did"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// InitOptions contains options for initializing a new polis site.
type InitOptions struct {
	SiteTitle string // Optional site title
	Author    string // Optional author name (falls back to git config user.name)
	Email     string // Optional email address — private by default, only written if explicitly provided
	Theme     string // Optional initial theme (default: empty, selected randomly on first render)
	Generator string // e.g. "polis-cli-go/0.59.0" — used in metadata; falls back to package default if empty

	// License is the licence profile to state at init: "reserved", "open", or
	// "none". Empty means the caller has not asked, and NOTHING IS WRITTEN —
	// no licence file, no pointer, no robots.txt.
	//
	// That silence is deliberate and it is the conservative option. A
	// license.json is a signed statement of the author's INTENT; creating one
	// the author never made would be authoring on their behalf, which is worse
	// than the operator-signing cases already ruled out. Absent = unstated is a
	// defined, honest state: they have not said.
	//
	// A default nobody saw is not consent, so the caller is responsible for
	// having asked (interactive prompt) or for surfacing the choice prominently
	// afterwards (hosted signup).
	License string

	// BaseURL is the site's public URL. Only used to build the licence
	// payload's pointers, which must live on the author's own domain. When
	// empty the licence is still written, without the terms/contact URLs.
	BaseURL string

	// AdoptExistingKey initialises the site AROUND the identity key already in
	// its store, instead of generating one. ⭐ For an instance that made its own
	// key on first start (EnsureIdentityKey), before any site existed.
	//
	// ⛔ The CLI never sets it: `polis init` keeps refusing a site that has a
	// key. When set:
	//   - a missing or unparsable key is an error, never a generation;
	//   - BaseURL is required, because a site with no domain cannot say whose
	//     it is;
	//   - the first write is a CONDITIONAL create of .well-known/polis, which
	//     is the claim: of two starts racing, one initialises and the other
	//     gets ErrAlreadyInitialised having written nothing;
	//   - InitPendingKey is written before the claim and removed after the
	//     last write, so a start that finds the claim beside a marker older
	//     than resumeAfter FINISHES the site (each write idempotent, nothing
	//     authored overwritten) instead of leaving it half-built for good;
	//   - a site that already holds some of init's output but no
	//     .well-known/polis gets ErrPartialSite, and nothing is written;
	//   - git is never asked for an author: an instance has no user of its
	//     own, and on a laptop it would put the operator's name on the site.
	//     The title falls back to BaseURL's host.
	AdoptExistingKey bool
}

// ErrAlreadyInitialised: an adopting init found .well-known/polis already
// there, or lost the race to create it. Nothing was written.
var ErrAlreadyInitialised = errors.New("site: already initialised")

// ErrInitInProgress: an adopting init found the claim and a pending marker
// younger than resumeAfter: another start is writing the site, or stopped
// moments ago. Nothing was written; a start after resumeAfter finishes it.
//
// It wraps ErrAlreadyInitialised: to a caller that only asks "may I write the
// site?", the answer is the same.
var ErrInitInProgress = fmt.Errorf("%w; another start is still writing it", ErrAlreadyInitialised)

// InitPendingKey marks an adopting init that has started and not finished. It
// is private, holds the time it was written, and is removed last.
const InitPendingKey = ".polis/init-pending"

// resumeAfter is how old a pending marker must be before another start
// finishes the site. ⭐ Longer than any one start can run (a function's
// maximum duration), so a finisher never works beside the start that claimed
// the site; if one did, every write is idempotent anyway.
var resumeAfter = 15 * time.Minute

// initNow is the clock the pending marker is read against (tests replace it).
var initNow = time.Now

// ErrPartialSite: an adopting init found some of what init writes but no
// .well-known/polis — an init that stopped part way. Nothing was written;
// finishing it is a resume, not a second init.
var ErrPartialSite = errors.New("site: partly initialised")

// partialSiteKeys are init outputs whose presence, without .well-known/polis,
// means an earlier init stopped part way. ⚠️ .env is not one: the server
// writes it on start.
var partialSiteKeys = []string{
	"content/pub.polis.core/bundle.json",
	bundle.RegistryKey,
	".polis/storage-salt",
}

// adoptKey reads the identity key an adopting init builds the site around,
// after checking the site is one it may initialise. resume says the site was
// claimed by a start that never finished, and this one finishes it.
func adoptKey(site storage.Site, opts InitOptions) (privKey, pubKey []byte, resume bool, err error) {
	ctx := context.Background()
	if polisurl.ExtractDomain(opts.BaseURL) == "" {
		return nil, nil, false, errors.New("no base URL: a site initialised around an existing key needs its domain")
	}
	st := site.Store()
	present, err := st.Exists(ctx, WellKnownKey)
	if err != nil {
		return nil, nil, false, fmt.Errorf("could not check whether .well-known/polis exists — refusing to initialise: %w", err)
	}
	if present {
		marker, err := st.Get(ctx, InitPendingKey)
		if errors.Is(err, storage.ErrNotFound) {
			return nil, nil, false, ErrAlreadyInitialised
		}
		if err != nil {
			return nil, nil, false, fmt.Errorf("could not check whether the site's initialisation finished: %w", err)
		}
		// ⚠️ An unreadable time counts as old: a marker nobody can date is not
		// evidence that a start is still writing.
		if at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(marker))); err == nil && initNow().Sub(at) < resumeAfter {
			return nil, nil, false, ErrInitInProgress
		}
		resume = true
	} else {
		if err := refusePartial(st); err != nil {
			return nil, nil, false, err
		}
	}
	privKey, err = st.Get(ctx, privateKeyKey)
	if err != nil {
		return nil, nil, false, fmt.Errorf("read the identity key to initialise around: %w", err)
	}
	pubKey, err = signing.PublicKeyFromPrivate(privKey)
	if err != nil {
		// ⛔ Name the key, never its bytes.
		return nil, nil, false, fmt.Errorf("identity key %s does not parse: %w", privateKeyKey, err)
	}
	return privKey, pubKey, resume, nil
}

// refusePartial returns ErrPartialSite when init's later outputs are present
// with no .well-known/polis: an init from before the claim came first.
func refusePartial(st storage.Store) error {
	ctx := context.Background()
	for _, key := range partialSiteKeys {
		some, err := st.Exists(ctx, key)
		if err != nil {
			return fmt.Errorf("could not check whether %s exists — refusing to initialise: %w", key, err)
		}
		if !some {
			continue
		}
		// ⭐ Init writes these only after its claim, so seeing one may mean
		// another start claimed the site a moment ago, not that a past one
		// stopped. Look again before calling it partial.
		if again, err := st.Exists(ctx, WellKnownKey); err == nil && again {
			return ErrAlreadyInitialised
		}
		return fmt.Errorf("%w: %s is present but .well-known/polis is not", ErrPartialSite, key)
	}
	return nil
}

// InitResult contains the result of site initialization.
type InitResult struct {
	Success      bool     `json:"success"`
	SiteDir      string   `json:"site_dir"`
	PublicKey    string   `json:"public_key"`
	DirsCreated  []string `json:"directories_created,omitempty"`
	FilesCreated []string `json:"files_created,omitempty"`
	// License is the profile actually stated, or "" if none was. Surfaced so
	// callers can SHOW what was chosen rather than merely recording it.
	License string `json:"license,omitempty"`
	// DID is the did:web identifier published for this site, or "" when the
	// site's canonical host was not known at init (no BaseURL) and the
	// document could not be built. Surfaced rather than swallowed so a
	// missing DID is observable at the moment it fails to appear instead of
	// being discovered later by a resolver.
	DID string `json:"did,omitempty"`
	// Resumed says this init finished a site an earlier start claimed and
	// left part-written, rather than creating it.
	Resumed  bool `json:"resumed,omitempty"`
	KeyPaths struct {
		Private string `json:"private"`
		Public  string `json:"public"`
	} `json:"key_paths"`
	Error string `json:"error,omitempty"`
}

// absentKey reports whether a key is absent.
//
// ⚠️ It discards nothing the old code kept: `if absent, _ := storage.PathAbsent(p)`
// was the shape at every one of these sites, so a key we cannot see is one we
// treat as present and leave alone. ⛔ That is why a failed check answers
// FALSE: the callers ignore the error, and "absent" is what makes them write.
func absentKey(site storage.Site, key string) (bool, error) {
	present, err := site.Store().Exists(context.Background(), key)
	if err != nil {
		return false, err
	}
	return !present, nil
}

// whereKey names a key for a message: its real path on a filesystem, exactly
// as these messages have always said, and the key itself on a store that has
// no path to name.
func whereKey(site storage.Site, key string) string {
	if st := site.Store(); st.Capabilities().RealPath {
		if p, err := st.RealPath(key); err == nil {
			return p
		}
	}
	return key
}

// refuseIfPresent refuses init when an identity file is already there.
//
// ⛔ IT ASKS THE SITE'S STORE, never a local path. On a store with no
// filesystem a local path holds nothing, so a path check would answer "absent"
// for a site that has an identity and let init overwrite it. ⛔ And a check
// that FAILS refuses too: "could not look" is not "nothing there", and the
// cost of guessing wrong is a destroyed identity.
func refuseIfPresent(site storage.Site, key, what string) error {
	present, err := site.Store().Exists(context.Background(), key)
	if err != nil {
		return fmt.Errorf("could not check whether %s exists at %s — refusing to overwrite: %w", what, whereKey(site, key), err)
	}
	if present {
		return fmt.Errorf("%s already exists at %s — refusing to overwrite", what, whereKey(site, key))
	}
	return nil
}

// Identity files Init refuses to overwrite, by key.
const (
	privateKeyKey = ".polis/keys/id_ed25519"
	publicKeyKey  = ".polis/keys/id_ed25519.pub"
)

// initDirKeys is the directory skeleton a new site gets on a filesystem, by
// key. "" is the site root.
var initDirKeys = []string{
	"",
	// Identity
	".well-known",
	// Private state
	".polis",
	".polis/keys",
	".polis/logs",
	".polis/policies",
	".polis/webapp",
	".polis/webapp/hooks",
	// Private content state (pub.polis.core)
	".polis/bundles/pub.polis.core/posts/drafts",
	".polis/bundles/pub.polis.core/comments/drafts",
	".polis/bundles/pub.polis.core/comments/pending",
	".polis/bundles/pub.polis.core/comments/denied",
	// DM storage (encrypted conversations)
	".polis/bundles/pub.polis.core/dm/conversations",
	// Site resources
	"site/snippets",
	// Public content (pub.polis.core)
	"content/pub.polis.core/post",
	"content/pub.polis.core/comment",
	"content/pub.polis.core/follow",
	"content/pub.polis.core/tag",
	"content/pub.polis.core/license",
	// Policies
	"policies",
}

// Init creates a new polis site with the bundle-based layout.
// This function has no webapp dependencies and can be used by CLI tools.
// It will NOT overwrite existing keys or .well-known/polis — safety feature.
func Init(site storage.Site, opts InitOptions) (*InitResult, error) {
	gen := opts.Generator
	if gen == "" {
		gen = GetGenerator()
	}

	result := &InitResult{
		Success: false,
		SiteDir: site.Dir(),
	}

	// SAFETY: Check if keys already exist — refuse to overwrite. An adopting
	// init is the one caller that expects a key, and it checks the rest.
	var privKey, pubKey []byte
	var resume bool
	if opts.AdoptExistingKey {
		var err error
		if privKey, pubKey, resume, err = adoptKey(site, opts); err != nil {
			return nil, err
		}
		result.Resumed = resume
	} else {
		if err := refuseIfPresent(site, privateKeyKey, "private key"); err != nil {
			return nil, err
		}
		if err := refuseIfPresent(site, publicKeyKey, "public key"); err != nil {
			return nil, err
		}
		if err := refuseIfPresent(site, WellKnownKey, ".well-known/polis"); err != nil {
			return nil, err
		}
	}

	// Create directory structure.
	//
	// ⛔ PERMISSION RULE, and NOT the storage interface: this loop is the
	// authority on the 0700-under-.polis split, and it creates directories
	// that are legitimately empty on a new site. Filesystem only, on the real
	// path behind the store's capability: an object store has no directories
	// to create and no modes to set, and a site there starts by writing keys,
	// each of which brings its own parents.
	var dirsCreated []string
	if st := site.Store(); st.Capabilities().RealPath {
		for _, key := range initDirKeys {
			dir, err := st.RealPath(key)
			if err != nil {
				return nil, fmt.Errorf("failed to create directory %s: %w", key, err)
			}
			// .polis directory and its children use restrictive permissions (private data)
			perm := os.FileMode(0755)
			if key == ".polis" || strings.HasPrefix(key, ".polis/") {
				perm = 0700
			}
			if err := os.MkdirAll(dir, perm); err != nil {
				return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
			if key != "" {
				dirsCreated = append(dirsCreated, filepath.FromSlash(key))
			}
		}
	}
	result.DirsCreated = dirsCreated

	if !opts.AdoptExistingKey {
		// Generate keypair
		var err error
		if privKey, pubKey, err = signing.GenerateKeypair(); err != nil {
			return nil, fmt.Errorf("failed to generate keypair: %w", err)
		}

		if err := site.Store().Put(context.Background(), privateKeyKey, privKey, storage.WithMode(0600)); err != nil {
			return nil, fmt.Errorf("failed to write private key: %w", err)
		}
		if err := site.Store().Put(context.Background(), publicKeyKey, pubKey); err != nil {
			return nil, fmt.Errorf("failed to write public key: %w", err)
		}

		// Create storage salt for DM encryption. ⚠️ An adopting init makes it
		// after its claim, so two racing starts cannot each write one.
		if err := dm.EnsureSalt(site); err != nil {
			return nil, fmt.Errorf("failed to create storage salt: %w", err)
		}
	}

	result.KeyPaths.Private = privateKeyKey
	result.KeyPaths.Public = publicKeyKey

	// Create .well-known/polis
	setupTime := time.Now().UTC()
	author := opts.Author
	if author == "" && !opts.AdoptExistingKey {
		author = getGitConfig("user.name")
	}

	// Default site title to author name when not supplied — avoids
	// rendered <title></title> + leading-space description artifacts
	// on fresh tenants where the operator didn't set --site-title. With no
	// author either (an instance initialising itself), the domain.
	siteTitle := opts.SiteTitle
	if siteTitle == "" {
		siteTitle = author
	}
	if siteTitle == "" {
		siteTitle = polisurl.ExtractDomain(opts.BaseURL)
	}

	wk := &WellKnown{
		Version:    gen,
		AuthorName: author,
		Email:      opts.Email,
		PublicKey:  strings.TrimSpace(string(pubKey)),
		SiteTitle:  siteTitle,
		Created:    setupTime.Format(time.RFC3339),
		// active_theme intentionally not set here — moved to
		// .polis/bundles/registry.json (step-01/1e).
		Bundles: map[string]BundleEntry{
			"pub.polis.core": {
				Path: "content/pub.polis.core/bundle.json",
			},
		},
	}

	wk.Avatar = GenerateDefaultAvatar()

	if opts.AdoptExistingKey {
		if !resume {
			// ⭐ The pending marker goes down BEFORE the claim, so no claim
			// ever exists without one until the site is complete. A start that
			// then loses the race leaves a marker behind only if the winner
			// already finished; the next start finishes a complete site, which
			// writes nothing that matters.
			if err := site.Store().Put(context.Background(), InitPendingKey,
				[]byte(initNow().UTC().Format(time.RFC3339)+"\n"), storage.WithMode(0o600)); err != nil {
				return nil, fmt.Errorf("failed to mark the initialisation as started: %w", err)
			}
			// ⛔⛔ THE CLAIM. A conditional create, before anything else is
			// written: the start that loses has written nothing else, so two
			// cold starts cannot write two creation times, avatars or themes.
			data, err := json.MarshalIndent(wk, "", "  ")
			if err != nil {
				return nil, err
			}
			err = site.Store().Put(context.Background(), WellKnownKey, append(data, '\n'), storage.IfAbsent(), storage.WithMode(0644))
			if errors.Is(err, storage.ErrExists) {
				return nil, ErrAlreadyInitialised
			}
			if err != nil {
				return nil, fmt.Errorf("failed to create .well-known/polis: %w", err)
			}
		}
		// ⛔ From here every write is idempotent, because a resume runs it all
		// again over whatever the claiming start managed to write.
		if err := dm.EnsureSalt(site); err != nil {
			return nil, fmt.Errorf("failed to create storage salt: %w", err)
		}
	} else if err := SaveWellKnown(site, wk); err != nil {
		return nil, fmt.Errorf("failed to create .well-known/polis: %w", err)
	}

	// Publish the genesis entry of this key's history.
	//
	// A site that publishes only its CURRENT key stops being able to verify its
	// own past the moment it rotates: everything signed under the old key fails
	// against what the site publishes. So the chain starts here, at epoch 0, and
	// every rotation appends to it.
	//
	// ⭐ NO DS CONSULT IS NEEDED HERE, unlike Medic's provisioning for existing
	// tenants. Medic has to ask because it cannot prove from the site alone that
	// the key it finds is the FIRST key. A key generated three lines above has no
	// history to contradict, so the assumption is not an assumption.
	//
	// The entry says nothing the document does not already say — public_key and
	// created, restated as a chain — which is what makes it derived rather than
	// authored, and safe to write unasked.
	if _, err := WriteGenesisKeyHistory(site); err != nil {
		return nil, fmt.Errorf("failed to publish the genesis key-history entry: %w", err)
	}

	// Provision the DM keyring (bootstrap epoch) and publish its signed
	// public_key_messages block into .well-known/polis, so the site can receive
	// encrypted DMs from message #1. Medic performs the same on existing tenants.
	if err := ProvisionAndPublishMessagesKey(site, privKey); err != nil {
		return nil, fmt.Errorf("failed to provision DM keys: %w", err)
	}

	// Generate favicon from avatar config
	if err := WriteFavicon(site); err != nil {
		return nil, fmt.Errorf("failed to create favicon.svg: %w", err)
	}

	var filesCreated []string

	// Publish the same identity key a second time, as a did:web DID Document.
	//
	// Derived, not authored: it re-encodes the public_key written just above
	// and says nothing the site is not already saying, which is why it can be
	// written without asking anyone. Only the canonical host is new
	// information, and it comes from the caller — a site initialised without
	// one gets no document, because a DID whose id names the wrong host does
	// not resolve and is worse than none. Medic and Tailor publish it later,
	// once the host is known.
	//
	// Not fatal: this is a projection of an identity that already exists on
	// disk, so a failure here must not take down site creation (hosted signup
	// runs through this path). The result carries what happened.
	if host := polisurl.ExtractDomain(opts.BaseURL); host != "" {
		if err := PublishDIDDocument(site, host); err == nil {
			result.DID = did.ID(host)
			filesCreated = append(filesCreated, ".well-known/did.json")
		}
	}

	// Create bundle.json
	coreBundle := bundle.DefaultCoreBundle()
	if err := bundle.SaveBundleAt(site, "content/pub.polis.core/bundle.json", coreBundle); err != nil {
		return nil, fmt.Errorf("failed to create bundle.json: %w", err)
	}
	filesCreated = append(filesCreated, "content/pub.polis.core/bundle.json")

	// Install the reference bundle payload (shapes, themes) into .polis/bundles/.
	// This makes a brand-new site immediately renderable without any background
	// actors (Patrol/Medic/Tailor). EnsureReferencePayload also writes
	// registry.json with per-shape/per-theme version stamps that drive
	// Patrol's NeedsRefresh check.
	if err := bundle.EnsureReferencePayload(site, "pub.polis.core"); err != nil {
		return nil, fmt.Errorf("failed to install bundle reference payload: %w", err)
	}
	filesCreated = append(filesCreated, ".polis/bundles/pub.polis.core/")
	filesCreated = append(filesCreated, ".polis/bundles/registry.json")

	// Set active theme on the registry that EnsureReferencePayload just stamped.
	// Both branches use load+modify+save (via bundle.SetActiveThemeName /
	// theme.SelectRandomTheme → SetActiveTheme → bundle.SetActiveThemeName)
	// so the version stamps are preserved.
	if opts.Theme != "" {
		if err := bundle.SetActiveThemeName(site, opts.Theme); err != nil {
			return nil, fmt.Errorf("failed to set active theme: %w", err)
		}
	} else if reg, err := bundle.LoadRegistry(site); err == nil && reg.ActiveTheme != "" {
		// A resumed init keeps the theme the claiming start chose.
	} else {
		// No theme requested — pick a random one from the installed bundle and
		// persist it so the registry is fully initialized after init (rather
		// than half-initialized until first render).
		// ⚠️ A choice that could not be saved stays non-fatal here, as it
		// always was: render picks the same stable theme until it is saved.
		if _, err := theme.SelectRandomTheme(site, ""); err != nil && !errors.Is(err, theme.ErrThemeNotSaved) {
			return nil, fmt.Errorf("failed to select default theme: %w", err)
		}
	}

	// Create content files
	if err := initContentFiles(site, &filesCreated, gen); err != nil {
		return nil, fmt.Errorf("failed to create content files: %w", err)
	}

	// State the licence, if and only if the caller has actually chosen one.
	if opts.License != "" {
		stated, err := StateLicense(site, opts.License, opts.BaseURL, privKey)
		if err != nil {
			return nil, fmt.Errorf("failed to state licence: %w", err)
		}
		if stated {
			filesCreated = append(filesCreated, "content/pub.polis.core/license/license.json")
			result.License = opts.License
		}
	}

	// Create default about snippet
	aboutKey := "site/snippets/about.md"
	if absent, _ := absentKey(site, aboutKey); absent {
		aboutContent := "Hi \u2014 I'm just getting set up here. A real about page is coming soon.\n\nThis site runs on *polis*: signed markdown on my own domain. No platform, no middleman.\n"
		if err := site.Store().Put(context.Background(), aboutKey, []byte(aboutContent)); err != nil {
			return nil, fmt.Errorf("failed to create default about snippet: %w", err)
		}
		filesCreated = append(filesCreated, "site/snippets/about.md")
	}

	// Create default public policy file (DM rules + blessing emit rules + catch-all deny)
	policyKey := "policies/rules.jsonl"
	if absent, _ := absentKey(site, policyKey); absent {
		// PERMISSION RULE: the public policy is 0644, stated here rather than
		// left to the store's default. Its parent is created at 0755 by the
		// skeleton above, or by the store, which creates public parents 0755.
		if err := site.Store().Put(context.Background(), policyKey, []byte(policy.DefaultPublicPolicyContent()), storage.WithMode(0644)); err != nil {
			return nil, fmt.Errorf("failed to create default policy file: %w", err)
		}
		filesCreated = append(filesCreated, "policies/rules.jsonl")
	}

	// Create default private policy file (empty — overrides only)
	privatePolicyKey := ".polis/policies/rules.jsonl"
	if absent, _ := absentKey(site, privatePolicyKey); absent {
		// PERMISSION RULE: the private policy, 0600 under .polis.
		if err := site.Store().Put(context.Background(), privatePolicyKey, []byte(policy.DefaultPrivatePolicyContent()), storage.WithMode(0600)); err != nil {
			return nil, fmt.Errorf("failed to create default private policy file: %w", err)
		}
		filesCreated = append(filesCreated, ".polis/policies/rules.jsonl")
	}

	// Create webapp config
	webappConfigKey := ".polis/webapp/config.json"
	if absent, _ := absentKey(site, webappConfigKey); absent {
		webappConfig := map[string]interface{}{
			"setup_at":         setupTime.Format(time.RFC3339),
			"show_frontmatter": false,
		}
		data, _ := json.MarshalIndent(webappConfig, "", "  ")
		data = append(data, '\n')
		if err := site.Store().Put(context.Background(), webappConfigKey, data); err != nil {
			return nil, fmt.Errorf("failed to create webapp config: %w", err)
		}
		filesCreated = append(filesCreated, ".polis/webapp/config.json")
	}

	// Add well-known and key files to the list
	filesCreated = append(filesCreated, ".well-known/polis")
	filesCreated = append(filesCreated, result.KeyPaths.Private, result.KeyPaths.Public)
	result.FilesCreated = filesCreated

	// Create .gitignore — on a filesystem only. On an object store nothing
	// reads it, and a public prefix would serve it.
	if site.Store().Capabilities().RealPath {
		if err := initGitignore(site); err != nil {
			fmt.Printf("warning: failed to create .gitignore: %v\n", err)
		}
	}

	// ⭐ Last: until this, a later start may finish what this one began.
	if opts.AdoptExistingKey {
		if err := site.Store().Delete(context.Background(), InitPendingKey, storage.IgnoreMissing()); err != nil {
			return nil, fmt.Errorf("failed to mark the initialisation as finished: %w", err)
		}
	}

	result.Success = true
	result.PublicKey = strings.TrimSpace(string(pubKey))
	return result, nil
}

// EnsureDMPolicies appends default DM acceptance policies to the private rules file
// if no pub.polis.dm rules exist yet. This is idempotent — safe to call on every init.
func EnsureDMPolicies(privatePolicyPath string) error {
	dmRules := `{"active":true,"policy":"allow pub.polis.dm from following"}` + "\n" +
		`{"active":true,"policy":"deny pub.polis.dm from all"}` + "\n"

	// Check if DM rules already exist
	if data, err := storage.ReadAtPath(privatePolicyPath); err == nil {
		if strings.Contains(string(data), "pub.polis.dm") {
			return nil // DM rules already present
		}
	}

	// Ensure directory exists
	// PERMISSION RULE: 0700 because this is .polis, and the directory must be
	// created HERE rather than left to the append.
	//
	// ⛔ AppendAtPath reaches the file through a store rooted at the file's own
	// DIRECTORY (see storage.WriteAtPath), so any parent it creates gets that
	// store's default mode — 0755. ⚠️ Converting this site without the line
	// below would have made .polis/policies world-readable on a fresh init,
	// which is exactly the hazard storage.NewSite exists to prevent. The
	// conversion is still right; the MkdirAll is not redundant.
	if err := os.MkdirAll(filepath.Dir(privatePolicyPath), 0700); err != nil {
		return err
	}

	// ⭐ The DM rules are ADDED to whatever is already there, which is why this
	// was O_APPEND. It now goes through storage.AppendAtPath: one behaviour on
	// every backend, because an object store has no append to fall back to.
	return storage.AppendAtPath(privatePolicyPath, []byte(dmRules), storage.WithMode(0600))
}

// initContentFiles creates the initial content files for pub.polis.core.
func initContentFiles(site storage.Site, filesCreated *[]string, gen string) error {
	// Create empty content index
	indexKey := "content/pub.polis.core/index.jsonl"
	if absent, _ := absentKey(site, indexKey); absent {
		// Writes an EMPTY file deliberately, which the store handles the same way.
		if err := site.Store().Put(context.Background(), indexKey, []byte{}, storage.WithMode(0644)); err != nil {
			return err
		}
		*filesCreated = append(*filesCreated, "content/pub.polis.core/index.jsonl")
	}

	// Create following.json
	followingKey := "content/pub.polis.core/follow/following.json"
	if absent, _ := absentKey(site, followingKey); absent {
		following := map[string]interface{}{
			"version":   gen,
			"following": []interface{}{},
		}
		data, _ := json.MarshalIndent(following, "", "  ")
		data = append(data, '\n')
		if err := site.Store().Put(context.Background(), followingKey, data); err != nil {
			return err
		}
		*filesCreated = append(*filesCreated, "content/pub.polis.core/follow/following.json")
	}

	// Create blessed.json
	blessedKey := "content/pub.polis.core/comment/blessed.json"
	if absent, _ := absentKey(site, blessedKey); absent {
		blessed := map[string]interface{}{
			"version":  gen,
			"comments": []interface{}{},
		}
		data, _ := json.MarshalIndent(blessed, "", "  ")
		data = append(data, '\n')
		if err := site.Store().Put(context.Background(), blessedKey, data); err != nil {
			return err
		}
		*filesCreated = append(*filesCreated, "content/pub.polis.core/comment/blessed.json")
	}

	return nil
}

// initGitignore creates the .gitignore file for the new layout.
func initGitignore(site storage.Site) error {
	gitignoreKey := ".gitignore"
	if absent, _ := absentKey(site, gitignoreKey); absent {
		content := `# Polis internals and secrets
.polis/
.env*

# Binaries
/polis
/polis-server
/polis-full
`
		return site.Store().Put(context.Background(), gitignoreKey, []byte(content))
	}
	return nil
}

// getGitConfig retrieves a git config value.
// Returns empty string if git is not available or the config key is not set.
func getGitConfig(key string) string {
	cmd := exec.Command("git", "config", key)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
