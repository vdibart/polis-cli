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

// ⭐⭐ ON VERCEL A STORE OUTLIVES ITS PROJECT. Deleting a project disconnects
// its Blob store and deletes the store's token setting, but leaves the store
// and everything in it (measured 2026-10-16). The store of a seeded instance
// holds the site's identity key, so a teardown that deleted only the project
// would leave that key in a store no setting reaches any more.
//
// ⛔ SO DESTROY TEARS DOWN IN THIS ORDER, AND REFUSES RATHER THAN SKIP A STEP:
//  1. empty the store, with the store's own read-write token, read from the
//     project's settings while the project still exists (a store must be
//     empty before Vercel deletes it: 409 not_empty, measured);
//  2. delete the store;
//  3. delete the project.
//
// ⭐ Before the project goes, the deploy tokens recorded in its settings are
// revoked, best effort: once the project is deleted, the record of which
// tokens were handed over to it goes with it.
//
// If the store cannot be emptied or deleted, the project is NOT deleted: with
// the project gone nothing would be left to obtain the store's token through.

// teardownBlobPrefix names the setting a teardown connection injects, when
// the store's own token setting is gone (Seed deletes it). ⭐ A distinct
// prefix, because a project already connected to a store refuses a second
// BLOB_READ_WRITE_TOKEN (project_env_var_not_unique, measured 2026-10-10).
const teardownBlobPrefix = "POLIS_TEARDOWN_BLOB"

// teardownTokenSetting is the setting the teardown connection injects.
const teardownTokenSetting = teardownBlobPrefix + "_READ_WRITE_TOKEN"

// Destroy removes the instance: its private store, emptied and deleted, then
// the project. See the order above. ⭐ Destroying an instance already gone is
// success, so a flow unwinding after a failure can run it twice.
func (v *Vercel) Destroy(ctx context.Context, inst Instance) error {
	if inst.ID == "" {
		return fmt.Errorf("provision: destroy needs the project")
	}
	storeID, err := v.instanceStore(ctx, inst)
	if err != nil {
		return fmt.Errorf("provision: the project %s was not deleted, because its store could not be found: %w", inst.ID, err)
	}
	if storeID != "" {
		if err := v.emptyStore(ctx, inst, storeID); err != nil {
			return fmt.Errorf("provision: the store %s could not be emptied, so it and the project %s were not deleted "+
				"(deleting the project first would leave no way to empty a store that may hold the site's identity key): %w",
				storeID, inst.ID, err)
		}
		if err := v.deleteStore(ctx, storeID); err != nil {
			return fmt.Errorf("provision: the store %s is empty but could not be deleted, so the project %s was not deleted either; run the teardown again: %w",
				storeID, inst.ID, err)
		}
	}
	v.revokeRecordedDeployTokens(ctx, inst)
	return v.deleteProject(ctx, inst.ID)
}

// revokeRecordedDeployTokens revokes every deploy token recorded as handed
// over to the project. ⚠️ Best effort: a token it cannot revoke is scoped to
// a project about to be deleted, and expires.
func (v *Vercel) revokeRecordedDeployTokens(ctx context.Context, inst Instance) {
	ids, err := v.handedOverTokenIDs(ctx, inst.ID)
	if err != nil {
		return
	}
	for _, id := range ids {
		_ = v.RevokeToken(ctx, id)
	}
}

// instanceStore is the id of the store to tear down with inst: the store
// Create recorded, or the one store connected to the project. "" means none.
//
// ⛔ More than one is refused: an instance has exactly one, and a teardown
// does not guess which of several to empty.
func (v *Vercel) instanceStore(ctx context.Context, inst Instance) (string, error) {
	var listed struct {
		Stores []blobStore `json:"stores"`
	}
	if err := v.call(ctx, http.MethodGet, "/v1/storage/stores", nil, &listed); err != nil {
		return "", fmt.Errorf("provision: listing the stores: %w", err)
	}
	// ⭐ Also by NAME: a teardown removes the store's connection to read a
	// fresh token (teardownCredential), and one interrupted after that leaves
	// a store no connection points at. A later teardown that knows only the
	// project must still find it, or it would delete the project and orphan
	// the store holding the site's key.
	name := inst.Name
	if name == "" {
		var proj struct {
			Name string `json:"name"`
		}
		if err := v.call(ctx, http.MethodGet, "/v9/projects/"+url.PathEscape(inst.ID), nil, &proj); err == nil {
			name = proj.Name
		} else if !isStatus(err, http.StatusNotFound) {
			return "", fmt.Errorf("provision: reading the project's name to find its store: %w", err)
		}
	}
	var found []string
	for _, s := range listed.Stores {
		if s.ID == "" || (s.Type != "" && s.Type != "blob") {
			continue
		}
		mine := s.ID == inst.Store || (name != "" && s.Name == SiteStoreName(name))
		for _, p := range s.Projects {
			if p.ProjectID == inst.ID {
				mine = true
			}
		}
		if mine {
			found = append(found, s.ID)
		}
	}
	switch len(found) {
	case 0:
		// ⭐ Already deleted, or never made.
		return "", nil
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("provision: the project %s has %d stores (%s); an instance has one, so remove the others by hand first", inst.ID, len(found), strings.Join(found, ", "))
	}
}

// emptyStore deletes every object in the store, using a read-write token
// obtained through the project, and checks nothing is left.
func (v *Vercel) emptyStore(ctx context.Context, inst Instance, storeID string) (err error) {
	cred, discard, err := v.teardownCredential(ctx, inst, storeID)
	if err != nil {
		return err
	}
	defer func() {
		if derr := discard(ctx); derr != nil && err == nil {
			err = derr
		}
	}()
	open := v.openSeedStore
	if open == nil {
		open = openSeedStore
	}
	st, named, err := open(cred)
	if err != nil {
		return err
	}
	// ⛔ Never empty a store other than the one being torn down.
	if named != storeID {
		return fmt.Errorf("provision: the project's store token names store %s, not %s; refusing to empty a store this teardown is not for", named, storeID)
	}
	entries, err := st.List(ctx, "", storage.Recursive())
	if err != nil {
		return fmt.Errorf("provision: listing the store: %w", err)
	}
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		if err := st.Delete(ctx, e.Key, storage.IgnoreMissing()); err != nil {
			return fmt.Errorf("provision: deleting %s from the store: %w", e.Key, err)
		}
	}
	left, err := st.List(ctx, "", storage.Recursive())
	if err != nil {
		return fmt.Errorf("provision: checking the store is empty: %w", err)
	}
	if len(left) > 0 {
		return fmt.Errorf("provision: %d objects are still in the store after emptying it", len(left))
	}
	return nil
}

// teardownCredential returns a read-write token for the project's store, and
// how to discard whatever was set up to obtain it.
//
// ⭐ Two routes, in order:
//  1. the store's own BLOB_READ_WRITE_TOKEN setting, while it is there (an
//     instance that was never seeded keeps it);
//  2. otherwise, a second connection of the store to the same project under
//     the POLIS_TEARDOWN_BLOB prefix, which injects a fresh token setting; it
//     is deleted again once the store is emptied. A setting an earlier,
//     interrupted teardown left is reused rather than connected again.
//
// ⭐ Route 2, measured 2026-10-17: Vercel refuses a second connection of a
// store to a project it is already connected to
// (store_project_connection_not_unique), so the existing connection is removed
// first and the store connected again.
//
// ⛔ The token is held as a secret.Value and never quoted in an error.
func (v *Vercel) teardownCredential(ctx context.Context, inst Instance, storeID string) (secretpkg.Value, func(context.Context) error, error) {
	none := func(context.Context) error { return nil }
	envs, err := v.listEnv(ctx, inst.ID)
	if err != nil {
		return secretpkg.Value{}, none, fmt.Errorf("provision: listing the project's settings to find its store's token: %w", err)
	}
	if id, ok := soleEnv(envs, blobTokenSetting); ok {
		cred, err := v.readSecretSetting(ctx, inst.ID, id, blobTokenSetting)
		return cred, none, err
	}
	discard := func(ctx context.Context) error {
		if _, err := v.DeleteEnv(ctx, inst.ID, teardownTokenSetting); err != nil {
			return fmt.Errorf("provision: the teardown's %s setting could not be deleted; delete it by hand: %w", teardownTokenSetting, err)
		}
		return nil
	}
	id, ok := soleEnv(envs, teardownTokenSetting)
	if !ok {
		connect := func() error {
			return v.call(ctx, http.MethodPost, "/v1/storage/stores/"+url.PathEscape(storeID)+"/connections", map[string]any{
				"projectId":          inst.ID,
				"envVarEnvironments": []string{"production"},
				"envVarPrefix":       teardownBlobPrefix,
				"type":               "integration",
			}, nil)
		}
		err := connect()
		if isCode(err, "store_project_connection_not_unique") {
			// ⭐ MEASURED 2026-10-17: a store connects to a project ONCE, so a
			// second connection under the teardown prefix is refused. Remove
			// the existing one (its token setting is already gone), then
			// connect again. ⚠️ From here the store is findable only by its
			// id or its name (instanceStore), so a failure names the store.
			if derr := v.disconnectStore(ctx, storeID, inst.ID); derr != nil {
				return secretpkg.Value{}, none, fmt.Errorf("provision: the store's own token setting is gone, and its connection to the project could not be removed to read a new one: %w", derr)
			}
			if err = connect(); err != nil {
				return secretpkg.Value{}, none, fmt.Errorf("provision: the store %s was disconnected from the project to read a new token, and connecting it again failed; nothing was deleted, and running the teardown again finds the store by its name: %w", storeID, err)
			}
		}
		if err != nil {
			return secretpkg.Value{}, none, fmt.Errorf("provision: the store's own token setting is gone, and connecting the store again to read a new one failed: %w", err)
		}
		if envs, err = v.listEnv(ctx, inst.ID); err != nil {
			return secretpkg.Value{}, discard, fmt.Errorf("provision: listing the project's settings after connecting the store: %w", err)
		}
		if id, ok = soleEnv(envs, teardownTokenSetting); !ok {
			return secretpkg.Value{}, discard, fmt.Errorf("provision: connecting the store again set no single %s", teardownTokenSetting)
		}
	}
	cred, err := v.readSecretSetting(ctx, inst.ID, id, teardownTokenSetting)
	return cred, discard, err
}

// soleEnv returns the id of the one setting named key, if there is exactly
// one.
func soleEnv(envs []envEntry, key string) (string, bool) {
	var ids []string
	for _, e := range envs {
		if e.Key == key && e.ID != "" {
			ids = append(ids, e.ID)
		}
	}
	if len(ids) != 1 {
		return "", false
	}
	return ids[0], true
}

// readSecretSetting reads one setting's value as a secret. ⛔ Errors name the
// setting, never the value.
func (v *Vercel) readSecretSetting(ctx context.Context, project, envID, key string) (secretpkg.Value, error) {
	var got struct {
		Value string `json:"value"`
	}
	if err := v.call(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/env/"+url.PathEscape(envID), nil, &got); err != nil {
		return secretpkg.Value{}, fmt.Errorf("provision: reading %s: %w", key, err)
	}
	cred := secretpkg.New(strings.TrimSpace(got.Value))
	got.Value = ""
	if cred.IsZero() {
		return secretpkg.Value{}, fmt.Errorf("provision: %s reads back empty", key)
	}
	return cred, nil
}

// deleteStore deletes an empty store; one already gone is success.
// ⭐ Measured both ways: on a store connected to no project (200), and on an
// emptied store still connected to its project, as this order does (accepted,
// and the project was then deleted; 2026-10-16).
func (v *Vercel) deleteStore(ctx context.Context, id string) error {
	err := v.call(ctx, http.MethodDelete, "/v1/storage/stores/blob/"+url.PathEscape(id), nil, nil)
	if isStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// deleteProject deletes a project; one already gone is success.
func (v *Vercel) deleteProject(ctx context.Context, id string) error {
	path := fmt.Sprintf("/v9/projects/%s", url.PathEscape(id))
	err := v.call(ctx, http.MethodDelete, path, nil, nil)
	// ⭐ Destroying something already gone is success, not an error: a flow
	// unwinding after a failure must be able to run its teardown twice.
	if isStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// disconnectStore removes every connection of the store to the project.
func (v *Vercel) disconnectStore(ctx context.Context, storeID, projectID string) error {
	var listed struct {
		Connections []struct {
			ID        string `json:"id"`
			ProjectID string `json:"projectId"`
		} `json:"connections"`
	}
	if err := v.call(ctx, http.MethodGet, "/v1/storage/stores/"+url.PathEscape(storeID)+"/connections", nil, &listed); err != nil {
		return fmt.Errorf("provision: listing the store's connections: %w", err)
	}
	removed := 0
	for _, c := range listed.Connections {
		if c.ProjectID != projectID || c.ID == "" {
			continue
		}
		if err := v.call(ctx, http.MethodDelete, "/v1/storage/stores/"+url.PathEscape(storeID)+"/connections/"+url.PathEscape(c.ID), nil, nil); err != nil {
			return fmt.Errorf("provision: removing the store's connection: %w", err)
		}
		removed++
	}
	if removed == 0 {
		return fmt.Errorf("provision: the store reports no connection to the project to remove")
	}
	return nil
}
