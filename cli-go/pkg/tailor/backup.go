package tailor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐ BACKUPS ARE THE RUN'S JOB, NOT EACH CHECK'S.
//
// Before a check applies, the run asks the same check what it WOULD do (a dry
// run against the current state, which includes everything earlier checks just
// applied) and copies every existing path that plan rewrites or removes into
// the run's backup directory. Only then does the check apply. So a new check
// cannot forget its backup: it only has to name, in its Actions, the paths it
// touches — which it already does, because that is the dry-run output.
//
// Until this, the backup directory was created empty and four call sites copied
// anything into it, while recursive deletes of theme trees ran unbacked.

// backupManifestName is the record of everything one apply run did, written at
// the root of its backup directory so the run can be undone by hand.
const backupManifestName = "manifest.json"

// noBackupChecks write only output that is regenerated from sources Tailor does
// back up, or files that did not exist before. Backing up a full re-render
// would copy every rendered page of the site on every run.
var noBackupChecks = map[string]string{
	"render-site":    "rendered HTML is regenerated from the signed sources on every render",
	"dm-directories": "creates a directory or changes its mode; no file's bytes change",
}

// extraBackupPaths are files a check rewrites that its Actions do not name.
// ⚠️ The Actions are a published shape (golden testdata, Patrol baselines and
// operator tooling read them), so widening a Path there is a breaking change;
// the extra paths live here instead. A check that starts writing a new file
// adds it here.
var extraBackupPaths = map[string][]string{
	"bundle-reference-payload":    {".polis/bundles/registry.json"},
	"reference-payload-integrity": {".polis/bundles/registry.json"},
	"dm-keyring":                  {".well-known/polis"},
	"avatar-config":               {"favicon.svg"},
	// render-site writes registry.json only to pick a theme when none is
	// active, through the same field-preserving save; nothing is lost, and
	// backing it up would make every routine run leave a backup behind.
}

// narrowBackup replaces an action Path that names a whole tree with the parts
// of it the check can actually rewrite. ⛔ The core bundle's private tree also
// holds the DM keyring (whose bootstrap key is meant to be destroyed after its
// window), conversations and drafts; the payload checks only ever rewrite
// shapes/ and themes/, so only those are copied.
var narrowBackup = map[string][]string{
	".polis/bundles/pub.polis.core": {".polis/bundles/pub.polis.core/shapes", ".polis/bundles/pub.polis.core/themes"},
}

// secretPath reports whether a site-relative path is, or holds, key material:
// the identity keys, the DM tree (keyring and conversations), the storage
// salt. ⛔ A backup never copies one; a check whose plan names one is not
// applied (no check is meant to rewrite or remove them).
func secretPath(rel string) bool {
	rel = strings.TrimSuffix(filepath.ToSlash(rel), "/")
	for _, s := range []string{".polis/keys", ".polis/storage-salt", ".polis/bundles/pub.polis.core/dm", ".polis/bundles"} {
		if s == ".polis/bundles" {
			if rel == s {
				return true // the whole private bundle tree contains dm/
			}
			continue
		}
		if rel == s || strings.HasPrefix(rel, s+"/") || strings.HasPrefix(s, rel+"/") {
			return true
		}
	}
	return rel == ".polis"
}

// opsThatOverwrite are the Action ops whose Path (and, for migrate, Dest) may
// hold existing bytes the action changes or removes. "move" relocates a file
// intact and "flag" changes nothing, so neither needs a copy.
var opsThatOverwrite = map[string]bool{
	"update": true, "remove": true, "remove_key": true, "migrate": true, "create": true,
}

// ManifestEntry is one applied check in the backup manifest.
type ManifestEntry struct {
	Check    string   `json:"check"`
	Message  string   `json:"message"`
	Actions  []Action `json:"actions"`
	BackedUp []string `json:"backed_up,omitempty"` // site-relative paths copied before the check applied
}

// Manifest is written to <backup dir>/manifest.json at the end of an apply run.
type Manifest struct {
	Generator string          `json:"generator"`
	SiteDir   string          `json:"site_dir"`
	Started   string          `json:"started"`
	Applied   []ManifestEntry `json:"applied"`
}

// backupPathsFor returns the existing site-relative paths a planned result
// would rewrite or remove. A Path may list several paths separated by ", ".
func backupPathsFor(siteDir string, plan CheckResult) []string {
	if plan.Status != StatusFail {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	var add func(field string)
	add = func(field string) {
		// ⚠️ Multi-path actions join with exactly ", ". A bare "," can be part of
		// a file name (a cached comment's id is chosen remotely).
		for _, p := range strings.Split(field, ", ") {
			p = strings.TrimSuffix(filepath.ToSlash(p), "/")
			if narrowed, ok := narrowBackup[p]; ok {
				for _, q := range narrowed {
					add(q)
				}
				continue
			}
			if p == "" || seen[p] {
				continue
			}
			if _, err := storage.StatAtPath(filepath.Join(siteDir, filepath.FromSlash(p))); err != nil {
				continue // nothing there yet: a create, or already gone
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	if _, exempt := noBackupChecks[plan.Name]; !exempt {
		for _, a := range plan.Actions {
			if !opsThatOverwrite[a.Op] {
				continue
			}
			add(a.Path)
			if a.Op == "migrate" && a.Dest != "" {
				add(a.Dest)
			}
			if plan.Name == "blessed-cache-gc" {
				add(a.Path + ".meta.json") // removed with its body; may exist but not parse
			}
		}
	}
	for _, p := range extraBackupPaths[plan.Name] {
		add(p)
	}
	return out
}

// ensureBackupDir creates the run's backup directory on first use, so a run
// that changes nothing leaves nothing behind.
func (ctx *runContext) ensureBackupDir() error {
	if ctx.backupDir == "" {
		return errors.New("no backup directory for this run")
	}
	// NOT the storage interface: creates the backup root, 0700 because it is
	// under .polis and holds copies of private files.
	return os.MkdirAll(ctx.backupDir, 0o700)
}

// backupStore is the store every backup copy is written through: files 0600,
// directories 0700, whatever the original's mode, because the backup sits under
// .polis and may hold copies of private files.
func (ctx *runContext) backupStore() storage.Store {
	return storage.NewPOSIX(ctx.backupDir, storage.WithDirMode(0o700))
}

// backupPath copies one site-relative file or directory tree into the backup
// directory at the same relative path. A path already backed up in this run is
// not copied again: the first copy is the one that holds the original bytes.
func (ctx *runContext) backupPath(rel string) error {
	if ctx.backedUp[rel] {
		return nil
	}
	if secretPath(rel) {
		return fmt.Errorf("%s holds key material, which a backup never copies", rel)
	}
	if err := ctx.ensureBackupDir(); err != nil {
		return err
	}
	src := filepath.Join(ctx.siteDir, filepath.FromSlash(rel))
	info, err := storage.StatAtPath(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", rel, err)
	}
	store := ctx.backupStore()
	if !info.IsDir {
		data, err := storage.ReadAtPath(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		if err := store.Put(context.Background(), rel, data, storage.WithMode(0o600)); err != nil {
			return fmt.Errorf("back up %s: %w", rel, err)
		}
	} else {
		entries, err := storage.NewPOSIX(src).List(context.Background(), "", storage.Recursive())
		if err != nil {
			// ⛔ A tree we could not fully read is not backed up, so the check
			// must not go on to remove it.
			return fmt.Errorf("list %s: %w", rel, err)
		}
		for _, e := range entries {
			if e.IsDir {
				continue
			}
			data, err := storage.ReadAtPath(filepath.Join(src, filepath.FromSlash(e.Key)))
			if err != nil {
				return fmt.Errorf("read %s/%s: %w", rel, e.Key, err)
			}
			if err := store.Put(context.Background(), rel+"/"+e.Key, data, storage.WithMode(0o600)); err != nil {
				return fmt.Errorf("back up %s/%s: %w", rel, e.Key, err)
			}
		}
	}
	if ctx.backedUp == nil {
		ctx.backedUp = map[string]bool{}
	}
	ctx.backedUp[rel] = true
	return nil
}

// recordApplied adds an applied check to the manifest. Results that changed
// nothing (pass, skip, or only flags) are not recorded.
func (ctx *runContext) recordApplied(cr CheckResult, backedUp []string) {
	if cr.Status != StatusFail {
		return
	}
	changed := false
	for _, a := range cr.Actions {
		if a.Op != "flag" {
			changed = true
			break
		}
	}
	if !changed {
		return
	}
	ctx.manifest.Applied = append(ctx.manifest.Applied, ManifestEntry{
		Check: cr.Name, Message: cr.Message, Actions: cr.Actions, BackedUp: backedUp,
	})
}

// writeManifest writes the manifest if the run applied anything, and reports
// whether the backup directory exists afterwards.
//
// A run whose only changes are regenerated output (a re-render, which happens
// on every apply of a site with posts) writes nothing: a backup directory per
// routine run would bury the one that holds a real change.
func (ctx *runContext) writeManifest() (bool, error) {
	substantive := len(ctx.backedUp) > 0
	for _, e := range ctx.manifest.Applied {
		if _, derived := noBackupChecks[e.Check]; !derived {
			substantive = true
		}
	}
	if !substantive {
		return false, nil
	}
	if err := ctx.ensureBackupDir(); err != nil {
		return false, err
	}
	data, err := json.MarshalIndent(ctx.manifest, "", "  ")
	if err != nil {
		return true, err
	}
	return true, ctx.backupStore().Put(context.Background(), backupManifestName, append(data, '\n'), storage.WithMode(0o600))
}

// newBackupDir names (but does not create) a timestamped backup directory. Two
// runs in the same second get different directories, so a second run can never
// overwrite the first run's copies of the original bytes.
func newBackupDir(siteDir string) string {
	base := filepath.Join(siteDir, ".polis", "tailor-backup", time.Now().Format("20060102-150405"))
	dir := base
	for i := 2; ; i++ {
		// Any stat error ends the search: an unreadable .polis fails the first
		// backup write loudly, which is better than looping here.
		if _, err := storage.StatAtPath(dir); err != nil {
			return dir
		}
		dir = fmt.Sprintf("%s-%d", base, i)
	}
}
