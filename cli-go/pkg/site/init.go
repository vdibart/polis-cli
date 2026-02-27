package site

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// InitOptions contains options for initializing a new polis site.
type InitOptions struct {
	SiteTitle string // Optional site title
	Author    string // Optional author name (falls back to git config user.name)
	Email     string // Optional email address — private by default, only written if explicitly provided
	Version   string // CLI version (e.g. "0.47.0") for metadata files
	// Custom directory paths (empty = use defaults)
	KeysDir     string
	PostsDir    string
	CommentsDir string
	SnippetsDir string
	ThemesDir   string
	VersionsDir string
	// Custom file paths (empty = use defaults)
	PublicIndex     string
	BlessedComments string
	FollowingIndex  string
}

// applyDefaults fills in default values for any empty fields.
func (o *InitOptions) applyDefaults() {
	if o.Version == "" {
		o.Version = "dev"
	}
	if o.KeysDir == "" {
		o.KeysDir = ".polis/keys"
	}
	if o.PostsDir == "" {
		o.PostsDir = "posts"
	}
	if o.CommentsDir == "" {
		o.CommentsDir = "comments"
	}
	if o.SnippetsDir == "" {
		o.SnippetsDir = "snippets"
	}
	if o.ThemesDir == "" {
		o.ThemesDir = ".polis/themes"
	}
	if o.VersionsDir == "" {
		o.VersionsDir = ".versions"
	}
	if o.PublicIndex == "" {
		o.PublicIndex = "metadata/public.jsonl"
	}
	if o.BlessedComments == "" {
		o.BlessedComments = "metadata/blessed-comments.json"
	}
	if o.FollowingIndex == "" {
		o.FollowingIndex = "metadata/following.json"
	}
}

// InitResult contains the result of site initialization.
type InitResult struct {
	Success      bool     `json:"success"`
	SiteDir      string   `json:"site_dir"`
	PublicKey     string   `json:"public_key"`
	DirsCreated  []string `json:"directories_created,omitempty"`
	FilesCreated []string `json:"files_created,omitempty"`
	KeyPaths     struct {
		Private string `json:"private"`
		Public  string `json:"public"`
	} `json:"key_paths"`
	Error string `json:"error,omitempty"`
}

// Init creates a new polis site in the given directory.
// This function has no webapp dependencies and can be used by CLI tools.
// It will NOT overwrite existing keys or .well-known/polis - this is a safety feature.
func Init(siteDir string, opts InitOptions) (*InitResult, error) {
	opts.applyDefaults()

	result := &InitResult{
		Success: false,
		SiteDir: siteDir,
	}

	// SAFETY: Check if keys already exist - refuse to overwrite
	privKeyPath := filepath.Join(siteDir, opts.KeysDir, "id_ed25519")
	pubKeyPath := filepath.Join(siteDir, opts.KeysDir, "id_ed25519.pub")
	wellKnownPath := filepath.Join(siteDir, ".well-known", "polis")

	if _, err := os.Stat(privKeyPath); err == nil {
		return nil, fmt.Errorf("private key already exists at %s - refusing to overwrite", privKeyPath)
	}
	if _, err := os.Stat(pubKeyPath); err == nil {
		return nil, fmt.Errorf("public key already exists at %s - refusing to overwrite", pubKeyPath)
	}
	if _, err := os.Stat(wellKnownPath); err == nil {
		return nil, fmt.Errorf(".well-known/polis already exists at %s - refusing to overwrite", wellKnownPath)
	}

	// Derive metadata dir from the public index path
	metadataDir := filepath.Dir(filepath.Join(siteDir, opts.PublicIndex))

	// Create directory structure (matches CLI's init behavior)
	dirs := []string{
		siteDir,
		// Private directories (matches CLI)
		filepath.Join(siteDir, ".polis"),
		filepath.Join(siteDir, opts.KeysDir),
		filepath.Join(siteDir, opts.ThemesDir),
		filepath.Join(siteDir, ".polis", "posts", "drafts"),
		filepath.Join(siteDir, ".polis", "comments", "drafts"),
		filepath.Join(siteDir, ".polis", "comments", "pending"),
		filepath.Join(siteDir, ".polis", "comments", "denied"),
		// Public directories (matches CLI)
		filepath.Join(siteDir, ".well-known"),
		filepath.Join(siteDir, opts.PostsDir),
		filepath.Join(siteDir, opts.CommentsDir),
		filepath.Join(siteDir, opts.SnippetsDir),
		metadataDir,
	}

	var dirsCreated []string
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
		// Track relative path for result
		rel, _ := filepath.Rel(siteDir, dir)
		if rel != "." {
			dirsCreated = append(dirsCreated, rel)
		}
	}
	result.DirsCreated = dirsCreated

	// Generate keypair
	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate keypair: %w", err)
	}

	// Save keys with appropriate permissions
	if err := os.WriteFile(privKeyPath, privKey, 0600); err != nil {
		return nil, fmt.Errorf("failed to write private key: %w", err)
	}
	if err := os.WriteFile(pubKeyPath, pubKey, 0644); err != nil {
		return nil, fmt.Errorf("failed to write public key: %w", err)
	}

	result.KeyPaths.Private = opts.KeysDir + "/id_ed25519"
	result.KeyPaths.Public = opts.KeysDir + "/id_ed25519.pub"

	// Create .well-known/polis
	setupTime := time.Now().UTC()

	// Get author: use opts if provided, fall back to git config
	author := opts.Author
	if author == "" {
		author = getGitConfig("user.name")
	}

	wk := &WellKnown{
		Version:   GetGenerator(),
		Author:    author,
		Email:     opts.Email, // Only written if explicitly provided (omitempty in JSON)
		PublicKey: strings.TrimSpace(string(pubKey)),
		SiteTitle: opts.SiteTitle,
		Created:   setupTime.Format(time.RFC3339),
		Config: &WellKnownConfig{
			Directories: WellKnownDirectories{
				Keys:     opts.KeysDir,
				Posts:    opts.PostsDir,
				Comments: opts.CommentsDir,
				Snippets: opts.SnippetsDir,
				Themes:   opts.ThemesDir,
				Versions: opts.VersionsDir,
			},
			Files: WellKnownFiles{
				PublicIndex:     opts.PublicIndex,
				BlessedComments: opts.BlessedComments,
				FollowingIndex:  opts.FollowingIndex,
			},
		},
	}

	if err := SaveWellKnown(siteDir, wk); err != nil {
		return nil, fmt.Errorf("failed to create .well-known/polis: %w", err)
	}

	// Create metadata files (matches CLI initialization)
	var filesCreated []string
	if err := initMetadataFiles(siteDir, opts.Version, opts, &filesCreated); err != nil {
		return nil, fmt.Errorf("failed to create metadata files: %w", err)
	}

	// Create default about snippet
	aboutPath := filepath.Join(siteDir, opts.SnippetsDir, "about.md")
	if _, err := os.Stat(aboutPath); os.IsNotExist(err) {
		aboutContent := "Welcome to my polis space. This site runs on *polis*\u2014signed markdown on your own domain. No platform, no middleman, just you and your words.\n"
		if err := os.WriteFile(aboutPath, []byte(aboutContent), 0644); err != nil {
			return nil, fmt.Errorf("failed to create default about snippet: %w", err)
		}
		rel, _ := filepath.Rel(siteDir, aboutPath)
		filesCreated = append(filesCreated, rel)
	}

	// Create webapp-config.json with webapp-specific defaults only.
	// Discovery credentials (DISCOVERY_SERVICE_URL/KEY) belong in .env
	// and are loaded at runtime by the webapp's LoadEnv().
	webappConfigPath := filepath.Join(siteDir, ".polis", "webapp-config.json")
	if _, err := os.Stat(webappConfigPath); os.IsNotExist(err) {
		webappConfig := map[string]interface{}{
			"setup_at":         setupTime.Format(time.RFC3339),
			"view_mode":        "list",
			"show_frontmatter": false,
		}
		data, _ := json.MarshalIndent(webappConfig, "", "  ")
		data = append(data, '\n')
		if err := os.WriteFile(webappConfigPath, data, 0644); err != nil {
			return nil, fmt.Errorf("failed to create webapp-config.json: %w", err)
		}
		filesCreated = append(filesCreated, ".polis/webapp-config.json")
	}

	// Add well-known and key files to the list
	filesCreated = append(filesCreated, ".well-known/polis")
	filesCreated = append(filesCreated, result.KeyPaths.Private, result.KeyPaths.Public)
	result.FilesCreated = filesCreated

	// Create .gitignore (matches CLI format)
	if err := initGitignore(siteDir); err != nil {
		// Non-fatal, just log
		fmt.Printf("warning: failed to create .gitignore: %v\n", err)
	}

	result.Success = true
	result.PublicKey = strings.TrimSpace(string(pubKey))
	return result, nil
}

// initMetadataFiles creates the initial metadata files.
func initMetadataFiles(siteDir, version string, opts InitOptions, filesCreated *[]string) error {
	// Derive metadata dir from the public index path
	metadataDir := filepath.Dir(filepath.Join(siteDir, opts.PublicIndex))

	// Create manifest.json in the metadata dir
	manifestPath := filepath.Join(metadataDir, "manifest.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		manifest := map[string]interface{}{
			"version":        GetGenerator(),
			"last_published": "",
			"post_count":     0,
			"comment_count":  0,
		}
		data, _ := json.MarshalIndent(manifest, "", "  ")
		if err := os.WriteFile(manifestPath, data, 0644); err != nil {
			return err
		}
		rel, _ := filepath.Rel(siteDir, manifestPath)
		*filesCreated = append(*filesCreated, rel)
	}

	// Create following index
	followingPath := filepath.Join(siteDir, opts.FollowingIndex)
	if err := os.MkdirAll(filepath.Dir(followingPath), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(followingPath); os.IsNotExist(err) {
		following := map[string]interface{}{
			"version":   GetGenerator(),
			"following": []interface{}{},
		}
		data, _ := json.MarshalIndent(following, "", "  ")
		if err := os.WriteFile(followingPath, data, 0644); err != nil {
			return err
		}
		*filesCreated = append(*filesCreated, opts.FollowingIndex)
	}

	// Create blessed comments
	blessedPath := filepath.Join(siteDir, opts.BlessedComments)
	if err := os.MkdirAll(filepath.Dir(blessedPath), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(blessedPath); os.IsNotExist(err) {
		blessed := map[string]interface{}{
			"version":  GetGenerator(),
			"comments": []interface{}{},
		}
		data, _ := json.MarshalIndent(blessed, "", "  ")
		if err := os.WriteFile(blessedPath, data, 0644); err != nil {
			return err
		}
		*filesCreated = append(*filesCreated, opts.BlessedComments)
	}

	// Create empty public index
	publicPath := filepath.Join(siteDir, opts.PublicIndex)
	if err := os.MkdirAll(filepath.Dir(publicPath), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(publicPath); os.IsNotExist(err) {
		if err := os.WriteFile(publicPath, []byte{}, 0644); err != nil {
			return err
		}
		*filesCreated = append(*filesCreated, opts.PublicIndex)
	}

	return nil
}

// initGitignore creates the .gitignore file.
func initGitignore(siteDir string) error {
	gitignorePath := filepath.Join(siteDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		content := `.polis/
/themes
/polis
/polis-tui
.env*
`
		return os.WriteFile(gitignorePath, []byte(content), 0644)
	}
	return nil
}

// getGitConfig retrieves a git config value.
// Returns empty string if git is not available or the config key is not set.
func getGitConfig(key string) string {
	cmd := exec.Command("git", "config", key)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
