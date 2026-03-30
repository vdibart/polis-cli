package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsRegisteredLocally_NoFile(t *testing.T) {
	dir := t.TempDir()
	if IsRegisteredLocally(dir, "https://ds.example.com") {
		t.Error("expected false when no marker file exists")
	}
}

func TestIsRegisteredLocally_EmptyArgs(t *testing.T) {
	if IsRegisteredLocally("", "https://ds.example.com") {
		t.Error("expected false for empty dataDir")
	}
	if IsRegisteredLocally("/tmp", "") {
		t.Error("expected false for empty dsURL")
	}
	if IsRegisteredLocally("", "") {
		t.Error("expected false for both empty")
	}
}

func TestRegistrationMarkerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dsURL := "https://ds.example.com"

	// Initially not registered
	if IsRegisteredLocally(dir, dsURL) {
		t.Fatal("expected not registered initially")
	}

	// Write marker
	if err := WriteRegistrationMarker(dir, dsURL, "mysite.example.com"); err != nil {
		t.Fatalf("WriteRegistrationMarker: %v", err)
	}

	// Now registered
	if !IsRegisteredLocally(dir, dsURL) {
		t.Error("expected registered after writing marker")
	}

	// Verify marker file contents
	path := registrationMarkerPath(dir, dsURL)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if len(data) == 0 {
		t.Error("marker file is empty")
	}

	// Remove marker
	if err := RemoveRegistrationMarker(dir, dsURL); err != nil {
		t.Fatalf("RemoveRegistrationMarker: %v", err)
	}

	// No longer registered
	if IsRegisteredLocally(dir, dsURL) {
		t.Error("expected not registered after removing marker")
	}
}

func TestRemoveRegistrationMarker_Idempotent(t *testing.T) {
	dir := t.TempDir()
	// Removing a non-existent marker should not error
	if err := RemoveRegistrationMarker(dir, "https://ds.example.com"); err != nil {
		t.Errorf("expected nil error for non-existent marker, got: %v", err)
	}
}

func TestWriteRegistrationMarker_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	dsURL := "https://ds.example.com"

	if err := WriteRegistrationMarker(dir, dsURL, "test.com"); err != nil {
		t.Fatalf("WriteRegistrationMarker: %v", err)
	}

	// Verify directory was created
	dsDir := filepath.Join(dir, ".polis", "ds", "ds.example.com")
	info, err := os.Stat(dsDir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
	// Check permissions (0700)
	if info.Mode().Perm() != 0700 {
		t.Errorf("expected 0700 permissions, got %o", info.Mode().Perm())
	}
}

func TestRegistrationMarkerPath_EmptyDSURL(t *testing.T) {
	path := registrationMarkerPath("/tmp", "")
	if path != "" {
		t.Errorf("expected empty path for empty dsURL, got %q", path)
	}
}

func TestRegistrationMarkerPath_InvalidDSURL(t *testing.T) {
	path := registrationMarkerPath("/tmp", "not-a-url")
	// url.Parse succeeds for most strings, Hostname() returns ""
	if path != "" {
		t.Errorf("expected empty path for invalid dsURL, got %q", path)
	}
}

func TestIsRegisteredLocally_DifferentDS(t *testing.T) {
	dir := t.TempDir()
	ds1 := "https://ds1.example.com"
	ds2 := "https://ds2.example.com"

	// Register with ds1
	if err := WriteRegistrationMarker(dir, ds1, "mysite.com"); err != nil {
		t.Fatal(err)
	}

	if !IsRegisteredLocally(dir, ds1) {
		t.Error("expected registered with ds1")
	}
	if IsRegisteredLocally(dir, ds2) {
		t.Error("expected NOT registered with ds2")
	}
}
