package provision

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	secretpkg "github.com/vdibart/polis-cli/cli-go/pkg/secret"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ ON VERCEL A SEED IS WRITTEN STRAIGHT INTO THE PROJECT'S PRIVATE BLOB
// STORE, before the first deployment, with a store-write credential obtained
// with the provisioner's own Vercel login. Nothing of the site passes through
// a project setting or a static file, so nothing of it is readable from a
// public URL.
//
// ⭐ THE CREDENTIAL IS THE STORE'S READ-WRITE TOKEN, which Vercel injects into
// the project as the BLOB_READ_WRITE_TOKEN setting when the store is connected,
// and which the provisioner's token can read back (measured 2026-10-16). The
// instance never uses it: it reaches its store with the deployment's own OIDC
// token. ⛔ So Seed DELETES THAT SETTING once the store is written, before the
// first deployment, and no deployment is ever created carrying it. With it
// gone, no project setting grants access to the site, and the instance reads
// its store exactly as before (measured the same day). The token itself is
// held only for the length of Seed and is never written anywhere.

// blobTokenSetting is the setting Vercel injects for a connected Blob store.
// ⛔ Vercel's name; it appears nowhere outside this file.
const blobTokenSetting = "BLOB_READ_WRITE_TOKEN"

// seedTarget is an instance's private store opened for writing from outside
// a deployment, and how to discard the credential that opened it.
type seedTarget struct {
	store   storage.Store
	discard func(ctx context.Context) error
}

// obtainStoreWriteCredential reads the read-write token of inst's connected
// Blob store, checks the store is private, and opens it.
//
// ⛔ Exactly one connected store, and it must be private: a public store
// serves every pathname to anyone, and a seed carries the identity key.
// ⛔ Errors name the setting, never the value.
func (v *Vercel) obtainStoreWriteCredential(ctx context.Context, inst Instance) (seedTarget, error) {
	var listed struct {
		Envs []struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"envs"`
	}
	base := "/v9/projects/" + url.PathEscape(inst.ID) + "/env"
	if err := v.call(ctx, http.MethodGet, base, nil, &listed); err != nil {
		return seedTarget{}, fmt.Errorf("provision: listing the project's settings to find its store: %w", err)
	}
	var ids []string
	for _, e := range listed.Envs {
		if e.Key == blobTokenSetting && e.ID != "" {
			ids = append(ids, e.ID)
		}
	}
	if len(ids) != 1 {
		return seedTarget{}, fmt.Errorf("provision: the project has %d %s settings; seeding needs exactly one connected private store", len(ids), blobTokenSetting)
	}
	var got struct {
		Value string `json:"value"`
	}
	if err := v.call(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(inst.ID)+"/env/"+url.PathEscape(ids[0]), nil, &got); err != nil {
		return seedTarget{}, fmt.Errorf("provision: reading %s: %w", blobTokenSetting, err)
	}
	cred := secretpkg.New(strings.TrimSpace(got.Value))
	got.Value = ""
	if cred.IsZero() {
		return seedTarget{}, fmt.Errorf("provision: %s reads back empty, so the store cannot be written from here", blobTokenSetting)
	}
	open := v.openSeedStore
	if open == nil {
		open = openSeedStore
	}
	store, storeID, err := open(cred)
	if err != nil {
		return seedTarget{}, err
	}
	// ⛔ The token must name the store Create made or adopted, when it said.
	if inst.Store != "" && storeID != inst.Store {
		return seedTarget{}, fmt.Errorf("provision: %s names store %s, not the instance's store %s; refusing to seed a store the instance does not use", blobTokenSetting, storeID, inst.Store)
	}
	var meta struct {
		Store struct {
			Access string `json:"access"`
		} `json:"store"`
	}
	if err := v.call(ctx, http.MethodGet, "/v1/storage/stores/"+url.PathEscape(storeID), nil, &meta); err != nil {
		return seedTarget{}, fmt.Errorf("provision: reading the store's access: %w", err)
	}
	if meta.Store.Access != "private" {
		return seedTarget{}, fmt.Errorf("provision: the project's store is %q, not private; a seed carries the identity key and is never written to a store that serves it", meta.Store.Access)
	}
	return seedTarget{
		store: store,
		discard: func(ctx context.Context) error {
			if _, err := v.DeleteEnv(ctx, inst.ID, blobTokenSetting); err != nil {
				return fmt.Errorf("provision: the store is seeded but its %s setting could not be deleted, so delete it by hand before the first deployment: %w", blobTokenSetting, err)
			}
			return nil
		},
	}, nil
}

// openSeedStore opens the private store a read-write token names, and returns
// the store id it names (not secret, but it is never quoted beside the token).
func openSeedStore(cred secretpkg.Value) (storage.Store, string, error) {
	tok := cred.Expose()
	parts := strings.Split(tok, "_")
	if len(parts) < 4 || parts[3] == "" {
		return nil, "", fmt.Errorf("provision: %s is not a Blob read-write token", blobTokenSetting)
	}
	st, err := storage.NewVercelBlob(storage.VercelBlobConfig{Token: tok, Access: "private"})
	if err != nil {
		return nil, "", fmt.Errorf("provision: opening the store %s names: %w", blobTokenSetting, err)
	}
	return st, "store_" + parts[3], nil
}

// Seed writes src into the project's private store before its first
// deployment, then deletes the setting that carried the store's token. See
// SeedStore for the order and the refusals.
//
// ⭐ The setting is deleted whether or not the seed landed: the instance never
// needs it. A failed provision's Destroy reaches the store again through a
// connection of its own to empty it.
func (v *Vercel) Seed(ctx context.Context, inst Instance, src storage.Site) (err error) {
	if inst.ID == "" {
		return fmt.Errorf("provision: seeding needs the project")
	}
	target, err := v.obtainStoreWriteCredential(ctx, inst)
	if err != nil {
		return err
	}
	defer func() {
		if derr := target.discard(ctx); derr != nil && err == nil {
			err = derr
		}
	}()
	return SeedStore(ctx, target.store, src)
}
