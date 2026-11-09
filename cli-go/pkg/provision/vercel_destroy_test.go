package provision

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// callIndex is the position of the first call matching method+path, or -1.
func callIndex(f *fakeVercelAPI, call string) int {
	for i, c := range f.calls {
		if c == call {
			return i
		}
	}
	return -1
}

// assertTeardownOrder checks the store was emptied (its token read), then
// deleted, then the project deleted, in that order.
func assertTeardownOrder(t *testing.T, f *fakeVercelAPI, project, store string) {
	t.Helper()
	delStore := callIndex(f, "DELETE /v1/storage/stores/blob/"+store)
	read := -1
	for i, c := range f.calls {
		if i < delStore && strings.HasPrefix(c, "GET /v1/projects/"+project+"/env/") {
			read = i
		}
	}
	delProject := callIndex(f, "DELETE /v9/projects/"+project)
	if read < 0 || delStore < 0 || delProject < 0 || !(read < delStore && delStore < delProject) {
		t.Errorf("teardown order: token read at %d, store deleted at %d, project deleted at %d; want read < store < project\ncalls: %v",
			read, delStore, delProject, f.calls)
	}
}

// seededInstance is an instance Create made whose store Seed wrote, so the
// store holds the identity key and its token setting is gone.
func seededInstance(t *testing.T, f *fakeVercelAPI, p *Vercel) Instance {
	t.Helper()
	ctx := context.Background()
	inst, err := p.Create(ctx, CreateRequest{Name: "poc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Seed(ctx, inst, branchedSite(t)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.stores[inst.Store].data.Exists(ctx, site.PrivateKeyKey); !ok {
		t.Fatal("the seed left no key in the store")
	}
	if _, ok := f.env[inst.ID+"/"+blobTokenSetting]; ok {
		t.Fatalf("%s is still set after the seed", blobTokenSetting)
	}
	return inst
}

// ⭐⭐ DESTROY EMPTIES THE STORE, DELETES IT, THEN DELETES THE PROJECT, in
// that order. After a seed the store's own token setting is gone, so the
// teardown connects the store again under its own prefix to read a token,
// and deletes that setting once the store is empty.
func TestDestroyEmptiesTheStoreThenDeletesItThenTheProject(t *testing.T) {
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst := seededInstance(t, f, p)
	data := f.stores[inst.Store].data
	f.calls = nil

	if err := p.Destroy(context.Background(), inst); err != nil {
		t.Fatal(err)
	}
	if left, _ := data.List(context.Background(), ""); len(left) != 0 {
		t.Errorf("the store still holds %d entries", len(left))
	}
	if len(f.stores) != 0 || len(f.projects) != 0 {
		t.Errorf("%d stores, %d projects left; want none", len(f.stores), len(f.projects))
	}
	connect := callIndex(f, "POST /v1/storage/stores/"+inst.Store+"/connections")
	if connect < 0 {
		t.Errorf("the store was not connected again to read a token: %v", f.calls)
	}
	dropSetting := -1
	for i, c := range f.calls {
		if strings.HasPrefix(c, "DELETE /v9/projects/"+inst.ID+"/env/") {
			dropSetting = i
		}
	}
	if delStore := callIndex(f, "DELETE /v1/storage/stores/blob/"+inst.Store); dropSetting < 0 || dropSetting > delStore {
		t.Errorf("the teardown's token setting was not deleted once the store was empty (at %d, store deleted at %d)", dropSetting, delStore)
	}
	assertTeardownOrder(t, f, inst.ID, inst.Store)
}

// ⭐ An instance never seeded still has its store's token setting, and the
// teardown uses it rather than connecting again.
func TestDestroyUsesTheStoresOwnTokenWhileItIsThere(t *testing.T) {
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, err := p.Create(context.Background(), CreateRequest{Name: "poc"})
	if err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := p.Destroy(context.Background(), inst); err != nil {
		t.Fatal(err)
	}
	if callIndex(f, "POST /v1/storage/stores/"+inst.Store+"/connections") >= 0 {
		t.Error("connected the store again though its token setting was there")
	}
	if len(f.stores) != 0 || len(f.projects) != 0 {
		t.Errorf("%d stores, %d projects left; want none", len(f.stores), len(f.projects))
	}
	assertTeardownOrder(t, f, inst.ID, inst.Store)
}

// ⭐ Without Instance.Store (a caller holding only the project's id), the
// store connected to the project is the one torn down.
func TestDestroyFindsTheConnectedStore(t *testing.T) {
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst := seededInstance(t, f, p)
	if err := p.Destroy(context.Background(), Instance{ID: inst.ID}); err != nil {
		t.Fatal(err)
	}
	if len(f.stores) != 0 || len(f.projects) != 0 {
		t.Errorf("%d stores, %d projects left; want none", len(f.stores), len(f.projects))
	}
}

// ⛔⛔ IF THE STORE CANNOT BE EMPTIED, DESTROY REFUSES TO DELETE THE PROJECT,
// and says why: deleting it would leave a store holding the key with no way
// left to reach it.
func TestDestroyRefusesRatherThanOrphanTheStore(t *testing.T) {
	for name, c := range map[string]struct {
		break_ func(f *fakeVercelAPI, inst Instance)
		why    string
	}{
		"the store cannot be connected again": {
			break_: func(f *fakeVercelAPI, inst Instance) {
				f.failNext["POST /v1/storage/stores/"+inst.Store+"/connections"] = http.StatusForbidden
			},
			why: "connecting the store again",
		},
		"the token cannot be read": {
			break_: func(f *fakeVercelAPI, inst Instance) {
				for id, k := range f.envID {
					if k == inst.ID+"/"+teardownTokenSetting {
						f.failNth["GET /v1/projects/"+inst.ID+"/env/"+id] = 1
					}
				}
			},
			why: "reading",
		},
		"the store will not delete": {
			break_: func(f *fakeVercelAPI, inst Instance) {
				f.failNext["DELETE /v1/storage/stores/blob/"+inst.Store] = http.StatusConflict
			},
			why: "could not be deleted",
		},
		"the stores cannot be listed": {
			break_: func(f *fakeVercelAPI, inst Instance) {
				f.failNext["GET /v1/storage/stores"] = http.StatusInternalServerError
			},
			why: "could not be found",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeVercel(t)
			p := f.provisioner(t)
			inst := seededInstance(t, f, p)
			if name == "the token cannot be read" {
				// make the teardown setting exist, so its read can be failed:
				// an earlier teardown removed the connection and connected
				// the store again, then stopped
				if err := p.disconnectStore(context.Background(), inst.Store, inst.ID); err != nil {
					t.Fatal(err)
				}
				if err := p.call(context.Background(), http.MethodPost, "/v1/storage/stores/"+inst.Store+"/connections",
					map[string]any{"projectId": inst.ID, "envVarPrefix": teardownBlobPrefix}, nil); err != nil {
					t.Fatal(err)
				}
			}
			c.break_(f, inst)
			err := p.Destroy(context.Background(), inst)
			if err == nil {
				t.Fatal("Destroy reported success")
			}
			if !strings.Contains(err.Error(), "not deleted") || !strings.Contains(err.Error(), c.why) {
				t.Errorf("err = %v; want it to say the project was not deleted, and why (%q)", err, c.why)
			}
			if _, ok := f.projects[inst.ID]; !ok {
				t.Error("⛔ the project was deleted though its store was not")
			}
			if strings.Contains(err.Error(), "vercel_blob_rw_") {
				t.Errorf("⛔ the error names a store token: %v", err)
			}
			// ⭐ Once the fault clears, the same teardown completes.
			if err := p.Destroy(context.Background(), inst); err != nil {
				t.Fatalf("the teardown run again: %v", err)
			}
			if len(f.stores) != 0 || len(f.projects) != 0 {
				t.Errorf("%d stores, %d projects left after the second teardown", len(f.stores), len(f.projects))
			}
		})
	}
}

// ⛔ A project already gone with its store left behind (an earlier teardown
// that deleted only the project) is refused, naming the store, rather than
// reported destroyed: no setting is left to empty it through.
func TestDestroyRefusesAStoreItsProjectNoLongerReaches(t *testing.T) {
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst := seededInstance(t, f, p)
	if err := p.deleteProject(context.Background(), inst.ID); err != nil {
		t.Fatal(err)
	}
	err := p.Destroy(context.Background(), inst)
	if err == nil || !strings.Contains(err.Error(), inst.Store) {
		t.Fatalf("err = %v; want a refusal naming the store %s", err, inst.Store)
	}
	if _, ok := f.stores[inst.Store]; !ok {
		t.Error("the store was deleted while it still held the site")
	}
}

// ⭐ A seeded instance is torn down although Vercel refuses a second
// connection of its store to the project (measured 2026-10-17): the existing
// connection is removed, the store connected again under the teardown prefix,
// emptied and deleted, then the project.
func TestDestroyTearsDownASeededInstanceThroughOneConnection(t *testing.T) {
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst := seededInstance(t, f, p)
	if err := p.Destroy(context.Background(), inst); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if f.disconnections != 1 {
		t.Errorf("%d connections removed; want 1 (the store's own, before connecting it again)", f.disconnections)
	}
	assertTeardownOrder(t, f, inst.ID, inst.Store)
	if len(f.stores) != 0 || len(f.projects) != 0 {
		t.Errorf("%d stores, %d projects left", len(f.stores), len(f.projects))
	}
}

// ⛔ A failure after the connection was removed deletes nothing more and names
// the store; a later teardown that knows only the project still finds the
// store (by its name) and empties it before deleting the project.
func TestDestroyInterruptedAfterDisconnectingStillFindsTheStore(t *testing.T) {
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst := seededInstance(t, f, p)
	conn := "POST /v1/storage/stores/" + inst.Store + "/connections"
	f.failNth[conn] = f.seen[conn] + 2 // the reconnect, after not_unique
	err := p.Destroy(context.Background(), inst)
	if err == nil || !strings.Contains(err.Error(), inst.Store) {
		t.Fatalf("err = %v; want a refusal naming the store %s", err, inst.Store)
	}
	if _, ok := f.projects[inst.ID]; !ok {
		t.Fatal("⛔ the project was deleted after a failed reconnect")
	}
	if f.stores[inst.Store].project != "" {
		t.Fatal("test premise: the store should be disconnected now")
	}
	// Only the project is known, as to a teardown run from another process.
	if err := p.Destroy(context.Background(), Instance{ID: inst.ID}); err != nil {
		t.Fatalf("the teardown run again with only the project: %v", err)
	}
	if len(f.stores) != 0 || len(f.projects) != 0 {
		t.Errorf("%d stores, %d projects left; want the store emptied and deleted, then the project", len(f.stores), len(f.projects))
	}
}
