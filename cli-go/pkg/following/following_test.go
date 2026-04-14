package following

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddAndRemove(t *testing.T) {
	f := &FollowingFile{
		Version:   Version,
		Following: []FollowingEntry{},
	}

	// Test Add
	added := f.Add("https://example.com")
	if !added {
		t.Error("Expected Add to return true for new entry")
	}

	if f.Count() != 1 {
		t.Errorf("Expected count 1, got %d", f.Count())
	}

	// Test duplicate Add
	added = f.Add("https://example.com")
	if added {
		t.Error("Expected Add to return false for duplicate entry")
	}

	// Test IsFollowing
	if !f.IsFollowing("https://example.com") {
		t.Error("Expected IsFollowing to return true")
	}

	if f.IsFollowing("https://other.com") {
		t.Error("Expected IsFollowing to return false for non-existent entry")
	}

	// Test Remove
	removed := f.Remove("https://example.com")
	if !removed {
		t.Error("Expected Remove to return true")
	}

	if f.Count() != 0 {
		t.Errorf("Expected count 0 after remove, got %d", f.Count())
	}

	// Test Remove non-existent
	removed = f.Remove("https://other.com")
	if removed {
		t.Error("Expected Remove to return false for non-existent entry")
	}
}

func TestSaveAndLoad(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "following.json")

	// Create and save
	f := &FollowingFile{
		Version:   Version,
		Following: []FollowingEntry{},
	}
	f.Add("https://example.com")

	if err := Save(path, f); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("File was not created")
	}

	// Load and verify
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Count() != 1 {
		t.Errorf("Expected count 1, got %d", loaded.Count())
	}

	if !loaded.IsFollowing("https://example.com") {
		t.Error("Expected loaded file to contain entry")
	}
}

func TestLoadNonExistent(t *testing.T) {
	// Loading non-existent file should return empty following
	f, err := Load("/non/existent/path.json")
	if err != nil {
		t.Fatalf("Load of non-existent file failed: %v", err)
	}

	if f.Count() != 0 {
		t.Errorf("Expected count 0 for non-existent file, got %d", f.Count())
	}

	if f.Version != "" {
		t.Errorf("default version = %q, want empty string for new file", f.Version)
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "following.json")

	f := &FollowingFile{
		Version: Version,
		Following: []FollowingEntry{
			{URL: "https://alice.com", AddedAt: "2025-01-01T00:00:00Z", SiteTitle: "Alice's Blog", AuthorName: "Alice"},
			{URL: "https://bob.com", AddedAt: "2025-01-02T00:00:00Z"},
		},
	}

	if err := Save(path, f); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	alice := loaded.Get("https://alice.com")
	if alice == nil {
		t.Fatal("Expected alice entry")
	}
	if alice.SiteTitle != "Alice's Blog" {
		t.Errorf("SiteTitle = %q, want %q", alice.SiteTitle, "Alice's Blog")
	}
	if alice.AuthorName != "Alice" {
		t.Errorf("AuthorName = %q, want %q", alice.AuthorName, "Alice")
	}

	bob := loaded.Get("https://bob.com")
	if bob == nil {
		t.Fatal("Expected bob entry")
	}
	if bob.SiteTitle != "" {
		t.Errorf("Expected empty SiteTitle for bob, got %q", bob.SiteTitle)
	}
}

func TestUpdateMetadata(t *testing.T) {
	f := &FollowingFile{
		Version:   Version,
		Following: []FollowingEntry{{URL: "https://alice.com", AddedAt: "2025-01-01T00:00:00Z"}},
	}

	updated := f.UpdateMetadata("https://alice.com", "Alice's Site", "Alice A.")
	if !updated {
		t.Error("Expected UpdateMetadata to return true")
	}

	entry := f.Get("https://alice.com")
	if entry.SiteTitle != "Alice's Site" {
		t.Errorf("SiteTitle = %q, want %q", entry.SiteTitle, "Alice's Site")
	}
	if entry.AuthorName != "Alice A." {
		t.Errorf("AuthorName = %q, want %q", entry.AuthorName, "Alice A.")
	}

	// Non-existent entry
	updated = f.UpdateMetadata("https://nobody.com", "X", "Y")
	if updated {
		t.Error("Expected UpdateMetadata to return false for non-existent URL")
	}
}

func TestEntriesMissingMetadata(t *testing.T) {
	f := &FollowingFile{
		Version: Version,
		Following: []FollowingEntry{
			{URL: "https://alice.com", AddedAt: "2025-01-01T00:00:00Z", SiteTitle: "Alice's Blog"},
			{URL: "https://bob.com", AddedAt: "2025-01-02T00:00:00Z"},
			{URL: "https://carol.com", AddedAt: "2025-01-03T00:00:00Z", AuthorName: "Carol"},
			{URL: "https://dave.com", AddedAt: "2025-01-04T00:00:00Z"},
		},
	}

	missing := f.EntriesMissingMetadata()
	if len(missing) != 2 {
		t.Fatalf("Expected 2 missing, got %d", len(missing))
	}
	if missing[0].URL != "https://bob.com" {
		t.Errorf("Expected bob first, got %s", missing[0].URL)
	}
	if missing[1].URL != "https://dave.com" {
		t.Errorf("Expected dave second, got %s", missing[1].URL)
	}
}

func TestGet(t *testing.T) {
	f := &FollowingFile{
		Version:   Version,
		Following: []FollowingEntry{},
	}
	f.Add("https://example.com")

	entry := f.Get("https://example.com")
	if entry == nil {
		t.Fatal("Expected to get entry")
	}

	if entry.URL != "https://example.com" {
		t.Errorf("Expected URL https://example.com, got %s", entry.URL)
	}

	if entry.AddedAt == "" {
		t.Error("Expected AddedAt to be set")
	}

	// Try getting non-existent
	entry = f.Get("https://other.com")
	if entry != nil {
		t.Error("Expected nil for non-existent entry")
	}
}

func TestURLNormalization(t *testing.T) {
	f := &FollowingFile{
		Version:   Version,
		Following: []FollowingEntry{},
	}

	// Add with trailing slash
	f.Add("https://example.com/")
	if f.Count() != 1 {
		t.Fatalf("Expected count 1, got %d", f.Count())
	}

	// Duplicate without trailing slash should be detected
	added := f.Add("https://example.com")
	if added {
		t.Error("Expected Add to detect duplicate with different trailing slash")
	}
	if f.Count() != 1 {
		t.Errorf("Expected count still 1, got %d", f.Count())
	}

	// IsFollowing should match both forms
	if !f.IsFollowing("https://example.com/") {
		t.Error("IsFollowing should match with trailing slash")
	}
	if !f.IsFollowing("https://example.com") {
		t.Error("IsFollowing should match without trailing slash")
	}

	// Get should match both forms
	if f.Get("https://example.com") == nil {
		t.Error("Get should find entry without trailing slash")
	}
	if f.Get("https://example.com/") == nil {
		t.Error("Get should find entry with trailing slash")
	}

	// Remove should match both forms
	removed := f.Remove("https://example.com") // stored as "https://example.com/"
	if !removed {
		t.Error("Remove should match without trailing slash")
	}
	if f.Count() != 0 {
		t.Errorf("Expected count 0 after remove, got %d", f.Count())
	}
}

func TestRemoveAllDuplicates(t *testing.T) {
	f := &FollowingFile{
		Version: Version,
		Following: []FollowingEntry{
			{URL: "https://example.com/", AddedAt: "2025-01-01T00:00:00Z"},
			{URL: "https://example.com", AddedAt: "2025-01-02T00:00:00Z"},
			{URL: "https://other.com/", AddedAt: "2025-01-03T00:00:00Z"},
		},
	}

	// Remove should remove ALL matching entries (both slash variants)
	removed := f.Remove("https://example.com")
	if !removed {
		t.Error("Expected Remove to return true")
	}
	if f.Count() != 1 {
		t.Errorf("Expected count 1 after removing duplicates, got %d", f.Count())
	}
	if f.Following[0].URL != "https://other.com/" {
		t.Errorf("Expected remaining entry to be other.com, got %s", f.Following[0].URL)
	}
}
