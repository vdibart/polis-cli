package tailor_test

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/tailor"
)

// ERA FIXTURES — old sites, built by the releases that wrote them.
//
// The unit tests and the golden fleet describe old layouts from memory: a
// fixture written today by the person who also wrote the migration. This test
// does not trust that memory. For each era it downloads the polis release that
// shipped then (github.com/vdibart/polis-cli), has THAT binary build a site
// (init, a post, a follow, a signed comment where the release can do them
// without the network), and then runs the CURRENT Tailor over the result:
//
//  1. `tailor.Apply`, then `tailor.Diagnose` must report no failing check
//     except the ones listed in eraExpectedFailures, each with its reason.
//  2. `polis validate` (sitecheck.RunLocalWithThemes) must report nothing
//     failed.
//  3. Every signed file the old release wrote still exists byte for byte
//     (Tailor may move a signed file, never rewrite it), and every signed
//     markdown artifact on the site verifies against the site's key.
//
// ⛔ SKIPPED BY DEFAULT: it downloads release binaries from GitHub.
//
//	POLIS_TAILOR_ERAS=1 go test ./pkg/tailor/ -run TestEraFixtures -v -count=1 -timeout 20m
//
// POLIS_TAILOR_ERAS_VERSIONS=0.45.0,0.58.0 narrows the eras. Downloads are
// cached under the user cache directory (polis/tailor-eras/v<version>/) and
// checked against the release's checksums.txt.
//
// TestEraRunbookRecipes (below) is the second half: for one pre-0.58 era it
// applies the public upgrade guide's hand recipes for phases 1 to 5 to one
// copy of the site and Tailor to another, and lists where the trees differ.

// eraVersions are chosen at layout changes, oldest first.
var eraVersions = []struct {
	version string
	why     string
}{
	{"0.45.0", "pre-0.50: config block in .well-known/polis, metadata/ indexes, posts/ sources, .polis/themes"},
	{"0.49.0", "last before 0.50's well-known cleanup; init flags differ from 0.45"},
	{"0.55.0", "webapp config at .polis/webapp-config.json era; still posts/ + metadata/"},
	{"0.58.0", "first content/pub.polis.core layout + bundle.json"},
	{"0.62.0", "last before the v4 shape and the bundle registry: .polis/content/<bundle>/"},
	{"0.63.1", "first with .polis/bundles/registry.json and the v4 stream shape"},
	{"0.70.0", "latest release"},
}

// eraExpectedFailures lists the Diagnose checks allowed to stay failing after
// Apply, on every era, with the reason. Anything else failing is a finding.
var eraExpectedFailures = map[string]string{
	// The test binary's Version is "dev", so Apply's download asks GitHub for a
	// release named vdev, gets a 404 and skips; the following dry run then says
	// it would download. Network-only, and nothing about the site's state.
	// ⚠️ It would 404 for a real version too: the check downloads a bare
	// polis-full-<os>-<arch>, and every release from v0.45.0 to v0.70.0 ships
	// that asset only as .tar.gz (or .zip on Windows).
	"cli-update": "network-only: Apply could not download a release for the test binary's version",
}

// eraExpectedFailuresFor lists failures expected on particular eras only.
var eraExpectedFailuresFor = map[string]map[string]string{
	// These releases' `polis init` took no author flag and wrote none, so the
	// site never had an author. bundle-path-integrity reports an empty
	// author_name and deliberately does not invent one: only the owner can
	// say who they are (`polis site set author-name`).
	"0.49.0": {"bundle-path-integrity": "the release's init recorded no author; report-only by design"},
	"0.55.0": {"bundle-path-integrity": "the release's init recorded no author; report-only by design"},
}

// eraKnownFindings are checks that stay failing after Apply because of a
// defect in Tailor, not in the site. The test logs each as a FINDING instead
// of failing, and fails when one no longer reproduces, so the entry is
// removed in the same change as the fix.
var eraKnownFindings = map[string]string{}

// eraStrandedPaths are private locations earlier releases wrote and no current
// reader looks in. A file left there after Apply is invisible to the CLI and
// the webapp: present on disk, lost to the user.
var eraStrandedPaths = map[string]string{
	".polis/comments/": "pre-0.58 comment drafts/pending/denied; current code reads .polis/bundles/pub.polis.core/comments/",
	".polis/posts/":    "pre-0.58 post drafts; current code reads .polis/bundles/pub.polis.core/posts/",
	".polis/content/":  "0.58-0.62 per-bundle private state; legacy-content-path moves it to .polis/bundles/",
}

// eraKnownStranded: eras where Tailor is known to leave a stranded path (a
// Tailor defect, logged as a FINDING). legacy-content-path migrates
// .polis/content/<bundle>/ (0.58 onward) but nothing migrates the flat
// .polis/comments/ that earlier releases wrote, so a signed comment awaiting a
// blessing (or a draft) on such a site disappears from `polis comment list`
// after Tailor.
var eraKnownStranded = map[string]string{}

const eraBaseURL = "https://era.example.com"

func TestEraFixtures(t *testing.T) {
	if os.Getenv("POLIS_TAILOR_ERAS") != "1" {
		t.Skip("era fixtures download old releases from GitHub; set POLIS_TAILOR_ERAS=1 to run")
	}
	followee := startFolloweeSite(t)
	t.Setenv("POLIS_BASE_URL", eraBaseURL)
	// Nothing here should reach the production discovery service.
	t.Setenv("DISCOVERY_SERVICE_URL", "https://127.0.0.1:1")

	var summary []string
	for _, era := range selectedEras() {
		era := era
		t.Run("v"+era.version, func(t *testing.T) {
			bin, err := eraBinary(era.version)
			if err != nil {
				summary = append(summary, fmt.Sprintf("v%s  NOT BUILT  download: %v", era.version, err))
				t.Fatalf("download v%s: %v", era.version, err)
			}
			siteDir := filepath.Join(t.TempDir(), "site")
			steps, err := buildEraSite(t, bin, siteDir, followee)
			for _, s := range steps {
				t.Logf("build step: %s", s)
			}
			if err != nil {
				summary = append(summary, fmt.Sprintf("v%s  NOT BUILT  %v", era.version, err))
				t.Fatalf("v%s could not build a site: %v", era.version, err)
			}
			summary = append(summary, fmt.Sprintf("v%s  built [%s]  (%s)", era.version, strings.Join(steps, "; "), era.why))
			verdicts := checkEraAfterTailor(t, era.version, siteDir)
			summary = append(summary, verdicts...)
		})
	}
	t.Logf("ERA SUMMARY\n%s", strings.Join(summary, "\n"))
}

// checkEraAfterTailor runs the three assertions on a built site and returns
// one summary line per assertion.
func checkEraAfterTailor(t *testing.T, version, siteDir string) []string {
	t.Helper()
	signedBefore := signedFiles(t, siteDir)
	keep := os.Getenv("POLIS_TAILOR_ERAS_KEEP")
	if keep != "" {
		keepTree(t, siteDir, filepath.Join(keep, "v"+version, "before"))
		defer keepTree(t, siteDir, filepath.Join(keep, "v"+version, "after"))
	}

	// Under Apply a "fail" means "found and fixed", so the outcome logged here
	// is what Tailor changed; only a skip under Apply can mean it could not.
	applied := tailor.Apply(siteDir)
	var changed, skipped []string
	for _, c := range applied.Checks {
		switch c.Status {
		case tailor.StatusFail:
			changed = append(changed, c.Name)
		case tailor.StatusSkip:
			skipped = append(skipped, c.Name+": "+c.Message)
		}
	}
	t.Logf("apply changed: %s", strings.Join(changed, ", "))
	for _, s := range skipped {
		t.Logf("apply skipped: %s", s)
	}

	var out []string

	// (1) A dry run afterwards is clean.
	after := tailor.Diagnose(siteDir)
	var unexpected, expected, findings []string
	seenKnown := map[string]bool{}
	for _, c := range after.Checks {
		if c.Status != tailor.StatusFail {
			continue
		}
		if why, ok := eraExpectedFailures[c.Name]; ok {
			expected = append(expected, c.Name+" ("+why+")")
			continue
		}
		if why, ok := eraExpectedFailuresFor[version][c.Name]; ok {
			expected = append(expected, c.Name+" ("+why+")")
			continue
		}
		if _, ok := eraKnownFindings[c.Name]; ok {
			seenKnown[c.Name] = true
			findings = append(findings, fmt.Sprintf("%s: %s", c.Name, c.Message))
			t.Logf("KNOWN FINDING: tailor dry run after apply: %s still fails: %s", c.Name, c.Message)
			continue
		}
		unexpected = append(unexpected, fmt.Sprintf("%s: %s", c.Name, c.Message))
		t.Errorf("tailor dry run after apply: %s still fails: %s", c.Name, c.Message)
	}
	for name := range eraKnownFindings {
		if !seenKnown[name] && postCount(siteDir) > 0 {
			t.Errorf("known finding %s no longer reproduces: remove it from eraKnownFindings", name)
		}
	}
	out = append(out, fmt.Sprintf("    (1) dry run after apply: unexpected fail %v; known findings %v; expected fail %v", unexpected, findings, expected))

	// (2) polis validate passes.
	report := sitecheck.RunLocalWithThemes(siteDir, sitecheck.StaticDSKeys(nil), "")
	var failed []string
	if report.FatalError != "" {
		failed = append(failed, "fatal: "+report.FatalError)
	}
	for _, c := range report.Checks {
		if c.Outcome == sitecheck.OutcomeFailed {
			failed = append(failed, fmt.Sprintf("%s: %s %v", c.ID, c.Detail, c.Findings))
		}
	}
	for _, f := range failed {
		t.Errorf("polis validate after tailor: %s", f)
	}
	out = append(out, fmt.Sprintf("    (2) validate: passed=%d failed=%d n/a=%d warning=%d %v",
		report.Totals.Passed, report.Totals.Failed, report.Totals.NotApplicable, report.Totals.Warning, failed))

	// (2b) Nothing is left in a private path that no current reader looks in.
	var stranded []string
	walkSite(t, siteDir, func(rel string, _ []byte) {
		for prefix := range eraStrandedPaths {
			if strings.HasPrefix(rel, prefix) {
				stranded = append(stranded, rel)
			}
		}
	})
	sort.Strings(stranded)
	for _, rel := range stranded {
		if known, ok := eraKnownStranded[version]; ok && strings.HasPrefix(rel, known) {
			t.Logf("KNOWN FINDING: %s is stranded after tailor: %s", rel, eraStrandedPaths[known])
			continue
		}
		t.Errorf("%s is stranded after tailor: nothing current reads that path", rel)
	}
	for v, known := range eraKnownStranded {
		if v != version {
			continue
		}
		found := false
		for _, rel := range stranded {
			found = found || strings.HasPrefix(rel, known)
		}
		if !found {
			t.Errorf("known finding (%s stranded) no longer reproduces on v%s: remove it from eraKnownStranded", known, v)
		}
	}
	out = append(out, fmt.Sprintf("    (2b) stranded in legacy private paths: %v", stranded))

	// (3a) Every signed file still exists, byte for byte, somewhere on the site.
	present := map[string]string{}
	walkSite(t, siteDir, func(rel string, data []byte) { present[sum(data)] = rel })
	var lost []string
	for h, rel := range signedBefore {
		if _, ok := present[h]; !ok {
			lost = append(lost, rel)
		}
	}
	sort.Strings(lost)
	for _, rel := range lost {
		t.Errorf("signed file %s was rewritten or removed by tailor (its bytes are nowhere on the site)", rel)
	}

	// (3b) Every signed markdown artifact verifies against the site's key.
	verified, bad := verifyMarkdown(t, siteDir)
	for _, b := range bad {
		t.Errorf("signature: %s", b)
	}
	out = append(out, fmt.Sprintf("    (3) signed files before=%d preserved=%d; markdown verified=%d failed=%d %v %v",
		len(signedBefore), len(signedBefore)-len(lost), verified, len(bad), lost, bad))
	return out
}

// keepTree copies a site out of the test's temp dir (POLIS_TAILOR_ERAS_KEEP),
// so a failure can be inspected by hand.
func keepTree(t *testing.T, src, dst string) {
	t.Helper()
	os.RemoveAll(dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-a", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("cp -a %s %s: %v %s", src, dst, err, out)
	}
}

// postCount is the number of post sources on the site after Tailor.
func postCount(siteDir string) int {
	n := 0
	filepath.WalkDir(filepath.Join(siteDir, "content", "pub.polis.core", "post"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") && !strings.Contains(filepath.ToSlash(p), "/.versions/") {
			n++
		}
		return nil
	})
	return n
}

func selectedEras() []struct{ version, why string } {
	want := os.Getenv("POLIS_TAILOR_ERAS_VERSIONS")
	var out []struct{ version, why string }
	for _, e := range eraVersions {
		if want == "" || strings.Contains(","+want+",", ","+e.version+",") {
			out = append(out, struct{ version, why string }{e.version, e.why})
		}
	}
	return out
}

// ---------- building a site with an old release ----------

type followee struct {
	url     string
	certPEM string // file the old binary trusts the server through (SSL_CERT_FILE)
}

// startFolloweeSite serves a minimal second polis site over TLS on loopback,
// so an old `polis follow` (which insists on https) has someone to follow
// without leaving the machine. Go binaries on Unix honour SSL_CERT_FILE, which
// is how a release built months ago comes to trust this server.
func startFolloweeSite(t *testing.T) followee {
	t.Helper()
	_, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	wk, _ := json.MarshalIndent(map[string]any{
		"version":     "polis-cli-go/0.45.0",
		"public_key":  strings.TrimSpace(string(pub)),
		"site_title":  "Followee",
		"author_name": "Followee",
		"author":      "Followee",
		"created":     "2026-01-01T00:00:00Z",
	}, "", "  ")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			w.Header().Set("Content-Type", "application/json")
			w.Write(wk)
		case "/metadata/public.jsonl", "/content/pub.polis.core/index.jsonl":
			w.Write(nil)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	certFile := filepath.Join(t.TempDir(), "followee.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(certFile, block, 0o644); err != nil {
		t.Fatal(err)
	}
	return followee{url: srv.URL, certPEM: certFile}
}

// buildEraSite runs the old binary. init is required; post, follow and
// comment are attempted and their outcome is recorded rather than required,
// because not every release can do them offline.
func buildEraSite(t *testing.T, bin, siteDir string, f followee) ([]string, error) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		return nil, err
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"TMPDIR=" + os.TempDir(),
		"POLIS_BASE_URL=" + eraBaseURL,
		"DISCOVERY_SERVICE_URL=https://127.0.0.1:1",
		"SSL_CERT_FILE=" + f.certPEM,
		"GIT_CONFIG_NOSYSTEM=1",
	}
	run := func(stdin string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = siteDir
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		done := make(chan struct{})
		var out []byte
		var err error
		go func() { out, err = cmd.CombinedOutput(); close(done) }()
		select {
		case <-done:
		case <-time.After(60 * time.Second):
			cmd.Process.Kill()
			<-done
			err = fmt.Errorf("timed out")
		}
		return string(out), err
	}

	var steps []string

	// init: pass only the flags this release's init knows.
	help, _ := run("", "init", "--help")
	flags := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s+-([a-z-]+)`).FindAllStringSubmatch(help, -1) {
		flags[m[1]] = true
	}
	args := []string{"init"}
	switch {
	case flags["site-title"]:
		args = append(args, "--site-title", "Era Site")
	case flags["title"]:
		args = append(args, "--title", "Era Site")
	}
	if flags["author"] {
		args = append(args, "--author", "Era Author")
	}
	if flags["email"] {
		args = append(args, "--email", "era@example.com")
	}
	if flags["base-url"] {
		args = append(args, "--base-url", eraBaseURL)
	}
	if flags["license"] {
		args = append(args, "--license", "reserved")
	}
	if out, err := run("", args...); err != nil {
		return steps, fmt.Errorf("%s: %v: %s", strings.Join(args, " "), err, lastLines(out, 3))
	}
	if _, err := os.Stat(filepath.Join(siteDir, ".well-known", "polis")); err != nil {
		return steps, fmt.Errorf("init wrote no .well-known/polis")
	}
	steps = append(steps, strings.Join(args, " "))

	// A post.
	if err := os.WriteFile(filepath.Join(siteDir, "hello.md"), []byte("# Hello from an old release\n\nA body line.\n\nA second paragraph with a backslash \\ in it.\n"), 0o644); err != nil {
		return steps, err
	}
	steps = append(steps, outcome("post", run2(run("", "post", "hello.md"))))
	os.Remove(filepath.Join(siteDir, "hello.md"))

	// A follow.
	steps = append(steps, outcome("follow", run2(run("", "follow", f.url))))

	// A signed comment on the site's own post: draft, give it a body, sign.
	postURL := ""
	walkSite(t, siteDir, func(rel string, _ []byte) {
		if postURL != "" || !strings.HasSuffix(rel, ".md") || strings.Contains(rel, "/.versions/") {
			return
		}
		switch {
		case strings.HasPrefix(rel, "posts/"):
			postURL = eraBaseURL + "/" + rel
		case strings.HasPrefix(rel, "content/pub.polis.core/post/"):
			postURL = eraBaseURL + "/posts/" + strings.TrimPrefix(rel, "content/pub.polis.core/post/")
		}
	})
	if postURL != "" {
		out, err := run("", "--json", "comment", "draft", postURL)
		var draft struct {
			ID string `json:"id"`
		}
		if err == nil {
			json.Unmarshal([]byte(lastLines(out, 1)), &draft)
		}
		if err != nil || draft.ID == "" {
			steps = append(steps, outcome("comment draft", stepResult{out, fmt.Errorf("no draft id (%v)", err)}))
		} else {
			var draftPath string
			filepath.WalkDir(filepath.Join(siteDir, ".polis"), func(p string, d fs.DirEntry, _ error) error {
				if d != nil && !d.IsDir() && d.Name() == draft.ID+".md" {
					draftPath = p
				}
				return nil
			})
			if draftPath != "" {
				fh, _ := os.OpenFile(draftPath, os.O_APPEND|os.O_WRONLY, 0)
				fh.WriteString("\nA comment from an old release.\n")
				fh.Close()
			}
			steps = append(steps, outcome("comment sign", run2(run("", "comment", "sign", draft.ID))))
		}
	}

	// Render, as a user of that release would have.
	steps = append(steps, outcome("render", run2(run("", "render"))))
	return steps, nil
}

type stepResult struct {
	out string
	err error
}

func run2(out string, err error) stepResult { return stepResult{out, err} }

func outcome(name string, r stepResult) string {
	if r.err != nil {
		return fmt.Sprintf("%s FAILED (%s)", name, lastLines(r.out, 1))
	}
	return name + " ok"
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// ---------- release download and cache ----------

func eraBinary(version string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "polis", "tailor-eras", "v"+version)
	bin := filepath.Join(dir, "polis")
	if fi, err := os.Stat(bin); err == nil && fi.Mode().IsRegular() {
		return bin, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	asset := fmt.Sprintf("polis-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("https://github.com/vdibart/polis-cli/releases/download/v%s/", version)
	sums, err := httpGet(base + "checksums.txt")
	if err != nil {
		return "", err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			want = fields[0]
		}
	}
	if want == "" {
		return "", fmt.Errorf("release v%s lists no %s in checksums.txt", version, asset)
	}
	tgz, err := httpGet(base + asset)
	if err != nil {
		return "", err
	}
	if got := sum(tgz); got != want {
		return "", fmt.Errorf("%s: sha256 %s, checksums.txt says %s", asset, got, want)
	}
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(h.Name) != "polis" || h.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return "", err
		}
		tmp := bin + ".tmp"
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return "", err
		}
		return bin, os.Rename(tmp, bin)
	}
	return "", fmt.Errorf("%s holds no polis binary", asset)
}

func httpGet(url string) ([]byte, error) {
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// ---------- signatures ----------

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// walkSite visits every regular file under siteDir except the ones Tailor is
// expected to own outright: installed bundle payload, theme copies, its own
// backups, logs and downloaded binaries.
func walkSite(t *testing.T, siteDir string, fn func(rel string, data []byte)) {
	t.Helper()
	skip := []string{".polis/bundles/pub.polis.core/shapes", ".polis/bundles/pub.polis.core/themes", ".polis/themes", "site/themes", ".polis/tailor-backup", ".polis/logs", ".polis/bin"}
	filepath.WalkDir(siteDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(siteDir, p)
		rel = filepath.ToSlash(rel)
		for _, s := range skip {
			if rel == s {
				return filepath.SkipDir
			}
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err == nil {
			fn(rel, data)
		}
		return nil
	})
}

// signedFiles maps the sha256 of every signed file on the site to its path: a
// markdown file whose frontmatter carries `signature:` at column 0, or a JSON
// object with a top-level "signature".
func signedFiles(t *testing.T, siteDir string) map[string]string {
	out := map[string]string{}
	walkSite(t, siteDir, func(rel string, data []byte) {
		if isSignedMarkdown(data) || isSignedJSON(data) {
			out[sum(data)] = rel
		}
	})
	return out
}

func frontmatter(data []byte) (fields map[string]string, ok bool) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	if !sc.Scan() || sc.Text() != "---" {
		return nil, false
	}
	fields = map[string]string{}
	for sc.Scan() {
		line := sc.Text()
		if line == "---" {
			return fields, true
		}
		if k, v, found := strings.Cut(line, ":"); found && !strings.HasPrefix(line, " ") {
			fields[k] = strings.TrimSpace(v)
		}
	}
	return nil, false
}

func isSignedMarkdown(data []byte) bool {
	fm, ok := frontmatter(data)
	return ok && fm["signature"] != ""
}

func isSignedJSON(data []byte) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	s, ok := m["signature"]
	return ok && string(s) != `""` && string(s) != "null"
}

// verifyMarkdown checks every signed markdown file outside .polis (the
// published posts and comments) against the site's published key.
func verifyMarkdown(t *testing.T, siteDir string) (int, []string) {
	pub, err := sitecheck.PublishedKey(siteDir)
	if err != nil {
		return 0, []string{"no published key: " + err.Error()}
	}
	var verified int
	var bad []string
	walkSite(t, siteDir, func(rel string, data []byte) {
		if !strings.HasSuffix(rel, ".md") {
			return
		}
		fm, ok := frontmatter(data)
		if !ok || fm["signature"] == "" {
			return
		}
		typ := signing.MarkdownObjectTypeFor(fm["type"], fm["in_reply_to"] != "" || fm["in-reply-to"] != "")
		res := sitecheck.VerifyContentWithHistory(string(data), pub, nil, typ)
		if res.ParseError == nil && res.Signature == sitecheck.SigValid && res.Hash != sitecheck.HashMismatch {
			verified++
			return
		}
		bad = append(bad, fmt.Sprintf("%s: signature=%s hash=%s parse=%v sigerr=%v", rel, res.Signature, res.Hash, res.ParseError, res.SigError))
	})
	return verified, bad
}
