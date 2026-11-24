package provision

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" //nolint:gosec // G505: SHA-1 is what Vercel's file-upload API names files by; not used for integrity or signing
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/privatepath"
)

// ⭐⭐ THE BUILD MODEL IS A PREBUILT BUILD OUTPUT API DEPLOYMENT. The function
// is compiled on the machine that provisions (webapp/deploy/vercel/deploy.sh
// runs `vercel build` with CGO_ENABLED=0), and only its output is uploaded:
// the .vercel/output tree, never source. ⚠️ So Vercel never builds Go, and the
// cli-go replace in webapp/go.mod, which points outside the webapp directory,
// never has to resolve on Vercel's side; it resolves where the build runs.
//
// Code.Archive is that tree as a tarball or zip, with entries named relative
// to .vercel/output (a leading ".vercel/output/" is also accepted).

// defaultFunction is the function the build serves every path from
// (webapp/api/index.go, routed by webapp/vercel.json).
const defaultFunction = "api/index"

// File modes as Vercel's deployment API wants them: the whole st_mode, file
// type included, as `vercel deploy` sends it.
const (
	modeFile       = 0o100644
	modeExecutable = 0o100755
)

// maxBuildArchive bounds what PutCode will read out of an archive. The
// function binary is about 16 MB; this leaves room and refuses a bomb.
const maxBuildArchive = 256 << 20

// uploadFile is one file a deployment names by digest.
type uploadFile struct {
	name string // as the deployment names it: ".vercel/output/…"
	data []byte
	mode int
	sha  string // hex SHA-1, the digest Vercel's file API keys on
}

// buildOutputFiles reads a prebuilt build out of code and returns exactly the
// files its deployment needs.
//
// ⛔⛔ AN ALLOW-LIST, NEVER "EVERYTHING IN THE ARCHIVE". What is uploaded is
// config.json, the function's .vc-config.json and its handler binary, and
// nothing else. ⛔ An archive that also carries a private name (a key, .env,
// anything under .polis, or any name pkg/privatepath denies) is REFUSED
// whole: a build holding one is broken, and skipping it quietly would hide
// that. A static file is refused too: the CODE carries none, because the
// deployment's static files are the SITE's public tree, which the instance
// itself deploys (staticFiles), and a static file in the build would shadow
// one of the site's. Other build leftovers (builds.json, diagnostics) are not
// uploaded.
func buildOutputFiles(code Code) ([]uploadFile, error) {
	entries, err := readArchive(code)
	if err != nil {
		return nil, err
	}
	fn := code.Entrypoint
	if fn == "" {
		fn = defaultFunction
	}
	fn = strings.Trim(fn, "/")
	if fn == "" || strings.Contains(fn, "..") || strings.ContainsAny(fn, "\\:") {
		return nil, fmt.Errorf("provision: function %q is not a function path", code.Entrypoint)
	}
	fnDir := "functions/" + fn + ".func/"
	vcConfig := fnDir + ".vc-config.json"

	raw, ok := entries[vcConfig]
	if !ok {
		return nil, fmt.Errorf("provision: the build has no %s; was it built with `vercel build`?", vcConfig)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("provision: %s does not parse", vcConfig)
	}
	if _, ok := cfg["filePathMap"]; ok {
		// ⚠️ The builder points at a binary outside the build; the published
		// builder (webapp/cmd/vercel-function) moves it in and drops the
		// pointer. A build that still has one uploads a function with no binary.
		return nil, fmt.Errorf("provision: %s still has a filePathMap, so its binary is outside the build; build the function with `go run ./cmd/vercel-function` in webapp/", vcConfig)
	}
	handler, _ := cfg["handler"].(string)
	if handler == "" {
		handler = "bootstrap"
	}
	if strings.ContainsAny(handler, "/\\") || handler == "." || handler == ".." {
		return nil, fmt.Errorf("provision: %s names a handler outside the function", vcConfig)
	}

	allowed := map[string]int{
		"config.json":   modeFile,
		vcConfig:        modeFile,
		fnDir + handler: modeExecutable,
	}
	for name := range entries {
		if _, ok := allowed[name]; ok || name == "static/.gitkeep" {
			// The empty static directory's placeholder is never uploaded.
			continue
		}
		if isPrivateBuildName(name) {
			// ⛔ Name the entry, never its contents.
			return nil, fmt.Errorf("provision: the build archive holds %s, a private name; refusing to upload any of it", name)
		}
		if strings.HasPrefix(name, "static/") {
			return nil, fmt.Errorf("provision: the build has a static file (%s); the code carries none, because static files are the site's own; refusing", name)
		}
	}

	// ⭐ The instance's routing goes into the build's own config.json, which is
	// what a vercel.json compiles to (VercelInstanceConfig).
	if code.Routing != nil {
		cfgJSON, ok := entries["config.json"]
		if !ok {
			return nil, fmt.Errorf("provision: the build has no config.json")
		}
		routed, err := VercelInstanceConfig(cfgJSON, *code.Routing)
		if err != nil {
			return nil, err
		}
		entries["config.json"] = routed
	}

	var files []uploadFile
	for _, name := range []string{"config.json", vcConfig, fnDir + handler} {
		data, ok := entries[name]
		if !ok {
			return nil, fmt.Errorf("provision: the build has no %s", name)
		}
		sum := sha1.Sum(data) //nolint:gosec // G401: SHA-1 is what Vercel's file-upload API names files by; not used for integrity or signing
		files = append(files, uploadFile{
			name: ".vercel/output/" + name, data: data, mode: allowed[name], sha: hex.EncodeToString(sum[:]),
		})
	}
	return files, nil
}

// isPrivateBuildName reports whether an archive entry names something that
// must never leave the machine: what pkg/privatepath denies (which covers
// .polis, keys and every hidden name), and .env files by any spelling.
func isPrivateBuildName(name string) bool {
	if privatepath.Denied(name) {
		return true
	}
	base := strings.ToLower(path.Base(name))
	return base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env")
}

// readArchive returns the regular files of a build archive by name, relative
// to .vercel/output. ⛔ A link, an absolute name, a ".." segment or a
// backslash refuses the whole archive: each could name a file outside the
// build.
func readArchive(code Code) (map[string][]byte, error) {
	out := map[string][]byte{}
	total := 0
	add := func(name string, r io.Reader) error {
		clean, err := buildEntryName(name)
		if err != nil {
			return err
		}
		if clean == "" {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(r, int64(maxBuildArchive-total)+1))
		if err != nil {
			return fmt.Errorf("provision: read %s from the build archive: %w", clean, err)
		}
		total += len(data)
		if total > maxBuildArchive {
			return fmt.Errorf("provision: the build archive is larger than %d MB", maxBuildArchive>>20)
		}
		out[clean] = data
		return nil
	}
	switch code.Format {
	case FormatTarGz:
		gz, err := gzip.NewReader(bytes.NewReader(code.Archive))
		if err != nil {
			return nil, fmt.Errorf("provision: the build archive is not gzip: %w", err)
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("provision: the build archive does not read: %w", err)
			}
			switch h.Typeflag {
			case tar.TypeDir:
				continue
			case tar.TypeReg:
				if err := add(h.Name, tr); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("provision: the build archive holds %s, which is not a regular file; refusing", h.Name)
			}
		}
	case FormatZip:
		zr, err := zip.NewReader(bytes.NewReader(code.Archive), int64(len(code.Archive)))
		if err != nil {
			return nil, fmt.Errorf("provision: the build archive is not a zip: %w", err)
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			if !f.Mode().IsRegular() {
				return nil, fmt.Errorf("provision: the build archive holds %s, which is not a regular file; refusing", f.Name)
			}
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("provision: read %s from the build archive: %w", f.Name, err)
			}
			err = add(f.Name, rc)
			_ = rc.Close() // read side
			if err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("provision: unknown code format %q", code.Format)
	}
	return out, nil
}

// buildEntryName is an archive entry's name relative to .vercel/output.
func buildEntryName(name string) (string, error) {
	if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("provision: the build archive holds %q, which could name a file outside the build; refusing", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return "", fmt.Errorf("provision: the build archive holds %q, which could name a file outside the build; refusing", name)
		}
	}
	clean := strings.TrimPrefix(path.Clean("/"+name), "/")
	clean = strings.TrimPrefix(clean, ".vercel/output/")
	if clean == "." || clean == ".vercel/output" {
		return "", nil
	}
	return clean, nil
}

// upload sends one file to Vercel's file API, keyed by its digest.
//
// ⛔ An error names the status, the file and its digest, never a response body
// or the file's contents, as every error in this file does.
func (v *Vercel) upload(ctx context.Context, f uploadFile) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.withTeam("/v2/files"), bytes.NewReader(f.data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+v.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("x-now-digest", f.sha)
	req.Header.Set("x-now-size", strconv.Itoa(len(f.data)))
	req.ContentLength = int64(len(f.data))
	resp, err := v.hc.Do(req)
	if err != nil {
		return fmt.Errorf("provision: upload %s (sha1 %s): %w", f.name, f.sha, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("provision: upload %s (sha1 %s): HTTP %d", f.name, f.sha, resp.StatusCode)
	}
	return nil
}

// ArchiveBuildOutput packs a .vercel/output directory as Code for PutCode,
// keeping each file's executable bit. ⚠️ It packs what is there; PutCode's
// allow-list decides what is uploaded.
func ArchiveBuildOutput(dir string) (Code, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("provision: %s is not a regular file", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // G304: a file this walk found in the build directory
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: int64(info.Mode().Perm()),
			Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		return Code{}, err
	}
	if err := tw.Close(); err != nil {
		return Code{}, err
	}
	if err := gz.Close(); err != nil {
		return Code{}, err
	}
	return Code{Archive: buf.Bytes(), Format: FormatTarGz}, nil
}

// ArchiveFunctionBuild packs the function of a .vercel/output directory as
// Code for PutCode: config.json and everything under functions/, nothing else.
//
// ⭐ The code carries no static file: a deployment's static files are the
// site's public tree, which the instance deploys itself. ⛔ A directory with no
// config.json is refused, naming what it lacks.
func ArchiveFunctionBuild(dir string) (Code, error) {
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil || !info.Mode().IsRegular() {
		return Code{}, fmt.Errorf("provision: %s holds no config.json; name a build output directory (.vercel/output)", dir)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() || !(rel == "config.json" || strings.HasPrefix(rel, "functions/")) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("provision: %s is not a regular file", p)
		}
		data, err := os.ReadFile(p) //nolint:gosec // G304: a regular file in the build directory, checked above
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: rel, Mode: int64(info.Mode().Perm()), Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		return Code{}, fmt.Errorf("provision: packing %s: %w", dir, err)
	}
	if err := tw.Close(); err != nil {
		return Code{}, err
	}
	if err := gz.Close(); err != nil {
		return Code{}, err
	}
	return Code{Archive: buf.Bytes(), Format: FormatTarGz}, nil
}
