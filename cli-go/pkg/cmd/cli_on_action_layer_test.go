package cmd

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ polis publish IS THE OPERATION /v1 SERVES. The same markdown published by
// the CLI and dispatched to the action layer as the API dispatches it leaves
// the same files, and the same signed post apart from its timestamp and the
// signature over it.

func actionSite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	s := storage.At(dir)
	if _, err := site.Init(s, site.InitOptions{BaseURL: "https://maya.example", License: "reserved"}); err != nil {
		t.Fatal(err)
	}
	if err := bundle.EnsureReferencePayload(s, "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	if err := bundle.SetActiveShapeName(s, "v4"); err != nil {
		t.Fatal(err)
	}
	if err := bundle.SetActiveThemeName(s, "turbo"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out
}

var timeOrSig = regexp.MustCompile(`(?m)^(published|updated|signature|asserted|  asserted): .*$|\(\d{4}-\d\d-\d\dT[0-9:]+Z\)`)

func TestPolisPublishIsTheAPIsOperation(t *testing.T) {
	if os.Getenv("POLIS_TEST_PUBLISH_HELPER") == "1" {
		dataDir = os.Getenv("POLIS_TEST_DIR")
		baseURL = os.Getenv("POLIS_BASE_URL") // as the CLI's root sets it
		requestID = "req-cli-test"
		initCLILogger()
		handlePublish([]string{"--filename", "same", os.Getenv("POLIS_TEST_FILE")})
		os.Exit(0)
	}
	const markdown = "---\nlicense: open\n---\n# The same post\n\nWritten once, published twice.\n"

	// The CLI, as a user runs it.
	cliDir := actionSite(t)
	in := filepath.Join(t.TempDir(), "post.md")
	if err := os.WriteFile(in, []byte(markdown), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPolisPublishIsTheAPIsOperation$")
	logFile := filepath.Join(t.TempDir(), "cli.log")
	cmd.Env = append(os.Environ(), "POLIS_TEST_PUBLISH_HELPER=1", "POLIS_TEST_DIR="+cliDir, "POLIS_TEST_FILE="+in,
		"POLIS_BASE_URL=https://maya.example", "DISCOVERY_SERVICE_URL=", "POLIS_LOG_FILE="+logFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("polis publish: %v\n%s", err, out)
	}

	// Its events went to POLIS_LOG_FILE, under the API's names, as the CLI's.
	logged, _ := os.ReadFile(logFile)
	var publishRow string
	for _, l := range strings.Split(string(logged), "\n") {
		if strings.Contains(l, `"pub.polis.post.publish"`) {
			publishRow = l
		}
	}
	if publishRow == "" || !strings.Contains(publishRow, `"surface":"cli"`) || !strings.Contains(publishRow, `"request_id":"req-cli-test"`) ||
		!strings.Contains(publishRow, `"op":"pub.polis.post.create"`) {
		t.Errorf("POLIS_LOG_FILE has no pub.polis.post.publish row with surface cli, its op and request id:\n%s", logged)
	}

	// The action layer, as /v1 dispatches it.
	apiDir := actionSite(t)
	s := storage.At(apiDir)
	priv, _ := os.ReadFile(filepath.Join(apiDir, ".polis", "keys", "id_ed25519"))
	bundles := map[string]*bundle.Bundle{"pub.polis.core": loadOrDefaultBundle(apiDir)}
	after := ops.NewAfterWrite(ops.AfterWriteConfig{Site: s, NewRenderer: func() (ops.SiteRenderer, error) {
		t.Setenv("POLIS_BASE_URL", "https://maya.example")
		return newCascadeRenderer(apiDir)
	}})
	e, err := ops.NewEngine(ops.EngineConfig{Resolver: resolve.New(s, bundles), Bundles: bundles, PrivateKey: priv,
		BaseURL: "https://maya.example", AfterWrite: after.Apply})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Dispatch(context.Background(), ops.ActionRequest{Action: "create", ContentType: "pub.polis.post",
		Payload: map[string]any{"markdown": markdown, "filename": "same"}})
	if err != nil {
		t.Fatal(err)
	}

	cliFiles, apiFiles := filesUnder(t, cliDir), filesUnder(t, apiDir)
	if strings.Join(cliFiles, "\n") != strings.Join(apiFiles, "\n") {
		t.Errorf("different files:\nCLI: %v\nAPI: %v", cliFiles, apiFiles)
	}
	path := res.Data["path"].(string)
	cliPost, err1 := os.ReadFile(filepath.Join(cliDir, path))
	apiPost, err2 := os.ReadFile(filepath.Join(apiDir, path))
	if err1 != nil || err2 != nil {
		t.Fatalf("the post is not at %s in both: %v %v", path, err1, err2)
	}
	if a, b := timeOrSig.ReplaceAllString(string(cliPost), "$1: X"), timeOrSig.ReplaceAllString(string(apiPost), "$1: X"); a != b {
		t.Errorf("the signed posts differ beyond time and signature:\nCLI:\n%s\nAPI:\n%s", a, b)
	}
	if !strings.Contains(string(cliPost), "profile: pub.polis.license.open/1") {
		t.Errorf("the CLI's post lost the licence its author wrote:\n%s", cliPost)
	}
}

// --license states a work's terms by its own license: line, replacing any.
func TestTheLicenceFlagWritesTheAuthorsLine(t *testing.T) {
	for in, want := range map[string]string{
		"# Hi\n\nbody": "---\nlicense: open\n---\n# Hi\n\nbody",
		"---\ntitle: x\nlicense: reserved\n---\nbody":                                                            "---\ntitle: x\nlicense: open\n---\nbody",
		"---\nlicense:\n  v: pub.polis.license.v1\n  profile: pub.polis.license.reserved/1\ntitle: y\n---\nbody": "---\ntitle: y\nlicense: open\n---\nbody",
	} {
		if got := withLicenseLine(in, "open"); got != want {
			t.Errorf("withLicenseLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// ⭐ polis unpublish and /v1's post.unpublish are one implementation
// (ops.Unpublish): the same post, published the same way and taken down each
// way, leaves the same files, and its text back in the v5 app's drafts.
func TestPolisUnpublishIsTheAPIsOperation(t *testing.T) {
	const markdown = "# Going away\n\nPublished, then taken down.\n"
	t.Setenv("POLIS_BASE_URL", "https://maya.example")
	t.Setenv("DISCOVERY_SERVICE_URL", "")
	publishOn := func(dir string) (*ops.Engine, string) {
		s := storage.At(dir)
		if err := bundle.SetActiveShapeName(s, "v5"); err != nil {
			t.Fatal(err)
		}
		priv, _ := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
		bundles := map[string]*bundle.Bundle{"pub.polis.core": loadOrDefaultBundle(dir)}
		after := ops.NewAfterWrite(ops.AfterWriteConfig{Site: s, NewRenderer: func() (ops.SiteRenderer, error) {
			return newCascadeRenderer(dir)
		}})
		e, err := ops.NewEngine(ops.EngineConfig{Resolver: resolve.New(s, bundles), Bundles: bundles, PrivateKey: priv,
			BaseURL: "https://maya.example", AfterWrite: after.Apply})
		if err != nil {
			t.Fatal(err)
		}
		res, err := e.Dispatch(context.Background(), ops.ActionRequest{Action: "create", ContentType: "pub.polis.post",
			Payload: map[string]any{"markdown": markdown, "filename": "going"}})
		if err != nil {
			t.Fatal(err)
		}
		return e, res.Data["path"].(string)
	}

	cliDir, apiDir := actionSite(t), actionSite(t)
	_, path := publishOn(cliDir)
	e, apiPath := publishOn(apiDir)
	if path != apiPath {
		t.Fatalf("published at %s and %s", path, apiPath)
	}

	for _, dir := range []string{cliDir, apiDir} {
		if _, err := os.Stat(filepath.Join(dir, "posts", filepath.Base(filepath.Dir(path)), "going.html")); err != nil {
			t.Fatalf("%s: the post's page was never rendered, so its removal proves nothing: %v", dir, err)
		}
	}

	// The CLI, as polis unpublish -y runs it.
	saved := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = saved })
	if err := RunUnpublish(cliDir, path, true); err != nil {
		t.Fatalf("polis unpublish: %v", err)
	}
	// The action layer, as /v1 dispatches it.
	res, err := e.Dispatch(context.Background(), ops.ActionRequest{Action: "unpublish", ContentType: "pub.polis.post",
		Payload: map[string]any{"path": apiPath}})
	if err != nil {
		t.Fatalf("post.unpublish: %v", err)
	}

	cliFiles, apiFiles := filesUnder(t, cliDir), filesUnder(t, apiDir)
	if strings.Join(cliFiles, "\n") != strings.Join(apiFiles, "\n") {
		t.Errorf("different files:\nCLI: %v\nAPI: %v", cliFiles, apiFiles)
	}
	for _, dir := range []string{cliDir, apiDir} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("%s: the published file is still there", dir)
		}
		page := filepath.Join(dir, "posts", filepath.Base(filepath.Dir(path)), "going.html")
		if _, err := os.Stat(page); err == nil {
			t.Errorf("%s: the post's page is still there", dir)
		}
		d, err := os.ReadFile(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "going.md"))
		if err != nil || !strings.Contains(string(d), "# Going away") || strings.Contains(string(d), "signature:") {
			t.Errorf("%s: the draft is missing or still signed: %v\n%s", dir, err, d)
		}
	}
	if res.Data["draft_id"] != "going" || res.Data["discovery"] != "skipped" {
		t.Errorf("answer: %v", res.Data)
	}
}
