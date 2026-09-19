// Package fleettest builds a fixture fleet: a set of sites, each a healthy site
// with exactly one thing broken, for tests that pin what a site check reports
// over them.
//
// ⛔ Goldens read this fleet. A site check whose findings are consumed by
// string-matching (a heal keyed on a message) cannot reword a message as a
// cosmetic change, so a golden that changes during a refactor is a FINDING,
// not a file to regenerate. Tailor's golden (pkg/tailor) reads it here; the
// hosted service's checks read the same fleet.
//
// It is a package rather than a _test file so that tests in more than one
// package can build the same fleet. Nothing outside tests imports it.
package fleettest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// BaseDomain is the operator domain the fleet is checked under.
const BaseDomain = "polis.pub"

// DSDomain is the operator's discovery service; any other .polis/ds/<domain>/
// is foreign. Callers export DISCOVERY_SERVICE_URL=https://DSDomain for the
// hosted envelope, and pass it as ExpectedDSDomains to CheckTenant.
const DSDomain = "ds.polis.pub"

var (
	// T0 is every fixture file's mtime when the fleet is built; T1 is the mtime a
	// between-sweeps mutation stamps. Fixed so snapshot.json and mtime alerts are
	// reproducible.
	T0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	T1 = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	// fixedSalt replaces site.Init's random storage salt, so the salt hash in
	// snapshot.json is the same on every run.
	fixedSalt = strings.Repeat("ab", 32)
)

// Tenant is one fixture: a healthy site with Break applied. Mutate, when set,
// runs between the first and second sweep — the snapshot-based alerts need a
// baseline to differ from.
type Tenant struct {
	Name   string
	Break  func(t testing.TB, s *Site)
	Mutate func(t testing.TB, s *Site)
}

// Site is a fixture tenant on disk.
type Site struct {
	Dir    string
	Handle string
}

// Domain is the tenant's host under BaseDomain.
func (s *Site) Domain() string { return s.Handle + "." + BaseDomain }

// Path joins rel (slash-separated) onto the site directory.
func (s *Site) Path(rel string) string { return filepath.Join(s.Dir, filepath.FromSlash(rel)) }

// Write writes content at rel with perm, creating parent directories.
func (s *Site) Write(t testing.TB, rel, content string, perm os.FileMode) {
	t.Helper()
	p := s.Path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, perm); err != nil {
		t.Fatal(err)
	}
}

// Read returns the file at rel.
func (s *Site) Read(t testing.TB, rel string) string {
	t.Helper()
	b, err := os.ReadFile(s.Path(rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Remove deletes rel (recursively).
func (s *Site) Remove(t testing.TB, rel string) {
	t.Helper()
	if err := os.RemoveAll(s.Path(rel)); err != nil {
		t.Fatal(err)
	}
}

// Mkdir creates rel with perm.
func (s *Site) Mkdir(t testing.TB, rel string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(s.Path(rel), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Path(rel), perm); err != nil {
		t.Fatal(err)
	}
}

// Chmod sets rel's permissions.
func (s *Site) Chmod(t testing.TB, rel string, perm os.FileMode) {
	t.Helper()
	if err := os.Chmod(s.Path(rel), perm); err != nil {
		t.Fatal(err)
	}
}

// Touch sets rel's mtime.
func (s *Site) Touch(t testing.TB, rel string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(s.Path(rel), at, at); err != nil {
		t.Fatal(err)
	}
}

// EditJSON loads the JSON object at rel, lets edit change it, and writes it
// back (two-space indent, trailing newline), keeping the file's permissions.
func (s *Site) EditJSON(t testing.TB, rel string, edit func(m map[string]any)) {
	t.Helper()
	p := s.Path(rel)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s.Read(t, rel)), &m); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	edit(m)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	s.Write(t, rel, string(out)+"\n", info.Mode().Perm())
}

// PrivateKey returns the site's private key PEM.
func (s *Site) PrivateKey(t testing.TB) []byte {
	t.Helper()
	return []byte(s.Read(t, ".polis/keys/id_ed25519"))
}

// SignedMarkdown writes a post (or, isComment, a comment) signed with priv the
// way publish signs one, claiming published. author, when set, is written as a
// frontmatter field (outside a comment's signature, inside a post's).
func (s *Site) SignedMarkdown(t testing.TB, rel string, priv []byte, body, published, author string, isComment bool) {
	t.Helper()
	canonical := signing.CanonicalizeContent(body)
	sum := sha256.Sum256([]byte(canonical))
	hash := hex.EncodeToString(sum[:])
	head := fmt.Sprintf("---\ntitle: Fixture\npublished: %s\n", published)
	if isComment {
		head += "type: comment\n"
	} else if author != "" {
		head += "author: " + author + "\n"
	}
	unsigned := head + fmt.Sprintf("current-version: sha256:%s\n---", hash)
	sig, err := signing.SignContent([]byte(signing.CanonicalizeContent(unsigned+"\n\n"+canonical)), priv)
	if err != nil {
		t.Fatal(err)
	}
	tail := fmt.Sprintf("current-version: sha256:%s\n", hash)
	if isComment && author != "" {
		tail += "author: " + author + "\n"
	}
	tail += "signature: " + bareSignature(sig) + "\n---"
	s.Write(t, rel, head+tail+"\n\n"+canonical, 0644)
}

// bareSignature strips the PEM armour from an SSH signature.
func bareSignature(pem string) string {
	var b strings.Builder
	for _, line := range strings.Split(pem, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// Healthy builds a site that passes every Patrol check.
func Healthy(t testing.TB, dir string) *Site {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	s := &Site{Dir: dir, Handle: filepath.Base(dir)}
	if _, err := site.Init(dir, site.InitOptions{
		Author:    "Fixture " + s.Handle,
		SiteTitle: s.Handle,
		Theme:     "vice",
		BaseURL:   "https://" + s.Domain(),
	}); err != nil {
		t.Fatalf("init %s: %v", s.Handle, err)
	}
	s.Write(t, ".polis/storage-salt", fixedSalt, 0600)
	return s
}

// StampTimes sets every path under dir to T0, so mtimes in snapshots and alerts
// never depend on when the test ran.
func StampTimes(t testing.TB, dir string) {
	t.Helper()
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		return os.Chtimes(p, T0, T0)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// BuildFleet builds every fixture tenant under tenantsDir, stamped to T0.
func BuildFleet(t testing.TB, tenantsDir string) []*Site {
	t.Helper()
	var sites []*Site
	for _, tn := range Tenants() {
		s := Healthy(t, filepath.Join(tenantsDir, tn.Name))
		if tn.Break != nil {
			tn.Break(t, s)
		}
		sites = append(sites, s)
	}
	StampTimes(t, tenantsDir)
	return sites
}

// MutateFleet applies each tenant's between-sweeps mutation.
func MutateFleet(t testing.TB, tenantsDir string) {
	t.Helper()
	for _, tn := range Tenants() {
		if tn.Mutate == nil {
			continue
		}
		dir := filepath.Join(tenantsDir, tn.Name)
		tn.Mutate(t, &Site{Dir: dir, Handle: tn.Name})
	}
}

// BuildDataRoot plants the data-root findings the hosted envelope reports
// outside any tenant: a shell artifact and a stale archive.
func BuildDataRoot(t testing.TB, dataDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, ".bash_history"), []byte("ls\n"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dataDir, "archives", "gone.tar.gz")
	if err := os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	// 120 days and a half: age_days is floored, so a slow run cannot tip it.
	old := time.Now().Add(-(120*24 + 12) * time.Hour)
	if err := os.Chtimes(archive, old, old); err != nil {
		t.Fatal(err)
	}
}

// Normalize replaces the run-specific directories in s with fixed names, so a
// message naming a path compares across runs.
func Normalize(s string, dirs map[string]string) string {
	// Longest first: a tenants dir sits inside the root dir, and the more
	// specific name must win every time.
	keys := make([]string, 0, len(dirs))
	for dir := range dirs {
		keys = append(keys, dir)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, dir := range keys {
		s = strings.ReplaceAll(s, dir, dirs[dir])
	}
	return s
}

var generatedAt = regexp.MustCompile(`"generated_at": "[^"]*"`)

// NormalizeSnapshot blanks snapshot.json's generated_at — the one field that is
// the wall clock — and leaves every other byte alone.
func NormalizeSnapshot(b []byte) []byte {
	return generatedAt.ReplaceAll(b, []byte(`"generated_at": "<now>"`))
}

// zeroedEnvelope turns a bare signature into the 0.59.0 defect: the same
// signature bytes, an all-zero embedded key.
func zeroedEnvelope(t testing.TB, bare string) string {
	t.Helper()
	z, changed, err := signing.RepairEnvelope(bare, make(ed25519.PublicKey, ed25519.PublicKeySize))
	if err != nil || !changed {
		t.Fatalf("zero envelope: changed=%v err=%v", changed, err)
	}
	return z
}
