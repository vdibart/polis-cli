package branch

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

const privateKeyBlock = "-----BEGIN OPENSSH PRIVATE KEY-----"

// keyLines is the base64 body of an OpenSSH private key, line by line, minus
// the lines every Ed25519 key shares (the format header): any one left found in
// a file means the key leaked there, wrapped or not.
func keyLines(t *testing.T, priv []byte) []string {
	t.Helper()
	other, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	split := func(b []byte) []string {
		var got []string
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if l = strings.TrimSpace(l); len(l) >= 32 && !strings.HasPrefix(l, "-----") {
				got = append(got, l)
			}
		}
		return got
	}
	common := map[string]bool{}
	for _, l := range split(other) {
		common[l] = true
	}
	var got []string
	for _, l := range split(priv) {
		if !common[l] {
			got = append(got, l)
		}
	}
	return got
}

// assertNoRecoveryKey fails on any file holding the recovery private key, and
// on any private key block outside the identity key's own file.
func assertNoRecoveryKey(t *testing.T, name string, files map[string][]byte, recovery []byte) {
	t.Helper()
	lines := keyLines(t, recovery)
	if len(lines) == 0 {
		t.Fatal("the recovery key has no body to look for")
	}
	for p, b := range files {
		if bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(recovery)) {
			t.Errorf("⛔ %s: %s IS the recovery private key", name, p)
		}
		flat := string(bytes.ReplaceAll(b, []byte("\n"), nil))
		for _, l := range lines {
			if strings.Contains(flat, l) {
				t.Errorf("⛔ %s: the recovery private key is in %s", name, p)
				break
			}
		}
		if p != site.PrivateKeyKey && bytes.Contains(b, []byte(privateKeyBlock)) {
			t.Errorf("⛔ %s: a private key block is in %s", name, p)
		}
	}
}

func dirFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	got := map[string][]byte{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		got[filepath.ToSlash(rel)] = readFile(t, p)
		return nil
	})
	return got
}

// zipFiles reads the archive's entries directly, not by unpacking it.
func zipFiles(t *testing.T, archive string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = b
	}
	return got
}

// ⛔⛔ A server-signed branch makes the owner's recovery key, writes the
// private half OUTSIDE the output (by default under ~/.polis/recovery, 0600 in a
// 0700 directory), and the output carries only the public half. Both output
// forms are walked for the private key.
func TestBranchMakesTheRecoveryKeyOutsideTheOutput(t *testing.T) {
	for _, archive := range []bool{false, true} {
		name := map[bool]string{false: "directory", true: "archive"}[archive]
		t.Run(name, func(t *testing.T) {
			noNetwork(t)
			home := os.Getenv("HOME")
			origin, _ := buildOrigin(t)
			opts := Options{Source: origin, To: targetURL}
			if archive {
				opts.Archive = filepath.Join(t.TempDir(), "branched.zip")
			} else {
				opts.Out = branchOut(t)
			}
			res, err := Run(opts)
			if err != nil {
				t.Fatalf("branch: %v", err)
			}

			want := filepath.Join(home, ".polis", "recovery", targetDomain)
			if res.RecoveryKeyPath != want {
				t.Fatalf("recovery key at %s, want the default %s", res.RecoveryKeyPath, want)
			}
			info, err := os.Stat(want)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("recovery key mode = %o, want 600", info.Mode().Perm())
			}
			dinfo, _ := os.Stat(filepath.Dir(want))
			if dinfo.Mode().Perm() != 0o700 {
				t.Errorf("recovery key directory mode = %o, want 700", dinfo.Mode().Perm())
			}
			recovery := readFile(t, want)
			if !bytes.Contains(recovery, []byte(privateKeyBlock)) {
				t.Fatal("the recovery key is not an OpenSSH private key")
			}

			var files map[string][]byte
			if archive {
				files = zipFiles(t, opts.Archive)
			} else {
				files = dirFiles(t, opts.Out)
			}
			assertNoRecoveryKey(t, name, files, recovery)

			// The public half is in the output, matches the private half, and is
			// not the identity key: server-signed, the owner keeps a separate key.
			pub, ok := files[owner.RecoveryPublicKeyKey]
			if !ok {
				t.Fatalf("the output has no %s", owner.RecoveryPublicKeyKey)
			}
			derived, _ := signing.PublicKeyFromPrivate(recovery)
			if !site.SameSSHKey(string(pub), string(derived)) {
				t.Error("the public half in the output does not match the recovery private key")
			}
			if site.SameSSHKey(string(pub), res.NewPublicKey) {
				t.Error("server-signed, the recovery key must not be the identity key")
			}
			if res.RecoveryPublicKey != strings.TrimSpace(string(pub)) {
				t.Errorf("result names recovery public key %q", res.RecoveryPublicKey)
			}
		})
	}
}

// --recovery-key names another path; it is never overwritten, and never inside
// the input or the output. Each refusal writes nothing.
func TestBranchRecoveryKeyPathRefusals(t *testing.T) {
	noNetwork(t)
	cases := []struct {
		name string
		path func(origin, out string) string
		text string
	}{
		{"an existing file", func(_, _ string) string {
			p := filepath.Join(t.TempDir(), "existing")
			os.WriteFile(p, []byte("not mine to replace"), 0o600)
			return p
		}, "never overwritten"},
		{"inside the output", func(_, out string) string { return filepath.Join(out, "recovery") }, "outside"},
		{"inside the input", func(origin, _ string) string { return filepath.Join(origin, "recovery") }, "outside"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			origin, _ := buildOrigin(t)
			out := branchOut(t)
			p := c.path(origin, out)
			before, _ := os.ReadFile(p)
			_, err := Run(Options{Source: origin, To: targetURL, Out: out, RecoveryKey: p})
			if err == nil || !strings.Contains(err.Error(), c.text) {
				t.Fatalf("err = %v, want a refusal mentioning %q", err, c.text)
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Error("the file at the recovery path changed")
			}
			if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
				t.Error("a refused branch wrote its output")
			}
		})
	}
}

// A branch that fails after the recovery key is written removes it: no orphan
// key for a site that does not exist.
func TestAFailedBranchLeavesNoRecoveryKey(t *testing.T) {
	noNetwork(t)
	origin, _ := buildOrigin(t)
	p := filepath.Join(t.TempDir(), "rk")
	prev := finishOutput
	finishOutput = func(string, string, bool) error {
		if _, err := os.Stat(p); err != nil {
			t.Error("the recovery key was not written before the output")
		}
		return errors.New("disk full")
	}
	t.Cleanup(func() { finishOutput = prev })
	if _, err := Run(Options{Source: origin, To: targetURL, Out: branchOut(t), RecoveryKey: p}); err == nil {
		t.Fatal("the branch did not fail")
	}
	if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
		t.Error("⛔ a failed branch left its recovery key behind")
	}
}

// ⭐ The custody seam: one function decides which key the owner keeps.
// Client-signed is designed for and refused, and the seam already answers it:
// the identity key is withheld and is the recovery key.
func TestCustodySeam(t *testing.T) {
	identity, identityPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	withheld, pub, withholdIdentity, err := ownerKeys(ServerSigned, identity)
	if err != nil {
		t.Fatal(err)
	}
	if withholdIdentity || bytes.Equal(withheld, identity) || site.SameSSHKey(string(pub), string(identityPub)) {
		t.Error("server-signed must make a separate recovery key and leave the identity key in the output")
	}
	if d, _ := signing.PublicKeyFromPrivate(withheld); !site.SameSSHKey(string(d), string(pub)) {
		t.Error("server-signed: the public half does not match the withheld key")
	}

	withheld, pub, withholdIdentity, err = ownerKeys(ClientSigned, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !withholdIdentity || !bytes.Equal(withheld, identity) || !site.SameSSHKey(string(pub), string(identityPub)) {
		t.Error("client-signed: the identity key must be withheld and be the recovery key")
	}

	if _, _, _, err := ownerKeys("custodied", identity); err == nil {
		t.Error("an unknown custody was accepted")
	}

	noNetwork(t)
	origin, _ := buildOrigin(t)
	out := branchOut(t)
	_, err = Run(Options{Source: origin, To: targetURL, Out: out, Custody: ClientSigned})
	if !errors.Is(err, ErrClientSignedNotBuilt) {
		t.Fatalf("client-signed: err = %v, want ErrClientSignedNotBuilt", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a refused client-signed branch wrote its output")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".polis")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a refused client-signed branch wrote a key")
	}
}
