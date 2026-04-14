// Package tag manages tag files for the pub.polis.tag content type.
//
// Tags are organizational metadata that let authors tag their own content
// and content from others across the network. Each tag is stored as a JSON
// file at content/pub.polis.core/tag/<name>.json containing a list of target URIs.
package tag

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for tag metadata.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// TagFile represents a tag file on disk.
type TagFile struct {
	Tag       string      `json:"tag"`
	Targets   []TagTarget `json:"targets"`
	Created   string      `json:"created"`
	Updated   string      `json:"updated"`
	Generator string      `json:"generator"`
	Version   string      `json:"current_version"`
	Signature string      `json:"signature"`
}

// TagTarget represents a single tagged content reference.
type TagTarget struct {
	URI   string `json:"uri"`
	Added string `json:"added"`
}

var tagNameRe = regexp.MustCompile(`[^a-z0-9-]`)

// NormalizeTagName normalizes a tag name: lowercase, trim, only alphanumeric
// and hyphens, 1-64 chars. Returns an error if the result is empty or invalid.
func NormalizeTagName(name string) (string, error) {
	name = strings.TrimSpace(strings.ToLower(name))

	// Replace spaces and underscores with hyphens
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")

	// Remove anything that isn't a-z, 0-9, or hyphen
	name = tagNameRe.ReplaceAllString(name, "")

	// Collapse multiple hyphens
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}

	// Trim leading/trailing hyphens
	name = strings.Trim(name, "-")

	if name == "" {
		return "", fmt.Errorf("tag name is empty after normalization")
	}
	if len(name) > 64 {
		name = name[:64]
		name = strings.TrimRight(name, "-")
	}
	if name == "" {
		return "", fmt.Errorf("tag name is empty after truncation")
	}

	return name, nil
}

// TagPath returns the filesystem path for a tag file.
func TagPath(dataDir, tagName string) string {
	return filepath.Join(dataDir, "content", "pub.polis.core", "tag", tagName+".json")
}

// tagDir returns the directory containing tag files.
func tagDir(dataDir string) string {
	return filepath.Join(dataDir, "content", "pub.polis.core", "tag")
}

// Load reads and parses a tag file from disk.
func Load(path string) (*TagFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tag file: %w", err)
	}

	var tf TagFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("parse tag file: %w", err)
	}

	return &tf, nil
}

// ListTags reads all tag files from the tag directory.
func ListTags(dataDir string) ([]TagFile, error) {
	dir := tagDir(dataDir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read tag directory: %w", err)
	}

	var tags []TagFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		tf, err := Load(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue // skip malformed tag files
		}
		tags = append(tags, *tf)
	}

	return tags, nil
}

// ApplyTag adds a target URI to a tag, creating the tag file if it doesn't exist.
// The file is signed after mutation.
func ApplyTag(dataDir, tagName, targetURI string, privateKey []byte, generator ...string) (*TagFile, error) {
	gen := GetGenerator()
	if len(generator) > 0 && generator[0] != "" {
		gen = generator[0]
	}
	name, err := NormalizeTagName(tagName)
	if err != nil {
		return nil, fmt.Errorf("invalid tag name: %w", err)
	}

	targetURI = strings.TrimSpace(targetURI)
	if targetURI == "" {
		return nil, fmt.Errorf("target URI is required")
	}

	path := TagPath(dataDir, name)
	now := time.Now().UTC().Format(time.RFC3339)

	var tf *TagFile

	// Load existing or create new
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &tf); err != nil {
			return nil, fmt.Errorf("parse existing tag file: %w", err)
		}
	} else if os.IsNotExist(err) {
		tf = &TagFile{
			Tag:     name,
			Created: now,
		}
	} else {
		return nil, fmt.Errorf("read tag file: %w", err)
	}

	// Check for duplicate (idempotent)
	for _, t := range tf.Targets {
		if t.URI == targetURI {
			return tf, nil // already tagged
		}
	}

	// Add target
	tf.Targets = append(tf.Targets, TagTarget{
		URI:   targetURI,
		Added: now,
	})
	tf.Updated = now
	tf.Generator = gen

	// Sign and write
	if err := signAndWrite(tf, path, privateKey); err != nil {
		return nil, err
	}

	return tf, nil
}

// RemoveTarget removes a target URI from a tag and re-signs.
// Returns the updated tag file, or an error if the tag or target doesn't exist.
func RemoveTarget(dataDir, tagName, targetURI string, privateKey []byte, generator ...string) (*TagFile, error) {
	gen := GetGenerator()
	if len(generator) > 0 && generator[0] != "" {
		gen = generator[0]
	}
	name, err := NormalizeTagName(tagName)
	if err != nil {
		return nil, fmt.Errorf("invalid tag name: %w", err)
	}

	path := TagPath(dataDir, name)
	tf, err := Load(path)
	if err != nil {
		return nil, fmt.Errorf("load tag: %w", err)
	}

	// Find and remove
	found := false
	var remaining []TagTarget
	for _, t := range tf.Targets {
		if t.URI == targetURI {
			found = true
			continue
		}
		remaining = append(remaining, t)
	}

	if !found {
		return nil, fmt.Errorf("target %q not found in tag %q", targetURI, name)
	}

	tf.Targets = remaining
	tf.Updated = time.Now().UTC().Format(time.RFC3339)
	tf.Generator = gen

	if err := signAndWrite(tf, path, privateKey); err != nil {
		return nil, err
	}

	return tf, nil
}

// DeleteTag removes an entire tag file from disk.
func DeleteTag(dataDir, tagName string) error {
	name, err := NormalizeTagName(tagName)
	if err != nil {
		return fmt.Errorf("invalid tag name: %w", err)
	}

	path := TagPath(dataDir, name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("tag %q does not exist", name)
		}
		return fmt.Errorf("delete tag file: %w", err)
	}

	return nil
}

// signAndWrite computes the signature and writes the tag file to disk.
func signAndWrite(tf *TagFile, path string, privateKey []byte) error {
	// Compute canonical JSON for signing (exclude signature and current_version)
	canonical, err := canonicalJSON(tf)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}

	// Compute version hash
	hash := sha256.Sum256(canonical)
	tf.Version = "sha256:" + hex.EncodeToString(hash[:])

	// Sign
	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return fmt.Errorf("sign tag: %w", err)
	}
	tf.Signature = sig

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create tag directory: %w", err)
	}

	// Write
	data, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tag: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write tag file: %w", err)
	}

	return nil
}

// canonicalJSON produces a deterministic JSON representation for signing.
// Excludes the signature and current_version fields.
func canonicalJSON(tf *TagFile) ([]byte, error) {
	// Build a signing-only structure
	signable := struct {
		Tag       string      `json:"tag"`
		Targets   []TagTarget `json:"targets"`
		Created   string      `json:"created"`
		Updated   string      `json:"updated"`
		Generator string      `json:"generator"`
	}{
		Tag:       tf.Tag,
		Targets:   tf.Targets,
		Created:   tf.Created,
		Updated:   tf.Updated,
		Generator: tf.Generator,
	}

	// Ensure nil targets serialize as empty array
	if signable.Targets == nil {
		signable.Targets = []TagTarget{}
	}

	return json.Marshal(signable)
}
