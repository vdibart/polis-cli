package provision

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The files of a build as webapp/deploy/vercel/deploy.sh leaves it.
var (
	testConfig   = []byte(`{"version":3,"routes":[{"src":"/(.*)","dest":"/api/index"}]}`)
	testVCConfig = []byte(`{"runtime":"executable","handler":"bootstrap","architecture":"x86_64"}`)
	testBinary   = []byte("\x7fELF the function")
)

// testBuild is a prebuilt .vercel/output tree as an archive, with extra
// entries planted beside it.
func testBuild(t *testing.T, format CodeFormat, extra map[string][]byte) Code {
	t.Helper()
	files := map[string][]byte{
		"config.json": testConfig,
		"functions/api/index.func/.vc-config.json": testVCConfig,
		"functions/api/index.func/bootstrap":       testBinary,
		"builds.json":                              []byte(`{}`),
		"static/.gitkeep":                          nil,
	}
	for k, v := range extra {
		files[k] = v
	}
	names := make([]string, 0, len(files))
	for k := range files {
		names = append(names, k)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	switch format {
	case FormatZip:
		zw := zip.NewWriter(&buf)
		for _, n := range names {
			w, err := zw.Create(n)
			if err != nil {
				t.Fatal(err)
			}
			w.Write(files[n])
		}
		zw.Close()
	default:
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for _, n := range names {
			tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(files[n])), Typeflag: tar.TypeReg})
			tw.Write(files[n])
		}
		tw.Close()
		gz.Close()
	}
	return Code{Archive: buf.Bytes(), Format: format}
}

func uploadedNames(f *fakeVercelAPI) []string {
	var out []string
	for _, df := range f.deployed {
		out = append(out, df.File)
	}
	sort.Strings(out)
	return out
}

// ⭐⭐ The deployment names exactly the build's three files, by digest, each
// uploaded first, the binary executable, and Vercel builds nothing.
func TestPutCodeUploadsTheBuildAndTheDeploymentNamesItByDigest(t *testing.T) {
	ctx := context.Background()
	for _, format := range []CodeFormat{FormatTarGz, FormatZip} {
		f := newFakeVercel(t)
		p := f.provisioner(t)
		inst, _ := p.Create(ctx, CreateRequest{Name: "alice"})
		if _, err := p.PutCode(ctx, inst, testBuild(t, format, nil)); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		want := []string{
			".vercel/output/config.json",
			".vercel/output/functions/api/index.func/.vc-config.json",
			".vercel/output/functions/api/index.func/bootstrap",
		}
		if got := uploadedNames(f); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: deployed %v; want exactly %v", format, got, want)
		}
		for _, df := range f.deployed {
			data, ok := f.uploaded[df.SHA]
			if !ok || len(data) != df.Size {
				t.Errorf("%s: %s names a digest that was not uploaded, or the wrong size", format, df.File)
			}
			wantMode := modeFile
			if strings.HasSuffix(df.File, "/bootstrap") {
				wantMode = modeExecutable
			}
			if df.Mode != wantMode {
				t.Errorf("%s: %s mode %o; want %o", format, df.File, df.Mode, wantMode)
			}
		}
		if !f.prebuilt {
			t.Errorf("%s: the deployment did not ask for prebuilt=1, so Vercel would try to build it", format)
		}
	}
}

// ⛔⛔ A build archive carrying a private name is refused whole, and nothing is
// uploaded: not the planted file, and not the rest of the build either.
func TestPutCodeRefusesABuildHoldingAPrivateName(t *testing.T) {
	ctx := context.Background()
	for _, planted := range []string{
		".polis/keys/id_ed25519",
		".polis/keys/id_ed25519.pub",
		".polis/storage-salt",
		".polis/webapp/config.json",
		".env",
		"functions/api/index.func/.env.local",
		"functions/api/index.func/production.env",
		"static/.git/config",
		"id_ed25519",
		"functions/api/index.func/.ssh/id_rsa",
	} {
		f := newFakeVercel(t)
		p := f.provisioner(t)
		inst, _ := p.Create(ctx, CreateRequest{Name: "alice"})
		_, err := p.PutCode(ctx, inst, testBuild(t, FormatTarGz, map[string][]byte{planted: []byte("-----BEGIN OPENSSH PRIVATE KEY-----")}))
		if err == nil {
			t.Errorf("⛔ a build holding %s was accepted", planted)
			continue
		}
		if len(f.uploaded) != 0 || len(f.deployed) != 0 {
			t.Errorf("⛔ a build holding %s uploaded %d files", planted, len(f.uploaded))
		}
		if strings.Contains(err.Error(), "PRIVATE KEY") {
			t.Errorf("⛔ the refusal quotes the planted file's contents: %v", err)
		}
	}
}

// ⛔ Static files are matched before the rewrite to the function, so a build
// with one is refused; and a build still pointing at a binary outside itself,
// or naming a file outside itself, is refused too.
func TestPutCodeRefusesABrokenBuild(t *testing.T) {
	ctx := context.Background()
	for name, code := range map[string]Code{
		"a static file": testBuild(t, FormatTarGz, map[string][]byte{"static/index.html": []byte("<html>")}),
		"a filePathMap": testBuild(t, FormatTarGz, map[string][]byte{
			"functions/api/index.func/.vc-config.json": []byte(`{"handler":"bootstrap","filePathMap":{"bootstrap":"/tmp/x"}}`),
		}),
		"no binary": testBuild(t, FormatTarGz, map[string][]byte{
			"functions/api/index.func/.vc-config.json": []byte(`{"handler":"main"}`),
		}),
		"a path out": testBuild(t, FormatTarGz, map[string][]byte{"../outside": []byte("x")}),
	} {
		f := newFakeVercel(t)
		p := f.provisioner(t)
		inst, _ := p.Create(ctx, CreateRequest{Name: "alice"})
		if _, err := p.PutCode(ctx, inst, code); err == nil {
			t.Errorf("a build with %s was accepted", name)
		}
		if len(f.uploaded) != 0 {
			t.Errorf("a build with %s uploaded files", name)
		}
	}
}

// ⛔ An upload error names the status, the file and its digest, never the
// response body or the file's contents.
func TestAnUploadErrorNamesTheFileAndDigestOnly(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, _ := p.Create(ctx, CreateRequest{Name: "alice"})
	f.failNext["/v2/files"] = 500
	_, err := p.PutCode(ctx, inst, testBuild(t, FormatTarGz, nil))
	if err == nil {
		t.Fatal("a failed upload was not reported")
	}
	msg := err.Error()
	if !strings.Contains(msg, "500") || !strings.Contains(msg, ".vercel/output/") || !strings.Contains(msg, "sha1 ") {
		t.Errorf("the error does not name the status, file and digest: %s", msg)
	}
	if strings.Contains(msg, "injected") || strings.Contains(msg, "the function") {
		t.Errorf("⛔ the error carries a response body or file contents: %s", msg)
	}
}

// ⭐ ArchiveBuildOutput packs a real directory into Code PutCode accepts.
func TestArchiveBuildOutputPacksADirectoryPutCodeAccepts(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, data []byte, mode os.FileMode) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", testConfig, 0o644)
	write("functions/api/index.func/.vc-config.json", testVCConfig, 0o644)
	write("functions/api/index.func/bootstrap", testBinary, 0o755)
	code, err := ArchiveBuildOutput(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := buildOutputFiles(code)
	if err != nil || len(files) != 3 {
		t.Fatalf("files = %d, err = %v; want the three of the build", len(files), err)
	}
}
