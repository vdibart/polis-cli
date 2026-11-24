package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/releasekey"
)

// ⭐⭐ THE FUNCTION'S FILE LIST IS A MANIFEST OF THE PROGRAM, NOT OF THE
// INSTANCE. A publish deployment names the function's files by digest beside
// the site's pages, so the instance has to know them; the provisioner cannot
// tell it through the instance's private store, which only the running
// deployment can open. So the list travels with the BUILD: a release
// publishes it beside the function build, and the instance fetches the one
// for its own version. A deployment is then traceable to a published release
// by digest. A development build hands the same document to the instance
// directly (FunctionManifestEnv).
//
// ⚠️ This holds only while the routed config.json is the same for every
// instance, which it is while the page CSP is a constant of the webapp. A
// per-instance config.json could not be listed in a per-release manifest.

// FunctionManifestV1 names the manifest format.
const FunctionManifestV1 = "pub.polis.vercel-function.v1"

// FunctionManifestEnv is the non-secret variable a development build's
// manifest reaches the instance through, inline. It names digests, never
// contents, so it is plain configuration.
const FunctionManifestEnv = "POLIS_FUNCTION_MANIFEST"

// FunctionManifest lists one build of the function by digest.
type FunctionManifest struct {
	V       string       `json:"v"`
	Version string       `json:"version"`
	Files   []DeployFile `json:"files"`
}

// Fingerprint is a digest of what the manifest makes an instance deploy: its
// version and every file's path, digest, size and mode, in path order. Two
// manifests with the same fingerprint deploy the same function.
func (m FunctionManifest) Fingerprint() string {
	files := append([]DeployFile(nil), m.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].File < files[j].File })
	h := sha256.New()
	fmt.Fprintf(h, "%s\n", strings.TrimPrefix(m.Version, "v"))
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d\n", f.File, f.SHA, f.Size, f.Mode)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// BootstrapSHA is the digest the manifest names for the function's executable
// (the file named bootstrap), or "" when it names none.
func (m FunctionManifest) BootstrapSHA() string {
	for _, f := range m.Files {
		if path.Base(f.File) == "bootstrap" {
			return f.SHA
		}
	}
	return ""
}

// FunctionManifestFor returns the manifest of a build, as PutCode would
// upload it: the allow-listed files, the instance routing applied.
func FunctionManifestFor(code Code, version string) (FunctionManifest, error) {
	if strings.TrimSpace(version) == "" {
		return FunctionManifest{}, fmt.Errorf("provision: a function manifest needs the build's version")
	}
	files, err := buildOutputFiles(code)
	if err != nil {
		return FunctionManifest{}, err
	}
	m := FunctionManifest{V: FunctionManifestV1, Version: version}
	for _, f := range files {
		m.Files = append(m.Files, DeployFile{File: f.name, SHA: f.sha, Size: int64(len(f.data)), Mode: f.mode})
	}
	return m, m.check()
}

// ParseFunctionManifest reads a manifest from bytes that came from outside (a
// release asset, the environment), refusing anything it does not model.
func ParseFunctionManifest(b []byte) (FunctionManifest, error) {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var m FunctionManifest
	if err := dec.Decode(&m); err != nil {
		return FunctionManifest{}, fmt.Errorf("provision: the function manifest does not parse")
	}
	if dec.More() {
		return FunctionManifest{}, fmt.Errorf("provision: the function manifest has trailing content")
	}
	return m, m.check()
}

// check enforces what a function manifest may name.
//
// ⛔ Only the function: config.json and files under functions/, never a
// static file (those are the site's own, and one here would shadow a page)
// and never a private name. ⛔ An error names the entry, never a digest's
// source.
func (m FunctionManifest) check() error {
	if m.V != FunctionManifestV1 {
		return fmt.Errorf("provision: the function manifest is not %s", FunctionManifestV1)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("provision: the function manifest names no version")
	}
	seen := map[string]bool{}
	config := false
	for _, f := range m.Files {
		rel, ok := strings.CutPrefix(f.File, ".vercel/output/")
		if !ok || rel == "" {
			return fmt.Errorf("provision: the function manifest names %s, outside the build output", f.File)
		}
		if seen[f.File] {
			return fmt.Errorf("provision: the function manifest lists %s twice", f.File)
		}
		seen[f.File] = true
		if c, err := buildEntryName(rel); err != nil || c != rel {
			return fmt.Errorf("provision: the function manifest names %s, which is not a build entry", f.File)
		}
		switch {
		case rel == "config.json":
			config = true
		case strings.HasPrefix(rel, "functions/"):
		default:
			return fmt.Errorf("provision: the function manifest names %s; it may name only config.json and the function", f.File)
		}
		if isPrivateBuildName(rel) && !strings.HasSuffix(rel, "/.vc-config.json") {
			return fmt.Errorf("provision: the function manifest names %s, a private name", f.File)
		}
		if !hexSHA1.MatchString(f.SHA) || f.Size < 0 {
			return fmt.Errorf("provision: the function manifest has no valid digest for %s", f.File)
		}
		if f.Mode != modeFile && f.Mode != modeExecutable {
			return fmt.Errorf("provision: the function manifest gives %s a mode Vercel does not take", f.File)
		}
	}
	if !config {
		return fmt.Errorf("provision: the function manifest has no config.json")
	}
	return nil
}

// The release a function build is published in. ⭐ The asset names are the
// contract between the release and every instance: an instance fetches them
// by its own version.
const (
	ReleaseRepo           = "vdibart/polis-cli"
	FunctionManifestAsset = "vercel-function.json"
	FunctionBuildAsset    = "vercel-function.tar.gz"
)

// releaseBase is GitHub, or a test's server.
var releaseBase = "https://github.com"

// releaseKey is the release key the assets must be signed by: the pinned one,
// or a test's.
var releaseKey = releasekey.PublicKey

// maxChecksums bounds a release's checksums.txt and its signature; a real
// checksums.txt is a few kilobytes.
const maxChecksums = 256 << 10

// fetchReleaseChecksums fetches a release's checksums.txt and checksums.txt.sig
// and verifies the signature with the pinned release key.
//
// ⛔ This, not the manifest, is what makes a release asset trustworthy. The
// manifest and the build come from the same place, so both could be replaced
// together; the signature is made by a key that place never holds. A release
// with no signature (one made before releases were signed) is refused: an
// instance fetches only its own version, and every version that carries this
// code is signed.
func fetchReleaseChecksums(ctx context.Context, hc *http.Client, version string) (releasekey.Checksums, error) {
	sums, err := fetchAsset(ctx, hc, ReleaseAssetURL(version, releasekey.ChecksumsAsset), maxChecksums)
	if err != nil {
		return nil, err
	}
	sig, err := fetchAsset(ctx, hc, ReleaseAssetURL(version, releasekey.SignatureAsset), maxChecksums)
	if err != nil {
		return nil, fmt.Errorf("provision: the release of %s publishes no signature: %w", version, err)
	}
	c, err := releasekey.VerifyWith(releaseKey, sums, string(sig))
	if err != nil {
		return nil, fmt.Errorf("provision: the release of %s: %w", version, err)
	}
	return c, nil
}

// fetchSignedAsset fetches one release asset and refuses it unless it is the
// file the release's signed checksums.txt names.
func fetchSignedAsset(ctx context.Context, hc *http.Client, version, asset string, limit int64) ([]byte, error) {
	sums, err := fetchReleaseChecksums(ctx, hc, version)
	if err != nil {
		return nil, err
	}
	b, err := fetchAsset(ctx, hc, ReleaseAssetURL(version, asset), limit)
	if err != nil {
		return nil, err
	}
	if err := sums.Check(asset, b); err != nil {
		return nil, fmt.Errorf("provision: the release of %s: %w", version, err)
	}
	return b, nil
}

// ReleaseAssetURL is where a release's asset is published.
func ReleaseAssetURL(version, asset string) string {
	return releaseBase + "/" + ReleaseRepo + "/releases/download/v" + url.PathEscape(strings.TrimPrefix(version, "v")) + "/" + asset
}

// IsReleasedVersion reports whether a version could name a release: a build
// stamped by the release, never a development build.
func IsReleasedVersion(version string) bool {
	return releasedVersion.MatchString(strings.TrimPrefix(version, "v"))
}

var releasedVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// maxManifest bounds a fetched manifest; a real one is under a kilobyte.
const maxManifest = 64 << 10

// FetchFunctionManifest fetches the function manifest a release published.
// ⛔ A manifest for another version is refused: the instance must deploy the
// function it was told to, not whatever the release page now says.
func FetchFunctionManifest(ctx context.Context, hc *http.Client, version string) (FunctionManifest, error) {
	if !IsReleasedVersion(version) {
		return FunctionManifest{}, fmt.Errorf("provision: %q is not a released version, so it has no published function manifest; a development build passes its manifest in %s", version, FunctionManifestEnv)
	}
	b, err := fetchSignedAsset(ctx, hc, version, FunctionManifestAsset, maxManifest)
	if err != nil {
		return FunctionManifest{}, err
	}
	m, err := ParseFunctionManifest(b)
	if err != nil {
		return FunctionManifest{}, err
	}
	if strings.TrimPrefix(m.Version, "v") != strings.TrimPrefix(version, "v") {
		return FunctionManifest{}, fmt.Errorf("provision: the release of %s published a manifest for %s; refusing it", version, m.Version)
	}
	return m, nil
}

// ReleaseFunctionFiles returns a FunctionFile source that fetches the
// release's function build, once, when a file is first asked for.
func ReleaseFunctionFiles(hc *http.Client, version string) func(context.Context, DeployFile) ([]byte, error) {
	var (
		mu  sync.Mutex
		src func(context.Context, DeployFile) ([]byte, error)
	)
	return func(ctx context.Context, f DeployFile) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if src == nil {
			b, err := fetchSignedAsset(ctx, hc, version, FunctionBuildAsset, maxBuildArchive)
			if err != nil {
				return nil, err
			}
			s, err := FunctionFilesFromCode(Code{Archive: b, Format: FormatTarGz})
			if err != nil {
				return nil, err
			}
			src = s
		}
		return src(ctx, f)
	}
}

func fetchAsset(ctx context.Context, hc *http.Client, u string, limit int64) ([]byte, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("provision: fetch %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provision: fetch %s: HTTP %d", u, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("provision: fetch %s: %w", u, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("provision: %s is larger than any release asset of its kind", u)
	}
	return b, nil
}

// ReleasedFunctionCode fetches the function build a release published, as
// Code for PutCode, and checks it is the build the release's manifest names.
//
// ⭐ A released build carries its routing already (its manifest lists the
// routed config.json), so the Code carries none. ⛔ A version with no release,
// or a release that publishes no function build, is ErrNoReleasedCode; a build
// that differs from its manifest by a single digest is refused, because every
// later deploy of the instance names the manifest's files.
func ReleasedFunctionCode(ctx context.Context, hc *http.Client, version string) (Code, error) {
	if !IsReleasedVersion(version) {
		return Code{}, fmt.Errorf("%w: %q is a development version", ErrNoReleasedCode, version)
	}
	m, err := FetchFunctionManifest(ctx, hc, version)
	if err != nil {
		return Code{}, fmt.Errorf("%w: the release of %s publishes no usable function manifest (%v)", ErrNoReleasedCode, version, err)
	}
	b, err := fetchSignedAsset(ctx, hc, version, FunctionBuildAsset, maxBuildArchive)
	if err != nil {
		return Code{}, fmt.Errorf("%w: the release of %s publishes no function build it signed (%v)", ErrNoReleasedCode, version, err)
	}
	code := Code{Archive: b, Format: FormatTarGz}
	got, err := FunctionManifestFor(code, version)
	if err != nil {
		return Code{}, err
	}
	want := map[string]DeployFile{}
	for _, f := range m.Files {
		want[f.File] = f
	}
	if len(got.Files) != len(want) {
		return Code{}, fmt.Errorf("provision: the release of %s publishes a function build that is not the one its manifest names", version)
	}
	for _, f := range got.Files {
		if w, ok := want[f.File]; !ok || w.SHA != f.SHA || w.Mode != f.Mode {
			return Code{}, fmt.Errorf("provision: the release of %s publishes a function build that is not the one its manifest names (%s differs)", version, f.File)
		}
	}
	return code, nil
}
