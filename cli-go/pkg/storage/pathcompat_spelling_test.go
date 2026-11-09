package storage

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"testing"
)

// spellingFixture builds
//
//	<tmp>/work/d/a.md
//	<tmp>/work/d/sub/b.md
//	<tmp>/work/f.txt
//
// and changes into <tmp>/work/d, so that ".", ".." and relative spellings all
// mean something.
func spellingFixture(t *testing.T) (work string) {
	t.Helper()
	work = filepath.Join(t.TempDir(), "work")
	for _, f := range []string{"d/a.md", "d/sub/b.md", "f.txt"} {
		p := filepath.Join(work, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(filepath.Join(work, "d"))
	return work
}

func names(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, path.Base(e.Key))
	}
	sort.Strings(out)
	return out
}

func osNames(t *testing.T, dir string) ([]string, error) {
	t.Helper()
	des, err := os.ReadDir(dir)
	var out []string
	for _, de := range des {
		out = append(out, de.Name())
	}
	sort.Strings(out)
	return out, err
}

// ⛔ The path helpers replace os calls, so a spelling the os call accepted must
// address the SAME directory through the helper. Two fixes in a row got this
// wrong in different directions: a trailing slash once addressed d/d, and the
// fix for that made "." and "/" a refusal — so `patrol .` run from the tenants
// directory swept zero tenants and exited clean.
//
// The oracle here is the os call itself, on purpose: this test has no opinion
// about any spelling, only that the helper agrees with what it replaced.
func TestListDirAgreesWithOsReadDirForEverySpelling(t *testing.T) {
	work := spellingFixture(t)
	abs := filepath.Join(work, "d")

	for _, dir := range []string{
		".", "./", "..", "../", "../d", "../d/", "../d//", "sub", "sub/", "./sub", "sub/..", "sub/../",
		abs, abs + "/", abs + "//", abs + "/.", abs + "/sub/..", abs + "/..",
		"/",
		"", "missing", "missing/", abs + "/missing", "a.md",
	} {
		want, wantErr := osNames(t, dir)
		got, gotErr := ListDir(dir)
		if (wantErr == nil) != (gotErr == nil) {
			t.Errorf("ListDir(%q): err = %v, os.ReadDir err = %v", dir, gotErr, wantErr)
			continue
		}
		if wantErr != nil {
			continue
		}
		if g := names(got); !equalStrings(g, want) {
			t.Errorf("ListDir(%q) = %v, os.ReadDir = %v", dir, g, want)
		}
	}
}

func TestPathHelpersAgreeWithOsStatForEverySpelling(t *testing.T) {
	work := spellingFixture(t)
	abs := filepath.Join(work, "d")

	for _, p := range []string{
		".", "./", "..", "../", "../d", "../d/", "sub", "sub/", "sub/..", "a.md", "./a.md", "sub/../a.md", "../f.txt",
		abs, abs + "/", abs + "/.", abs + "/..", abs + "/a.md", abs + "/sub/../a.md",
		"/",
		"", "missing", "missing/", "sub/missing", abs + "/missing",
	} {
		fi, osErr := os.Stat(p)

		present, err := PathExists(p)
		if err != nil || present != (osErr == nil) {
			t.Errorf("PathExists(%q) = %v, %v; os.Stat err = %v", p, present, err, osErr)
		}
		absent, err := PathAbsent(p)
		if err != nil || absent != (osErr != nil) {
			t.Errorf("PathAbsent(%q) = %v, %v; os.Stat err = %v", p, absent, err, osErr)
		}

		info, err := StatAtPath(p)
		if (err == nil) != (osErr == nil) {
			t.Errorf("StatAtPath(%q): err = %v, os.Stat err = %v", p, err, osErr)
			continue
		}
		if osErr != nil {
			continue
		}
		if info.IsDir != fi.IsDir() {
			t.Errorf("StatAtPath(%q).IsDir = %v, os says %v", p, info.IsDir, fi.IsDir())
		}
		if !fi.IsDir() {
			if info.Size != fi.Size() {
				t.Errorf("StatAtPath(%q).Size = %d, os says %d", p, info.Size, fi.Size())
			}
			want, _ := os.ReadFile(p)
			got, err := ReadAtPath(p)
			if err != nil || string(got) != string(want) {
				t.Errorf("ReadAtPath(%q) = %q, %v; os.ReadFile = %q", p, got, err, want)
			}
		}
	}
}

// A write or a delete addressed with a trailing slash must land on the path it
// names, not one level deeper.
func TestWriteAndDeleteAtPathAddressTheNamedPath(t *testing.T) {
	work := spellingFixture(t)

	target := filepath.Join(work, "w", "x")
	if err := WriteAtPath(target+"/", []byte("hi")); err != nil {
		t.Fatalf("WriteAtPath: %v", err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "hi" {
		t.Errorf("the write did not land at %s: %q, %v", target, b, err)
	}
	if _, err := os.Stat(filepath.Join(target, "x")); err == nil {
		t.Error("the write landed one level too deep")
	}

	dir := filepath.Join(work, "d", "sub")
	if err := DeleteAtPath(dir+"/", DeleteRecursive()); err != nil {
		t.Fatalf("DeleteAtPath: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the directory named with a trailing slash was not removed")
	}

	// The empty path names nothing, as it did for the os calls.
	if err := WriteAtPath("", []byte("x")); err == nil {
		t.Error(`WriteAtPath("") succeeded`)
	}
	if err := DeleteAtPath(""); err == nil {
		t.Error(`DeleteAtPath("") succeeded`)
	}
	if err := DeleteAtPath("", IgnoreMissing()); err != nil {
		t.Errorf(`DeleteAtPath("", IgnoreMissing()) = %v, want nil as for a missing path`, err)
	}
	if _, err := ReadAtPath(""); err == nil {
		t.Error(`ReadAtPath("") succeeded`)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
