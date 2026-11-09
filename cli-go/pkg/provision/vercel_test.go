package provision

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	secretpkg "github.com/vdibart/polis-cli/cli-go/pkg/secret"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// fakeVercelAPI is Vercel's provisioning surface, as this package uses it.
//
// ⭐ IT EXISTS SO THE IMPLEMENTATION IS VERIFIED WITHOUT A TOKEN. Testing only
// against credentials someone holds leaves the provisioner unverified on every
// machine but one, and in CI unverified everywhere — and this is the code that
// creates and destroys real infrastructure, so unverified is the wrong place to
// be.
type fakeVercelAPI struct {
	srv *httptest.Server

	// mu serialises the fake's state: a deploy uploads concurrently.
	mu sync.Mutex
	// uploadDelay holds each upload open, so concurrent ones overlap; and
	// peakUploads is the most that were in flight at once.
	uploadDelay time.Duration
	inFlight    atomic.Int64
	peakUploads atomic.Int64

	projects map[string]string // id → name
	env      map[string]string // "id/KEY" → type/visibility
	envVal   map[string]string // "id/KEY" → value
	envID    map[string]string // env id → "id/KEY"
	envSeq   int
	stores   map[string]*fakeStore
	domains  map[string]bool   // "id/domain" → verified
	promoted map[string]string // id → release id
	calls    []string          // method+path, in order
	uploaded map[string][]byte // sha1 → bytes, from /v2/files
	deployed []deployedFile    // the files the last deployment named
	prebuilt bool              // the last deployment asked for prebuilt=1

	deploySeq   int
	deployments map[string]string // deployment id → project id
	// readyStates is what successive polls of a deployment answer; once it
	// runs out, READY.
	readyStates []string
	// autoProduction makes a deployment take production when it is READY,
	// as a production deployment does unless production is pinned.
	autoProduction bool
	promotes       int
	// selfDeploy makes the instance deploy itself the moment its function's
	// deployment is READY: a newer production deployment, as a new instance's
	// first request makes (measured 2026-10-19).
	selfDeploy bool
	// selfBuilding is what polls answer after the self-deployment is made,
	// before READY: production names it while it builds.
	selfBuilding []string
	selfID       string
	// readyByID is what successive polls of one deployment answer, before
	// readyStates.
	readyByID map[string][]string
	// created orders deployments, as Vercel's createdAt does.
	created map[string]int64

	deletedStores []string
	lastTokenRead string // the store whose token was read last

	tokens  map[string]bool // live token ids
	tokSeq  int
	revoked []string

	// disconnections counts store connections removed.
	disconnections int

	// failNext makes the next matching path fail, for the error paths.
	failNext map[string]int

	// failNth fails the nth call of a "METHOD path" with a 500.
	failNth map[string]int
	seen    map[string]int
}

type fakeStore struct {
	name, access, project string

	// data is what the store holds, opened by its read-write token
	// (fakeVercelAPI.openStore); nil until something opens it.
	data storage.Store
	// connections counts the connections made after creation.
	connections int
}

// blobToken is the read-write token the fake injects for a store.
func blobToken(storeID string) string {
	return "vercel_blob_rw_" + strings.TrimPrefix(storeID, "store_") + "_rwsecret"
}

// openStore opens the store whose read-write token the provisioner read
// last, as Seed and Destroy do. ⭐ The credential is never exposed: the fake
// knows which store's token it served. And no test reaches the real Blob API.
func (f *fakeVercelAPI) openStore(cred secretpkg.Value) (storage.Store, string, error) {
	if cred.IsZero() {
		return nil, "", errors.New("no credential")
	}
	st, ok := f.stores[f.lastTokenRead]
	if !ok {
		return nil, "", errors.New("the token names no store")
	}
	if st.data == nil {
		st.data = storage.NewObjectStore("")
	}
	return st.data, f.lastTokenRead, nil
}

// storeEmpty reports whether a store holds nothing.
func storeEmpty(st *fakeStore) bool {
	if st.data == nil {
		return true
	}
	left, _ := st.data.List(context.Background(), "", storage.Recursive())
	return len(left) == 0
}

func newFakeVercel(t *testing.T) *fakeVercelAPI {
	f := &fakeVercelAPI{
		projects:    map[string]string{},
		env:         map[string]string{},
		envVal:      map[string]string{},
		envID:       map[string]string{},
		stores:      map[string]*fakeStore{},
		domains:     map[string]bool{},
		promoted:    map[string]string{},
		failNext:    map[string]int{},
		uploaded:    map[string][]byte{},
		deployments: map[string]string{},
		tokens:      map[string]bool{},
		created:     map[string]int64{},
		failNth:     map[string]int{},
		seen:        map[string]int{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.route))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeVercelAPI) projectByName(name string) (string, bool) {
	for id, n := range f.projects {
		if n == name {
			return id, true
		}
	}
	return "", false
}

func (f *fakeVercelAPI) putEnv(project, key, value, typ string) {
	k := project + "/" + key
	if _, ok := f.env[k]; !ok {
		f.envSeq++
		f.envID["env_"+strconv.Itoa(f.envSeq)] = k
	}
	f.env[k], f.envVal[k] = typ, value
}

func (f *fakeVercelAPI) deleteEnvID(id string) bool {
	k, ok := f.envID[id]
	if !ok {
		return false
	}
	delete(f.envID, id)
	delete(f.env, k)
	delete(f.envVal, k)
	return true
}

func (f *fakeVercelAPI) envIDOf(k string) string {
	for id, kk := range f.envID {
		if kk == k {
			return id
		}
	}
	return ""
}

func (f *fakeVercelAPI) route(w http.ResponseWriter, r *http.Request) {
	// ⛔ Every call must be authorised, so a missing header fails here rather
	// than in production.
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, `{"error":{"message":"missing token"}}`, http.StatusUnauthorized)
		return
	}
	p := r.URL.Path
	if r.Method == http.MethodPost && p == "/v2/files" {
		n := f.inFlight.Add(1)
		for {
			peak := f.peakUploads.Load()
			if n <= peak || f.peakUploads.CompareAndSwap(peak, n) {
				break
			}
		}
		if f.uploadDelay > 0 {
			time.Sleep(f.uploadDelay)
		}
		defer f.inFlight.Add(-1)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+p)

	f.seen[r.Method+" "+p]++
	if n, ok := f.failNth[r.Method+" "+p]; ok && f.seen[r.Method+" "+p] == n {
		delete(f.failNth, r.Method+" "+p)
		http.Error(w, `{"error":{"code":"injected"}}`, http.StatusInternalServerError)
		return
	}
	if code, ok := f.failNext[r.Method+" "+p]; ok {
		delete(f.failNext, r.Method+" "+p)
		http.Error(w, `{"error":{"message":"injected"}}`, code)
		return
	}
	if code, ok := f.failNext[p]; ok {
		delete(f.failNext, p)
		http.Error(w, `{"error":{"message":"injected"}}`, code)
		return
	}

	switch {
	case r.Method == http.MethodPost && p == "/v11/projects":
		var body struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		// ⚠️ Vercel normalises names; lowercase it so the test proves the
		// Instance carries what Vercel said, not what was asked.
		name := strings.ToLower(body.Name)
		if _, ok := f.projectByName(name); ok {
			http.Error(w, `{"error":{"code":"conflict","message":"exists"}}`, http.StatusConflict)
			return
		}
		id := "prj_" + name
		f.projects[id] = name
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "name": name})

	case r.Method == http.MethodGet && p == "/v1/storage/stores":
		var list []map[string]any
		for id, st := range f.stores {
			var meta []map[string]string
			if st.project != "" {
				meta = append(meta, map[string]string{"projectId": st.project})
			}
			list = append(list, map[string]any{"id": id, "name": st.name, "type": "blob", "projectsMetadata": meta})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"stores": list})

	case r.Method == http.MethodPost && p == "/v1/storage/stores/blob":
		var body struct{ Name, Access, Region, ProjectID string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := "store_" + strconv.Itoa(len(f.stores)+1)
		f.stores[id] = &fakeStore{name: body.Name, access: body.Access, project: body.ProjectID}
		// ⭐ As Vercel: connecting injects the store's read-write token.
		f.putEnv(body.ProjectID, blobTokenSetting, blobToken(id), "encrypted/")
		_ = json.NewEncoder(w).Encode(map[string]any{"store": map[string]string{"id": id}})

	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v1/storage/stores/") && strings.HasSuffix(p, "/connections"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/storage/stores/"), "/connections")
		st, ok := f.stores[id]
		if !ok {
			http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
			return
		}
		var conns []map[string]string
		if st.project != "" {
			conns = append(conns, map[string]string{"id": "spc_" + id, "projectId": st.project})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"connections": conns})

	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/v1/storage/stores/") && strings.Contains(p, "/connections/"):
		rest := strings.TrimPrefix(p, "/v1/storage/stores/")
		id := rest[:strings.Index(rest, "/")]
		st, ok := f.stores[id]
		if !ok || st.project == "" || !strings.HasSuffix(p, "/connections/spc_"+id) {
			http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
			return
		}
		// Disconnecting removes the token settings the connection injected.
		for eid, k := range f.envID {
			if strings.HasPrefix(k, st.project+"/") && f.envVal[k] == blobToken(id) {
				f.deleteEnvID(eid)
			}
		}
		st.project = ""
		f.disconnections++
		w.WriteHeader(http.StatusCreated)

	case r.Method == http.MethodPost && strings.HasPrefix(p, "/v1/storage/stores/") && strings.HasSuffix(p, "/connections"):
		st, ok := f.stores[strings.TrimSuffix(strings.TrimPrefix(p, "/v1/storage/stores/"), "/connections")]
		var body struct {
			ProjectID    string `json:"projectId"`
			EnvVarPrefix string `json:"envVarPrefix"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, live := f.projects[body.ProjectID]; !ok || !live {
			http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
			return
		}
		// ⚠️ As Vercel (measured 2026-10-17): a store connects to a project
		// once; a second connection is refused whatever its prefix.
		if st.project == body.ProjectID {
			http.Error(w, `{"error":{"code":"store_project_connection_not_unique"}}`, http.StatusBadRequest)
			return
		}
		key := blobTokenSetting
		if body.EnvVarPrefix != "" {
			key = body.EnvVarPrefix + "_READ_WRITE_TOKEN"
		}
		// ⚠️ As Vercel: a second setting of the same name is refused.
		if _, exists := f.env[body.ProjectID+"/"+key]; exists {
			http.Error(w, `{"error":{"code":"project_env_var_not_unique"}}`, http.StatusBadRequest)
			return
		}
		for id, s2 := range f.stores {
			if s2 == st {
				f.putEnv(body.ProjectID, key, blobToken(id), "encrypted/")
			}
		}
		st.project = body.ProjectID
		st.connections++
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/v1/storage/stores/blob/"):
		id := strings.TrimPrefix(p, "/v1/storage/stores/blob/")
		st, ok := f.stores[id]
		if !ok {
			http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
			return
		}
		// ⭐ As Vercel (measured): a store that is not empty is not deleted.
		if !storeEmpty(st) {
			http.Error(w, `{"error":{"code":"not_empty"}}`, http.StatusConflict)
			return
		}
		delete(f.stores, id)
		f.deletedStores = append(f.deletedStores, id)
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v1/storage/stores/"):
		st, ok := f.stores[strings.TrimPrefix(p, "/v1/storage/stores/")]
		if !ok {
			http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"store": map[string]string{"access": st.access, "name": st.name}})

	case r.Method == http.MethodPost && p == "/v2/files":
		data, _ := io.ReadAll(r.Body)
		sum := sha1.Sum(data)
		// ⚠️ As Vercel: the digest header must be the body's SHA-1.
		if r.Header.Get("x-now-digest") != hex.EncodeToString(sum[:]) ||
			r.Header.Get("x-now-size") != strconv.Itoa(len(data)) {
			http.Error(w, `{"error":{"code":"invalid_digest","message":"echo `+string(data)+`"}}`, http.StatusBadRequest)
			return
		}
		f.uploaded[hex.EncodeToString(sum[:])] = data
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodPost && p == "/v13/deployments":
		var body struct {
			Name  string         `json:"name"`
			Files []deployedFile `json:"files"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var missing []string
		for _, df := range body.Files {
			if _, ok := f.uploaded[df.SHA]; !ok {
				missing = append(missing, df.SHA)
			}
		}
		if len(body.Files) == 0 || len(missing) > 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "missing_files", "missing": missing}})
			return
		}
		f.deployed, f.prebuilt = body.Files, r.URL.Query().Get("prebuilt") == "1"
		f.deploySeq++
		id := "dpl_" + strconv.Itoa(f.deploySeq)
		f.deployments[id], _ = f.projectByName(body.Name)
		f.created[id] = int64(1792370000000 + f.deploySeq*1000)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id": id, "url": strings.ReplaceAll(id, "_", "-") + ".vercel.app",
		})

	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v13/deployments/"):
		id := strings.TrimPrefix(p, "/v13/deployments/")
		proj, ok := f.deployments[id]
		if !ok {
			http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
			return
		}
		state := "READY"
		if q := f.readyByID[id]; len(q) > 0 {
			state, f.readyByID[id] = q[0], q[1:]
		} else if len(f.readyStates) > 0 {
			state, f.readyStates = f.readyStates[0], f.readyStates[1:]
		}
		if state == "READY" && f.autoProduction {
			f.promoted[proj] = id
		}
		if state == "READY" && f.selfDeploy && f.promoted[proj] == "" {
			f.deploySeq++
			self := "dpl_" + strconv.Itoa(f.deploySeq)
			f.deployments[self] = proj
			f.created[self] = int64(1792370000000 + f.deploySeq*1000)
			f.promoted[proj] = self
			// ⚠️ As Vercel: production names it while it is still building.
			if f.readyByID == nil {
				f.readyByID = map[string][]string{}
			}
			f.readyByID[self] = append([]string(nil), f.selfBuilding...)
			f.selfID = self
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "readyState": state, "createdAt": f.created[id]})

	case r.Method == http.MethodPost && p == "/v3/user/tokens":
		var body struct {
			ProjectID string `json:"projectId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.tokSeq++
		id := "tok_" + strconv.Itoa(f.tokSeq)
		f.tokens[id] = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": map[string]any{"id": id, "expiresAt": 1900000000000}, "bearerToken": "bearer-" + id,
		})

	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/v3/user/tokens/"):
		id := strings.TrimPrefix(p, "/v3/user/tokens/")
		if !f.tokens[id] {
			http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
			return
		}
		delete(f.tokens, id)
		f.revoked = append(f.revoked, id)

	case r.Method == http.MethodPost && strings.HasSuffix(p, "/env"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/v10/projects/"), "/env")
		var body struct{ Key, Value, Type, Visibility string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		// ⚠️ As Vercel: without upsert a second setting of a name is refused.
		if _, exists := f.env[id+"/"+body.Key]; exists && r.URL.Query().Get("upsert") != "true" {
			http.Error(w, `{"error":{"code":"env_already_exists"}}`, http.StatusBadRequest)
			return
		}
		f.putEnv(id, body.Key, body.Value, body.Type+"/"+body.Visibility)
		w.WriteHeader(http.StatusCreated)

	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v9/projects/") && strings.HasSuffix(p, "/env"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/v9/projects/"), "/env")
		var envs []map[string]string
		for eid, k := range f.envID {
			if strings.HasPrefix(k, id+"/") {
				envs = append(envs, map[string]string{"id": eid, "key": strings.TrimPrefix(k, id+"/")})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"envs": envs})

	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v1/projects/") && strings.Contains(p, "/env/"):
		eid := p[strings.LastIndex(p, "/")+1:]
		k, ok := f.envID[eid]
		if !ok {
			http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
			return
		}
		if v := f.envVal[k]; strings.HasPrefix(v, "vercel_blob_rw_") {
			f.lastTokenRead = "store_" + strings.Split(v, "_")[3]
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"value": f.envVal[k]})

	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/v9/projects/") && strings.Contains(p, "/env/"):
		if !f.deleteEnvID(p[strings.LastIndex(p, "/")+1:]) {
			http.Error(w, `{"error":{"code":"not_found"}}`, http.StatusNotFound)
			return
		}

	case r.Method == http.MethodPost && strings.HasSuffix(p, "/domains"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/v10/projects/"), "/domains")
		var body struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.domains[id+"/"+body.Name] = false
		// ⚠️ Unverified on first attach, with a challenge record — which is
		// the normal case, not a failure.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": body.Name, "verified": false,
			"verification": []map[string]string{
				{"type": "cname", "domain": "_acme-challenge." + body.Name, "value": "x.vercel-dns.com"},
			},
		})

	case r.Method == http.MethodPost && strings.Contains(p, "/promote/"):
		parts := strings.Split(strings.TrimPrefix(p, "/v10/projects/"), "/promote/")
		// ⭐ As Vercel: an older deployment is not promoted over a newer one.
		if cur := f.promoted[parts[0]]; cur != "" && f.created[cur] > f.created[parts[1]] {
			http.Error(w, `{"error":{"code":"conflict"}}`, http.StatusConflict)
			return
		}
		f.promoted[parts[0]] = parts[1]
		f.promotes++
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodGet && strings.HasSuffix(p, "/domains"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/v9/projects/"), "/domains")
		var list []map[string]any
		for k, verified := range f.domains {
			if strings.HasPrefix(k, id+"/") {
				list = append(list, map[string]any{
					"name": strings.TrimPrefix(k, id+"/"), "verified": verified,
				})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"domains": list})

	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v9/projects/"):
		id := strings.TrimPrefix(p, "/v9/projects/")
		name, ok := f.projects[id]
		if !ok {
			// ⭐ As Vercel: a project is read by id or by name.
			if byName, found := f.projectByName(id); found {
				id, name, ok = byName, id, true
			}
		}
		if !ok {
			http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
			return
		}
		out := map[string]any{"id": id, "name": name, "targets": map[string]any{}}
		if rel, ok := f.promoted[id]; ok {
			out["targets"] = map[string]any{"production": map[string]string{"id": rel}}
		}
		_ = json.NewEncoder(w).Encode(out)

	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/v9/projects/"):
		id := strings.TrimPrefix(p, "/v9/projects/")
		if _, ok := f.projects[id]; !ok {
			http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
			return
		}
		delete(f.projects, id)
		// ⭐ As Vercel: the project's settings and its stores' connections go
		// with it; the stores themselves stay.
		for eid, k := range f.envID {
			if strings.HasPrefix(k, id+"/") {
				f.deleteEnvID(eid)
			}
		}
		for _, st := range f.stores {
			if st.project == id {
				st.project = ""
			}
		}
		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, `{"error":{"message":"unexpected `+p+`"}}`, http.StatusBadRequest)
	}
}

// deployedFile is one entry of a deployment's files list.
type deployedFile struct {
	File string `json:"file"`
	SHA  string `json:"sha"`
	Size int    `json:"size"`
	Mode int    `json:"mode"`
}

func (f *fakeVercelAPI) provisioner(t *testing.T) *Vercel {
	t.Helper()
	v, err := NewVercel(VercelConfig{
		Token: "test-token", BaseURL: f.srv.URL, HTTPClient: f.srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	v.pollEvery = time.Millisecond
	v.openSeedStore = f.openStore
	return v
}

// ⭐⭐⭐ THE WHOLE FLOW, IN ORDER, against the real implementation. This is the
// provisioning sequence a deploy button runs, so it is the thing worth having a
// test for before there is a button.
func TestTheProvisioningFlowEndToEnd(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)

	inst, err := p.Create(ctx, CreateRequest{Name: "Alice"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// ⚠️ Vercel normalises the name, and the Instance must carry what Vercel
	// said rather than what was asked for.
	if inst.Name != "alice" {
		t.Errorf("Instance.Name = %q; it must carry the platform's normalised name", inst.Name)
	}
	if inst.Existing {
		t.Error("a freshly created Vercel project reported Existing")
	}

	rel, err := p.PutCode(ctx, inst, testBuild(t, FormatTarGz, nil))
	if err != nil {
		t.Fatalf("put-code: %v", err)
	}
	if rel.ID == "" {
		t.Error("PutCode returned no release id, so Activate could not name one")
	}
	if !strings.HasPrefix(rel.URL, "https://") {
		t.Errorf("Release.URL = %q; it must be absolute for a smoke check", rel.URL)
	}

	if err := p.SetConfig(ctx, inst, Config{
		Values: map[string]string{"POLIS_BASE_URL": "https://alice.example", "TOKEN": "s3cret"},
		Secret: []string{"TOKEN"},
		Plain:  []string{"POLIS_BASE_URL"},
	}); err != nil {
		t.Fatalf("set-config: %v", err)
	}

	st, err := p.AttachDomain(ctx, inst, "alice.example")
	if err != nil {
		t.Fatalf("attach-domain: %v", err)
	}
	if !st.Attached {
		t.Error("AttachDomain did not report the domain attached")
	}
	// ⚠️ FALSE IS THE NORMAL FIRST ANSWER: issuance is asynchronous, and a
	// caller that treats it as failure reports a working provision as broken.
	if st.CertificateReady {
		t.Error("CertificateReady was true immediately; issuance is asynchronous")
	}
	if len(st.NeedsDNS) == 0 {
		t.Error("no DNS records surfaced — a missing ACME challenge record makes renewal " +
			"fail silently months later, so it must be returned rather than logged")
	}

	if err := p.Activate(ctx, inst, rel); err != nil {
		t.Fatalf("activate: %v", err)
	}
	got, err := p.Status(ctx, inst)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got.State != StateReady {
		t.Errorf("State = %q after activate, want ready (%q)", got.State, got.Message)
	}
	if got.ActiveRelease != rel.ID {
		t.Errorf("ActiveRelease = %q, want %q", got.ActiveRelease, rel.ID)
	}
	if got.CheckedAt.IsZero() {
		t.Error("Status.CheckedAt is zero; a caller caching a status needs its age")
	}

	if err := p.Destroy(ctx, inst); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	gone, err := p.Status(ctx, inst)
	if err != nil {
		t.Fatalf("status after destroy: %v", err)
	}
	if gone.State != StateGone {
		t.Errorf("State = %q after destroy, want gone", gone.State)
	}
}

// ⛔⛔ A SECRET MUST BE DECLARED TO THE PLATFORM, NEVER LEFT TO GUESS.
func TestSetConfigMarksSecretsAsSecret(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, _ := p.Create(ctx, CreateRequest{Name: "bob"})

	if err := p.SetConfig(ctx, inst, Config{
		Values: map[string]string{"PUBLIC": "x", "PRIVATE": "y"},
		Secret: []string{"PRIVATE"},
		Plain:  []string{"PUBLIC"},
	}); err != nil {
		t.Fatal(err)
	}
	// ⛔ "sensitive", never "encrypted": Vercel's "encrypted" is a readable
	// Config value; only "sensitive" (visibility "secret") is write-only.
	if got := f.env[inst.ID+"/PRIVATE"]; got != "sensitive/secret" {
		t.Errorf("PRIVATE stored as %q, want sensitive/secret — an encrypted value is "+
			"readable in the dashboard and by vercel env pull", got)
	}
	if got := f.env[inst.ID+"/PUBLIC"]; got != "plain/config" {
		t.Errorf("PUBLIC stored as %q, want plain", got)
	}
}

// ⛔ A FAILED SETCONFIG MUST NAME THE KEY AND NEVER THE VALUE. A secret in an
// error message ends up in a log, and the log ends up in a ticket.
func TestSetConfigErrorsNameTheKeyNotTheValue(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, _ := p.Create(ctx, CreateRequest{Name: "carol"})

	f.failNext["/v10/projects/"+inst.ID+"/env"] = http.StatusForbidden
	err := p.SetConfig(ctx, inst, Config{
		Values: map[string]string{"TOKEN": "sup3r-s3cret-value"},
		Secret: []string{"TOKEN"},
	})
	if err == nil {
		t.Fatal("a rejected env write reported success")
	}
	if !strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("the error does not name the key, so an operator cannot tell which value "+
			"failed: %v", err)
	}
	if strings.Contains(err.Error(), "sup3r-s3cret-value") {
		t.Errorf("⛔ THE ERROR LEAKED THE SECRET VALUE: %v", err)
	}
}

// ⛔ "I could not look" IS NOT "it is not there". A transient API failure must
// not read as a destroyed instance.
func TestStatusDistinguishesUnknownFromGone(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, _ := p.Create(ctx, CreateRequest{Name: "dave"})

	// A 500 is not a 404.
	f.failNext["/v9/projects/"+inst.ID] = http.StatusInternalServerError
	st, err := p.Status(ctx, inst)
	if err == nil {
		t.Error("a 500 from the platform reported a usable status")
	}
	if st.State == StateGone {
		t.Error("a 500 was reported as Gone — a transient API failure would look like a " +
			"destroyed instance, and a caller might then recreate or stop retrying")
	}

	// A 404 is Gone, and not an error.
	st, err = p.Status(ctx, Instance{ID: "prj_nonexistent"})
	if err != nil {
		t.Errorf("a missing project should report Gone rather than erroring: %v", err)
	}
	if st.State != StateGone {
		t.Errorf("State = %q for a missing project, want gone", st.State)
	}
}

// ⭐ Destroying something already gone is success: a flow unwinding after a
// failure has to be able to run its teardown twice.
func TestDestroyIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, _ := p.Create(ctx, CreateRequest{Name: "erin"})

	if err := p.Destroy(ctx, inst); err != nil {
		t.Fatalf("first destroy: %v", err)
	}
	if err := p.Destroy(ctx, inst); err != nil {
		t.Errorf("second destroy failed: %v — a teardown that cannot run twice leaves a "+
			"failed provision unrecoverable", err)
	}
}

// ⛔ Refuse to construct without a token, rather than failing partway through a
// flow having already created something.
func TestVercelRefusesToConstructWithoutAToken(t *testing.T) {
	if _, err := NewVercel(VercelConfig{}); err == nil {
		t.Error("constructed a provisioner with no token")
	}
}

// ⛔ Every call must carry the token; the fake refuses an unauthenticated one.
func TestEveryCallIsAuthenticated(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, err := p.Create(ctx, CreateRequest{Name: "frank"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rel, err := p.PutCode(ctx, inst, testBuild(t, FormatZip, nil))
	if err != nil {
		t.Fatalf("put-code: %v", err)
	}
	for name, fn := range map[string]func() error{
		"set-config": func() error {
			return p.SetConfig(ctx, inst, Config{Values: map[string]string{"A": "b"}, Plain: []string{"A"}})
		},
		"attach-domain": func() error { _, e := p.AttachDomain(ctx, inst, "f.example"); return e },
		"activate":      func() error { return p.Activate(ctx, inst, rel) },
		"status":        func() error { _, e := p.Status(ctx, inst); return e },
		"destroy":       func() error { return p.Destroy(ctx, inst) },
	} {
		if err := fn(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// ⛔ An archive is required, and a format must be known. A provisioner that
// uploads nothing and reports success produces an instance serving nothing.
func TestPutCodeRefusesAnEmptyOrUnknownBuild(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, _ := p.Create(ctx, CreateRequest{Name: "gail"})

	if _, err := p.PutCode(ctx, inst, Code{Format: FormatTarGz}); err == nil {
		t.Error("PutCode accepted an empty archive")
	}
	if _, err := p.PutCode(ctx, inst, Code{Archive: []byte("x"), Format: "rar"}); err == nil {
		t.Error("PutCode accepted an unknown format")
	}
}

// ⛔ FAIL CLOSED. A value nobody classified is refused, and so is a secret the
// seam knows about declared as plain — before anything is written.
func TestSetConfigRefusesUnclassifiedAndPlainSecrets(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, _ := p.Create(ctx, CreateRequest{Name: "erin"})
	before := map[string]bool{}
	for k := range f.env {
		before[k] = true
	}
	for name, cfg := range map[string]Config{
		"unclassified": {Values: map[string]string{"A": "1", "B": "2"}, Plain: []string{"A"}},
		"both":         {Values: map[string]string{"A": "1"}, Plain: []string{"A"}, Secret: []string{"A"}},
		"seam secret as plain": {Values: map[string]string{"DATABASE_URL": "a dsn holding hunter2"},
			Plain: []string{"DATABASE_URL"}},
		"actor key as plain": {Values: map[string]string{"POLIS_ACTOR_KEY_JUDGE": "k"},
			Plain: []string{"POLIS_ACTOR_KEY_JUDGE"}},
	} {
		err := p.SetConfig(ctx, inst, cfg)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: the refusal quotes a value: %v", name, err)
		}
	}
	for k := range f.env {
		if strings.HasPrefix(k, inst.ID+"/") && !before[k] {
			t.Errorf("a refused config still wrote %s", k)
		}
	}
}

// ⛔ An error carries the status and Vercel's error code, never the body: a
// body echoing a token would otherwise reach the logs.
func TestAPIErrorsNeverEchoTheBody(t *testing.T) {
	const token = "vercel_blob_rw_ABC:planted-in-a-body:0123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"bad token ` + token + `"}}`))
	}))
	defer srv.Close()
	p, err := NewVercel(VercelConfig{Token: "t", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = p.Destroy(context.Background(), Instance{ID: "x"})
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "bad token") {
		t.Fatalf("the error echoed the body: %v", err)
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("the error lost the status or the code: %v", err)
	}
	if SafeErrorCode("forbidden "+token) != "" {
		t.Error("a code with spaces or a token passed as an error code")
	}
}
