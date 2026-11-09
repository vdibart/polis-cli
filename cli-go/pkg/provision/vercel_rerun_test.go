package provision

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	secretpkg "github.com/vdibart/polis-cli/cli-go/pkg/secret"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// storesOf lists the ids of the fake's stores connected to a project.
func (f *fakeVercelAPI) storesOf(project string) []string {
	var ids []string
	for id, st := range f.stores {
		if st.project == project {
			ids = append(ids, id)
		}
	}
	return ids
}

// ⭐ Create makes the project and ONE private store named <project>-site,
// connected to it, and reports the store on the Instance.
func TestCreateMakesTheProjectAndItsOnePrivateStore(t *testing.T) {
	f := newFakeVercel(t)
	inst, err := f.provisioner(t).Create(context.Background(), CreateRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	ids := f.storesOf(inst.ID)
	if len(ids) != 1 || len(f.stores) != 1 {
		t.Fatalf("stores = %v (connected %v); want exactly one", f.stores, ids)
	}
	st := f.stores[ids[0]]
	if st.name != "alice-site" || st.access != "private" {
		t.Errorf("store = %+v; want alice-site, private", st)
	}
	if inst.Store != ids[0] {
		t.Errorf("Instance.Store = %q, want %q", inst.Store, ids[0])
	}
	if inst.Existing {
		t.Error("a new project reported Existing")
	}
	if f.env[inst.ID+"/"+provisionedMarker] != "plain/config" {
		t.Errorf("the project was not marked as provisioned: %q", f.env[inst.ID+"/"+provisionedMarker])
	}
}

// ⭐ A re-run adopts its own project and its store: one project, one store.
func TestCreateAdoptsItsOwnProjectAndStoreOnARerun(t *testing.T) {
	f := newFakeVercel(t)
	p := f.provisioner(t)
	first, err := p.Create(context.Background(), CreateRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(context.Background(), CreateRequest{Name: "alice"})
	if err != nil {
		t.Fatalf("a re-run of Create: %v", err)
	}
	if !again.Existing || again.ID != first.ID || again.Store != first.Store {
		t.Errorf("re-run = %+v; want the first instance %+v, Existing", again, first)
	}
	if len(f.projects) != 1 || len(f.stores) != 1 {
		t.Errorf("%d projects, %d stores after a re-run; want one of each", len(f.projects), len(f.stores))
	}
}

// ⛔ A project of that name that polis did not make is someone else's.
func TestCreateRefusesAStrangersProject(t *testing.T) {
	f := newFakeVercel(t)
	f.projects["prj_bob"] = "bob"
	_, err := f.provisioner(t).Create(context.Background(), CreateRequest{Name: "bob"})
	if err == nil || !strings.Contains(err.Error(), "bob") || !strings.Contains(err.Error(), "not made by polis") {
		t.Fatalf("err = %v; want a refusal naming the project", err)
	}
	if len(f.stores) != 0 {
		t.Errorf("a refused Create made a store: %v", f.stores)
	}
}

// ⛔ Adopting a store that is not private is refused; so is a project with
// more than one store; and so is an unconnected store of the instance's name,
// which may hold an earlier provision's site. None of them makes a store.
func TestCreateRefusesAStoreItCannotAdopt(t *testing.T) {
	for name, setup := range map[string]func(f *fakeVercelAPI){
		"public": func(f *fakeVercelAPI) {
			f.stores["store_pub"] = &fakeStore{name: "carol-site", access: "public", project: "prj_carol"}
		},
		"two": func(f *fakeVercelAPI) {
			f.stores["store_a"] = &fakeStore{name: "carol-site", access: "private", project: "prj_carol"}
			f.stores["store_b"] = &fakeStore{name: "other", access: "private", project: "prj_carol"}
		},
		"orphan": func(f *fakeVercelAPI) {
			f.stores["store_old"] = &fakeStore{name: "carol-site", access: "private"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeVercel(t)
			f.projects["prj_carol"] = "carol"
			f.putEnv("prj_carol", provisionedMarker, "1", "plain/config")
			setup(f)
			before := len(f.stores)
			_, err := f.provisioner(t).Create(context.Background(), CreateRequest{Name: "carol"})
			if err == nil {
				t.Fatal("adopted a store it must refuse")
			}
			if len(f.stores) != before {
				t.Errorf("a refused adoption made a store")
			}
			if name == "public" && !strings.Contains(err.Error(), "not private") {
				t.Errorf("err = %v; want it to say the store is not private", err)
			}
		})
	}
}

// ⭐ A project whose marker could not be written is deleted, so a re-run never
// mistakes it for a stranger's.
func TestCreateDeletesAProjectItCouldNotMark(t *testing.T) {
	f := newFakeVercel(t)
	f.failNth["POST /v10/projects/prj_dave/env"] = 1
	if _, err := f.provisioner(t).Create(context.Background(), CreateRequest{Name: "dave"}); err == nil {
		t.Fatal("Create reported success with no marker")
	}
	if len(f.projects) != 0 {
		t.Errorf("an unmarked project was left: %v", f.projects)
	}
}

// --- Activate waits for READY ---

func activateCase(t *testing.T, states []string, auto bool) (*fakeVercelAPI, *Vercel, Instance, Release) {
	t.Helper()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, err := p.Create(context.Background(), CreateRequest{Name: "erin"})
	if err != nil {
		t.Fatal(err)
	}
	rel, err := p.PutCode(context.Background(), inst, testBuild(t, FormatTarGz, nil))
	if err != nil {
		t.Fatal(err)
	}
	f.readyStates, f.autoProduction = states, auto
	return f, p, inst, rel
}

// ⭐ BUILDING then READY promotes, once.
func TestActivateWaitsForReadyThenPromotesOnce(t *testing.T) {
	f, p, inst, rel := activateCase(t, []string{"QUEUED", "BUILDING", "BUILDING"}, false)
	if err := p.Activate(context.Background(), inst, rel); err != nil {
		t.Fatal(err)
	}
	if f.promotes != 1 || f.promoted[inst.ID] != rel.ID {
		t.Errorf("promotes = %d, production = %q; want one promote of %s", f.promotes, f.promoted[inst.ID], rel.ID)
	}
	if len(f.readyStates) != 0 {
		t.Errorf("promoted before READY: %v polls left", f.readyStates)
	}
}

// ⭐ A deployment that took production by itself is not promoted.
func TestActivateDoesNotPromoteWhatIsAlreadyProduction(t *testing.T) {
	f, p, inst, rel := activateCase(t, []string{"BUILDING"}, true)
	if err := p.Activate(context.Background(), inst, rel); err != nil {
		t.Fatal(err)
	}
	if f.promotes != 0 || f.promoted[inst.ID] != rel.ID {
		t.Errorf("promotes = %d, production = %q; want none, and %s production", f.promotes, f.promoted[inst.ID], rel.ID)
	}
}

// ⛔ ERROR and CANCELED fail with the state named, and nothing is promoted.
func TestActivateFailsOnAFailedDeployment(t *testing.T) {
	for _, state := range []string{"ERROR", "CANCELED"} {
		f, p, inst, rel := activateCase(t, []string{"BUILDING", state}, false)
		err := p.Activate(context.Background(), inst, rel)
		if err == nil || !strings.Contains(err.Error(), state) {
			t.Errorf("%s: err = %v; want the state named", state, err)
		}
		if f.promotes != 0 {
			t.Errorf("%s: promoted a failed deployment", state)
		}
	}
}

// ⛔ The deadline fails with a clear message, and nothing is promoted.
func TestActivateFailsAtTheDeadline(t *testing.T) {
	states := make([]string, 100000)
	for i := range states {
		states[i] = "BUILDING"
	}
	f, p, inst, rel := activateCase(t, states, false)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := p.Activate(ctx, inst, rel)
	if err == nil || !strings.Contains(err.Error(), "deadline") || !strings.Contains(err.Error(), "BUILDING") {
		t.Fatalf("err = %v; want the deadline and the last state named", err)
	}
	if f.promotes != 0 {
		t.Error("promoted a deployment still building")
	}
}

// ⭐ A rate limit or a 5xx while polling is waited out; a 4xx is not.
func TestActivateRetriesTransientFailuresOnly(t *testing.T) {
	f, p, inst, rel := activateCase(t, nil, false)
	f.failNext["GET /v13/deployments/"+rel.ID] = http.StatusTooManyRequests
	f.failNth["GET /v13/deployments/"+rel.ID] = 2 // then a 500
	if err := p.Activate(context.Background(), inst, rel); err != nil {
		t.Fatalf("a rate limit and a 500 while polling: %v", err)
	}
	f, p, inst, rel = activateCase(t, nil, false)
	f.failNext["GET /v13/deployments/"+rel.ID] = http.StatusForbidden
	if err := p.Activate(context.Background(), inst, rel); err == nil {
		t.Error("a 403 while polling was retried as if transient")
	}
	if f.promotes != 0 {
		t.Error("promoted after a refused poll")
	}
}

// --- every operation survives a re-run ---

// unseededFlow is the provisioning a caller runs for an instance that starts
// empty: Create, SetConfig, the deploy-token handover, PutCode, Activate.
func unseededFlow(ctx context.Context, t *testing.T, p *Vercel) (Instance, Release, error) {
	inst, err := p.Create(ctx, CreateRequest{Name: "poc"})
	if err != nil {
		return inst, Release{}, err
	}
	cfg := Config{
		Values: map[string]string{
			"POLIS_MODE":             "remote-server-signed",
			"POLIS_STORAGE":          "vercel-blob",
			storage.EnvStorageSource: inst.Store,
		},
		Plain: []string{"POLIS_MODE", "POLIS_STORAGE", storage.EnvStorageSource},
	}
	if err := p.SetConfig(ctx, inst, cfg); err != nil {
		return inst, Release{}, err
	}
	if err := VercelDeployTokenHandover(time.Hour)(ctx, p, inst); err != nil {
		return inst, Release{}, err
	}
	rel, err := p.PutCode(ctx, inst, testBuild(t, FormatTarGz, nil))
	if err != nil {
		return inst, rel, err
	}
	return inst, rel, p.Activate(ctx, inst, rel)
}

// ⭐⭐ THE FLOW FAILS AT EACH STEP IN TURN, IS RUN AGAIN, AND COMPLETES — with
// one project, one store, one live deploy token and no duplicate settings.
func TestEveryStepSurvivesARerun(t *testing.T) {
	const env = "POST /v10/projects/prj_poc/env"
	// The settings are written in this order: the marker (1), the three
	// configuration values (2-4), the token's id record (5), the token (6).
	for _, step := range []struct {
		name, call string
		n          int
	}{
		{"creating the project", "POST /v11/projects", 1},
		{"marking the project", env, 1},
		{"listing the stores", "GET /v1/storage/stores", 1},
		{"making the store", "POST /v1/storage/stores/blob", 1},
		{"checking the store's access", "GET /v1/storage/stores/store_1", 1},
		{"setting the configuration", env, 3},
		{"minting the deploy token", "POST /v3/user/tokens", 1},
		{"recording the token's id", env, 5},
		{"setting the deploy token", env, 6},
		{"uploading the code", "POST /v2/files", 1},
		{"creating the deployment", "POST /v13/deployments", 1},
		{"waiting for READY", "GET /v13/deployments/dpl_1", 1},
		{"promoting", "POST /v10/projects/prj_poc/promote/dpl_1", 1},
	} {
		t.Run(step.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFakeVercel(t)
			p := f.provisioner(t)
			// a 500 waiting for READY is retried, so that step fails as a 4xx
			if strings.HasPrefix(step.call, "GET /v13/") {
				f.failNext[step.call] = http.StatusBadRequest
			} else {
				f.failNth[step.call] = step.n
			}
			if _, _, err := unseededFlow(ctx, t, p); err == nil {
				t.Fatalf("the injected failure at %s did not fail the flow", step.name)
			}
			inst, rel, err := unseededFlow(ctx, t, p)
			if err != nil {
				t.Fatalf("the re-run after a failure at %s: %v", step.name, err)
			}
			if len(f.projects) != 1 {
				t.Errorf("%d projects; want one", len(f.projects))
			}
			if ids := f.storesOf(inst.ID); len(f.stores) != 1 || len(ids) != 1 || ids[0] != inst.Store {
				t.Errorf("stores %v (connected %v), Instance.Store %q; want one, the instance's", f.stores, ids, inst.Store)
			}
			if len(f.tokens) != 1 {
				t.Errorf("%d live deploy tokens (%v); want one", len(f.tokens), f.tokens)
			}
			if f.envVal[inst.ID+"/"+storage.EnvStorageSource] != inst.Store {
				t.Errorf("%s = %q, want %q", storage.EnvStorageSource, f.envVal[inst.ID+"/"+storage.EnvStorageSource], inst.Store)
			}
			if f.env[inst.ID+"/"+secretpkg.EnvDeployToken] != "sensitive/secret" {
				t.Errorf("%s is %q; want write-only", secretpkg.EnvDeployToken, f.env[inst.ID+"/"+secretpkg.EnvDeployToken])
			}
			if len(f.envID) != len(f.env) {
				t.Errorf("%d settings under %d names: a name is set twice", len(f.envID), len(f.env))
			}
			if f.promoted[inst.ID] != rel.ID {
				t.Errorf("production = %q, want %q", f.promoted[inst.ID], rel.ID)
			}
		})
	}
}

// ⛔ No call made by a failed run and its re-run returns or reports a secret:
// a token the fake minted never appears in an error.
func TestARerunNeverEchoesATokenInAnError(t *testing.T) {
	f := newFakeVercel(t)
	p := f.provisioner(t)
	f.failNth["POST /v10/projects/prj_poc/env"] = 6
	_, _, err := unseededFlow(context.Background(), t, p)
	if err == nil {
		t.Fatal("the injected failure did not fail the flow")
	}
	if strings.Contains(err.Error(), "bearer-") {
		t.Errorf("⛔ the error names a token: %v", err)
	}
}

// --- the seeded flow on Vercel ---

// seededVercel is a provisioner against f whose stores are the fake's own
// (fakeVercelAPI.openStore), so a test can look inside them.
func seededVercel(t *testing.T, f *fakeVercelAPI) (*Vercel, map[string]*fakeStore) {
	return f.provisioner(t), f.stores
}

func seededRequest(t *testing.T) SeededProvision {
	return SeededProvision{
		Create:   CreateRequest{Name: "poc"},
		Site:     branchedSite(t),
		Config:   Config{Values: map[string]string{"POLIS_MODE": "remote-server-signed"}, Plain: []string{"POLIS_MODE"}},
		Handover: VercelDeployTokenHandover(time.Hour),
		Code:     testBuild(t, FormatTarGz, nil),
	}
}

// ⭐ A seeded provision on Vercel writes the site into the store Create made,
// hands the token over before the code deploys, and leaves no store token in
// the project's settings.
func TestASeededVercelProvisionUsesTheStoreCreateMade(t *testing.T) {
	f := newFakeVercel(t)
	p, stores := seededVercel(t, f)
	inst, rel, err := ProvisionSeeded(context.Background(), p, seededRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if st := stores[inst.Store]; st == nil || st.data == nil {
		t.Fatalf("the instance's store %s was never opened", inst.Store)
	}
	if ok, _ := stores[inst.Store].data.Exists(context.Background(), site.WellKnownKey); !ok {
		t.Errorf("the instance's store %s was not seeded", inst.Store)
	}
	if _, ok := f.env[inst.ID+"/"+blobTokenSetting]; ok {
		t.Errorf("%s is still set", blobTokenSetting)
	}
	if f.env[inst.ID+"/"+secretpkg.EnvDeployToken] != "sensitive/secret" || f.promoted[inst.ID] != rel.ID {
		t.Errorf("token %q, production %q", f.env[inst.ID+"/"+secretpkg.EnvDeployToken], f.promoted[inst.ID])
	}
}

// ⭐ A seeded provision that failed making its store resumes on a re-run.
func TestASeededProvisionResumesAfterItsStoreFailed(t *testing.T) {
	f := newFakeVercel(t)
	p, _ := seededVercel(t, f)
	f.failNth["POST /v1/storage/stores/blob"] = 1
	if _, _, err := ProvisionSeeded(context.Background(), p, seededRequest(t)); err == nil {
		t.Fatal("the injected failure did not fail the provision")
	}
	inst, _, err := ProvisionSeeded(context.Background(), p, seededRequest(t))
	if err != nil {
		t.Fatalf("the re-run: %v", err)
	}
	if !inst.Existing || len(f.projects) != 1 || len(f.stores) != 1 {
		t.Errorf("instance %+v, %d projects, %d stores; want the adopted one", inst, len(f.projects), len(f.stores))
	}
}

// ⭐ A seeded provision that fails after the seed tears its instance down
// completely: the store, which holds the seeded key, is emptied and deleted
// before the project. A re-run then provisions afresh.
func TestAFailedSeededProvisionTearsDownItsStore(t *testing.T) {
	f := newFakeVercel(t)
	p, _ := seededVercel(t, f)
	f.failNth["POST /v13/deployments"] = 1
	_, _, err := ProvisionSeeded(context.Background(), p, seededRequest(t))
	if err == nil || !strings.Contains(err.Error(), "destroyed") {
		t.Fatalf("err = %v; want the failure and the instance destroyed", err)
	}
	if len(f.stores) != 0 || len(f.projects) != 0 {
		t.Fatalf("%d stores and %d projects left after the teardown; want none", len(f.stores), len(f.projects))
	}
	assertTeardownOrder(t, f, "prj_poc", "store_1")
	inst, _, err := ProvisionSeeded(context.Background(), p, seededRequest(t))
	if err != nil {
		t.Fatalf("the re-run: %v", err)
	}
	if inst.Existing || len(f.projects) != 1 || len(f.stores) != 1 {
		t.Errorf("instance %+v, %d projects, %d stores; want a fresh one", inst, len(f.projects), len(f.stores))
	}
}

// --- the old deploy token is revoked only once the new code is live ---

// ⭐⭐ A RENEWAL REVOKES THE EARLIER TOKEN ONLY AFTER THE NEW CODE HAS
// DEPLOYED AND BEEN ACTIVATED. A deploy that fails, at PutCode or at
// Activate, leaves the earlier token working; activating an older deployment
// revokes nothing.
func TestARenewalRevokesTheEarlierTokenOnlyOnceTheNewCodeIsActive(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, first, err := unseededFlow(ctx, t, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.tokens) != 1 || !f.tokens["tok_1"] {
		t.Fatalf("tokens %v after the first provision; want tok_1", f.tokens)
	}
	renew := func() {
		t.Helper()
		if err := VercelDeployTokenHandover(time.Hour)(ctx, p, inst); err != nil {
			t.Fatal(err)
		}
	}

	// A renewal whose upload fails.
	renew()
	f.failNth["POST /v13/deployments"] = f.seen["POST /v13/deployments"] + 1
	if _, err := p.PutCode(ctx, inst, testBuild(t, FormatTarGz, nil)); err == nil {
		t.Fatal("the injected deploy failure did not fail PutCode")
	}
	if !f.tokens["tok_1"] {
		t.Fatal("⛔ a failed deploy revoked the token the live deployment publishes with")
	}

	// A renewal whose deployment fails to build.
	renew()
	rel, err := p.PutCode(ctx, inst, testBuild(t, FormatTarGz, nil))
	if err != nil {
		t.Fatal(err)
	}
	f.readyStates = []string{"BUILDING", "ERROR"}
	if err := p.Activate(ctx, inst, rel); err == nil {
		t.Fatal("a failed build was activated")
	}
	if !f.tokens["tok_1"] || len(f.revoked) != 0 {
		t.Fatalf("revoked %v after a failed activation; want nothing revoked", f.revoked)
	}

	// Activating the deployment that was already live revokes nothing: it
	// carries an earlier token.
	if err := p.Activate(ctx, inst, first); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 0 {
		t.Fatalf("activating an older deployment revoked %v", f.revoked)
	}

	// A renewal that deploys and is activated.
	renew()
	rel, err = p.PutCode(ctx, inst, testBuild(t, FormatTarGz, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !f.tokens["tok_1"] {
		t.Fatal("revoked before the new deployment was activated")
	}
	if err := p.Activate(ctx, inst, rel); err != nil {
		t.Fatal(err)
	}
	if len(f.tokens) != 1 || !f.tokens["tok_4"] {
		t.Errorf("live tokens %v after the activation; want only the newest, tok_4", f.tokens)
	}
	if got := f.envVal[inst.ID+"/"+deployTokenIDSetting]; got != "tok_4" {
		t.Errorf("%s = %q; want only the live token's id", deployTokenIDSetting, got)
	}
	ret, ok := p.DeployTokensRetired(inst)
	if !ok || len(ret.Revoked) != 3 || len(ret.Unrevoked) != 0 || ret.Err != nil {
		t.Errorf("retirement = %+v (%v); want tok_1..tok_3 revoked", ret, ok)
	}
}

// ⭐ An earlier token that cannot be revoked stays recorded, and the
// activation still succeeds: the release is live.
func TestAnUnrevokableEarlierTokenStaysRecorded(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, _, err := unseededFlow(ctx, t, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := VercelDeployTokenHandover(time.Hour)(ctx, p, inst); err != nil {
		t.Fatal(err)
	}
	rel, err := p.PutCode(ctx, inst, testBuild(t, FormatTarGz, nil))
	if err != nil {
		t.Fatal(err)
	}
	f.failNext["DELETE /v3/user/tokens/tok_1"] = http.StatusInternalServerError
	if err := p.Activate(ctx, inst, rel); err != nil {
		t.Fatalf("a failed revocation failed the activation: %v", err)
	}
	if got := f.envVal[inst.ID+"/"+deployTokenIDSetting]; got != "tok_2 tok_1" {
		t.Errorf("%s = %q; want the live token and the one still to revoke", deployTokenIDSetting, got)
	}
	if ret, _ := p.DeployTokensRetired(inst); len(ret.Unrevoked) != 1 || ret.Unrevoked[0] != "tok_1" {
		t.Errorf("retirement = %+v; want tok_1 unrevoked", ret)
	}
}

// ⭐ THE TAKEOVER (measured 2026-10-19): a new instance deploys itself as soon
// as its function is ready, so production moves to a newer deployment before
// the release could be promoted. That is the release, live: nothing is
// promoted over it (Vercel would refuse with a 409).
func TestActivateAcceptsANewInstanceThatDeployedItself(t *testing.T) {
	f, p, inst, rel := activateCase(t, []string{"BUILDING"}, false)
	f.selfDeploy = true
	// ⚠️ Measured: production names the instance's deployment while it is
	// still building, so the takeover waits for it.
	f.selfBuilding = []string{"BUILDING", "BUILDING"}
	if err := p.Activate(context.Background(), inst, rel); err != nil {
		t.Fatalf("a new instance that deployed itself: %v", err)
	}
	if left := f.readyByID[f.selfID]; len(left) != 0 {
		t.Errorf("accepted the takeover before the instance's deployment was READY: %v polls left", left)
	}
	if f.promotes != 0 || f.promoted[inst.ID] == rel.ID || f.promoted[inst.ID] == "" {
		t.Errorf("promotes = %d, production = %q; want the instance's own deployment left in production", f.promotes, f.promoted[inst.ID])
	}
}

// ⛔ An existing instance ran other code before this release, so a newer
// deployment it made is not taken as the release: the strict check stays, and
// the refused promote is reported.
func TestActivateKeepsTheStrictCheckForAnExistingInstance(t *testing.T) {
	f, p, inst, rel := activateCase(t, nil, false)
	f.selfDeploy = true
	inst.Existing = true
	err := p.Activate(context.Background(), inst, rel)
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("err = %v; want the refused promote reported", err)
	}
}

// ⛔ An OLDER production deployment is not the release.
func TestActivatePromotesOverAnOlderProduction(t *testing.T) {
	f, p, inst, rel := activateCase(t, nil, false)
	f.promoted[inst.ID] = "dpl_0"
	f.deployments["dpl_0"] = inst.ID
	f.created["dpl_0"] = 1
	if err := p.Activate(context.Background(), inst, rel); err != nil {
		t.Fatal(err)
	}
	if f.promotes != 1 || f.promoted[inst.ID] != rel.ID {
		t.Errorf("promotes = %d, production = %q; want %s promoted over the older one", f.promotes, f.promoted[inst.ID], rel.ID)
	}
}
