package server

import (
	"context"
	"crypto/sha1" //nolint:gosec // G505: SHA-1 is what Vercel's file-upload API names files by; not used for integrity or signing
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/deploy"
	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
	"github.com/vdibart/polis-cli/cli-go/pkg/secret"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ ON VERCEL, PUBLISHING IS A DEPLOY. The site's public tree reaches
// readers as the static files of a new production deployment, made from the
// private store at the end of each request that wrote a public file (pkg/deploy
// decides when and who; provision.Deployer makes it). This file is the
// instance's half: what it deploys with.

const (
	// deployTokenKey holds the instance's project-only deploy token, minted
	// at provisioning. ⛔ Held in the private store, never the environment:
	// the provisioner hands it over through a write-only setting, which the
	// instance adopts and deletes (adoptDeployToken). ⛔ Under .polis, so it
	// is never a static file.
	deployTokenKey = ".polis/deploy/token" //nolint:gosec // G101: a setting's or path's name, not a credential

	// deployTokenAdoptedKey lists the SHA-256 of every token adopted from the
	// setting, so a deployment older than a setting's deletion (a rollback)
	// cannot hand an old token back over a newer one.
	deployTokenAdoptedKey = deployTokenKey + "-adopted.json"

	// vercelProjectEnv names the instance's own project (plain, set at
	// provisioning), and vercelTeamEnv its team when the project is a team's.
	vercelProjectEnv = "POLIS_VERCEL_PROJECT"
	vercelTeamEnv    = "POLIS_VERCEL_TEAM"
)

// errNoDeployToken says why a site cannot publish yet.
var errNoDeployToken = errors.New("deploy: this instance has no deploy token")

// errDeployWaiting says the platform asked for a wait that has not passed.
var errDeployWaiting = errors.New("deploy: waiting for the platform's rate limit")

// deployStatusKey records the last deploy and why the site is behind, for the
// owner. ⭐ Written only by the deploy, which runs under the lease, so it has
// one writer. ⛔ Under .polis: never a static file.
const deployStatusKey = ".polis/deploy/status.json"

// deployFunctionKey records the function release the owner applied, which
// every deploy carries until the owner applies another. ⛔ Written only by
// the owner's apply. Absent, the instance deploys its own version.
const deployFunctionKey = ".polis/deploy/function.json"

type appliedFunction struct {
	Version   string    `json:"version"`
	AppliedAt time.Time `json:"applied_at"`
}

// defaultRateLimitWait is the wait after a rate limit that named none.
const defaultRateLimitWait = time.Minute

type deployStatus struct {
	LastDeployID  string     `json:"last_deploy_id,omitempty"`
	LastDeployURL string     `json:"last_deploy_url,omitempty"`
	LastDeployAt  *time.Time `json:"last_deploy_at,omitempty"`
	// LastDeployKey is the identity key the last deployment published, so a
	// deploy that cannot read the live site may proceed when it publishes
	// the same key again (see guardLiveIdentity).
	LastDeployKey string     `json:"last_deploy_key,omitempty"`
	PublicFiles   int        `json:"public_files"`
	LastError     string     `json:"last_error,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
	RetryAfter    *time.Time `json:"retry_after,omitempty"`
	// TokenSettingLeft says a handed-over token was adopted but its project
	// setting could not be deleted, so the owner must delete it.
	TokenSettingLeft bool `json:"token_setting_left,omitempty"`
}

func (s *Server) readDeployStatus(ctx context.Context, store storage.Store) deployStatus {
	var st deployStatus
	if b, err := store.Get(ctx, deployStatusKey); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

// recordDeploy writes what a deploy did. ⛔ An error is recorded by its
// message, which names causes and never a credential (every error on this
// path is written that way).
func (s *Server) recordDeploy(ctx context.Context, store storage.Store, st deployStatus, res provision.DeployResult, key string, err error) {
	now := time.Now().UTC()
	if err == nil {
		st.LastDeployID, st.LastDeployURL, st.LastDeployAt = res.ID, res.URL, &now
		st.LastDeployKey = key
		st.PublicFiles = res.Public
		st.LastError, st.LastErrorAt, st.RetryAfter = "", nil, nil
	} else {
		st.LastError, st.LastErrorAt = err.Error(), &now
		if wait, limited := provision.RateLimited(err); limited {
			if wait <= 0 {
				wait = defaultRateLimitWait
			}
			until := now.Add(wait)
			st.RetryAfter = &until
		}
	}
	if b, merr := json.Marshal(st); merr == nil {
		if perr := store.Put(ctx, deployStatusKey, b); perr != nil {
			log.Printf("[warning] deploy: could not record the deploy status: %v", perr)
		}
	}
}

// handleDeployStatus tells the owner whether the site readers see is the site
// the instance holds. ⭐ GET only, owner only (every /api/ route not listed
// public is). ⭐ It is also the takeover: when writes are waiting and no live
// holder has the lease, the poll deploys them itself, so a holder that died
// does not leave the site behind until the next write.
func (s *Server) handleDeployStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	q := s.deployQueue
	if q == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "not-applicable", "publishes_by_deploy": false})
		return
	}
	ctx := r.Context()
	store := s.Site().Store()
	marks, lerr := q.Dirty(ctx)
	if lerr == nil && len(marks) > 0 {
		if exp, held := q.LeaseExpiry(ctx); !held || time.Now().After(exp) {
			st := s.readDeployStatus(ctx, store)
			if st.RetryAfter == nil || time.Now().After(*st.RetryAfter) {
				dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
				q.Run(dctx)
				cancel()
				marks, lerr = q.Dirty(ctx)
			}
		}
	}
	st := s.readDeployStatus(ctx, store)
	out := map[string]any{
		"publishes_by_deploy": true,
		"waiting":             len(marks),
		"public_files":        st.PublicFiles,
		"max_files":           provision.MaxDeploymentFiles,
	}
	switch {
	case lerr != nil:
		out["state"] = "unknown"
		out["reason"] = "cannot read what is waiting"
	case len(marks) == 0:
		out["state"] = "live"
	default:
		out["state"] = "behind"
		if t, ok := deploy.MarkTime(marks[0]); ok {
			out["behind_since"] = t.UTC()
		}
		if exp, held := q.LeaseExpiry(ctx); held && time.Now().Before(exp) {
			out["state"] = "publishing"
		}
	}
	if v, err := s.appliedFunction(ctx, store); err == nil && v != "" {
		out["function_version"], out["function_applied"] = v, true
	} else if err == nil {
		out["function_version"] = s.CLIVersion
	}
	if st.LastDeployAt != nil {
		out["last_deploy"] = map[string]any{"id": st.LastDeployID, "url": st.LastDeployURL, "at": st.LastDeployAt}
	}
	if st.LastError != "" && len(marks) > 0 {
		out["reason"] = st.LastError
	}
	if st.RetryAfter != nil && time.Now().Before(*st.RetryAfter) {
		out["retry_after"] = st.RetryAfter
	}
	if ok, _ := store.Exists(ctx, deployTokenKey); !ok {
		out["token"] = "missing"
		if s.deployGetenv != nil && strings.TrimSpace(s.deployGetenv(secret.EnvDeployToken)) != "" {
			out["token"] = "handed-over" // adopted at the next deploy
		}
	}
	if st.TokenSettingLeft {
		out["token_setting_left"] = true
	}
	_ = json.NewEncoder(w).Encode(out)
}

// withDeployQueue puts a deploy queue in front of an instance's store when it
// runs on Vercel, and returns the store to use.
func (s *Server) withDeployQueue(b storage.Backend, getenv func(string) string) storage.Backend {
	if strings.TrimSpace(getenv(vercelRuntimeEnv)) == "" {
		return b
	}
	// ⭐ Every store call is counted and timed, the queue's and the deploy's
	// included, so a slow publish's log line can say where the time went.
	raw := &timedStore{Store: b.Store}
	q := &deploy.Queue{Store: raw, IsPublic: provision.IsPublicKey}
	q.Deployer = deploy.DeployFunc(func(ctx context.Context) error {
		return s.deploySite(ctx, raw, getenv)
	})
	s.deployQueue = q
	s.deployGetenv = getenv
	s.deployStore = raw
	b.Store = q.Track(raw)
	return b
}

// timedStore counts the calls made to a store and the time they take.
//
// ⚠️ Process-wide, not per request: a request's share is the difference
// between two snapshots, which is exact only when the process serves one
// request at a time. It is a diagnostic, never a decision input.
type timedStore struct {
	storage.Store
	calls atomic.Int64
	nanos atomic.Int64

	// trace, when set, sees every call's kind and key: for a test that counts
	// what an operation reads and writes. Nil in production.
	trace func(kind, key string)
}

func (t *timedStore) since(start time.Time) {
	t.calls.Add(1)
	t.nanos.Add(int64(time.Since(start)))
}

func (t *timedStore) traced(kind, key string) {
	if t.trace != nil {
		t.trace(kind, key)
	}
}

// snapshot is the count and total time of the calls so far.
func (t *timedStore) snapshot() (int64, time.Duration) {
	return t.calls.Load(), time.Duration(t.nanos.Load())
}

func (t *timedStore) Get(ctx context.Context, key string) ([]byte, error) {
	t.traced("Get", key)
	defer t.since(time.Now())
	return t.Store.Get(ctx, key)
}

func (t *timedStore) Put(ctx context.Context, key string, data []byte, opts ...storage.PutOption) error {
	t.traced("Put", key)
	defer t.since(time.Now())
	return t.Store.Put(ctx, key, data, opts...)
}

func (t *timedStore) Exists(ctx context.Context, key string) (bool, error) {
	t.traced("Exists", key)
	defer t.since(time.Now())
	return t.Store.Exists(ctx, key)
}

func (t *timedStore) Stat(ctx context.Context, key string) (storage.Info, error) {
	t.traced("Stat", key)
	defer t.since(time.Now())
	return t.Store.Stat(ctx, key)
}

func (t *timedStore) List(ctx context.Context, prefix string, opts ...storage.ListOption) ([]storage.Entry, error) {
	t.traced("List", prefix)
	defer t.since(time.Now())
	return t.Store.List(ctx, prefix, opts...)
}

func (t *timedStore) Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error {
	t.traced("Delete", key)
	defer t.since(time.Now())
	return t.Store.Delete(ctx, key, opts...)
}

// deployIfNeverDeployed records a change when the site's store holds a site
// but has never been deployed from (it has no public-tree manifest), so the
// request that started the instance carries its first deploy.
//
// ⭐ THIS IS HOW A SEEDED SITE REACHES READERS. A seed is written into the
// store from outside, before the first start, so no write of the instance's
// own marks it, and the provisioner's first deployment carries the function
// alone: until a deploy, every page of the site would be answered by the
// function's fallback. ⚠️ A manifest lost on a site that WAS deployed is
// rebuilt by the same deploy, which republishes the site as the store holds it.
func (s *Server) deployIfNeverDeployed(ctx context.Context) {
	if s.deployQueue == nil {
		return
	}
	st := s.Site().Store()
	if ok, err := st.Exists(ctx, site.WellKnownKey); err != nil || !ok {
		return
	}
	if ok, err := st.Exists(ctx, provision.PublicManifestKey); err != nil || ok {
		return
	}
	// ⭐ Not when a deploy is already waiting or under way: that deploy writes
	// the manifest and carries the whole site. A second instance started
	// while the first deployed (a platform starts one per concurrent request)
	// used to see no manifest yet, leave a mark, and the first holder's
	// re-check then made a second, identical deployment. ⚠️ An unreadable
	// queue still marks: a needless deploy is cheaper than a missed one.
	if marks, err := s.deployQueue.Dirty(ctx); err == nil && len(marks) > 0 {
		return
	}
	if exp, held := s.deployQueue.LeaseExpiry(ctx); held && time.Now().Before(exp) {
		return
	}
	log.Printf("[i] deploy: this site has never been deployed from its store; deploying it")
	s.deployQueue.Changed()
}

// deployMiddleware runs the deploy queue, when there is one, and logs how a
// request's time split between its handler and the deploy that followed it.
func (s *Server) deployMiddleware(next http.Handler) http.Handler {
	if s.deployQueue == nil {
		return next
	}
	type split struct {
		handlerDone time.Time
		calls       int64
		storeTime   time.Duration
	}
	type splitKey struct{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if sp, ok := r.Context().Value(splitKey{}).(*split); ok {
			sp.handlerDone = time.Now()
			if s.deployStore != nil {
				sp.calls, sp.storeTime = s.deployStore.snapshot()
			}
		}
	})
	queued := s.deployQueue.Middleware(inner)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var c0 int64
		var t0 time.Duration
		if s.deployStore != nil {
			c0, t0 = s.deployStore.snapshot()
		}
		sp := &split{}
		queued.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), splitKey{}, sp)))
		end := time.Now()
		if sp.handlerDone.IsZero() {
			return
		}
		after := end.Sub(sp.handlerDone)
		// ⭐ Every write, and any request a deploy followed (a first start).
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if after < 100*time.Millisecond {
				return
			}
		}
		var c2 int64
		var t2 time.Duration
		if s.deployStore != nil {
			c2, t2 = s.deployStore.snapshot()
		}
		ms := func(d time.Duration) time.Duration { return d.Round(time.Millisecond) }
		log.Printf("[i] timing: %s %s request_id=%s took %s: handler %s (store: %d calls, %s), then deploy %s (store: %d calls, %s)", //nolint:gosec // G706: newlines are escaped by server.OneLinePerLogEntry, so a value cannot start a second entry
			r.Method, r.URL.Path, RequestIDFromContext(r.Context()), ms(end.Sub(start)),
			ms(sp.handlerDone.Sub(start)), sp.calls-c0, ms(sp.storeTime-t0),
			ms(after), c2-sp.calls, ms(t2-sp.storeTime))
	})
}

// deploySite makes one deployment of the site as the store holds it now, and
// records what happened for the owner.
//
// ⛔ An error names the cause, never the token.
func (s *Server) deploySite(ctx context.Context, store storage.Store, getenv func(string) string) error {
	st := s.readDeployStatus(ctx, store)
	if st.RetryAfter != nil && time.Now().Before(*st.RetryAfter) {
		// ⭐ Not asked again before the platform said to: asking early is
		// refused and may extend the wait. ⚠️ Not recorded: the wait is.
		return fmt.Errorf("%w until %s", errDeployWaiting, st.RetryAfter.UTC().Format(time.RFC3339))
	}
	started := time.Now()
	if adopted, aerr := s.adoptDeployToken(ctx, store, getenv); adopted {
		st.TokenSettingLeft = aerr != nil
	} else if aerr != nil {
		log.Printf("[warning] deploy: %v", aerr)
	} else if strings.TrimSpace(getenv(secret.EnvDeployToken)) == "" {
		st.TokenSettingLeft = false // this deployment carries no setting
	}
	res, key, err := s.deployOnce(ctx, store, getenv, st)
	s.recordDeploy(ctx, store, st, res, key, err)
	if err != nil {
		log.Printf("[warning] deploy: the site was not deployed and stays behind: %v", err)
		return err
	}
	log.Printf("[i] deploy: %s with %d public files (%d read, %d uploaded) in %s (%s)",
		res.ID, res.Public, res.Hashed, res.Uploaded, time.Since(started).Round(time.Millisecond), res.Took)
	return nil
}

func (s *Server) deployOnce(ctx context.Context, store storage.Store, getenv func(string) string, st deployStatus) (provision.DeployResult, string, error) {
	var none provision.DeployResult
	project := strings.TrimSpace(getenv(vercelProjectEnv))
	if project == "" {
		return none, "", fmt.Errorf("deploy: %s is unset, so this instance does not know its own project", vercelProjectEnv)
	}
	raw, err := store.Get(ctx, deployTokenKey)
	if errors.Is(err, storage.ErrNotFound) {
		return none, "", errNoDeployToken
	}
	if err != nil {
		return none, "", fmt.Errorf("deploy: cannot read the deploy token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return none, "", errNoDeployToken
	}
	key, err := s.guardLiveIdentity(ctx, store, st)
	if err != nil {
		return none, "", err
	}
	fn, source, err := s.functionManifest(ctx, store, getenv)
	if err != nil {
		return none, "", err
	}
	v, err := provision.NewVercel(provision.VercelConfig{Token: token, TeamID: strings.TrimSpace(getenv(vercelTeamEnv)), BaseURL: s.deployAPIBase})
	if err != nil {
		return none, "", err
	}
	d := provision.Deployer{Store: store, Vercel: v, Project: project, Function: fn, FunctionFile: source}
	res, err := d.Deploy(ctx)
	return res, key, err
}

// liveWellKnownLimit bounds the live .well-known/polis read before a deploy.
const liveWellKnownLimit = 1 << 20

// guardLiveIdentity refuses a deploy that would replace the identity readers
// see with one the store's own history does not name (site.CheckLiveIdentity),
// and returns the identity key this deploy publishes.
//
// ⛔ A live copy that cannot be read refuses too, unless the last deployment
// from this store published this same key: then the deploy changes nothing
// about the identity, and an outage of the live site must not stop the
// publish that fixes it. A store that is empty or replaced has no such record,
// so it is refused.
//
// ⚠️ Inline here for now; a pre-publish hook in the storage API is the
// intended home.
func (s *Server) guardLiveIdentity(ctx context.Context, store storage.Store, st deployStatus) (string, error) {
	src, err := store.Get(ctx, site.WellKnownKey)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		src = nil
	case err != nil:
		return "", fmt.Errorf("deploy: cannot read %s to check the site's identity: %w", site.WellKnownKey, err)
	}
	key := publishedKeyString(src)
	live, lerr := s.fetchLiveWellKnown(ctx)
	if lerr != nil {
		if key != "" && key == st.LastDeployKey {
			log.Printf("[warning] deploy: %v; publishing anyway, because the last deployment published this same identity key", lerr)
			return key, nil
		}
		return "", fmt.Errorf("deploy: %w; not publishing, because this store has not published this identity before", lerr)
	}
	if err := site.CheckLiveIdentity(src, live); err != nil {
		return "", fmt.Errorf("deploy: %w", err)
	}
	return key, nil
}

// publishedKeyString is the identity key a .well-known/polis names, in a form
// two documents can be compared by; "" when there is none.
func publishedKeyString(doc []byte) string {
	if doc == nil {
		return ""
	}
	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if json.Unmarshal(doc, &wk) != nil {
		return ""
	}
	k, err := signing.ParsePublicKey([]byte(wk.PublicKey))
	if err != nil {
		return ""
	}
	return hex.EncodeToString(k)
}

// fetchLiveWellKnown reads the .well-known/polis readers are served now; nil
// when the live site serves none.
func (s *Server) fetchLiveWellKnown(ctx context.Context) ([]byte, error) {
	base := s.deployLiveBase
	if base == "" {
		base = s.GetBaseURL()
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return nil, fmt.Errorf("the instance's base URL is unset, so the live %s cannot be read", site.WellKnownKey)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+site.WellKnownKey, nil)
	if err != nil {
		return nil, fmt.Errorf("the live %s cannot be read: %w", site.WellKnownKey, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the live %s cannot be read: %w", site.WellKnownKey, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("the live %s answered HTTP %d", site.WellKnownKey, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, liveWellKnownLimit+1))
	if err != nil {
		return nil, fmt.Errorf("the live %s cannot be read: %w", site.WellKnownKey, err)
	}
	if len(b) > liveWellKnownLimit {
		return nil, fmt.Errorf("the live %s is larger than any such document", site.WellKnownKey)
	}
	return b, nil
}

// functionManifest resolves the function to deploy beside the pages: the
// release the owner applied, else a development build's manifest from the
// environment, else the published manifest of this build's own release.
//
// ⭐ The applied release comes first, so a publish never deploys an older
// function than the owner chose, even from an instance still running the
// older code while the update deploys.
func (s *Server) functionManifest(ctx context.Context, store storage.Store, getenv func(string) string) (provision.FunctionManifest, func(context.Context, provision.DeployFile) ([]byte, error), error) {
	applied, err := s.appliedFunction(ctx, store)
	if err != nil {
		return provision.FunctionManifest{}, nil, err
	}
	if applied != "" {
		return s.trustedReleaseFunction(ctx, store, applied, getenv)
	}
	if inline := strings.TrimSpace(getenv(provision.FunctionManifestEnv)); inline != "" {
		m, err := provision.ParseFunctionManifest([]byte(inline))
		// ⭐ No source: a development build's files were uploaded when it
		// was deployed, and Vercel keeps them by digest.
		return m, nil, err
	}
	return s.trustedReleaseFunction(ctx, store, s.CLIVersion, getenv)
}

// functionPinPrefix holds one file per release version: the fingerprint of
// the first manifest this instance accepted for it.
const functionPinPrefix = ".polis/deploy/function-pins/"

// trustedReleaseFunction is releaseFunction with the checks that stand in for
// a signature the release does not carry. A release's assets are fetched from
// GitHub on every deploy, and the function reads the private store (the
// identity key, the deploy token), so whoever could replace them would take
// over every instance on that version at its next publish.
//
//   - ⛔ PINNED ON FIRST USE: the first manifest accepted for a version is
//     recorded (a conditional create, so two cold starts cannot record two),
//     and a different one for the same version is refused from then on.
//   - ⛔ THE RUNNING BUILD IS THE ANCHOR for the instance's own version: on
//     Vercel the executable is the deployed bootstrap, so a manifest for this
//     version must name exactly its digest.
//
// ⚠️ Neither covers a release tampered with before an instance first fetches
// it. Signing the manifest with a release key would.
func (s *Server) trustedReleaseFunction(ctx context.Context, store storage.Store, version string, getenv func(string) string) (provision.FunctionManifest, func(context.Context, provision.DeployFile) ([]byte, error), error) {
	m, src, err := s.releaseFunction(ctx, version)
	if err != nil {
		return m, src, err
	}
	if strings.TrimPrefix(version, "v") == strings.TrimPrefix(s.CLIVersion, "v") {
		running, err := s.bootstrapSHA(getenv)
		if err != nil {
			return provision.FunctionManifest{}, nil, fmt.Errorf("deploy: cannot read the running function to check the release against it: %w", err)
		}
		if running != "" && m.BootstrapSHA() != running {
			return provision.FunctionManifest{}, nil, fmt.Errorf("deploy: the release of %s now publishes a function that is not the one running here; refusing it (the release may have been replaced)", version)
		}
	}
	if err := pinFunctionManifest(ctx, store, version, m); err != nil {
		return provision.FunctionManifest{}, nil, err
	}
	return m, src, nil
}

// pinFunctionManifest records m's fingerprint for version if none is, and
// refuses m if a different one is.
func pinFunctionManifest(ctx context.Context, store storage.Store, version string, m provision.FunctionManifest) error {
	key := functionPinPrefix + strings.TrimPrefix(version, "v")
	fp := m.Fingerprint()
	err := store.Put(ctx, key, []byte(fp+"\n"), storage.IfAbsent(), storage.WithMode(0o600))
	if err == nil {
		return nil
	}
	if !errors.Is(err, storage.ErrExists) {
		return fmt.Errorf("deploy: cannot record the release's function: %w", err)
	}
	b, err := store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("deploy: cannot read the recorded function of %s: %w", version, err)
	}
	if strings.TrimSpace(string(b)) != fp {
		return fmt.Errorf("deploy: the release of %s now publishes a different function than the one this instance accepted; refusing it (the release may have been replaced)", version)
	}
	return nil
}

// bootstrapSHA is the SHA-1 of the running executable when it is a deployed
// function (on Vercel), else "" (nothing to compare against).
func (s *Server) bootstrapSHA(getenv func(string) string) (string, error) {
	if s.runningBootstrapSHA != nil {
		return s.runningBootstrapSHA()
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if strings.TrimSpace(getenv(vercelRuntimeEnv)) == "" {
		return "", nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(exe) //nolint:gosec // G304: NOT the storage interface: the running function's own executable, not a site key
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(b) //nolint:gosec // G401: SHA-1 is what Vercel's file-upload API names files by; not used for integrity or signing
	return hex.EncodeToString(sum[:]), nil
}

// releaseFunction is a release's function manifest and the source of its
// files. ⭐ s.releaseFunctionFor replaces it in tests.
func (s *Server) releaseFunction(ctx context.Context, version string) (provision.FunctionManifest, func(context.Context, provision.DeployFile) ([]byte, error), error) {
	if s.releaseFunctionFor != nil {
		return s.releaseFunctionFor(ctx, version)
	}
	m, err := provision.FetchFunctionManifest(ctx, nil, version)
	if err != nil {
		return provision.FunctionManifest{}, nil, err
	}
	return m, provision.ReleaseFunctionFiles(nil, version), nil
}

// appliedFunction is the version the owner applied, or "" when none.
// ⛔ An unreadable record refuses the deploy rather than falling back to the
// instance's own version, which could be older than the one applied.
func (s *Server) appliedFunction(ctx context.Context, store storage.Store) (string, error) {
	b, err := store.Get(ctx, deployFunctionKey)
	if errors.Is(err, storage.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("deploy: cannot read the applied function release: %w", err)
	}
	var a appliedFunction
	if json.Unmarshal(b, &a) != nil || !provision.IsReleasedVersion(a.Version) {
		return "", fmt.Errorf("deploy: %s does not name a released version; not deploying a function nobody chose", deployFunctionKey)
	}
	return a.Version, nil
}

// adoptDeployToken moves a handed-over deploy token from its write-only
// project setting into the private store, then deletes the setting with the
// token itself. It reports whether a token was adopted.
//
// ⭐ A newer handover replaces the stored token (a renewal). ⛔ One already
// adopted never is again: a deployment created before its setting was deleted
// still carries it, and promoting that deployment must not undo a renewal.
//
// ⛔ The token is named by its setting and its key, never its value.
func (s *Server) adoptDeployToken(ctx context.Context, store storage.Store, getenv func(string) string) (bool, error) {
	res, err := secret.Resolve(s.effectiveMode(), secret.RemoteSet(), getenv)
	if err != nil {
		return false, err
	}
	v := res.Get(secret.EnvDeployToken)
	if v.IsZero() {
		return false, nil
	}
	// ⭐ Exposed here only: written to the store, hashed, and used once to
	// delete its own setting; never kept past this function.
	token := strings.TrimSpace(v.Expose())
	sum := sha256.Sum256([]byte(token))
	digest := hex.EncodeToString(sum[:])
	var adopted []string
	if b, gerr := store.Get(ctx, deployTokenAdoptedKey); gerr == nil {
		_ = json.Unmarshal(b, &adopted)
	} else if !errors.Is(gerr, storage.ErrNotFound) {
		return false, fmt.Errorf("cannot read which deploy tokens were adopted, so %s is not adopted: %w", secret.EnvDeployToken, gerr)
	}
	for _, d := range adopted {
		if d == digest {
			return false, nil
		}
	}
	if err := store.Put(ctx, deployTokenKey, []byte(token), storage.WithMode(0o600)); err != nil {
		return false, fmt.Errorf("cannot store the deploy token from %s: %w", secret.EnvDeployToken, err)
	}
	b, _ := json.Marshal(append(adopted, digest))
	if err := store.Put(ctx, deployTokenAdoptedKey, b); err != nil {
		log.Printf("[warning] deploy: could not record the adopted deploy token, so an older deployment could hand it back: %v", err)
	}
	log.Printf("[i] deploy: adopted the deploy token from %s into %s", secret.EnvDeployToken, deployTokenKey)
	project := strings.TrimSpace(getenv(vercelProjectEnv))
	vc, err := provision.NewVercel(provision.VercelConfig{Token: token, TeamID: strings.TrimSpace(getenv(vercelTeamEnv)), BaseURL: s.deployAPIBase})
	if err == nil {
		_, err = vc.DeleteEnv(ctx, project, secret.EnvDeployToken)
	}
	if err != nil {
		log.Printf("[warning] deploy: the deploy token was adopted but its %s setting could not be deleted; delete it in the project's settings: %v",
			secret.EnvDeployToken, err)
		return true, err
	}
	return true, nil
}
