package tag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestGetGenerator_UsesVersion(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "1.2.3"
	got := GetGenerator()
	if got != "polis-cli-go/1.2.3" {
		t.Errorf("GetGenerator() = %q, want %q", got, "polis-cli-go/1.2.3")
	}
}

func TestGetGenerator_DefaultDev(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "dev"

	got := GetGenerator()
	if got != "polis-cli-go/dev" {
		t.Errorf("GetGenerator() = %q, want %q", got, "polis-cli-go/dev")
	}
}

func generateTestKey(t *testing.T) (privateKey, publicKey []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return priv, pub
}

func TestNormalizeTagName(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"Rust", "rust", false},
		{"My Tag", "my-tag", false},
		{"  spaces  ", "spaces", false},
		{"UPPER_CASE", "upper-case", false},
		{"hello--world", "hello-world", false},
		{"a-b-c", "a-b-c", false},
		{"rust123", "rust123", false},
		{"café", "caf", false},
		{"../etc/passwd", "etcpasswd", false},
		{"/path/traversal", "pathtraversal", false},
		{"a!b@c#d$e", "abcde", false},
		{"-leading-", "leading", false},
		{"", "", true},
		{"   ", "", true},
		{"!!!!", "", true},
		// Long name gets truncated
		{"abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz", "abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz-abcdefghij", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := NormalizeTagName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NormalizeTagName(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("NormalizeTagName(%q) error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("NormalizeTagName(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if len(got) > 64 {
				t.Errorf("NormalizeTagName(%q) length %d > 64", tt.input, len(got))
			}
		})
	}
}

func TestTagPath(t *testing.T) {
	got := TagPath("/site", "rust")
	want := filepath.Join("/site", "content", "pub.polis.core", "tag", "rust.json")
	if got != want {
		t.Errorf("TagPath = %q, want %q", got, want)
	}
}

func TestApplyTag_CreateNew(t *testing.T) {
	dataDir := t.TempDir()
	privKey, pubKey := generateTestKey(t)

	tf, err := ApplyTag(dataDir, "rust", "https://alice.com/posts/hello", privKey)
	if err != nil {
		t.Fatalf("ApplyTag: %v", err)
	}

	if tf.Tag != "rust" {
		t.Errorf("Tag = %q, want %q", tf.Tag, "rust")
	}
	if len(tf.Targets) != 1 {
		t.Fatalf("Targets count = %d, want 1", len(tf.Targets))
	}
	if tf.Targets[0].URI != "https://alice.com/posts/hello" {
		t.Errorf("Target URI = %q", tf.Targets[0].URI)
	}
	if tf.Signature == "" {
		t.Error("Signature is empty")
	}
	if tf.Version == "" {
		t.Error("Version is empty")
	}
	if tf.Created == "" {
		t.Error("Created is empty")
	}

	// Verify file exists on disk
	path := TagPath(dataDir, "rust")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Tag file not created: %v", err)
	}

	// Verify signature
	canonical, _ := canonicalJSON(tf)
	valid, err := signing.VerifySignature(canonical, pubKey, tf.Signature)
	if err != nil {
		t.Fatalf("VerifySignature error: %v", err)
	}
	if !valid {
		t.Error("Signature verification failed")
	}
}

func TestApplyTag_DuplicateIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	tf1, err := ApplyTag(dataDir, "rust", "https://alice.com/posts/hello", privKey)
	if err != nil {
		t.Fatalf("ApplyTag first: %v", err)
	}

	tf2, err := ApplyTag(dataDir, "rust", "https://alice.com/posts/hello", privKey)
	if err != nil {
		t.Fatalf("ApplyTag duplicate: %v", err)
	}

	if len(tf2.Targets) != 1 {
		t.Errorf("Duplicate added a target: got %d, want 1", len(tf2.Targets))
	}

	// Signature should be unchanged (no mutation)
	if tf1.Signature != tf2.Signature {
		t.Error("Signature changed on duplicate apply")
	}
}

func TestApplyTag_MultipleTargets(t *testing.T) {
	dataDir := t.TempDir()
	privKey, pubKey := generateTestKey(t)

	ApplyTag(dataDir, "rust", "https://alice.com/posts/hello", privKey)
	tf, err := ApplyTag(dataDir, "rust", "https://bob.com/posts/world", privKey)
	if err != nil {
		t.Fatalf("ApplyTag second: %v", err)
	}

	if len(tf.Targets) != 2 {
		t.Fatalf("Targets count = %d, want 2", len(tf.Targets))
	}

	// Verify re-signed after mutation
	canonical, _ := canonicalJSON(tf)
	valid, err := signing.VerifySignature(canonical, pubKey, tf.Signature)
	if err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
	if !valid {
		t.Error("Signature invalid after second target")
	}
}

func TestApplyTag_NormalizesName(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	tf, err := ApplyTag(dataDir, "My Tag", "https://example.com", privKey)
	if err != nil {
		t.Fatalf("ApplyTag: %v", err)
	}
	if tf.Tag != "my-tag" {
		t.Errorf("Tag = %q, want %q", tf.Tag, "my-tag")
	}

	// File should be at normalized path
	path := TagPath(dataDir, "my-tag")
	if _, err := os.Stat(path); err != nil {
		t.Error("Tag file not at normalized path")
	}
}

func TestApplyTag_EmptyTarget(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	_, err := ApplyTag(dataDir, "rust", "", privKey)
	if err == nil {
		t.Error("Expected error for empty target")
	}
}

func TestApplyTag_InvalidTagName(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	_, err := ApplyTag(dataDir, "!!!", "https://example.com", privKey)
	if err == nil {
		t.Error("Expected error for invalid tag name")
	}
}

func TestRemoveTarget(t *testing.T) {
	dataDir := t.TempDir()
	privKey, pubKey := generateTestKey(t)

	ApplyTag(dataDir, "rust", "https://alice.com/posts/a", privKey)
	ApplyTag(dataDir, "rust", "https://alice.com/posts/b", privKey)

	tf, err := RemoveTarget(dataDir, "rust", "https://alice.com/posts/a", privKey)
	if err != nil {
		t.Fatalf("RemoveTarget: %v", err)
	}

	if len(tf.Targets) != 1 {
		t.Fatalf("Targets count = %d, want 1", len(tf.Targets))
	}
	if tf.Targets[0].URI != "https://alice.com/posts/b" {
		t.Errorf("Remaining target = %q", tf.Targets[0].URI)
	}

	// Verify re-signed
	canonical, _ := canonicalJSON(tf)
	valid, _ := signing.VerifySignature(canonical, pubKey, tf.Signature)
	if !valid {
		t.Error("Signature invalid after removal")
	}
}

func TestRemoveTarget_NonExistent(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	ApplyTag(dataDir, "rust", "https://alice.com/posts/a", privKey)

	_, err := RemoveTarget(dataDir, "rust", "https://nonexistent.com", privKey)
	if err == nil {
		t.Error("Expected error for non-existent target")
	}
}

func TestRemoveTarget_EmptyAfterRemoval(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	ApplyTag(dataDir, "rust", "https://alice.com/posts/a", privKey)

	tf, err := RemoveTarget(dataDir, "rust", "https://alice.com/posts/a", privKey)
	if err != nil {
		t.Fatalf("RemoveTarget: %v", err)
	}

	if len(tf.Targets) != 0 {
		t.Errorf("Targets count = %d, want 0", len(tf.Targets))
	}
}

func TestDeleteTag(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	ApplyTag(dataDir, "rust", "https://example.com", privKey)

	err := DeleteTag(dataDir, "rust")
	if err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}

	path := TagPath(dataDir, "rust")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Tag file still exists after delete")
	}
}

func TestDeleteTag_NonExistent(t *testing.T) {
	dataDir := t.TempDir()

	err := DeleteTag(dataDir, "nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent tag")
	}
}

func TestLoad_RoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	original, err := ApplyTag(dataDir, "rust", "https://alice.com/posts/hello", privKey)
	if err != nil {
		t.Fatalf("ApplyTag: %v", err)
	}

	loaded, err := Load(TagPath(dataDir, "rust"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Tag != original.Tag {
		t.Errorf("Tag mismatch: %q vs %q", loaded.Tag, original.Tag)
	}
	if loaded.Signature != original.Signature {
		t.Errorf("Signature mismatch")
	}
	if loaded.Version != original.Version {
		t.Errorf("Version mismatch: %q vs %q", loaded.Version, original.Version)
	}
	if len(loaded.Targets) != len(original.Targets) {
		t.Errorf("Targets count mismatch: %d vs %d", len(loaded.Targets), len(original.Targets))
	}
}

func TestListTags(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	ApplyTag(dataDir, "rust", "https://example.com/a", privKey)
	ApplyTag(dataDir, "go", "https://example.com/b", privKey)

	tags, err := ListTags(dataDir)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}

	if len(tags) != 2 {
		t.Errorf("ListTags count = %d, want 2", len(tags))
	}

	// Check both tags present
	names := map[string]bool{}
	for _, tf := range tags {
		names[tf.Tag] = true
	}
	if !names["rust"] || !names["go"] {
		t.Errorf("Missing tags: %v", names)
	}
}

func TestListTags_EmptyDir(t *testing.T) {
	dataDir := t.TempDir()

	tags, err := ListTags(dataDir)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if tags != nil {
		t.Errorf("Expected nil, got %d tags", len(tags))
	}
}

func TestListTags_SkipsMalformed(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	ApplyTag(dataDir, "good", "https://example.com", privKey)

	// Write a malformed JSON file
	dir := tagDir(dataDir)
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0644)

	tags, err := ListTags(dataDir)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}

	if len(tags) != 1 {
		t.Errorf("ListTags count = %d, want 1 (should skip malformed)", len(tags))
	}
}

func TestDirectoryAutoCreation(t *testing.T) {
	dataDir := t.TempDir()
	privKey, _ := generateTestKey(t)

	// Tag directory doesn't exist yet
	dir := tagDir(dataDir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("Tag directory should not exist yet")
	}

	_, err := ApplyTag(dataDir, "rust", "https://example.com", privKey)
	if err != nil {
		t.Fatalf("ApplyTag: %v", err)
	}

	// Directory should now exist
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Tag directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("Tag path is not a directory")
	}
}

func TestCanonicalJSON_NilTargets(t *testing.T) {
	tf := &TagFile{
		Tag:       "test",
		Targets:   nil,
		Created:   "2025-01-01T00:00:00Z",
		Updated:   "2025-01-01T00:00:00Z",
		Generator: "test",
	}

	data, err := canonicalJSON(tf)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}

	// Should contain "targets":[] not "targets":null
	var m map[string]json.RawMessage
	json.Unmarshal(data, &m)
	if string(m["targets"]) == "null" {
		t.Error("nil targets serialized as null, want []")
	}
}

func TestSignatureCoversAllFields(t *testing.T) {
	dataDir := t.TempDir()
	privKey, pubKey := generateTestKey(t)

	tf, _ := ApplyTag(dataDir, "rust", "https://example.com", privKey)

	// Verify original signature is valid
	canonical, _ := canonicalJSON(tf)
	valid, _ := signing.VerifySignature(canonical, pubKey, tf.Signature)
	if !valid {
		t.Fatal("Original signature invalid")
	}

	// Tamper with tag name — signature should fail
	tampered := *tf
	tampered.Tag = "tampered"
	canonical, _ = canonicalJSON(&tampered)
	valid, _ = signing.VerifySignature(canonical, pubKey, tf.Signature)
	if valid {
		t.Error("Signature should be invalid after tampering with tag name")
	}
}
