package main

// The recipe book's harness — Signet epic 33 D3.
//
// ⛔ EVERY RECIPE IS EXECUTABLE AND CI-CHECKED, OR IT DOES NOT SHIP. A recipe
// that silently drifts teaches developers the wrong thing, which is worse than
// no recipe. So this test reads docs/signet/recipes/*.md, runs every ```bash
// block in order in one shell against real fixture sites, and compares each
// ```json output / ```text output block to what the block before it printed.
//
// # What a recipe is, mechanically
//
//   - ```bash runs. Every one — there is no "skip" marker, on purpose. A command a
//     reader is shown but the harness never runs is exactly the drift this
//     exists to stop; put illustration in a ```text block instead.
//   - The ONE exception is whole-recipe and printed, never per-block and hidden:
//     notRunnableMark opening a line of the page, the same mark on its index row,
//     and an entry in notRunnable. The page is still parsed; it is not run.
//     TestRecipeNotRunnableIsDeclaredEverywhere fails unless all three agree.
//   - ```bash exit=1 runs and must exit with that status (a validation that is
//     supposed to fail).
//   - ```json output, immediately after a bash block, is a SUBSET match on the
//     JSON that block printed: every key shown must be present and match; keys
//     not shown are ignored. "…" alone matches any value; "sha256:…" matches any
//     string with that prefix; an array ending in "…" matches a longer array.
//     Several JSON values in a row (jq -c output) match value by value.
//   - ```text output matches lines in order: each line shown must appear, after
//     the previous one, as a line containing its fragments (split on "…").
//
// # The world a recipe runs in
//
// Each recipe gets a fresh empty directory and a fresh network. Every
// <name>.example domain in fixtureDomains is served from <workdir>/<name>.example
// — so `mkdir alice.example && cd alice.example && polis init` makes a site that
// is live at https://alice.example, which is what "deploy your site" means in
// prose. The polis binary reaches it through POLIS_ORIGIN_OVERRIDES (its own
// test-only transport hook); curl through a shim that makes the same
// substitution. ds.example is a fake discovery service that registers sites and
// content and countersigns registrations with discovery.Witness — the Go half
// of the witness wire contract, pinned against the DS's own golden bytes.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

const recipesDir = "../../../docs/signet/recipes"

// fixtureDomains are the parties a recipe may name. Adding a recipe that needs
// another party means adding it here.
var fixtureDomains = []string{
	"alice.example", "bob.example", "old.example", "new.example",
	"operator.example", "judge.example", "lookalike.example",
}

const fakeDSDomain = "ds.example"

// ---------------------------------------------------------------- parsing

type recipeBlock struct {
	Line     int
	Script   string
	WantExit int
	// Output is the expected-output block that follows, if any.
	OutputKind string // "json" | "text" | ""
	Output     string
	OutputLine int
}

var fenceRe = regexp.MustCompile("^```\\s*([A-Za-z]*)\\s*(.*)$")

// parseRecipe extracts the runnable blocks. It is strict about the things that
// would let a recipe pass without testing anything: an output block with no
// command before it, and an unknown attribute on a bash block.
func parseRecipe(md string) ([]recipeBlock, error) {
	var blocks []recipeBlock
	lines := strings.Split(md, "\n")
	lastWasBash := false
	for i := 0; i < len(lines); i++ {
		m := fenceRe.FindStringSubmatch(strings.TrimRight(lines[i], " "))
		if m == nil {
			if strings.TrimSpace(lines[i]) != "" {
				lastWasBash = false
			}
			continue
		}
		lang, attrs := m[1], strings.Fields(m[2])
		start := i + 1
		var body []string
		for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
			body = append(body, lines[i])
		}
		if i >= len(lines) {
			return nil, fmt.Errorf("line %d: unterminated code block", start)
		}
		text := strings.Join(body, "\n")

		isOutput := len(attrs) > 0 && attrs[0] == "output"
		switch {
		case lang == "bash":
			b := recipeBlock{Line: start, Script: text}
			for _, a := range attrs {
				k, v, ok := strings.Cut(a, "=")
				if !ok || k != "exit" {
					return nil, fmt.Errorf("line %d: unknown bash block attribute %q (only exit=N)", start, a)
				}
				n, err := strconv.Atoi(v)
				if err != nil {
					return nil, fmt.Errorf("line %d: exit=%q is not a number", start, v)
				}
				b.WantExit = n
			}
			blocks = append(blocks, b)
			lastWasBash = true
			continue
		case isOutput:
			if lang != "json" && lang != "text" {
				return nil, fmt.Errorf("line %d: output blocks are json or text, not %q", start, lang)
			}
			if !lastWasBash || blocks[len(blocks)-1].OutputKind != "" {
				return nil, fmt.Errorf("line %d: an output block must directly follow the bash block it checks", start)
			}
			b := &blocks[len(blocks)-1]
			b.OutputKind, b.Output, b.OutputLine = lang, text, start
		}
		lastWasBash = false
	}
	return blocks, nil
}

// ---------------------------------------------------------------- matching

const wild = "…"

// matchJSON reports why want does not match got, or "" when it does.
func matchJSON(want, got interface{}, path string) string {
	if s, ok := want.(string); ok {
		if s == wild {
			return ""
		}
		if strings.Contains(s, wild) {
			gs, ok := got.(string)
			if !ok {
				return fmt.Sprintf("%s: want a string like %q, got %v", path, s, got)
			}
			if !fragmentsMatch(s, gs, true) {
				return fmt.Sprintf("%s: %q does not match %q", path, gs, s)
			}
			return ""
		}
	}
	switch w := want.(type) {
	case map[string]interface{}:
		g, ok := got.(map[string]interface{})
		if !ok {
			return fmt.Sprintf("%s: want an object, got %v", path, got)
		}
		keys := make([]string, 0, len(w))
		for k := range w {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == wild {
				continue
			}
			gv, ok := g[k]
			if !ok {
				return fmt.Sprintf("%s.%s: missing", path, k)
			}
			if why := matchJSON(w[k], gv, path+"."+k); why != "" {
				return why
			}
		}
		return ""
	case []interface{}:
		g, ok := got.([]interface{})
		if !ok {
			return fmt.Sprintf("%s: want an array, got %v", path, got)
		}
		open := len(w) > 0 && w[len(w)-1] == wild
		if open {
			w = w[:len(w)-1]
			if len(g) < len(w) {
				return fmt.Sprintf("%s: want at least %d elements, got %d", path, len(w), len(g))
			}
		} else if len(g) != len(w) {
			return fmt.Sprintf("%s: want %d elements, got %d", path, len(w), len(g))
		}
		for i := range w {
			if why := matchJSON(w[i], g[i], fmt.Sprintf("%s[%d]", path, i)); why != "" {
				return why
			}
		}
		return ""
	default:
		wb, _ := json.Marshal(want)
		gb, _ := json.Marshal(got)
		if !bytes.Equal(wb, gb) {
			return fmt.Sprintf("%s: want %s, got %s", path, wb, gb)
		}
		return ""
	}
}

// fragmentsMatch: pattern split on "…"; anchored at both ends when anchored,
// otherwise the fragments need only appear in order.
func fragmentsMatch(pattern, s string, anchored bool) bool {
	parts := strings.Split(pattern, wild)
	if anchored {
		if !strings.HasPrefix(s, parts[0]) {
			return false
		}
		s = s[len(parts[0]):]
		parts = parts[1:]
		last := parts[len(parts)-1]
		if !strings.HasSuffix(s, last) {
			return false
		}
		s = s[:len(s)-len(last)]
		parts = parts[:len(parts)-1]
	}
	for _, p := range parts {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return true
}

func decodeAll(s string) ([]interface{}, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	var out []interface{}
	for {
		var v interface{}
		err := dec.Decode(&v)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, v)
	}
}

func checkOutput(kind, want, got string) string {
	switch kind {
	case "json":
		wv, err := decodeAll(want)
		if err != nil {
			return "the recipe's expected JSON does not parse: " + err.Error()
		}
		gv, err := decodeAll(got)
		if err != nil {
			return "the command's output is not JSON: " + err.Error()
		}
		if len(wv) != len(gv) {
			return fmt.Sprintf("want %d JSON value(s), got %d", len(wv), len(gv))
		}
		for i := range wv {
			if why := matchJSON(wv[i], gv[i], fmt.Sprintf("$%d", i)); why != "" {
				return why
			}
		}
		return ""
	case "text":
		gotLines := strings.Split(got, "\n")
		at := 0
		for _, w := range strings.Split(want, "\n") {
			w = strings.TrimSpace(w)
			if w == "" {
				continue
			}
			found := false
			for ; at < len(gotLines); at++ {
				if fragmentsMatch(w, gotLines[at], false) {
					found, at = true, at+1
					break
				}
			}
			if !found {
				return fmt.Sprintf("no line matching %q (in order)", w)
			}
		}
		return ""
	}
	return ""
}

// ---------------------------------------------------------------- the world

type fakeDS struct {
	priv []byte
	pub  string
	mu   sync.Mutex
}

func newFakeDS(t *testing.T) *fakeDS {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeDS{priv: priv, pub: strings.TrimSpace(string(pub))}
}

// ServeHTTP answers the three calls a recipe makes of a discovery service.
// ⚠️ It is a FIXTURE, not a model of the DS: it verifies nothing a caller sends.
// What it signs is built by discovery.Witness, whose canonical form is pinned to
// the real DS's golden bytes — which is the only part a recipe's reader checks.
func (d *fakeDS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/sites":
		var req struct {
			Domain string `json:"domain"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true, "domain": req.Domain,
			"created_at": time.Now().UTC().Format(time.RFC3339),
		})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/content":
		var req struct {
			Type     string                 `json:"type"`
			URL      string                 `json:"url"`
			Version  string                 `json:"version"`
			Author   string                 `json:"author"`
			Metadata map[string]interface{} `json:"metadata"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		wit := discovery.Witness{
			Action: discovery.WitnessActionContent,
			Type:   req.Type, URL: req.URL, Version: req.Version, Author: req.Author,
			DS: "https://" + fakeDSDomain, DSKeyID: "recipe-key",
			WitnessedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		}
		if h, ok := req.Metadata[discovery.MetadataArtifactHash].(string); ok {
			wit.ArtifactHash = h
		}
		d.mu.Lock()
		canonical, err := wit.Canonical()
		if err == nil {
			wit.Signature, err = signing.SignContent(canonical, d.priv)
		}
		d.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true, "type": req.Type, "url": req.URL, "status": "created", "witness": wit,
		})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/sites/public-key":
		_ = json.NewEncoder(w).Encode(map[string]string{"public_key": d.pub, "key_id": r.URL.Query().Get("key_id")})
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not served by the recipe fixture"}`))
	}
}

// newWorld serves workDir's <domain>/ directories and the fake DS.
func newWorld(t *testing.T, workDir string) *httptest.Server {
	ds := newFakeDS(t)
	mux := http.NewServeMux()
	mux.Handle("/"+fakeDSDomain+"/", http.StripPrefix("/"+fakeDSDomain, ds))
	for _, d := range fixtureDomains {
		mux.Handle("/"+d+"/", http.StripPrefix("/"+d, http.FileServer(http.Dir(filepath.Join(workDir, d)))))
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

// buildPolis builds this package's binary once per test run.
func buildPolis(t *testing.T) string {
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "polis-recipes-bin-")
		if err != nil {
			buildErr = err
			return
		}
		out, err := exec.Command("go", "build", "-o", filepath.Join(dir, "polis"), ".").CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build: %v\n%s", err, out)
			return
		}
		binDir = dir
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binDir
}

const curlShim = `#!/usr/bin/env bash
# Recipe harness only: route https://<fixture>.example to the local fixture
# server — the substitution POLIS_ORIGIN_OVERRIDES makes for the polis binary.
args=()
for a in "$@"; do
  if [[ $a =~ ^https://([a-z0-9-]+\.example)(/.*)?$ ]]; then
    a="$RECIPE_ORIGIN/${BASH_REMATCH[1]}${BASH_REMATCH[2]}"
  fi
  args+=("$a")
done
exec "$RECIPE_REAL_CURL" "${args[@]}"
`

// ---------------------------------------------------------------- running

type blockResult struct {
	Stdout, Stderr string
	Exit           int
	Ran            bool
}

// diedMidBlock is the Exit of a block in which some command failed under
// errexit, so the shell never reached the end of it.
const diedMidBlock = -1

func runRecipe(t *testing.T, blocks []recipeBlock) []blockResult {
	t.Helper()
	polisDir := buildPolis(t)
	realCurl, err := exec.LookPath("curl")
	if err != nil {
		t.Fatal("the recipes use curl, and it is not installed")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("the recipes use jq, and it is not installed")
	}

	root := t.TempDir()
	work := filepath.Join(root, "work")
	state := filepath.Join(root, "state")
	shims := filepath.Join(root, "bin")
	home := filepath.Join(root, "home")
	for _, d := range []string{work, state, shims, home} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(shims, "curl"), []byte(curlShim), 0755); err != nil {
		t.Fatal(err)
	}

	srv := newWorld(t, work)
	var overrides []string
	for _, d := range append(append([]string{}, fixtureDomains...), fakeDSDomain) {
		overrides = append(overrides, d+"="+srv.URL+"/"+d)
	}

	var script strings.Builder
	script.WriteString("set -eo pipefail\ncd " + strconv.Quote(work) + "\n")
	for i, b := range blocks {
		f := filepath.Join(state, fmt.Sprintf("%d.sh", i))
		if err := os.WriteFile(f, []byte(b.Script+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		started := filepath.Join(state, fmt.Sprintf("%d.started", i))
		out, errf, code := filepath.Join(state, fmt.Sprintf("%d.out", i)), filepath.Join(state, fmt.Sprintf("%d.err", i)), filepath.Join(state, fmt.Sprintf("%d.code", i))
		if b.WantExit == 0 {
			// ⛔ errexit stays ON: any command in the block that fails — not just
			// the last — stops the recipe, and the block is reported as died.
			fmt.Fprintf(&script, "touch %q\nsource %q > %q 2> %q\necho 0 > %q\n", started, f, out, errf, code)
		} else {
			fmt.Fprintf(&script, "touch %q\nset +e\nsource %q > %q 2> %q\necho $? > %q\nset -e\n[ \"$(cat %q)\" = %q ] || exit 97\n",
				started, f, out, errf, code, code, strconv.Itoa(b.WantExit))
		}
	}
	scriptPath := filepath.Join(state, "recipe.sh")
	if err := os.WriteFile(scriptPath, []byte(script.String()), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Env = []string{
		"PATH=" + shims + ":" + polisDir + ":" + os.Getenv("PATH"),
		"HOME=" + home,
		"TMPDIR=" + root,
		"LANG=C.UTF-8",
		"POLIS_ORIGIN_OVERRIDES=" + strings.Join(overrides, ","),
		// The CLI's discovery client does not go through the origin override, so
		// it is pointed at the fixture directly. Only a site that has run
		// `polis register` ever calls it.
		"DISCOVERY_SERVICE_URL=" + srv.URL + "/" + fakeDSDomain,
		"RECIPE_ORIGIN=" + srv.URL,
		"RECIPE_REAL_CURL=" + realCurl,
	}
	cmd.Dir = work
	done := make(chan struct{})
	var runErr error
	go func() { runErr = cmd.Run(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatal("recipe timed out")
	}
	_ = runErr // outcomes are read per block below

	results := make([]blockResult, len(blocks))
	for i := range blocks {
		if _, err := os.Stat(filepath.Join(state, fmt.Sprintf("%d.started", i))); err != nil {
			continue
		}
		stdout, _ := os.ReadFile(filepath.Join(state, fmt.Sprintf("%d.out", i)))
		stderr, _ := os.ReadFile(filepath.Join(state, fmt.Sprintf("%d.err", i)))
		n := diedMidBlock
		if code, err := os.ReadFile(filepath.Join(state, fmt.Sprintf("%d.code", i))); err == nil {
			n, _ = strconv.Atoi(strings.TrimSpace(string(code)))
		}
		results[i] = blockResult{Stdout: string(stdout), Stderr: string(stderr), Exit: n, Ran: true}
	}
	return results
}

// ---------------------------------------------------------------- the tests

// notRunnableMark parks a recipe that is still worth reading while a surface it
// needs is unavailable in this release. It must open a line of the page — the
// paragraph that tells the reader the test suite does not run it.
const notRunnableMark = "⏸️ not runnable in this release"

// notRunnable is every recipe allowed to carry notRunnableMark, and why. A mark
// on a page not listed here fails, so parking a recipe is a reviewed code
// change, never a quiet docs edit.
var notRunnable = map[string]string{
	"07-check-an-actor.md": "polis actor register no-ops on a site that is not already an operator, and the setup starts from a fresh site",
}

var notRunnableStatus = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(notRunnableMark))

func recipeFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(recipesDir, "[0-9][0-9]-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no recipes found in %s — the recipe book is part of the docs, not an optional extra", recipesDir)
	}
	return files
}

// TestRecipes runs every recipe in the book.
func TestRecipes(t *testing.T) {
	if testing.Short() {
		t.Skip("recipes build the CLI and run it end to end")
	}
	for _, path := range recipeFiles(t) {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			md, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			blocks, err := parseRecipe(string(md))
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			checked := 0
			for _, b := range blocks {
				if b.OutputKind != "" {
					checked++
				}
			}
			if len(blocks) == 0 || checked == 0 {
				t.Fatalf("%s has %d bash blocks and %d checked outputs — a recipe with nothing checked is not tested", path, len(blocks), checked)
			}
			if notRunnableStatus.Match(md) {
				t.Skipf("%s is marked %q: parsed, not run (%s)", filepath.Base(path), notRunnableMark, notRunnable[filepath.Base(path)])
			}

			results := runRecipe(t, blocks)
			for i, b := range blocks {
				r := results[i]
				where := fmt.Sprintf("%s:%d", filepath.Base(path), b.Line)
				if !r.Ran {
					t.Fatalf("%s: never ran — an earlier block stopped the recipe", where)
				}
				if r.Exit != b.WantExit {
					what := fmt.Sprintf("exited %d, want %d", r.Exit, b.WantExit)
					if r.Exit == diedMidBlock {
						what = "a command in this block failed before its end"
					}
					t.Fatalf("%s: %s\n--- script\n%s\n--- stdout\n%s\n--- stderr\n%s",
						where, what, b.Script, r.Stdout, r.Stderr)
				}
				if b.OutputKind == "" {
					continue
				}
				if why := checkOutput(b.OutputKind, b.Output, r.Stdout); why != "" {
					t.Fatalf("%s: output drifted from %s:%d — %s\n--- script\n%s\n--- stdout\n%s\n--- stderr\n%s",
						where, filepath.Base(path), b.OutputLine, why, b.Script, r.Stdout, r.Stderr)
				}
			}
		})
	}
}

// TestRecipeIndexListsEveryRecipe keeps the book's index and its pages in step.
func TestRecipeIndexListsEveryRecipe(t *testing.T) {
	index, err := os.ReadFile(filepath.Join(recipesDir, "README.md"))
	if err != nil {
		t.Fatalf("the recipe book has no index: %v", err)
	}
	for _, path := range recipeFiles(t) {
		if !strings.Contains(string(index), "("+filepath.Base(path)+")") {
			t.Errorf("README.md does not link %s", filepath.Base(path))
		}
	}
	for _, m := range regexp.MustCompile(`\]\(([0-9][0-9]-[a-z0-9-]+\.md)\)`).FindAllStringSubmatch(string(index), -1) {
		if _, err := os.Stat(filepath.Join(recipesDir, m[1])); err != nil {
			t.Errorf("README.md links %s, which does not exist", m[1])
		}
	}
}

// TestRecipeNotRunnableIsDeclaredEverywhere keeps the one exception honest: a
// parked recipe says so on its page, on its index row, and in notRunnable — all
// three or none.
func TestRecipeNotRunnableIsDeclaredEverywhere(t *testing.T) {
	index, err := os.ReadFile(filepath.Join(recipesDir, "README.md"))
	if err != nil {
		t.Fatalf("the recipe book has no index: %v", err)
	}
	link := regexp.MustCompile(`\]\(([0-9][0-9]-[a-z0-9-]+\.md)\)`)
	rows := map[string]string{}
	for _, line := range strings.Split(string(index), "\n") {
		if m := link.FindStringSubmatch(line); m != nil && strings.HasPrefix(line, "|") {
			if _, ok := rows[m[1]]; !ok { // the recipe table comes first
				rows[m[1]] = line
			}
		}
	}
	seen := map[string]bool{}
	for _, path := range recipeFiles(t) {
		name := filepath.Base(path)
		md, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		marked := notRunnableStatus.Match(md)
		_, listed := notRunnable[name]
		inIndex := strings.Contains(rows[name], notRunnableMark)
		if marked != listed || marked != inIndex {
			t.Errorf("%s: page mark=%v, notRunnable entry=%v, index row mark=%v — a parked recipe is declared in all three or none", name, marked, listed, inIndex)
		}
		seen[name] = true
	}
	for name := range notRunnable {
		if !seen[name] {
			t.Errorf("notRunnable lists %s, which is not a recipe", name)
		}
	}
}

// ---------------------------------------------------------------- the harness's own tests

func TestRecipeMatcher(t *testing.T) {
	cases := []struct {
		kind, want, got string
		ok              bool
	}{
		{"json", `{"a":"…"}`, `{"a":3,"b":4}`, true},
		{"json", `{"a":"sha256:…"}`, `{"a":"sha256:abc"}`, true},
		{"json", `{"a":"sha256:…"}`, `{"a":"md5:abc"}`, false},
		{"json", `{"a":"x"}`, `{"a":"y"}`, false},
		{"json", `{"missing":1}`, `{"a":1}`, false},
		{"json", `{"l":["x","…"]}`, `{"l":["x","y","z"]}`, true},
		{"json", `{"l":["x"]}`, `{"l":["x","y"]}`, false},
		{"json", `{"n":1}` + "\n" + `{"n":2}`, `{"n":1}{"n":2}`, true},
		{"json", `{"n":1}`, `{"n":1}{"n":2}`, false},
		{"json", `{"s":"a…c…e"}`, `{"s":"abcde"}`, true},
		{"json", `{"s":"a…c…e"}`, `{"s":"abde"}`, false},
		{"json", `{"s":"a…c"}`, `{"s":"abcx"}`, false},
		{"text", "one\nthree", "one\ntwo\nthree", true},
		{"text", "three\none", "one\ntwo\nthree", false},
		{"text", "t…o", "one\ntwo", true},
	}
	for _, c := range cases {
		why := checkOutput(c.kind, c.want, c.got)
		if (why == "") != c.ok {
			t.Errorf("checkOutput(%s, %q, %q) = %q, want ok=%v", c.kind, c.want, c.got, why, c.ok)
		}
	}
}

func TestRecipeParserRefusesUncheckableShapes(t *testing.T) {
	for name, md := range map[string]string{
		"orphan output":   "```json output\n{}\n```\n",
		"unknown attr":    "```bash norun\necho hi\n```\n",
		"separated":       "```bash\necho hi\n```\nprose in between\n```json output\n{}\n```\n",
		"unterminated":    "```bash\necho hi\n",
		"two outputs":     "```bash\necho hi\n```\n```text output\nhi\n```\n```text output\nhi\n```\n",
		"odd output lang": "```bash\necho hi\n```\n```yaml output\nhi\n```\n",
	} {
		if _, err := parseRecipe(md); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
	blocks, err := parseRecipe("```bash exit=1\nfalse\n```\n\n```text output\n\n```\n")
	if err != nil || len(blocks) != 1 || blocks[0].WantExit != 1 || blocks[0].OutputKind != "text" {
		t.Errorf("a well-formed block did not parse: %+v %v", blocks, err)
	}
}
