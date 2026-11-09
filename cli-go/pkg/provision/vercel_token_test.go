package provision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	secretpkg "github.com/vdibart/polis-cli/cli-go/pkg/secret"
)

const mintedBearer = "minted-bearer-0123456789"

// fakeTokenAPI is the slice of Vercel's API a deploy-token handover uses.
type fakeTokenAPI struct {
	mu       sync.Mutex
	minted   map[string]any    // the last mint request
	env      map[string]string // id → key, as the project holds them
	values   map[string]string // key → value, as written
	types    map[string]string // key → type
	revoked  []string
	failEnv  bool
	calls    []string
	envSeq   int
	mints    int
	teamSeen bool
}

func newFakeTokenAPI(t *testing.T) (*fakeTokenAPI, *Vercel) {
	f := &fakeTokenAPI{env: map[string]string{}, values: map[string]string{}, types: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		if r.URL.Query().Get("teamId") == "team_1" {
			f.teamSeen = true
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/user/tokens":
			_ = json.NewDecoder(r.Body).Decode(&f.minted)
			f.mints++
			id := "tok_" + string(rune('0'+f.mints))
			_, _ = w.Write([]byte(`{"token":{"id":"` + id + `","expiresAt":1900000000000},"bearerToken":"` + mintedBearer + `"}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v3/user/tokens/"):
			f.revoked = append(f.revoked, strings.TrimPrefix(r.URL.Path, "/v3/user/tokens/"))
		case r.Method == http.MethodPost && r.URL.Path == "/v10/projects/prj_1/env":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			key := body["key"].(string)
			if f.failEnv && key == secretpkg.EnvDeployToken {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":"forbidden"}}`))
				return
			}
			// ⭐ upsert, as the request asks: one setting per name.
			for id, k := range f.env {
				if k == key {
					delete(f.env, id)
				}
			}
			f.envSeq++
			f.env["env_"+string(rune('0'+f.envSeq))] = key
			f.values[key], _ = body["value"].(string)
			f.types[key], _ = body["type"].(string)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/projects/prj_1/env/"):
			k := f.env[strings.TrimPrefix(r.URL.Path, "/v1/projects/prj_1/env/")]
			_ = json.NewEncoder(w).Encode(map[string]string{"value": f.values[k]})
		case r.Method == http.MethodGet && (r.URL.Path == "/v9/projects/poc/env" || r.URL.Path == "/v9/projects/prj_1/env"):
			var envs []map[string]string
			for id, k := range f.env {
				envs = append(envs, map[string]string{"id": id, "key": k})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"envs": envs})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v9/projects/poc/env/"):
			delete(f.env, strings.TrimPrefix(r.URL.Path, "/v9/projects/poc/env/"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	v, err := NewVercel(VercelConfig{Token: "personal", TeamID: "team_1", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return f, v
}

// ⭐⭐ The handover: a token scoped to the one project, set write-only under the
// name the instance adopts, and never handed back to the caller.
func TestADeployTokenIsMintedForTheProjectAndSetWriteOnly(t *testing.T) {
	f, v := newFakeTokenAPI(t)
	exp := time.Now().Add(DeployTokenLifetime)
	grant, err := v.IssueDeployToken(context.Background(), Instance{ID: "prj_1", Name: "poc"}, exp)
	if err != nil {
		t.Fatal(err)
	}
	if f.minted["projectId"] != "prj_1" || f.minted["expiresAt"] == nil {
		t.Errorf("mint request = %v; want the project and an expiry", f.minted)
	}
	if f.values[secretpkg.EnvDeployToken] != mintedBearer || f.types[secretpkg.EnvDeployToken] != "sensitive" {
		t.Errorf("setting %s: type %q; want the minted token, write-only", secretpkg.EnvDeployToken, f.types[secretpkg.EnvDeployToken])
	}
	if !f.teamSeen {
		t.Error("the calls were not scoped to the team")
	}
	if grant.ID != "tok_1" || grant.ExpiresAt.IsZero() {
		t.Errorf("grant = %+v", grant)
	}
	if strings.Contains(strings.ReplaceAll(jsonOf(grant), " ", ""), mintedBearer) {
		t.Error("⛔ the grant carries the token")
	}
}

// ⛔ A token whose setting could not be written is revoked, and the error
// never names the token.
func TestAnUndeliveredDeployTokenIsRevoked(t *testing.T) {
	f, v := newFakeTokenAPI(t)
	f.failEnv = true
	_, err := v.IssueDeployToken(context.Background(), Instance{ID: "prj_1"}, time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("a failed handover reported success")
	}
	if len(f.revoked) != 1 || f.revoked[0] != "tok_1" {
		t.Errorf("revoked %v; want the minted token", f.revoked)
	}
	if strings.Contains(err.Error(), mintedBearer) {
		t.Error("⛔ the error names the token")
	}
	if !strings.Contains(err.Error(), "setting "+secretpkg.EnvDeployToken+":") {
		t.Errorf("the error does not name the setting: %v", err)
	}
}

func TestADeployTokenNeedsAProjectAndAFutureExpiry(t *testing.T) {
	_, v := newFakeTokenAPI(t)
	if _, err := v.IssueDeployToken(context.Background(), Instance{}, time.Now().Add(time.Hour)); err == nil {
		t.Error("issued with no project")
	}
	if _, err := v.IssueDeployToken(context.Background(), Instance{ID: "prj_1"}, time.Now().Add(-time.Hour)); err == nil {
		t.Error("issued an already-expired token")
	}
}

// ⭐ DeleteEnv removes every setting of that name and nothing else.
func TestDeleteEnvRemovesOnlyTheNamedSetting(t *testing.T) {
	f, v := newFakeTokenAPI(t)
	f.env["env_a"] = secretpkg.EnvDeployToken
	f.env["env_b"] = "POLIS_MODE"
	n, err := v.DeleteEnv(context.Background(), "poc", secretpkg.EnvDeployToken)
	if err != nil || n != 1 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	if _, ok := f.env["env_b"]; !ok || len(f.env) != 1 {
		t.Errorf("settings left: %v", f.env)
	}
	if n, err := v.DeleteEnv(context.Background(), "poc", secretpkg.EnvDeployToken); err != nil || n != 0 {
		t.Errorf("a second delete: %d, %v; want nothing to do", n, err)
	}
}

// ⭐ A renewal mints a new token and replaces the setting, and it does NOT yet
// revoke the token the earlier handover left: the live deployment still
// publishes with it. It records both ids, so the revocation can follow the
// deploy. Nothing echoes a value.
func TestARenewedDeployTokenWaitsToRevokeTheEarlierOne(t *testing.T) {
	f, v := newFakeTokenAPI(t)
	ctx := context.Background()
	inst := Instance{ID: "prj_1", Name: "poc"}
	first, err := v.IssueDeployToken(ctx, inst, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := VercelDeployTokenHandover(time.Hour)(ctx, v, inst); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 0 {
		t.Errorf("revoked %v at the handover; the earlier token must stay live until the new code is activated", f.revoked)
	}
	if want := "tok_2 " + first.ID; f.values[deployTokenIDSetting] != want || f.types[deployTokenIDSetting] != "plain" {
		t.Errorf("%s = %q (%s); want %q, plain", deployTokenIDSetting, f.values[deployTokenIDSetting], f.types[deployTokenIDSetting], want)
	}
	n := 0
	for _, k := range f.env {
		if k == secretpkg.EnvDeployToken {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d %s settings after a renewal; want one", n, secretpkg.EnvDeployToken)
	}
	if f.values[deployTokenIDSetting] == mintedBearer {
		t.Error("⛔ the id setting holds the token")
	}
}

// ⭐ The handover refuses a platform that is not Vercel, rather than skipping.
func TestTheVercelHandoverRefusesAnotherPlatform(t *testing.T) {
	if err := VercelDeployTokenHandover(0)(context.Background(), &fakePlatform{}, Instance{ID: "x"}); err == nil {
		t.Error("the handover ran on a platform that cannot take it")
	}
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }
