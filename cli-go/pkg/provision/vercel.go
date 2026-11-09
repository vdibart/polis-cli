package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	secretpkg "github.com/vdibart/polis-cli/cli-go/pkg/secret"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Vercel provisions on Vercel.
//
// ⛔⛔ THIS FILE IS THE ONLY THING IN THE TREE THAT KNOWS ABOUT VERCEL. Every
// endpoint, every field name, every quirk lives here; a caller sees only the
// Provisioner interface. ⚠️ If a Vercel concept starts appearing in a caller,
// the seam has leaked and the fix is here, not there.
//
// ⛔ NO GIT. PutCode uploads the files of a prebuilt deployment directly, which
// the A0 spike proved possible — so none of Vercel's repository integration is
// reachable from this path, and a user needs no GitHub account.
type Vercel struct {
	token  string
	teamID string
	base   string
	hc     *http.Client

	// pollEvery is Activate's first polling interval; zero is the default.
	// ⚠️ For tests.
	pollEvery time.Duration

	// openSeedStore overrides how Seed opens a store from its token, for
	// tests. Nil is the real Blob client.
	openSeedStore func(secretpkg.Value) (storage.Store, string, error)

	// mu guards renewals and retired: the deploy-token handovers this
	// process made, waiting for their deployment to be activated, and what
	// Activate then revoked.
	mu       sync.Mutex
	renewals map[string]*tokenRenewal
	retired  map[string]DeployTokenRetirement
}

// VercelConfig is what the provisioner needs.
type VercelConfig struct {
	// Token is a Vercel API token. ⛔ A secret; read through pkg/secret.
	Token string

	// TeamID scopes every call to a team, when the token belongs to one.
	TeamID string

	// BaseURL overrides the API endpoint, for tests.
	BaseURL string

	// HTTPClient overrides the client, for tests and timeout control.
	HTTPClient *http.Client
}

// NewVercel returns a Provisioner backed by Vercel.
func NewVercel(cfg VercelConfig) (*Vercel, error) {
	if cfg.Token == "" {
		// ⛔ Refuse rather than defaulting to an anonymous client: a
		// provisioner that constructs without credentials fails partway
		// through a flow, having already created something.
		return nil, fmt.Errorf("provision: vercel needs an API token")
	}
	base := cfg.BaseURL
	if base == "" {
		base = "https://api.vercel.com"
	}
	hc := cfg.HTTPClient
	if hc == nil {
		// ⚠️ Longer than a request timeout: a deployment upload carries a
		// build archive.
		hc = &http.Client{Timeout: 120 * time.Second}
	}
	return &Vercel{
		token:  cfg.Token,
		teamID: cfg.TeamID,
		base:   strings.TrimSuffix(base, "/"),
		hc:     hc,
	}, nil
}

func (v *Vercel) Name() string { return "vercel" }

// withTeam adds the team scope every call needs when the token is a team's.
func (v *Vercel) withTeam(path string) string {
	if v.teamID == "" {
		return v.base + path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return v.base + path + sep + "teamId=" + url.QueryEscape(v.teamID)
}

func (v *Vercel) call(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, v.withTeam(path), r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+v.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := v.hc.Do(req)
	if err != nil {
		return fmt.Errorf("vercel %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// ⛔ ONLY THE STATUS AND VERCEL'S ERROR CODE, never the body. The body
		// used to be echoed (1 KiB) into errors that reach logs, and an API that
		// ever echoed a submitted value or header would have written a secret
		// there. The code ("forbidden", "not_found") is enough to look up.
		return &apiError{method: method, path: path, status: resp.StatusCode, code: errorCode(resp.Body), retryAfter: retryAfter(resp)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// provisionedMarker is the plain project setting Create writes on a project
// it made. ⭐ It is how a re-run tells its own half-finished project from a
// stranger's project that happens to share the name.
const provisionedMarker = "POLIS_PROVISIONED"

// defaultStoreRegion is where the store is made when the request names no
// region. ⚠️ The region the store API was measured with, not a mapping.
const defaultStoreRegion = "iad1"

// SiteStoreName is the name of an instance's private store: the project's
// name with "-site", after what it holds (the whole site, .polis included).
func SiteStoreName(project string) string { return project + "-site" }

// Create makes the instance: the project and its ONE private Blob store. On
// Vercel "an instance" is both, so both are made here rather than in a
// separate operation.
//
// ⭐ A RE-RUN RESUMES. A project with the requested name that carries this
// provisioner's marker is adopted (Existing), and a store already connected to
// it is adopted. ⛔ An unmarked project is someone else's and is refused by
// name: a name collision never takes over a stranger's project.
//
// ⛔ The store is private or nothing: adopting a public store is refused, as
// is a project with more than one store connected. No public store is ever
// made (publish = deploy: the public surface is the deployment's files).
func (v *Vercel) Create(ctx context.Context, req CreateRequest) (Instance, error) {
	if req.Name == "" {
		return Instance{}, fmt.Errorf("provision: create needs a name")
	}
	inst, err := v.createProject(ctx, req.Name)
	if err != nil {
		return Instance{}, err
	}
	store, err := v.ensureStore(ctx, inst, req.Region)
	if err != nil {
		// ⭐ The project is marked, so a re-run adopts it and makes the store.
		return inst, fmt.Errorf("provision: the project %s exists but its store is not ready, so provisioning again resumes here: %w", inst.Name, err)
	}
	inst.Store = store
	return inst, nil
}

// createProject makes the project, or adopts one this provisioner made.
func (v *Vercel) createProject(ctx context.Context, name string) (Instance, error) {
	body := map[string]any{"name": name}
	// ⛔ No framework and no gitRepository: this is a direct-upload project, so
	// Vercel must not try to infer a build from a repo that does not exist.
	body["framework"] = nil
	var out struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	err := v.call(ctx, http.MethodPost, "/v11/projects", body, &out)
	if isStatus(err, http.StatusConflict) {
		return v.adoptProject(ctx, name)
	}
	if err != nil {
		return Instance{}, err
	}
	// ⚠️ Vercel normalises project names, so the Instance carries what Vercel
	// called it rather than what was asked for.
	inst := Instance{ID: out.ID, Name: out.Name}
	// ⭐ Marked at once, before anything else can fail, so a re-run after any
	// later failure recognises the project as its own.
	if err := v.setPlain(ctx, inst.ID, provisionedMarker, "1"); err != nil {
		// ⛔ An unmarked project of ours would read as a stranger's on a
		// re-run, so it is removed rather than left.
		if derr := v.deleteProject(ctx, inst.ID); derr != nil {
			return Instance{}, fmt.Errorf("provision: marking the new project %s: %w; and deleting it failed, so delete it by hand before provisioning again: %v", inst.Name, err, derr)
		}
		return Instance{}, fmt.Errorf("provision: marking the new project %s: %w (the project was deleted)", inst.Name, err)
	}
	return inst, nil
}

// adoptProject adopts the existing project called name, only if it carries
// this provisioner's marker.
func (v *Vercel) adoptProject(ctx context.Context, name string) (Instance, error) {
	var proj struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := v.call(ctx, http.MethodGet, "/v9/projects/"+url.PathEscape(name), nil, &proj); err != nil {
		return Instance{}, fmt.Errorf("provision: a project named %s exists and could not be read: %w", name, err)
	}
	if proj.ID == "" {
		return Instance{}, fmt.Errorf("provision: a project named %s exists and could not be read", name)
	}
	keys, err := v.envKeys(ctx, proj.ID)
	if err != nil {
		return Instance{}, fmt.Errorf("provision: a project named %s exists, and whether polis made it could not be checked: %w", name, err)
	}
	if !keys[provisionedMarker] {
		return Instance{}, fmt.Errorf("provision: a project named %s already exists and was not made by polis provisioning; refusing to take it over, so choose another name", name)
	}
	if proj.Name == "" {
		proj.Name = name
	}
	return Instance{ID: proj.ID, Name: proj.Name, Existing: true}, nil
}

// blobStore is one store as the store listing reports it.
//
// ⚠️ The listing's shape (GET /v1/storage/stores, a store's connected projects
// under projectsMetadata) is from Vercel's API reference and was not measured;
// the access check after it uses the measured GET of one store.
type blobStore struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Projects []struct {
		ProjectID string `json:"projectId"`
	} `json:"projectsMetadata"`
}

// ensureStore returns the id of the instance's one private store, making it
// when the project has none.
func (v *Vercel) ensureStore(ctx context.Context, inst Instance, region string) (string, error) {
	var listed struct {
		Stores []blobStore `json:"stores"`
	}
	if err := v.call(ctx, http.MethodGet, "/v1/storage/stores", nil, &listed); err != nil {
		return "", fmt.Errorf("provision: listing the stores: %w", err)
	}
	name := SiteStoreName(inst.Name)
	var connected []string
	for _, s := range listed.Stores {
		if s.Type != "" && s.Type != "blob" {
			continue
		}
		for _, p := range s.Projects {
			if p.ProjectID == inst.ID && s.ID != "" {
				connected = append(connected, s.ID)
				break
			}
		}
	}
	var id string
	switch len(connected) {
	case 0:
		for _, s := range listed.Stores {
			if s.Name == name && (s.Type == "" || s.Type == "blob") {
				// ⛔ Not adopted and not duplicated: a store of this name that
				// no project holds may be a failed provision's, holding a
				// seeded site and its key.
				return "", fmt.Errorf("provision: a store named %s (%s) exists but is connected to no project; it may hold an earlier provision's site, so empty and delete it, or connect it to %s, before provisioning again", name, s.ID, inst.Name)
			}
		}
		if region == "" {
			region = defaultStoreRegion
		}
		var made struct {
			ID    string `json:"id"`
			Store struct {
				ID string `json:"id"`
			} `json:"store"`
		}
		// ⭐ projectId connects the store as it is made.
		if err := v.call(ctx, http.MethodPost, "/v1/storage/stores/blob", map[string]any{
			"name": name, "access": "private", "region": region, "projectId": inst.ID,
		}, &made); err != nil {
			return "", fmt.Errorf("provision: making the store %s: %w", name, err)
		}
		id = made.Store.ID
		if id == "" {
			id = made.ID
		}
		if id == "" {
			return "", fmt.Errorf("provision: making the store %s returned no id", name)
		}
	case 1:
		id = connected[0]
	default:
		return "", fmt.Errorf("provision: the project %s has %d stores connected (%s); an instance has exactly one, so disconnect the others", inst.Name, len(connected), strings.Join(connected, ", "))
	}
	access, err := v.storeAccess(ctx, id)
	if err != nil {
		return "", err
	}
	if access != "private" {
		return "", fmt.Errorf("provision: the project's store %s is %q, not private; a store that serves its pathnames is never the site's store", id, access)
	}
	return id, nil
}

// storeAccess reads a store's access ("private" or "public").
func (v *Vercel) storeAccess(ctx context.Context, id string) (string, error) {
	var meta struct {
		Store struct {
			Access string `json:"access"`
		} `json:"store"`
	}
	if err := v.call(ctx, http.MethodGet, "/v1/storage/stores/"+url.PathEscape(id), nil, &meta); err != nil {
		return "", fmt.Errorf("provision: reading the store's access: %w", err)
	}
	return meta.Store.Access, nil
}

// envKeys lists the names of a project's settings. ⭐ Names only: no value is
// asked for.
func (v *Vercel) envKeys(ctx context.Context, project string) (map[string]bool, error) {
	envs, err := v.listEnv(ctx, project)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, e := range envs {
		keys[e.Key] = true
	}
	return keys, nil
}

type envEntry struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func (v *Vercel) listEnv(ctx context.Context, project string) ([]envEntry, error) {
	var listed struct {
		Envs []envEntry `json:"envs"`
	}
	if err := v.call(ctx, http.MethodGet, "/v9/projects/"+url.PathEscape(project)+"/env", nil, &listed); err != nil {
		return nil, err
	}
	return listed.Envs, nil
}

// setPlain upserts one plain (readable, non-secret) production setting.
func (v *Vercel) setPlain(ctx context.Context, project, key, value string) error {
	path := fmt.Sprintf("/v10/projects/%s/env?upsert=true", url.PathEscape(project))
	return v.call(ctx, http.MethodPost, path, map[string]any{
		"key": key, "value": value, "type": "plain", "visibility": "config",
		"target": []string{"production"},
	}, nil)
}

// PutCode uploads a prebuilt build (see buildOutputFiles for the model and the
// allow-list) and creates a production deployment naming each file by digest.
// It does not promote it: that is Activate.
func (v *Vercel) PutCode(ctx context.Context, inst Instance, code Code) (Release, error) {
	if len(code.Archive) == 0 {
		return Release{}, fmt.Errorf("provision: put-code needs an archive")
	}
	if code.Format != FormatTarGz && code.Format != FormatZip {
		return Release{}, fmt.Errorf("provision: unknown code format %q", code.Format)
	}
	files, err := buildOutputFiles(code)
	if err != nil {
		return Release{}, err
	}
	// ⭐ Every file is uploaded first, so the deployment never waits on one;
	// Vercel keeps files by digest, so a re-run re-sends nothing new.
	list := make([]DeployFile, 0, len(files))
	for _, f := range files {
		if err := v.upload(ctx, f); err != nil {
			return Release{}, err
		}
		list = append(list, DeployFile{File: f.name, SHA: f.sha, Size: int64(len(f.data)), Mode: f.mode})
	}
	rel, missing, err := v.createDeployment(ctx, inst.Name, list)
	if err != nil {
		return Release{}, err
	}
	if len(missing) > 0 {
		return Release{}, fmt.Errorf("provision: Vercel reports %d files missing that were just uploaded", len(missing))
	}
	v.noteDeployment(inst, rel)
	return rel, nil
}

func (v *Vercel) SetConfig(ctx context.Context, inst Instance, cfg Config) error {
	secret, err := cfg.Classify(secretpkg.IsSecretName)
	if err != nil {
		return err
	}
	for k, val := range cfg.Values {
		// ⭐ Vercel's env API takes a type, so a secret is DECLARED rather than
		// inferred. ⛔ "sensitive" with visibility "secret" is the only
		// WRITE-ONLY type: Vercel's docs (Config and Secret environment
		// variables, 2026-08) say an "encrypted" value is Config, readable by
		// project members in the dashboard and the API and pulled by
		// `vercel env pull`. This used to send "encrypted" for secrets.
		typ, visibility := "plain", "config"
		if secret[k] {
			typ, visibility = "sensitive", "secret"
		}
		body := map[string]any{
			"key":        k,
			"value":      val,
			"type":       typ,
			"visibility": visibility,
			"target":     []string{"production"},
		}
		// ⭐ upsert: provisioning again replaces a value rather than failing on it.
		path := fmt.Sprintf("/v10/projects/%s/env?upsert=true", url.PathEscape(inst.ID))
		if err := v.call(ctx, http.MethodPost, path, body, nil); err != nil {
			// ⛔ Name the KEY, never the value. A secret in an error message
			// ends up in a log, and the log ends up in a ticket.
			return fmt.Errorf("provision: setting %s: %w", k, err)
		}
	}
	return nil
}

func (v *Vercel) AttachDomain(ctx context.Context, inst Instance, domain string) (DomainStatus, error) {
	if domain == "" {
		return DomainStatus{}, fmt.Errorf("provision: attach-domain needs a domain")
	}
	path := fmt.Sprintf("/v10/projects/%s/domains", url.PathEscape(inst.ID))
	var out struct {
		Name         string `json:"name"`
		Verified     bool   `json:"verified"`
		Verification []struct {
			Type   string `json:"type"`
			Domain string `json:"domain"`
			Value  string `json:"value"`
		} `json:"verification"`
	}
	if err := v.call(ctx, http.MethodPost, path, map[string]any{"name": domain}, &out); err != nil {
		return DomainStatus{}, err
	}
	st := DomainStatus{Domain: domain, Attached: true}
	// ⚠️ Vercel issues a managed certificate once the domain verifies, and
	// `verified` is the signal. CertificateReady therefore tracks verification
	// rather than being asserted — a caller polls Status until it is true.
	st.CertificateReady = out.Verified
	for _, ver := range out.Verification {
		st.NeedsDNS = append(st.NeedsDNS, DNSRecord{
			Type: strings.ToUpper(ver.Type), Name: ver.Domain, Value: ver.Value,
		})
	}
	return st, nil
}

// ActivateTimeout bounds Activate when the caller's context sets no deadline.
const ActivateTimeout = 3 * time.Minute

// Activate waits for the deployment to be READY, then makes it production,
// unless it already took production by itself.
//
// ⭐ A production deployment takes the domain once it is ready, and promoting
// one still building is refused (422), so Activate waits rather than leaving
// every caller to poll. It promotes only when the deployment did not take
// production by itself (production is pinned after a rollback).
//
// ⭐ Once the release is production, the deploy tokens a renewal in this
// process replaced are revoked, when this release was deployed after that
// renewal (see IssueDeployToken).
//
// ⛔ ERROR or CANCELED fails with the state named, and so does the deadline:
// the caller's context, or ActivateTimeout when it sets none. Polling backs
// off and honours a rate limit's Retry-After.
func (v *Vercel) Activate(ctx context.Context, inst Instance, rel Release) error {
	if rel.ID == "" {
		return fmt.Errorf("provision: activate needs a release")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ActivateTimeout)
		defer cancel()
	}
	if err := v.waitReady(ctx, rel.ID); err != nil {
		return err
	}
	if v.activated(ctx, inst, rel) {
		v.retireDeployTokens(ctx, inst, rel)
		return nil
	}
	// ⭐ Promotion is its own call: Vercel builds a deployment and then makes
	// it the production one, which is exactly why Activate is separate from
	// PutCode in the interface.
	path := fmt.Sprintf("/v10/projects/%s/promote/%s",
		url.PathEscape(inst.ID), url.PathEscape(rel.ID))
	err := v.call(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		// ⚠️ It may have taken production between the check and the promote,
		// or the instance may have deployed itself over it in that time.
		if v.activated(ctx, inst, rel) {
			v.retireDeployTokens(ctx, inst, rel)
			return nil
		}
		return fmt.Errorf("provision: promoting %s: %w", rel.ID, err)
	}
	v.retireDeployTokens(ctx, inst, rel)
	return nil
}

// waitReady polls a deployment until it is READY.
func (v *Vercel) waitReady(ctx context.Context, id string) error {
	every := v.pollEvery
	if every <= 0 {
		every = 2 * time.Second
	}
	const most = 15 * time.Second
	last := "unknown"
	for {
		var dep struct {
			ReadyState string `json:"readyState"`
		}
		err := v.call(ctx, http.MethodGet, "/v13/deployments/"+url.PathEscape(id), nil, &dep)
		wait := every
		switch {
		case err == nil:
			state := strings.ToUpper(dep.ReadyState)
			switch state {
			case "READY":
				return nil
			case "ERROR", "CANCELED":
				return fmt.Errorf("provision: deployment %s is %s; it was not promoted", id, state)
			}
			if state != "" {
				last = state
			}
		case ctx.Err() != nil:
			// the deadline, reported below
		default:
			if w, limited := RateLimited(err); limited {
				if w > 0 {
					wait = w
				}
			} else {
				var ae *apiError
				if errors.As(err, &ae) && ae.status/100 == 4 {
					return fmt.Errorf("provision: reading deployment %s: %w", id, err)
				}
				// ⭐ A 5xx or a network failure is retried until the deadline.
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("provision: deployment %s was still %s when the deadline passed; it was not promoted", id, last)
		case <-t.C:
		}
		if every *= 2; every > most {
			every = most
		}
	}
}

// activated reports whether the release is live: it is the production
// deployment, or a NEW instance has deployed itself over it.
//
// ⭐ THE TAKEOVER. A new instance starts as soon as its function's deployment
// is ready, and the first request deploys the seeded site, a newer production
// deployment beside the same function. Promoting the function's own
// deployment over that one is refused (409), and would take the site's pages
// down if it were not. So for a new instance a READY production deployment
// created after the release is the release, live.
//
// ⛔ ONLY FOR A NEW INSTANCE. An existing one ran other code before this
// release, and a deployment it made in the meantime may carry that code; the
// strict check stays there.
func (v *Vercel) activated(ctx context.Context, inst Instance, rel Release) bool {
	prod, err := v.productionID(ctx, inst)
	if err != nil || prod == "" {
		return false
	}
	if prod == rel.ID {
		return true
	}
	if inst.Existing {
		return false
	}
	type dep struct {
		CreatedAt int64 `json:"createdAt"`
	}
	var p, r dep
	if v.call(ctx, http.MethodGet, "/v13/deployments/"+url.PathEscape(prod), nil, &p) != nil ||
		v.call(ctx, http.MethodGet, "/v13/deployments/"+url.PathEscape(rel.ID), nil, &r) != nil {
		return false
	}
	if r.CreatedAt <= 0 || p.CreatedAt <= r.CreatedAt {
		return false
	}
	// ⚠️ Production names the instance's deployment while it is still
	// building (measured 2026-10-19), so wait for it as for the release.
	return v.waitReady(ctx, prod) == nil
}

// productionID is the id of the project's production deployment, if any.
func (v *Vercel) productionID(ctx context.Context, inst Instance) (string, error) {
	var proj struct {
		Targets struct {
			Production struct {
				ID string `json:"id"`
			} `json:"production"`
		} `json:"targets"`
	}
	if err := v.call(ctx, http.MethodGet, "/v9/projects/"+url.PathEscape(inst.ID), nil, &proj); err != nil {
		return "", err
	}
	return proj.Targets.Production.ID, nil
}

func (v *Vercel) Status(ctx context.Context, inst Instance) (Status, error) {
	st := Status{State: StateUnknown, CheckedAt: time.Now().UTC()}
	var proj struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Targets struct {
			Production struct {
				ID string `json:"id"`
			} `json:"production"`
		} `json:"targets"`
	}
	path := fmt.Sprintf("/v9/projects/%s", url.PathEscape(inst.ID))
	if err := v.call(ctx, http.MethodGet, path, nil, &proj); err != nil {
		// ⛔ A 404 is Gone; anything else leaves State Unknown. "I could not
		// look" must not read as "it is not there", or a transient API failure
		// looks like a destroyed instance.
		if isStatus(err, http.StatusNotFound) {
			st.State = StateGone
			st.Message = "project not found"
			return st, nil
		}
		return st, err
	}
	st.ActiveRelease = proj.Targets.Production.ID
	if st.ActiveRelease == "" {
		st.State = StateCreating
		st.Message = "project exists, no production deployment yet"
	} else {
		st.State = StateReady
	}

	var doms struct {
		Domains []struct {
			Name     string `json:"name"`
			Verified bool   `json:"verified"`
		} `json:"domains"`
	}
	dpath := fmt.Sprintf("/v9/projects/%s/domains", url.PathEscape(inst.ID))
	if err := v.call(ctx, http.MethodGet, dpath, nil, &doms); err == nil {
		for _, d := range doms.Domains {
			st.Domains = append(st.Domains, DomainStatus{
				Domain: d.Name, Attached: true, CertificateReady: d.Verified,
			})
		}
	}
	return st, nil
}

// apiError is a non-2xx answer from the Vercel API.
//
// ⭐ A TYPE, so a caller decides by status with errors.As. ⛔ Never by matching
// the message: "HTTP 404" in a string breaks silently the day the wording
// changes, and would also match a 404 quoted inside an error body.
type apiError struct {
	method, path string
	status       int
	code         string
	retryAfter   time.Duration // from a 429's Retry-After, when it gave one
}

func (e *apiError) Error() string {
	if e.code == "" {
		return fmt.Sprintf("vercel %s %s: HTTP %d", e.method, e.path, e.status)
	}
	return fmt.Sprintf("vercel %s %s: HTTP %d (%s)", e.method, e.path, e.status, e.code)
}

// errorCode reads Vercel's {"error":{"code":…}} from a bounded body and keeps
// it only if it looks like a code: short, lowercase letters, digits, '_' and
// '-'. ⛔ Anything else is dropped rather than echoed.
func errorCode(body io.Reader) string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(body, 4096)).Decode(&e) != nil {
		return ""
	}
	return SafeErrorCode(e.Error.Code)
}

// SafeErrorCode returns c if it is shaped like an API error code, else "".
func SafeErrorCode(c string) string {
	if c == "" || len(c) > 64 {
		return ""
	}
	for _, r := range c {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return ""
		}
	}
	return c
}

// RateLimited reports whether err is the platform refusing for a rate limit,
// and how long it asked the caller to wait (zero when it did not say).
func RateLimited(err error) (time.Duration, bool) {
	var ae *apiError
	if errors.As(err, &ae) && ae.status == http.StatusTooManyRequests {
		return ae.retryAfter, true
	}
	return 0, false
}

// retryAfter reads a Retry-After of whole seconds, bounded to a day; any
// other form is treated as absent.
func retryAfter(resp *http.Response) time.Duration {
	if resp.StatusCode != http.StatusTooManyRequests {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
	if err != nil || n <= 0 {
		return 0
	}
	if n > 86400 {
		n = 86400
	}
	return time.Duration(n) * time.Second
}

func isStatus(err error, status int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == status
}

// isCode reports whether err is a Vercel API error carrying this code.
func isCode(err error, code string) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.code == code
}

// ⛔ compile-time proof that Vercel satisfies the seam.
var _ Provisioner = (*Vercel)(nil)
