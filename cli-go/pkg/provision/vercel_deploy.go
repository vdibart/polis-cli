package provision

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // G505: SHA-1 is what Vercel's file-upload API names files by; not used for integrity or signing
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ PUBLISH = DEPLOY. Deploy rebuilds the instance's production deployment
// from its private store: the function, named by digest from its manifest,
// plus every public key of the site as a static file. Nothing is read back
// from a deployment; the private store is the truth and the deployment is its
// projection.
//
// ⭐ Only what changed is read or sent. The public-tree manifest records each
// key's size, modification time and digest as of the last deploy; a key whose
// listing matches is not re-read, and Vercel keeps files by digest, so only
// files it reports missing are uploaded. The function binary is never re-sent
// on a publish: it is already on Vercel by digest.
//
// ⛔ The manifest is DERIVED. Lost or unreadable, it is rebuilt by reading
// every public key once, which uploads nothing Vercel already has. It has one
// writer: whoever holds the deploy lease.

// PublicManifestKey is where the public-tree manifest lives in the private
// store. ⛔ Under .polis, so it is never itself a static file.
const PublicManifestKey = ".polis/deploy/manifest.json"

const publicManifestV1 = "pub.polis.vercel-public.v1"

type publicManifest struct {
	V     string                 `json:"v"`
	Files map[string]publicEntry `json:"files"`
}

type publicEntry struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	SHA     string    `json:"sha"`
}

// Deployer deploys one instance's site.
type Deployer struct {
	// Store is the site's private store, the truth the deployment is built
	// from.
	Store storage.Store

	// Vercel is a client holding the instance's deploy token.
	Vercel *Vercel

	// Project is the instance's project name.
	Project string

	// Function is the function to deploy beside the pages.
	Function FunctionManifest

	// FunctionFile returns a function file's bytes, for when Vercel no
	// longer has one by digest (a release asset). Nil refuses such a deploy.
	FunctionFile func(ctx context.Context, f DeployFile) ([]byte, error)
}

// DeployResult says what one deploy did.
type DeployResult struct {
	Release
	Public   int // public files in the deployment
	Hashed   int // public files read and hashed, because they changed or were new
	Uploaded int // files sent to Vercel, because it did not have them

	// Took is how long each stage of the deploy took, for the log line a
	// slow publish is diagnosed from.
	Took DeployTimings
}

// DeployTimings splits a deploy's time by stage. ⭐ Each is wall time on the
// instance, so they add up to the deploy as the owner waited for it.
type DeployTimings struct {
	List   time.Duration // listing the whole store
	Read   time.Duration // stat and read of changed files, and hashing them
	Create time.Duration // the create calls to Vercel, both of them when files were missing
	Upload time.Duration // sending the files Vercel did not have
}

// String is the timings as a log fragment.
func (t DeployTimings) String() string {
	r := func(d time.Duration) time.Duration { return d.Round(time.Millisecond) }
	return fmt.Sprintf("list %s, read %s, create %s, upload %s", r(t.List), r(t.Read), r(t.Create), r(t.Upload))
}

// ErrSiteChanged is returned when a file changed between being hashed and
// being uploaded. The deploy is abandoned; the next one carries the change.
var ErrSiteChanged = errors.New("provision: the site changed during the deploy")

// Deploy creates a production deployment of the site as the private store
// holds it now.
//
// ⛔ A listing that could not read everything REFUSES the deploy: a partial
// listing would publish a site missing pages, and a deployment replaces the
// whole public surface.
func (d *Deployer) Deploy(ctx context.Context) (DeployResult, error) {
	if d.Store == nil || d.Vercel == nil || d.Project == "" {
		return DeployResult{}, fmt.Errorf("provision: a deployer needs the store, a client and the project")
	}
	if err := d.Function.check(); err != nil {
		return DeployResult{}, err
	}
	var res DeployResult
	mark := time.Now()
	lap := func(into *time.Duration) {
		now := time.Now()
		*into += now.Sub(mark)
		mark = now
	}
	entries, err := d.Store.List(ctx, "", storage.Recursive())
	if err != nil {
		return DeployResult{}, fmt.Errorf("provision: cannot list the whole site, so it is not deployed: %w", err)
	}
	lap(&res.Took.List)
	old := d.readManifest(ctx)
	next := publicManifest{V: publicManifestV1, Files: map[string]publicEntry{}}
	read := map[string][]byte{} // digest → bytes, for files read this deploy
	var statics []StaticFile
	for _, e := range entries {
		if e.IsDir || !IsPublicKey(e.Key) {
			continue
		}
		size, mod := e.Size, e.ModTime
		if mod.IsZero() {
			info, err := d.Store.Stat(ctx, e.Key)
			if err != nil {
				return DeployResult{}, fmt.Errorf("provision: cannot stat %s: %w", e.Key, err)
			}
			size, mod = info.Size, info.ModTime
		}
		pe, ok := old.Files[e.Key]
		if !ok || mod.IsZero() || !pe.ModTime.Equal(mod) || pe.Size != size || !hexSHA1.MatchString(pe.SHA) {
			data, err := d.Store.Get(ctx, e.Key)
			if err != nil {
				return DeployResult{}, fmt.Errorf("provision: cannot read %s: %w", e.Key, err)
			}
			pe = publicEntry{Size: int64(len(data)), ModTime: mod, SHA: sha1Hex(data)}
			read[pe.SHA] = data
			res.Hashed++
		}
		next.Files[e.Key] = pe
		statics = append(statics, StaticFile{Key: e.Key, SHA: pe.SHA, Size: pe.Size})
	}
	if n := len(statics) + len(d.Function.Files); n > MaxDeploymentFiles {
		return DeployResult{}, fmt.Errorf("provision: the site has %d files with the function, over Vercel's %d per deployment", n, MaxDeploymentFiles)
	}
	files, err := staticDeploymentFiles(statics)
	if err != nil {
		return DeployResult{}, err
	}
	files = append(append([]DeployFile(nil), d.Function.Files...), files...)
	res.Public = len(statics)

	// ⭐ Written before the deploy: it records digests of the store, which
	// are true whether or not this deployment succeeds, so a refused deploy
	// does not cost the next one its reads.
	d.writeManifest(ctx, next)
	lap(&res.Took.Read)

	rel, missing, err := d.Vercel.createDeployment(ctx, d.Project, files)
	lap(&res.Took.Create)
	if err != nil {
		return res, err
	}
	if len(missing) > 0 {
		n, err := d.uploadMissing(ctx, files, missing, read)
		res.Uploaded = n
		lap(&res.Took.Upload)
		if err != nil {
			return res, err
		}
		rel, missing, err = d.Vercel.createDeployment(ctx, d.Project, files)
		lap(&res.Took.Create)
		if err != nil {
			return res, err
		}
		if len(missing) > 0 {
			return res, fmt.Errorf("provision: Vercel still reports %d files missing after they were uploaded", len(missing))
		}
	}
	res.Release = rel
	return res, nil
}

// uploadMissing sends Vercel the files it reported missing, by digest.
//
// ⛔ Every file is checked against the digest the deployment names before it
// is sent: a site file that changed since it was hashed abandons the deploy
// (ErrSiteChanged) rather than deploying bytes nobody listed, and a function
// file that does not match its manifest is refused.
func (d *Deployer) uploadMissing(ctx context.Context, files []DeployFile, missing []string, read map[string][]byte) (int, error) {
	bySHA := map[string]DeployFile{}
	for _, f := range files {
		bySHA[f.SHA] = f
	}
	todo := make([]DeployFile, 0, len(missing))
	for _, sha := range missing {
		f, ok := bySHA[sha]
		if !ok {
			return 0, fmt.Errorf("provision: Vercel reports a file missing that the deployment does not name")
		}
		todo = append(todo, f)
	}
	// ⭐ CONCURRENT, BOUNDED. One after another, 8 files took 1.9 s of a
	// 4 s deploy (measured 2026-10-17): each upload is a round trip, and
	// nothing orders them. ⛔ The first error cancels the rest and is the one
	// returned, and every file is still checked against its digest first.
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		sent     atomic.Int64
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		mu.Unlock()
	}
	slots := make(chan struct{}, uploadConcurrency)
	for _, f := range todo {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(f DeployFile) {
			defer wg.Done()
			defer func() { <-slots }()
			data, err := d.fileBytes(ctx, f, read)
			if err != nil {
				fail(err)
				return
			}
			if err := d.Vercel.upload(ctx, uploadFile{name: f.File, data: data, mode: f.Mode, sha: f.SHA}); err != nil {
				fail(err)
				return
			}
			sent.Add(1)
		}(f)
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	n := int(sent.Load())
	if firstErr == nil && n < len(todo) {
		// ⛔ Never a quiet shortfall: the caller's context ended first.
		firstErr = parent.Err()
		if firstErr == nil {
			firstErr = fmt.Errorf("provision: %d of %d missing files were sent", n, len(todo))
		}
	}
	return n, firstErr
}

// uploadConcurrency bounds the files a deploy sends at once.
const uploadConcurrency = 6

func (d *Deployer) fileBytes(ctx context.Context, f DeployFile, read map[string][]byte) ([]byte, error) {
	if key, ok := strings.CutPrefix(f.File, ".vercel/output/static/"); ok {
		data, ok := read[f.SHA]
		if !ok {
			var err error
			if data, err = d.Store.Get(ctx, key); err != nil {
				return nil, fmt.Errorf("provision: cannot read %s: %w", key, err)
			}
		}
		if sha1Hex(data) != f.SHA {
			return nil, fmt.Errorf("%w: %s", ErrSiteChanged, key)
		}
		return data, nil
	}
	if d.FunctionFile == nil {
		return nil, fmt.Errorf("provision: Vercel no longer has %s (sha1 %s) and no source for the function was given", f.File, f.SHA)
	}
	data, err := d.FunctionFile(ctx, f)
	if err != nil {
		return nil, err
	}
	if sha1Hex(data) != f.SHA {
		return nil, fmt.Errorf("provision: %s does not match the function manifest; refusing to deploy it", f.File)
	}
	return data, nil
}

func (d *Deployer) readManifest(ctx context.Context) publicManifest {
	b, err := d.Store.Get(ctx, PublicManifestKey)
	if err != nil {
		return publicManifest{}
	}
	var m publicManifest
	if json.Unmarshal(b, &m) != nil || m.V != publicManifestV1 {
		return publicManifest{}
	}
	return m
}

// writeManifest records the manifest. ⚠️ A failure is not the deploy's
// failure: the manifest is derived, and the next deploy rebuilds it.
func (d *Deployer) writeManifest(ctx context.Context, m publicManifest) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = d.Store.Put(ctx, PublicManifestKey, b, storage.Rebuildable())
}

// FunctionFilesFromCode returns a build's function files by digest, as a
// FunctionFile source.
func FunctionFilesFromCode(code Code) (func(context.Context, DeployFile) ([]byte, error), error) {
	files, err := buildOutputFiles(code)
	if err != nil {
		return nil, err
	}
	by := map[string][]byte{}
	for _, f := range files {
		by[f.sha] = f.data
	}
	return func(_ context.Context, f DeployFile) ([]byte, error) {
		b, ok := by[f.SHA]
		if !ok {
			return nil, fmt.Errorf("provision: the build has no %s with the manifest's digest", f.File)
		}
		return b, nil
	}, nil
}

// createDeployment creates a production deployment of a prebuilt build
// naming each file by digest, and returns the digests Vercel does not have,
// if it refused for that reason.
//
// ⛔ Only the missing digests are read from an error body; every other error
// is the status and Vercel's code, as everywhere in this package.
func (v *Vercel) createDeployment(ctx context.Context, project string, files []DeployFile) (Release, []string, error) {
	// ⭐ A deployment of the uploaded build: no git, no connected repository,
	// no build on Vercel's side (prebuilt=1, as `vercel deploy --prebuilt`).
	body, err := json.Marshal(map[string]any{
		"name":   project,
		"target": "production",
		"files":  files,
		"projectSettings": map[string]any{
			"framework": nil,
		},
	})
	if err != nil {
		return Release{}, nil, err
	}
	const path = "/v13/deployments?prebuilt=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.withTeam(path), bytes.NewReader(body))
	if err != nil {
		return Release{}, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+v.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.hc.Do(req)
	if err != nil {
		return Release{}, nil, fmt.Errorf("vercel POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Code    string   `json:"code"`
				Missing []string `json:"missing"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error.Code == "missing_files" && len(e.Error.Missing) > 0 {
			for _, m := range e.Error.Missing {
				if !hexSHA1.MatchString(m) {
					return Release{}, nil, fmt.Errorf("provision: Vercel reported a missing file that is not a digest")
				}
			}
			return Release{}, e.Error.Missing, nil
		}
		return Release{}, nil, &apiError{method: http.MethodPost, path: path, status: resp.StatusCode, code: SafeErrorCode(e.Error.Code), retryAfter: retryAfter(resp)}
	}
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Release{}, nil, fmt.Errorf("vercel POST %s: the response does not parse", path)
	}
	url := out.URL
	if url != "" && !strings.HasPrefix(url, "http") {
		url = "https://" + url
	}
	return Release{ID: out.ID, URL: url}, nil, nil
}

func sha1Hex(b []byte) string {
	s := sha1.Sum(b) //nolint:gosec // G401: SHA-1 is what Vercel's file-upload API names files by; not used for integrity or signing
	return hex.EncodeToString(s[:])
}
