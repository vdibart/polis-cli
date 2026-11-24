package cmd

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ EVERY CLI WRITE COMMAND LEAVES THE SITE /v1 LEAVES.
//
// Each command reaches the action layer's one implementation, so what happens
// after that call is the same code by construction (and the census test fails
// on a second implementation). What is the CLI's own is the step before it:
// turning flags, arguments and files into the operation's input. These tests
// run the real command in a subprocess on one copy of a site, and the
// operation as /v1 runs it on an identical copy (same key, same files), then
// compare every file the two leave, masking only what differs by the clock.
//
// polis dm send is the exception: a sealed message is encrypted under a fresh
// nonce, so no two sends are ever byte-identical. Its test shows the CLI hands
// the shared send the same message, recipient and options /v1 does.

// The helper process: one entry point that runs a named command as a user would.
func TestCLIBothWaysHelper(t *testing.T) {
	name := os.Getenv("POLIS_TEST_BOTHWAYS")
	if name == "" {
		t.Skip("the subprocess entry point of the both-ways tests")
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("POLIS_TEST_ARGS")), &args); err != nil {
		panic(err)
	}
	dataDir = os.Getenv("POLIS_TEST_DIR")
	baseURL = os.Getenv("POLIS_BASE_URL") // as the CLI's root sets them
	discoveryURL = os.Getenv("DISCOVERY_SERVICE_URL")
	requestID = "req-both-ways"
	jsonOutput = true
	initCLILogger()
	switch name {
	case "republish":
		handleRepublish(args)
	case "tag":
		handleTag(args)
	case "follow":
		handleFollow(args)
	case "unfollow":
		handleUnfollow(args)
	case "comment-sign":
		handleCommentSign(args)
	case "dm-send":
		handleDMSend(args[0], args[1])
	default:
		panic("no such command: " + name)
	}
	os.Exit(0)
}

// runCLI runs `polis <name> args…` on dir in a subprocess and returns its output.
func runCLI(t *testing.T, name, dir, base string, args []string, env ...string) []byte {
	t.Helper()
	raw, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCLIBothWaysHelper$")
	cmd.Env = append(os.Environ(), "POLIS_TEST_BOTHWAYS="+name, "POLIS_TEST_ARGS="+string(raw), "POLIS_TEST_DIR="+dir,
		"POLIS_BASE_URL="+base, "POLIS_LOG_FILE="+filepath.Join(t.TempDir(), "cli.log"))
	cmd.Env = append(cmd.Env, env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("polis %s %v: %v\n%s\n%s", name, args, err, out, stderr.String())
	}
	return out
}

// twinSites is two identical copies of one site at base (same key, same
// files), after prep has run on it: the CLI's copy and the API's.
func twinSites(t *testing.T, base string, prep func(dir string)) (cliDir, apiDir string) {
	t.Helper()
	dir := t.TempDir()
	s := storage.At(dir)
	if _, err := site.Init(s, site.InitOptions{BaseURL: base, License: "reserved"}); err != nil {
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
	if prep != nil {
		prep(dir)
	}
	cliDir, apiDir = t.TempDir(), t.TempDir()
	copyTree(t, dir, cliDir)
	copyTree(t, dir, apiDir)
	return cliDir, apiDir
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// apiEngine is the action layer as /v1 dispatches it, over the site at dir.
func apiEngine(t *testing.T, dir, base, dsURL string) *ops.Engine {
	t.Helper()
	s := storage.At(dir)
	priv, _ := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	pub, _ := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519.pub"))
	bundles := map[string]*bundle.Bundle{"pub.polis.core": loadOrDefaultBundle(dir)}
	after := ops.NewAfterWrite(ops.AfterWriteConfig{Site: s, NewRenderer: func() (ops.SiteRenderer, error) {
		t.Setenv("POLIS_BASE_URL", base)
		return newCascadeRenderer(dir)
	}})
	e, err := ops.NewEngine(ops.EngineConfig{Resolver: resolve.New(s, bundles), Bundles: bundles, PrivateKey: priv, PublicKey: pub,
		BaseURL: base, DiscoveryURL: dsURL, AfterWrite: after.Apply, AllowPrivateDMRecipients: true})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// What differs between two runs by the clock alone: timestamps, and the
// hashes and signatures over bytes that contain one.
var clockBound = []*regexp.Regexp{
	regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)`),
	regexp.MustCompile(`sha256:[0-9a-f]{64}`),
	regexp.MustCompile(`(?m)^signature: .*$`),
	regexp.MustCompile(`"(signature|ds_signature)":\s*"[^"]*"`),
	regexp.MustCompile(`-----BEGIN SSH SIGNATURE-----[\s\S]*?-----END SSH SIGNATURE-----`),
	// A version file's diff names the temporary files it was made from.
	regexp.MustCompile(`(?m)^(---|\+\+\+) \S+\s+\d{4}-\d\d-\d\d .*$`),
}

func maskClock(s string) string {
	for _, re := range clockBound {
		s = re.ReplaceAllString(s, "<clock>")
	}
	return s
}

// siteFiles is every file under dir with its contents, the clock masked, and
// keyed by its path with any extra masking applied. Logs are not the site.
func siteFiles(t *testing.T, dir string, maskPath func(string) string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".polis/logs/") {
			return nil
		}
		b, _ := os.ReadFile(p)
		key, body := rel, maskClock(string(b))
		if strings.HasSuffix(rel, ".jsonl") {
			// An index orders its entries by their times, so two writes in one
			// second on one copy and in two on the other swap places: the
			// order is the clock's, and the entries are compared as a set.
			lines := strings.Split(body, "\n")
			sort.Strings(lines)
			body = strings.Join(lines, "\n")
		}
		if maskPath != nil {
			key, body = maskPath(key), maskPath(body)
		}
		out[key] = body
		return nil
	})
	return out
}

// sameSite fails unless the two sites hold the same files with the same bytes,
// the clock apart.
func sameSite(t *testing.T, cliDir, apiDir string, maskPath func(string) string) {
	t.Helper()
	cli, api := siteFiles(t, cliDir, maskPath), siteFiles(t, apiDir, maskPath)
	for k, v := range cli {
		w, ok := api[k]
		switch {
		case !ok:
			t.Errorf("only the CLI wrote %s", k)
		case v != w:
			t.Errorf("%s differs:\nCLI:\n%s\nAPI:\n%s", k, v, w)
		}
	}
	for k := range api {
		if _, ok := cli[k]; !ok {
			t.Errorf("only the API wrote %s", k)
		}
	}
}

// publishedPost publishes one post on dir, as the site had it before a command.
func publishedPost(t *testing.T, dir, base, markdown string) string {
	t.Helper()
	res, err := apiEngine(t, dir, base, "").Dispatch(context.Background(), ops.ActionRequest{Action: "create",
		ContentType: "pub.polis.post", Payload: map[string]any{"markdown": markdown, "filename": "first"}})
	if err != nil {
		t.Fatal(err)
	}
	return res.Data["path"].(string)
}

const bothWaysBase = "https://maya.example"

func TestPolisRepublishIsTheAPIsOperation(t *testing.T) {
	var path string
	cliDir, apiDir := twinSites(t, bothWaysBase, func(dir string) {
		path = publishedPost(t, dir, bothWaysBase, "---\nlicense: open\n---\n# First\n\nThe first words.\n")
	})
	edited := "---\nlicense: open\n---\n# First, revised\n\nBetter words.\n"
	in := filepath.Join(t.TempDir(), "edited.md")
	if err := os.WriteFile(in, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	runCLI(t, "republish", cliDir, bothWaysBase, []string{path, in}, "DISCOVERY_SERVICE_URL=")
	if _, err := apiEngine(t, apiDir, bothWaysBase, "").Dispatch(context.Background(), ops.ActionRequest{Action: "update",
		ContentType: "pub.polis.post", Payload: map[string]any{"path": path, "markdown": edited}}); err != nil {
		t.Fatal(err)
	}
	sameSite(t, cliDir, apiDir, nil)
	if b, _ := os.ReadFile(filepath.Join(cliDir, path)); !strings.Contains(string(b), "Better words.") {
		t.Errorf("polis republish did not write the new version:\n%s", b)
	}
}

func TestPolisTagIsTheAPIsOperation(t *testing.T) {
	var target string
	cliDir, apiDir := twinSites(t, bothWaysBase, func(dir string) {
		target = bothWaysBase + "/" + publishedPost(t, dir, bothWaysBase, "# Tagged\n\nA post to tag.\n")
	})
	steps := []struct {
		cli []string
		op  string
		in  map[string]any
	}{
		{[]string{"apply", "Field Notes", target}, "apply", map[string]any{"tag": "Field Notes", "target_uri": target}},
		{[]string{"apply", "drafts", target}, "apply", map[string]any{"tag": "drafts", "target_uri": target}},
		{[]string{"remove", "drafts", target}, "remove", map[string]any{"tag": "drafts", "target_uri": target}},
	}
	for _, s := range steps {
		runCLI(t, "tag", cliDir, bothWaysBase, s.cli, "DISCOVERY_SERVICE_URL=")
		if _, err := apiEngine(t, apiDir, bothWaysBase, "").Dispatch(context.Background(),
			ops.ActionRequest{Action: s.op, ContentType: "pub.polis.tag", Payload: s.in}); err != nil {
			t.Fatalf("tag.%s: %v", s.op, err)
		}
		sameSite(t, cliDir, apiDir, nil)
	}
	// delete takes the name as polis tag delete normalises it, and so does /v1.
	runCLI(t, "tag", cliDir, bothWaysBase, []string{"delete", "Field Notes"}, "DISCOVERY_SERVICE_URL=")
	if _, err := apiEngine(t, apiDir, bothWaysBase, "").Dispatch(context.Background(),
		ops.ActionRequest{Action: "delete", ContentType: "pub.polis.tag", Payload: map[string]any{"tag": "Field Notes"}}); err != nil {
		t.Fatalf("tag.delete: %v", err)
	}
	sameSite(t, cliDir, apiDir, nil)
}

// followSiteFor is the follow as the web app's /v1 follow.create builds it.
func followSiteFor(dir, base, dsURL string) ops.FollowSite {
	priv, _ := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	return ops.FollowSite{Site: storage.At(dir), PrivateKey: priv, BaseURL: base, DiscoveryURL: dsURL,
		DS: discovery.NewAuthenticatedClient(dsURL, discovery.ExtractDomainFromURL(base), priv), Remote: remote.NewClient()}
}

func bobsSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			json.NewEncoder(w).Encode(map[string]any{"email": "bob@example.com", "public_key": "ssh-ed25519 AAAA", "author": "Bob", "site_title": "Bob's"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPolisFollowAndUnfollowAreTheAPIsOperations(t *testing.T) {
	const base = "https://alice.polis.pub"
	bob := bobsSite(t)
	t.Setenv("POLIS_ORIGIN_OVERRIDES", "bob.example="+bob.URL)
	ds, _ := signedBlessingDS(t, []string{"https://bob.example/comments/20260101/one.md"}, nil)
	cliDir, apiDir := twinSites(t, base, func(dir string) {
		if err := discovery.WriteRegistrationMarker(storage.At(dir), ds.URL, "alice.polis.pub", ""); err != nil {
			t.Fatal(err)
		}
	})
	env := []string{"DISCOVERY_SERVICE_URL=" + ds.URL, "POLIS_ORIGIN_OVERRIDES=bob.example=" + bob.URL}

	runCLI(t, "follow", cliDir, base, []string{"https://bob.example"}, env...)
	if _, err := ops.Follow(context.Background(), followSiteFor(apiDir, base, ds.URL), "https://bob.example"); err != nil {
		t.Fatal(err)
	}
	sameSite(t, cliDir, apiDir, nil)
	if f, _ := following.LoadSite(storage.At(cliDir)); f == nil || !f.IsFollowing("https://bob.example") {
		t.Fatal("polis follow did not follow bob")
	}

	runCLI(t, "unfollow", cliDir, base, []string{"https://bob.example"}, env...)
	if _, err := ops.Unfollow(context.Background(), followSiteFor(apiDir, base, ds.URL), "https://bob.example"); err != nil {
		t.Fatal(err)
	}
	sameSite(t, cliDir, apiDir, nil)
}

// commentDS is a discovery service that takes comment registrations and
// answers each as a new, pending blessing request. It returns what it was sent.
func commentDS(t *testing.T) (*httptest.Server, func() []discovery.ContentRegisterRequest) {
	t.Helper()
	var mu sync.Mutex
	var got []discovery.ContentRegisterRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/content" && r.Method == http.MethodPost {
			var req discovery.ContentRegisterRequest
			json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			got = append(got, req)
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"success": true, "type": req.Type, "url": req.URL,
				"status": "created", "relationship_status": "pending"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []discovery.ContentRegisterRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]discovery.ContentRegisterRequest(nil), got...)
	}
}

// polis comment sign signs a draft and sends it: published here, registered
// with the discovery service with its blessing request. /v1 comment.create
// signs with the shared signing, from exactly in_reply_to, root_post and
// markdown, and sends with the shared send; the CLI runs that same send. So
// the two sites must match, and the service must have had the same request.
func TestPolisCommentSignWritesAndSendsTheCommentTheAPIDoes(t *testing.T) {
	const post = "https://bob.example/posts/20260101/hello.md"
	const words = "A reply, *with* emphasis.\n\nAnd a second paragraph."
	ds, sent := commentDS(t)
	cliDir, apiDir := twinSites(t, bothWaysBase, func(dir string) {
		if err := discovery.WriteRegistrationMarker(storage.At(dir), ds.URL, discovery.ExtractDomainFromURL(bothWaysBase), ""); err != nil {
			t.Fatal(err)
		}
	})

	draft := &comment.CommentDraft{InReplyTo: post, RootPost: post, Content: words}
	if err := comment.SaveDraft(storage.At(cliDir), draft); err != nil {
		t.Fatal(err)
	}
	cliOut := runCLI(t, "comment-sign", cliDir, bothWaysBase, []string{draft.ID}, "DISCOVERY_SERVICE_URL="+ds.URL)
	var got struct {
		ID      string `json:"id"`
		Success bool   `json:"success"`
		Sent    bool   `json:"sent"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(cliOut, &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, cliOut)
	}
	if got.ID != draft.ID {
		t.Errorf("polis comment sign signed %q, want the draft's id %q", got.ID, draft.ID)
	}
	if !got.Success || !got.Sent || got.Status != "created" {
		t.Errorf("polis comment sign did not report the send: %s", cliOut)
	}

	cliSent := sent()
	if len(cliSent) != 1 {
		t.Fatalf("polis comment sign made %d registration(s); want 1", len(cliSent))
	}
	reg := cliSent[0]
	if reg.Type != "pub.polis.comment" || !strings.Contains(reg.URL, draft.ID) || reg.Signature == "" {
		t.Errorf("registration is not this comment's: %+v", reg)
	}
	// The blessing request is asked for by leaving beseech out (beseech=false
	// would register the comment without one).
	if _, asked := reg.Metadata["beseech"]; asked {
		t.Errorf("registration carries beseech=%v; the blessing request must be sent", reg.Metadata["beseech"])
	}
	if reg.Metadata["in_reply_to"] != post {
		t.Errorf("registration replies to %v, want %s", reg.Metadata["in_reply_to"], post)
	}
	published := filepath.Join(cliDir, "content", "pub.polis.core", "comment")
	var found bool
	filepath.WalkDir(published, func(p string, d fs.DirEntry, _ error) error {
		if d != nil && d.Name() == draft.ID+".md" {
			found = true
		}
		return nil
	})
	if !found {
		t.Errorf("the comment was not published under %s", published)
	}

	res, err := apiEngine(t, apiDir, bothWaysBase, ds.URL).Dispatch(context.Background(), ops.ActionRequest{Action: "create",
		ContentType: "pub.polis.comment", Payload: map[string]any{"in_reply_to": post, "root_post": post, "markdown": words}})
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := res.Data["comment"].(*comment.CommentMeta)
	if meta == nil || res.Data["sent"] != true {
		t.Fatalf("comment.create did not write and send: %+v", res.Data)
	}
	if all := sent(); len(all) != 2 || !sameMetadataKeys(all[0].Metadata, all[1].Metadata) {
		t.Errorf("the CLI and the API sent different registrations: %+v", all)
	}

	// The id is the draft's on the CLI and made from the clock on the API.
	ids := regexp.MustCompile(regexp.QuoteMeta(draft.ID) + `|` + regexp.QuoteMeta(meta.ID))
	sameSite(t, cliDir, apiDir, func(s string) string { return ids.ReplaceAllString(s, "<id>") })
}

func sameMetadataKeys(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func TestPolisDMSendHandsTheSharedSendWhatTheAPIDoes(t *testing.T) {
	// A recipient with a messages key that turns delivery away: the message is
	// sealed and kept, unsent, with what it says and to whom, which is what the
	// two must agree on.
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	kr := &dm.Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	block, err := dm.BuildMessagesKeyBlock(kr, privPEM)
	if err != nil {
		t.Fatal(err)
	}
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			json.NewEncoder(w).Encode(map[string]any{"public_key": string(pubSSH), "public_key_messages": block})
			return
		}
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(refusing.Close)
	recipient := refusing.URL
	const words = "Hello from the both-ways test."
	cliDir, apiDir := twinSites(t, bothWaysBase, nil)

	runCLI(t, "dm-send", cliDir, bothWaysBase, []string{recipient, words})
	if _, err := apiEngine(t, apiDir, bothWaysBase, "").Dispatch(context.Background(), ops.ActionRequest{Action: "send",
		ContentType: "pub.polis.dm", Payload: map[string]any{"recipient_url": recipient, "content": words}}); err != nil {
		t.Fatal(err)
	}
	cli, api := unsentDMs(t, cliDir), unsentDMs(t, apiDir)
	if len(cli) != 1 || len(api) != 1 {
		t.Fatalf("want one unsent message each, got CLI %v, API %v", cli, api)
	}
	if cli[0] != api[0] {
		t.Errorf("the CLI handed the send something else:\nCLI: %s\nAPI: %s", cli[0], api[0])
	}
	if !strings.Contains(cli[0], words) || !strings.Contains(cli[0], "127.0.0.1") {
		t.Errorf("the kept message lost its words or its recipient: %s", cli[0])
	}
}

// unsentDMs is each message the site keeps to send again, read as polis dm
// read reads it: to whom, what it says, what it replies to, and its state.
func unsentDMs(t *testing.T, dir string) []string {
	t.Helper()
	deks, err := dm.LoadAvailableDEKs(storage.At(dir))
	if err != nil {
		t.Fatal(err)
	}
	mb := dm.NewMailbox(storage.At(dir))
	unsent, err := mb.UnsentMessages()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, u := range unsent {
		msgs, err := mb.ReadConversation(u.Peer, deks)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range msgs {
			if m.ID == u.Message.ID {
				out = append(out, strings.Join([]string{u.Peer, m.From, m.To, m.Plaintext, u.Message.ReplyTo, m.Status}, " | "))
			}
		}
	}
	return out
}
