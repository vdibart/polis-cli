package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ SEEDING PUTS AN EXISTING SITE INTO A NEW INSTANCE'S STORE BEFORE THE
// INSTANCE FIRST STARTS, so its first start finds a key and a site and creates
// neither. That needs no adoption step in the instance: site.EnsureIdentityKey
// takes its existing-key path, and the site's own first-start init sees
// .well-known/polis and writes nothing.
//
// ⛔ THE ORDER IS THE SAFETY ARGUMENT, and it is fixed here rather than left to
// each platform:
//  1. the identity key, with a CONDITIONAL create. An instance that already
//     holds a key refuses the seed, and of two seeds racing only one lands;
//  2. every other file of the site;
//  3. .well-known/polis, LAST. It is what makes the store a site that publishes
//     an identity, so it is written only once everything it describes is there.
//
// ⭐ So a seed that fails part way leaves a key and some files but nothing that
// publishes an identity, never a site publishing a key the store lacks. The
// instance's first start then refuses to build a second site over it
// (site.ErrPartialSite), and a seed that failed is not resumed: the instance is
// destroyed and provisioned again.

// ErrSeedRefused is returned when the target already holds a site or an
// identity key. ⛔ A seed never overwrites an identity: that would be a second
// identity for one instance, and seeding a running instance is a restore,
// which is a different operation.
var ErrSeedRefused = errors.New("provision: refusing to seed")

// seedLeavesBehind is what never crosses into a seeded instance, by prefix:
// state that belongs to the instance a site came from rather than to the site.
//
// ⛔ .polis/deploy/ holds the origin's deploy token, lease, manifest and status,
// and an instance that started with another instance's deploy token would
// publish into the wrong project. ⭐ The owner's recovery PUBLIC key is the one
// exception (seedKeeps): it is the site's, not the origin instance's.
//
// ⛔ So do credentials the source's owner issued to other parties (API keys,
// the v0 key Kay connects with) and the record of what they did: a branch
// already leaves them behind, and this keeps a seed from any other source
// from carrying them.
var seedLeavesBehind = []string{
	".polis/deploy/",
	".polis/api-keys.json",
	".polis/kay/",
	".polis/mcp/",
	site.InitPendingKey,
}

// seedKeeps are keys under a left-behind prefix that cross anyway.
var seedKeeps = map[string]bool{owner.RecoveryPublicKeyKey: true}

// seedCrosses reports whether a key of the source site is seeded.
func seedCrosses(key string) bool {
	if seedKeeps[key] {
		return true
	}
	for _, p := range seedLeavesBehind {
		if key == p || strings.HasPrefix(key, p) {
			return false
		}
	}
	return true
}

// SeedStore writes the site src into dst, an instance's store that holds no
// site yet. It is the one implementation of seeding's order; each platform's
// Seed opens its store and calls this.
//
// ⛔ It refuses, having written nothing, when src is not a complete site whose
// private key is the one it publishes, when dst already holds an identity key
// or a .well-known/polis, or when a file is larger than dst accepts.
func SeedStore(ctx context.Context, dst storage.Store, src storage.Site) error {
	if dst == nil {
		return fmt.Errorf("provision: seeding needs the instance's store")
	}
	if !src.Valid() {
		return fmt.Errorf("provision: seeding needs the site to seed")
	}
	from := src.Store()

	priv, wk, err := seedIdentity(ctx, from)
	if err != nil {
		return err
	}
	files, err := seedFiles(ctx, from, dst)
	if err != nil {
		return err
	}

	// ⛔ The target is checked before anything is written, so a refusal leaves
	// it as it was. The conditional create below is what actually decides.
	for _, key := range []string{site.WellKnownKey, site.PrivateKeyKey} {
		ok, err := dst.Exists(ctx, key)
		if err != nil {
			// ⛔ "Could not look" is not "absent".
			return fmt.Errorf("provision: could not check the instance for %s, so it is not seeded: %w", key, err)
		}
		if ok {
			return fmt.Errorf("%w: the instance already holds %s; seeding never replaces an instance's identity, "+
				"and seeding a running instance is a restore", ErrSeedRefused, key)
		}
	}

	// 1. The key, conditionally.
	if err := dst.Put(ctx, site.PrivateKeyKey, priv, storage.IfAbsent(), storage.WithMode(0o600)); err != nil {
		if errors.Is(err, storage.ErrExists) {
			return fmt.Errorf("%w: the instance made or received an identity key while it was being seeded", ErrSeedRefused)
		}
		// ⛔ Name the key, never its bytes.
		return fmt.Errorf("provision: seeding %s: %w", site.PrivateKeyKey, err)
	}

	// 2. Everything else but .well-known/polis.
	for _, f := range files {
		data, err := from.Get(ctx, f.key)
		if err != nil {
			return fmt.Errorf("provision: reading %s to seed it: %w", f.key, err)
		}
		if err := dst.Put(ctx, f.key, data, storage.WithMode(f.mode)); err != nil {
			return fmt.Errorf("provision: seeding %s: %w", f.key, err)
		}
	}

	// 3. The identity, last.
	if err := dst.Put(ctx, site.WellKnownKey, wk, storage.IfAbsent(), storage.WithMode(0o644)); err != nil {
		return fmt.Errorf("provision: seeding %s: %w", site.WellKnownKey, err)
	}
	return nil
}

// seedIdentity reads the source's private key and .well-known/polis, and
// refuses a source whose key is not the one it publishes.
func seedIdentity(ctx context.Context, from storage.Store) (priv, wk []byte, err error) {
	priv, err = from.Get(ctx, site.PrivateKeyKey)
	if err != nil {
		return nil, nil, fmt.Errorf("provision: the site to seed has no identity key at %s: %w", site.PrivateKeyKey, err)
	}
	wk, err = from.Get(ctx, site.WellKnownKey)
	if err != nil {
		return nil, nil, fmt.Errorf("provision: the site to seed has no %s: %w", site.WellKnownKey, err)
	}
	var doc site.WellKnown
	if err := json.Unmarshal(wk, &doc); err != nil {
		return nil, nil, fmt.Errorf("provision: the site to seed has a %s that does not parse: %w", site.WellKnownKey, err)
	}
	published, err := signing.ParsePublicKey([]byte(strings.TrimSpace(doc.PublicKey)))
	if err != nil {
		return nil, nil, fmt.Errorf("provision: the site to seed publishes no usable identity key in %s", site.WellKnownKey)
	}
	pub, err := signing.PublicKeyFromPrivate(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("provision: the identity key %s of the site to seed does not parse", site.PrivateKeyKey)
	}
	mine, err := signing.ParsePublicKey(pub)
	if err != nil || !bytes.Equal(mine, published) {
		// ⛔ Seeding it would start an instance signing with a key nothing it
		// publishes verifies.
		return nil, nil, fmt.Errorf("provision: the identity key %s of the site to seed is not the key its %s publishes",
			site.PrivateKeyKey, site.WellKnownKey)
	}
	return priv, wk, nil
}

type seedFile struct {
	key  string
	mode fs.FileMode
}

// seedFiles lists what crosses, other than the key and .well-known/polis, in
// key order, and refuses before anything is written when a file is larger than
// the target accepts.
func seedFiles(ctx context.Context, from, dst storage.Store) ([]seedFile, error) {
	entries, err := from.List(ctx, "", storage.Recursive())
	if err != nil {
		// ⛔ A partial listing would seed a site missing files.
		return nil, fmt.Errorf("provision: cannot list the whole site to seed: %w", err)
	}
	limit := dst.Capabilities().MaxPutBytes
	modes := from.Capabilities().Modes
	var files []seedFile
	for _, e := range entries {
		if e.IsDir || e.Key == site.PrivateKeyKey || e.Key == site.WellKnownKey || !seedCrosses(e.Key) {
			continue
		}
		info, err := from.Stat(ctx, e.Key)
		if err != nil {
			return nil, fmt.Errorf("provision: cannot stat %s to seed it: %w", e.Key, err)
		}
		if limit > 0 && info.Size > limit {
			return nil, fmt.Errorf("provision: %s is %d bytes, over the %d the instance's store accepts in one write, so nothing is seeded",
				e.Key, info.Size, limit)
		}
		files = append(files, seedFile{key: e.Key, mode: seedMode(e.Key, info.Mode, modes)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].key < files[j].key })
	return files, nil
}

// seedMode keeps a file's permission bits where the source has them, and
// otherwise uses 0600 under .polis and 0644 elsewhere. ⭐ .polis directories
// get their 0700 from the target's site store, not from here.
func seedMode(key string, m fs.FileMode, sourceHasModes bool) fs.FileMode {
	if sourceHasModes && m.Perm() != 0 {
		return m.Perm()
	}
	if key == ".polis" || strings.HasPrefix(key, ".polis/") {
		return 0o600
	}
	return 0o644
}

// Directory seeds a site into a directory: a VPS, a laptop, or any host whose
// instance runs from a plain filesystem.
//
// ⭐ IT IS WHAT KEEPS THE VPS PATH OPEN. The instance's ID is the absolute
// directory, which is the platform-shaped identifier a filesystem has, and the
// directory is written through storage.NewSite (via storage.At), never
// NewPOSIX, so .polis keeps its private modes.
type Directory struct{}

// Seed writes src into the directory inst.ID names.
func (Directory) Seed(ctx context.Context, inst Instance, src storage.Site) error {
	dir := inst.ID
	if dir == "" || !filepath.IsAbs(dir) {
		return fmt.Errorf("provision: seeding a directory needs its absolute path as the instance ID")
	}
	return SeedStore(ctx, storage.At(dir).Store(), src)
}

// ⛔ compile-time proof that a directory seeds through the seam.
var _ Seeder = Directory{}
