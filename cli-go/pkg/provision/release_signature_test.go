package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/releasekey"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// signRelease publishes a signed checksums.txt beside a test release's assets,
// as the release does, and pins the throwaway key that signed it for the
// length of the test. It returns the private key, for a test that re-signs.
func signRelease(t *testing.T, served map[string][]byte, versions ...string) []byte {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	old := releaseKey
	releaseKey = strings.TrimSpace(string(pub))
	t.Cleanup(func() { releaseKey = old })
	for _, v := range versions {
		resignRelease(t, served, v, priv)
	}
	return priv
}

func resignRelease(t *testing.T, served map[string][]byte, version string, priv []byte) {
	t.Helper()
	prefix := "/" + ReleaseRepo + "/releases/download/v" + version + "/"
	var names []string
	for p := range served {
		if name, ok := strings.CutPrefix(p, prefix); ok && name != releasekey.ChecksumsAsset && name != releasekey.SignatureAsset {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, name := range names {
		s := sha256.Sum256(served[prefix+name])
		sums.WriteString(hex.EncodeToString(s[:]) + "  " + name + "\n")
	}
	sig, err := signing.SignContentNS([]byte(sums.String()), priv, releasekey.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	served[prefix+releasekey.ChecksumsAsset] = []byte(sums.String())
	served[prefix+releasekey.SignatureAsset] = []byte(sig)
}

// ⛔ A release asset is trusted only through the release's signature: a
// tampered checksums.txt, a missing signature, a signature by another key, or
// an asset that is not the one checksums.txt names are each refused, even
// when the manifest and the build agree with each other.
func TestReleaseAssetsAreRefusedWithoutTheReleaseSignature(t *testing.T) {
	ctx := context.Background()
	code := testBuild(t, FormatTarGz, nil)
	m, err := FunctionManifestFor(code, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	mb, _ := json.Marshal(m)
	prefix := "/" + ReleaseRepo + "/releases/download/v1.2.3/"
	served := map[string][]byte{prefix + FunctionManifestAsset: mb, prefix + FunctionBuildAsset: code.Archive}
	priv := signRelease(t, served, "1.2.3")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := served[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	old := releaseBase
	releaseBase = srv.URL
	defer func() { releaseBase = old }()

	if _, err := ReleasedFunctionCode(ctx, srv.Client(), "1.2.3"); err != nil {
		t.Fatalf("a signed release was refused: %v", err)
	}

	good := map[string][]byte{}
	for k, v := range served {
		good[k] = v
	}
	restore := func() {
		for k := range served {
			delete(served, k)
		}
		for k, v := range good {
			served[k] = v
		}
	}
	cases := []struct {
		name   string
		change func()
	}{
		{"a tampered checksums.txt", func() {
			served[prefix+releasekey.ChecksumsAsset] = append([]byte("0"), served[prefix+releasekey.ChecksumsAsset][1:]...)
		}},
		{"a missing signature", func() { delete(served, prefix+releasekey.SignatureAsset) }},
		{"a signature by another key", func() {
			other, _, _ := signing.GenerateKeypair()
			resignRelease(t, served, "1.2.3", other)
		}},
		{"a manifest that is not the signed one (rebuilt to match a swapped build)", func() {
			swapped := testBuild(t, FormatTarGz, map[string][]byte{"extra.txt": []byte("swapped")})
			sm, _ := FunctionManifestFor(swapped, "1.2.3")
			smb, _ := json.Marshal(sm)
			served[prefix+FunctionManifestAsset] = smb
			served[prefix+FunctionBuildAsset] = swapped.Archive
		}},
	}
	for _, c := range cases {
		restore()
		c.change()
		if _, err := ReleasedFunctionCode(ctx, srv.Client(), "1.2.3"); err == nil {
			t.Errorf("%s was accepted", c.name)
		}
		if _, err := FetchFunctionManifest(ctx, srv.Client(), "1.2.3"); err == nil && c.name != "a manifest that is not the signed one (rebuilt to match a swapped build)" {
			t.Errorf("%s: the manifest was accepted", c.name)
		}
	}
	// The same swap, re-signed by the release key, is a real release again: the signature is the trust.
	restore()
	swapped := testBuild(t, FormatTarGz, map[string][]byte{"extra.txt": []byte("swapped")})
	sm, _ := FunctionManifestFor(swapped, "1.2.3")
	smb, _ := json.Marshal(sm)
	served[prefix+FunctionManifestAsset] = smb
	served[prefix+FunctionBuildAsset] = swapped.Archive
	resignRelease(t, served, "1.2.3", priv)
	if _, err := ReleasedFunctionCode(ctx, srv.Client(), "1.2.3"); err != nil {
		t.Errorf("a release re-signed by the release key was refused: %v", err)
	}
}
