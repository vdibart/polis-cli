package patrol

import (
	"os"
	"testing"
)

func TestCheckVolume_ValidDir(t *testing.T) {
	dir := t.TempDir()
	status := CheckVolume(dir, 1) // 1 byte threshold — should pass
	if !status.OK {
		t.Errorf("expected OK for valid writable dir: %s", status.Message)
	}
}

func TestCheckVolume_MissingDir(t *testing.T) {
	status := CheckVolume("/nonexistent/path/12345", DefaultMinFreeBytes)
	if status.OK {
		t.Error("expected failure for missing dir")
	}
	if status.Message == "" {
		t.Error("expected error message")
	}
}

func TestCheckVolume_NotADir(t *testing.T) {
	f, err := os.CreateTemp("", "voltest")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	status := CheckVolume(f.Name(), 1)
	if status.OK {
		t.Error("expected failure for non-directory")
	}
}
