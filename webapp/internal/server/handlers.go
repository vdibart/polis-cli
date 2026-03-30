package server

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/blessing"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/notification"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/policycheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/snippet"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// draftIDSanitizer strips all characters except alphanumeric, hyphens, and underscores.
var draftIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// validatePostPath ensures the path is safe and within the posts content directory.
// This prevents path traversal attacks that could read/write arbitrary files.
func validatePostPath(path string) error {
	// Canonicalize first to normalize encoded traversals (e.g., ./, //)
	path = filepath.Clean(path)
	// Must start with "content/pub.polis.core/post/"
	if !strings.HasPrefix(path, "content/pub.polis.core/post/") {
		return fmt.Errorf("invalid path: must be under content/pub.polis.core/post/")
	}
	// No path traversal sequences
	if strings.Contains(path, "..") {
		return fmt.Errorf("invalid path: traversal not allowed")
	}
	// No null bytes (could bypass checks in some systems)
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("invalid path: null bytes not allowed")
	}
	return nil
}

// validateContentPath ensures the path is safe and within allowed directories.
// This prevents path traversal attacks.
func validateContentPath(path string) error {
	// Canonicalize first to normalize encoded traversals (e.g., ./, //)
	path = filepath.Clean(path)
	// No path traversal sequences
	if strings.Contains(path, "..") {
		return fmt.Errorf("invalid path: traversal not allowed")
	}
	// No null bytes
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("invalid path: null bytes not allowed")
	}

	// Allow root-level markdown and html files (e.g., index.md, index.html, about.md)
	if (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".html")) && !strings.Contains(path, "/") {
		return nil
	}

	// Must start with an allowed prefix
	allowedPrefixes := []string{
		"content/pub.polis.core/post/",
		"content/pub.polis.core/comment/",
		".polis/content/pub.polis.core/posts/drafts/",
		"posts/",    // Mount point (rendered output)
		"comments/", // Mount point (rendered output)
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return nil
		}
	}
	return fmt.Errorf("invalid path: must be a root .md/.html file or under content/pub.polis.core/post/, content/pub.polis.core/comment/, .polis/content/pub.polis.core/posts/drafts/, posts/, or comments/")
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Run validation to get current state
	validation := site.Validate(s.DataDir)

	// For backwards compatibility, "configured" is true if site is valid
	configured := validation.Status == site.StatusValid

	response := map[string]interface{}{
		"configured": configured,
		"site_title": s.GetSiteTitle(),
		"base_url":   s.GetBaseURL(),
		"validation": map[string]interface{}{
			"status": validation.Status,
			"errors": validation.Errors,
		},
	}

	// Include site info if valid
	if validation.SiteInfo != nil {
		response["site_info"] = validation.SiteInfo
	}

	showFrontmatter := true
	if s.Config != nil && s.Config.ShowFrontmatter != nil {
		showFrontmatter = *s.Config.ShowFrontmatter
	}
	response["show_frontmatter"] = showFrontmatter

	// Include avatar, author_name, and active_theme from .well-known/polis
	if wk, err := site.LoadWellKnown(s.DataDir); err == nil {
		if wk.Avatar != nil {
			response["avatar"] = wk.Avatar
		}
		if wk.AuthorName != "" {
			response["author_name"] = wk.AuthorName
		}
	}
	if activeTheme, _ := theme.GetActiveTheme(s.DataDir); activeTheme != "" {
		response["active_theme"] = activeTheme
	}

	json.NewEncoder(w).Encode(response)
}

// handleValidate returns the validation status of the site directory.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	validation := site.Validate(s.DataDir)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(validation)
}

// handleInit initializes a new polis site in the data directory.
func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SiteTitle    string `json:"site_title"`
		Author       string `json:"author"`
		Email        string `json:"email"`
		Theme        string `json:"theme"`
		BaseURL      string `json:"base_url"`
		DiscoveryURL string `json:"discovery_url"`
		DiscoveryKey string `json:"discovery_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Empty body is OK - all fields are optional
		req = struct {
			SiteTitle    string `json:"site_title"`
			Author       string `json:"author"`
			Email        string `json:"email"`
			Theme        string `json:"theme"`
			BaseURL      string `json:"base_url"`
			DiscoveryURL string `json:"discovery_url"`
			DiscoveryKey string `json:"discovery_key"`
		}{}
	}

	opts := site.InitOptions{
		SiteTitle: req.SiteTitle,
		Author:    req.Author,
		Email:     req.Email,
		Theme:     req.Theme,
	}

	s.LogDebug("Initializing new site at: %s", s.DataDir)
	result, err := site.Init(s.DataDir, opts)
	if err != nil {
		s.LogError("Failed to initialize site: %v", err)
		http.Error(w, "Failed to initialize site", http.StatusInternalServerError)
		return
	}
	s.LogEvent("pub.polis.site.init", map[string]interface{}{
		"site_dir": result.SiteDir,
		"title":    req.SiteTitle,
	})

	// Reload keys after successful init
	s.LoadKeys()

	// Write .env with discovery URL and base URL.
	// Use user-provided values when available, fall back to server defaults.
	// Note: DISCOVERY_SERVICE_KEY is no longer written — all auth uses Ed25519 signatures.
	dsURL := req.DiscoveryURL
	if dsURL == "" {
		dsURL = s.DiscoveryURL
	}
	envPath := filepath.Join(s.DataDir, ".env")
	envVars := map[string]string{
		"DISCOVERY_SERVICE_URL": dsURL,
	}
	if req.BaseURL != "" {
		req.BaseURL = strings.TrimSuffix(req.BaseURL, "/")
		envVars["POLIS_BASE_URL"] = req.BaseURL
		s.BaseURL = req.BaseURL
	}
	s.writeEnvFile(envPath, envVars)

	// Update server state with the (possibly user-provided) discovery config
	s.DiscoveryURL = dsURL

	// Create config file (for webapp settings like hooks, discovery)
	// SetupWizardDismissed defaults to false so wizard shows after init
	s.Config = &Config{
		SetupWizardDismissed: false,
	}
	s.ApplyDiscoveryDefaults()
	if err := s.SaveConfig(); err != nil {
		log.Printf("[warning] Failed to save config: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    result.Success,
		"site_dir":   result.SiteDir,
		"public_key": result.PublicKey,
		"site_title": s.GetSiteTitle(),
		"base_url":   s.BaseURL,
	})
}

// writeEnvFile writes or updates key=value pairs in a .env file.
// If the file exists, it updates existing keys in place and appends new ones.
// Otherwise it creates a new file with all pairs.
func (s *Server) writeEnvFile(envPath string, vars map[string]string) {
	data, err := os.ReadFile(envPath)
	if err == nil {
		// File exists - update existing keys, track which ones we found
		lines := strings.Split(string(data), "\n")
		remaining := make(map[string]string)
		for k, v := range vars {
			remaining[k] = v
		}
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			for key, val := range remaining {
				if strings.HasPrefix(trimmed, key+"=") || strings.HasPrefix(trimmed, "#"+key+"=") {
					lines[i] = key + "=" + val
					delete(remaining, key)
					break
				}
			}
		}
		// Append any keys that weren't found in existing file
		for key, val := range remaining {
			lines = append(lines, key+"="+val)
		}
		if err := os.WriteFile(envPath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			s.LogWarn("Failed to update .env: %v", err)
		}
	} else {
		// Create new .env file
		var b strings.Builder
		b.WriteString("# Polis Configuration\n")
		// Write in a stable order
		for _, key := range []string{"POLIS_BASE_URL", "DISCOVERY_SERVICE_URL", "DISCOVERY_SERVICE_KEY", "LOG_LEVEL", "LOG_RETENTION_DAYS"} {
			if val, ok := vars[key]; ok {
				b.WriteString(key + "=" + val + "\n")
			}
		}
		if err := os.WriteFile(envPath, []byte(b.String()), 0644); err != nil {
			s.LogWarn("Failed to create .env: %v", err)
		}
	}
}

// handleLink creates a symlink from data/ to an existing polis site.
func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Expand ~ to home directory
	targetPath := req.Path
	if strings.HasPrefix(targetPath, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			targetPath = filepath.Join(home, targetPath[2:])
		}
	}

	// Convert to absolute path
	targetPath, err := filepath.Abs(targetPath)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	// Validate the target is a valid polis site
	validation := site.Validate(targetPath)
	if validation.Status != site.StatusValid {
		errMsgs := []string{}
		for _, e := range validation.Errors {
			errMsgs = append(errMsgs, e.Message)
		}
		http.Error(w, "Target is not a valid polis site: "+strings.Join(errMsgs, "; "), http.StatusBadRequest)
		return
	}

	// Get the current data directory path (before it's a symlink)
	execPath, err := os.Executable()
	if err != nil {
		s.LogError("failed to get executable path: %v", err)
		http.Error(w, "Failed to get executable path", http.StatusInternalServerError)
		return
	}
	linkPath := filepath.Join(filepath.Dir(execPath), "data")

	// Safety check: refuse if data/ already has content
	entries, err := os.ReadDir(linkPath)
	if err == nil && len(entries) > 0 {
		// Check if it's already a symlink pointing somewhere
		info, err := os.Lstat(linkPath)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			// It's already a symlink - we can replace it
		} else {
			// It's a real directory with files
			http.Error(w, "Data directory already contains files. Remove them first or use init instead.", http.StatusConflict)
			return
		}
	}

	// Remove existing data directory/symlink
	if err := os.RemoveAll(linkPath); err != nil {
		s.LogError("failed to remove existing data directory: %v", err)
		http.Error(w, "Failed to remove existing data directory", http.StatusInternalServerError)
		return
	}

	// Create symlink
	s.LogDebug("Linking to existing site: %s", targetPath)
	if err := os.Symlink(targetPath, linkPath); err != nil {
		s.LogError("Failed to create symlink: %v", err)
		http.Error(w, "Failed to create symlink", http.StatusInternalServerError)
		return
	}
	s.LogEvent("pub.polis.site.link", map[string]interface{}{
		"path": targetPath,
	})

	// Update server's data directory to the resolved path
	s.DataDir = targetPath

	// Reload keys and config from the linked site
	s.LoadKeys()
	s.LoadConfig()
	s.LoadEnv()
	s.ApplyDiscoveryDefaults()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"linked_to":  targetPath,
		"site_title": s.GetSiteTitle(),
		"site_info":  validation.SiteInfo,
	})
}

func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Not configured - please complete setup first", http.StatusBadRequest)
		return
	}

	var req struct {
		Markdown string `json:"markdown"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if len(req.Markdown) > MaxPostBodySize {
		http.Error(w, "Post content too large (max 256KB)", http.StatusRequestEntityTooLarge)
		return
	}

	// Render markdown to HTML
	html, err := render.MarkdownToHTML(req.Markdown)
	if err != nil {
		s.LogError("render markdown: %v", err)
		http.Error(w, "Failed to render markdown", http.StatusInternalServerError)
		return
	}

	// Sign the content
	signature, err := signing.SignContent([]byte(req.Markdown), s.PrivateKey)
	if err != nil {
		s.LogError("sign content: %v", err)
		http.Error(w, "Failed to sign content", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"html":      html,
		"signature": signature,
	})
}

func (s *Server) handleDrafts(w http.ResponseWriter, r *http.Request) {
	draftsDir := filepath.Join(s.DataDir, ".polis", "content", "pub.polis.core", "posts", "drafts")

	switch r.Method {
	case http.MethodGet:
		// List drafts
		entries, err := os.ReadDir(draftsDir)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"drafts": []interface{}{},
			})
			return
		}

		type draftEntry struct {
			data    map[string]interface{}
			modTime time.Time
		}
		var drafts []draftEntry
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			draftID := strings.TrimSuffix(entry.Name(), ".md")
			d := map[string]interface{}{
				"id":       draftID,
				"name":     entry.Name(),
				"modified": info.ModTime().Format(time.RFC3339),
			}
			// Extract title and excerpt from draft content
			if content, err := os.ReadFile(filepath.Join(draftsDir, entry.Name())); err == nil {
				lines := strings.Split(string(content), "\n")
				title := ""
				bodyStart := 0
				for i, line := range lines {
					trimmed := strings.TrimSpace(line)
					if title == "" && strings.HasPrefix(trimmed, "# ") {
						title = strings.TrimPrefix(trimmed, "# ")
						bodyStart = i + 1
					} else if title == "" && trimmed != "" {
						// First non-empty line without # prefix
						bodyStart = i
						break
					}
				}
				if title != "" {
					d["title"] = title
				}
				// Build excerpt from body lines (skip blank lines after title)
				var excerptLines []string
				for i := bodyStart; i < len(lines) && len(excerptLines) < 3; i++ {
					trimmed := strings.TrimSpace(lines[i])
					if trimmed != "" {
						excerptLines = append(excerptLines, trimmed)
					}
				}
				if len(excerptLines) > 0 {
					excerpt := strings.Join(excerptLines, " ")
					if len(excerpt) > 200 {
						excerpt = excerpt[:200] + "..."
					}
					d["excerpt"] = excerpt
				}
			}
			drafts = append(drafts, draftEntry{
				data:    d,
				modTime: info.ModTime(),
			})
		}

		// Sort by most recently modified first
		sort.Slice(drafts, func(i, j int) bool {
			return drafts[i].modTime.After(drafts[j].modTime)
		})

		draftList := make([]map[string]interface{}, len(drafts))
		for i, d := range drafts {
			draftList[i] = d.data
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"drafts": draftList,
		})

	case http.MethodPost:
		// Save draft
		var req struct {
			ID       string `json:"id"`
			Markdown string `json:"markdown"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			if isBodyTooLargeError(err) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if len(req.Markdown) > MaxPostBodySize {
			http.Error(w, "Draft content too large (max 256KB)", http.StatusRequestEntityTooLarge)
			return
		}

		if req.ID == "" {
			req.ID = fmt.Sprintf("draft-%d", time.Now().Unix())
		}

		// Sanitize ID - whitelist only safe characters
		req.ID = draftIDSanitizer.ReplaceAllString(req.ID, "-")

		draftPath := filepath.Join(draftsDir, req.ID+".md")
		if err := os.WriteFile(draftPath, []byte(req.Markdown), 0644); err != nil {
			s.LogError("failed to save draft: %v", err)
			http.Error(w, "Failed to save draft", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"id":      req.ID,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDraft(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /api/drafts/{id}
	id := strings.TrimPrefix(r.URL.Path, "/api/drafts/")
	if id == "" {
		http.Error(w, "Draft ID required", http.StatusBadRequest)
		return
	}

	// Sanitize ID - whitelist only safe characters
	id = draftIDSanitizer.ReplaceAllString(id, "-")

	draftPath := filepath.Join(s.DataDir, ".polis", "content", "pub.polis.core", "posts", "drafts", id+".md")

	switch r.Method {
	case http.MethodGet:
		content, err := os.ReadFile(draftPath)
		if err != nil {
			http.Error(w, "Draft not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":       id,
			"markdown": string(content),
		})

	case http.MethodDelete:
		if err := os.Remove(draftPath); err != nil {
			s.LogError("failed to delete draft: %v", err)
			http.Error(w, "Failed to delete draft", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Not configured - please complete setup first", http.StatusBadRequest)
		return
	}

	var req struct {
		Markdown string `json:"markdown"`
		Filename string `json:"filename"`
		DraftID  string `json:"draft_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if len(req.Markdown) > MaxPostBodySize {
		http.Error(w, "Post content too large (max 256KB)", http.StatusRequestEntityTooLarge)
		return
	}

	if strings.TrimSpace(req.Markdown) == "" {
		http.Error(w, "Markdown content required", http.StatusBadRequest)
		return
	}

	// Strip existing frontmatter if present
	markdown := req.Markdown
	if publish.HasFrontmatter(markdown) {
		markdown = publish.StripFrontmatter(markdown)
	}

	s.LogDebug("Publishing post with filename: %s", req.Filename)
	result, err := publish.PublishPost(s.DataDir, markdown, req.Filename, s.PrivateKey, s.DiscoveryConfig())
	if err != nil {
		s.LogError("Failed to publish: %v", err)
		http.Error(w, "Failed to publish", http.StatusInternalServerError)
		return
	}
	s.LogEvent("pub.polis.post.publish", map[string]interface{}{
		"path":  result.Path,
		"title": result.Title,
	})

	// Render site to generate HTML files
	if err := s.RenderSite(); err != nil {
		// Log but don't fail - the post was published successfully
		log.Printf("[warning] post-publish render failed: %v", err)
	}

	// Delete the draft if this was published from a draft
	if req.DraftID != "" {
		cleanID := draftIDSanitizer.ReplaceAllString(req.DraftID, "-")
		draftPath := filepath.Join(s.DataDir, ".polis", "content", "pub.polis.core", "posts", "drafts", cleanID+".md")
		if err := os.Remove(draftPath); err != nil && !os.IsNotExist(err) {
			s.LogWarn("Failed to remove draft after publish: %v", err)
		}
	}

	// Run post-publish hook (checks explicit config, then auto-discovers .polis/webapp/hooks/)
	if s.EnableHooks {
		hc := s.getHookConfig()
		payload := &hooks.HookPayload{
			Event:         hooks.EventPostPublish,
			Path:          result.Path,
			Title:         result.Title,
			Version:       result.Version,
			Timestamp:     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
			CommitMessage: hooks.GenerateCommitMessage(hooks.EventPostPublish, result.Title),
		}
		hookResult, err := hooks.RunHook(s.DataDir, hc, payload)
		if err != nil {
			// Log hook error but don't fail the publish
			log.Printf("[warning] post-publish hook failed: %v", err)
			s.LogWarn("Post-publish hook failed: %v", err)
		}
		if hookResult != nil && hookResult.Executed {
			log.Printf("[info] post-publish hook executed: %s", hookResult.Output)
			s.LogEvent("pub.polis.hook.post_publish", map[string]interface{}{
				"output": hookResult.Output,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handlePosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read posts from public.jsonl
	indexPath := filepath.Join(s.DataDir, "content", "pub.polis.core", "index.jsonl")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		// No posts yet
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"posts": []interface{}{},
		})
		return
	}

	// Build blessed comment count map
	commentCountMap := make(map[string]int)
	if bc, err := metadata.LoadBlessedComments(s.DataDir); err == nil {
		for _, pc := range bc.Comments {
			commentCountMap[pc.Post] = len(pc.Blessed)
		}
	}

	var posts []map[string]interface{}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		// Filter out comments - only include posts
		if path, ok := entry["path"].(string); ok {
			if strings.HasPrefix(path, "comments/") || strings.Contains(path, "/comment/") {
				continue
			}
		}
		// Generate excerpt and get modification time from source markdown
		if path, ok := entry["path"].(string); ok {
			srcPath := filepath.Join(s.DataDir, path)
			if raw, err := os.ReadFile(srcPath); err == nil {
				body := stripFrontmatter(string(raw))
				entry["excerpt"] = makeExcerpt(body, 140)
			}
			if info, err := os.Stat(srcPath); err == nil {
				entry["modified"] = info.ModTime().Format(time.RFC3339)
			}
		}
		// Attach blessed comment count
		if path, ok := entry["path"].(string); ok {
			count := commentCountMap[path]
			if count == 0 {
				for k, v := range commentCountMap {
					if metadata.MatchesPostPath(k, path) {
						count = v
						break
					}
				}
			}
			entry["comment_count"] = count
		}
		posts = append(posts, entry)
	}

	// Reverse order (newest first)
	for i, j := 0, len(posts)-1; i < j; i, j = i+1, j-1 {
		posts[i], posts[j] = posts[j], posts[i]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"posts": posts,
	})
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract path from URL: /api/posts/posts/20260125/my-post.md
	postPath := strings.TrimPrefix(r.URL.Path, "/api/posts/")
	if postPath == "" {
		http.Error(w, "Post path required", http.StatusBadRequest)
		return
	}

	// Validate path to prevent directory traversal
	if err := validatePostPath(postPath); err != nil {
		s.LogEvent("pub.polis.security.path_traversal", map[string]interface{}{
			"path": postPath, "request_id": RequestIDFromContext(r.Context()),
		})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Read the post file
	fullPath := filepath.Join(s.DataDir, postPath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	rawMarkdown := string(content)

	// Strip frontmatter to get just the body markdown
	markdown := publish.StripFrontmatter(rawMarkdown)

	// Parse frontmatter for metadata
	frontmatter := publish.ParseFrontmatter(rawMarkdown)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"path":         postPath,
		"markdown":     markdown,
		"raw_markdown": rawMarkdown,
		"title":        frontmatter["title"],
		"published":    frontmatter["published"],
		"updated":      frontmatter["updated"],
	})
}

func (s *Server) handleRepublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Not configured - please complete setup first", http.StatusBadRequest)
		return
	}

	var req struct {
		Path     string `json:"path"`
		Markdown string `json:"markdown"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if len(req.Markdown) > MaxPostBodySize {
		http.Error(w, "Post content too large (max 256KB)", http.StatusRequestEntityTooLarge)
		return
	}

	if req.Path == "" {
		http.Error(w, "Post path required", http.StatusBadRequest)
		return
	}

	// Validate path to prevent directory traversal
	if err := validatePostPath(req.Path); err != nil {
		s.LogEvent("pub.polis.security.path_traversal", map[string]interface{}{
			"path": req.Path, "request_id": RequestIDFromContext(r.Context()),
		})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Markdown) == "" {
		http.Error(w, "Markdown content required", http.StatusBadRequest)
		return
	}

	// Strip existing frontmatter if present
	markdown := req.Markdown
	if publish.HasFrontmatter(markdown) {
		markdown = publish.StripFrontmatter(markdown)
	}

	s.LogDebug("Republishing post: %s", req.Path)
	result, err := publish.RepublishPost(s.DataDir, req.Path, markdown, s.PrivateKey, s.DiscoveryConfig())
	if err != nil {
		s.LogError("Failed to republish %s: %v", req.Path, err)
		http.Error(w, "Failed to republish", http.StatusInternalServerError)
		return
	}
	s.LogEvent("pub.polis.post.republish", map[string]interface{}{
		"path":  result.Path,
		"title": result.Title,
	})

	// Render site to generate HTML files
	if err := s.RenderSite(); err != nil {
		// Log but don't fail - the post was republished successfully
		log.Printf("[warning] post-republish render failed: %v", err)
	}

	// Run post-republish hook (checks explicit config, then auto-discovers .polis/webapp/hooks/)
	if s.EnableHooks {
		hc := s.getHookConfig()
		payload := &hooks.HookPayload{
			Event:         hooks.EventPostRepublish,
			Path:          result.Path,
			Title:         result.Title,
			Version:       result.Version,
			Timestamp:     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
			CommitMessage: hooks.GenerateCommitMessage(hooks.EventPostRepublish, result.Title),
		}
		hookResult, err := hooks.RunHook(s.DataDir, hc, payload)
		if err != nil {
			// Log hook error but don't fail the republish
			log.Printf("[warning] post-republish hook failed: %v", err)
		}
		if hookResult != nil && hookResult.Executed {
			log.Printf("[info] post-republish hook executed: %s", hookResult.Output)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleUnpublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Not configured - please complete setup first", http.StatusBadRequest)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, "Post path required", http.StatusBadRequest)
		return
	}

	// Validate path to prevent directory traversal
	if err := validatePostPath(req.Path); err != nil {
		s.LogEvent("pub.polis.security.path_traversal", map[string]interface{}{
			"path": req.Path, "request_id": RequestIDFromContext(r.Context()),
		})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Verify file exists
	fullPath := filepath.Join(s.DataDir, req.Path)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	// Build content URL from base URL + path
	polisBaseURL := s.BaseURL
	if polisBaseURL == "" {
		http.Error(w, "POLIS_BASE_URL not configured", http.StatusBadRequest)
		return
	}
	contentPath := strings.TrimSuffix(req.Path, ".md")
	contentURL := strings.TrimRight(polisBaseURL, "/") + "/" + contentPath + ".md"

	// Sign unregister request
	canonical := discovery.MakeContentUnregisterCanonicalJSON("pub.polis.post", contentURL)
	sig, err := signing.SignContent([]byte(canonical), s.PrivateKey)
	if err != nil {
		s.LogError("Failed to sign unregister request for %s: %v", req.Path, err)
		http.Error(w, "Failed to sign unregister request", http.StatusInternalServerError)
		return
	}

	// Call DS to unregister (soft-delete) — skip if not registered
	if s.IsRegisteredWithDS() {
		dsClient := s.NewDSClient(r)
		if err := dsClient.UnregisterContent("pub.polis.post", contentURL, sig); err != nil {
			s.LogError("DS unregister failed for %s: %v", req.Path, err)
			// Non-fatal: still clean up locally
		}
	}

	// Delete local .md file
	if err := os.Remove(fullPath); err != nil {
		s.LogError("Failed to delete post file %s: %v", req.Path, err)
		http.Error(w, "Failed to delete post file", http.StatusInternalServerError)
		return
	}

	// Delete rendered .html file if exists
	htmlPath := strings.TrimSuffix(fullPath, ".md") + ".html"
	os.Remove(htmlPath) // Ignore error — may not exist

	// Remove from metadata/public.jsonl
	if err := metadata.RemoveIndexEntry(s.DataDir, req.Path); err != nil {
		s.LogWarn("Could not remove index entry for %s: %v", req.Path, err)
	}

	// Remove version history if exists
	baseName := filepath.Base(req.Path)
	versionsPath := filepath.Join(s.DataDir, ".versions", baseName)
	os.Remove(versionsPath) // Ignore error — may not exist

	// Update manifest
	if err := publish.UpdateManifest(s.DataDir); err != nil {
		s.LogWarn("Failed to update manifest after unpublish: %v", err)
	}

	// Render site to regenerate HTML files
	if err := s.RenderSite(); err != nil {
		log.Printf("[warning] post-unpublish render failed: %v", err)
	}

	s.LogEvent("pub.polis.post.unpublish", map[string]interface{}{
		"path": req.Path,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

func (s *Server) handleRotateKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.PrivateKey == nil || s.PublicKey == nil {
		http.Error(w, "Not configured - keys not found", http.StatusBadRequest)
		return
	}

	polisBaseURL := s.GetBaseURL()
	if polisBaseURL == "" {
		http.Error(w, "POLIS_BASE_URL not configured", http.StatusBadRequest)
		return
	}

	// Extract domain from base URL (same as CLI rotate_key.go)
	domain := polisurl.ExtractDomain(polisBaseURL)
	if domain == "" {
		http.Error(w, "Could not extract domain from POLIS_BASE_URL", http.StatusBadRequest)
		return
	}

	// Read old keys before any changes
	oldPubKey := strings.TrimSpace(string(s.PublicKey))
	oldPrivKey := s.PrivateKey

	// Generate new keypair
	newPrivPEM, newPubSSH, err := signing.GenerateKeypair()
	if err != nil {
		s.LogError("Key rotation failed: keypair generation: %v", err)
		http.Error(w, "Failed to generate new keypair", http.StatusInternalServerError)
		return
	}
	newPubKey := strings.TrimSpace(string(newPubSSH))

	// Build canonical rotation JSON and sign with OLD key
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	canonical, err := discovery.MakeKeyRotationCanonicalJSON(domain, oldPubKey, newPubKey, timestamp)
	if err != nil {
		s.LogError("Key rotation failed: canonical JSON: %v", err)
		http.Error(w, "Failed to build rotation message", http.StatusInternalServerError)
		return
	}

	transitionSig, err := signing.SignContent(canonical, oldPrivKey)
	if err != nil {
		s.LogError("Key rotation failed: transition signature: %v", err)
		http.Error(w, "Failed to sign transition message", http.StatusInternalServerError)
		return
	}

	// Notify DS FIRST (strict ordering — if DS fails, do NOT proceed with local swap)
	dsClient := s.NewDSClient(r)
	err = dsClient.RotateKey(discovery.KeyRotationRequest{
		Domain:        domain,
		OldKey:        oldPubKey,
		NewKey:        newPubKey,
		TransitionSig: transitionSig,
		Timestamp:     timestamp,
	})
	if err != nil {
		s.LogError("Key rotation failed: DS notification: %v", err)
		http.Error(w, "Discovery service rejected key rotation", http.StatusBadGateway)
		return
	}

	// Backup old keys
	keysDir := filepath.Join(s.DataDir, ".polis", "keys")
	if err := os.Rename(filepath.Join(keysDir, "id_ed25519"), filepath.Join(keysDir, "id_ed25519.old")); err != nil {
		s.LogError("Key rotation failed: backup old private key: %v", err)
		http.Error(w, "Failed to backup old keys", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(filepath.Join(keysDir, "id_ed25519.pub"), filepath.Join(keysDir, "id_ed25519.pub.old")); err != nil {
		s.LogError("Key rotation failed: backup old public key: %v", err)
		http.Error(w, "Failed to backup old keys", http.StatusInternalServerError)
		return
	}

	// Write new keys
	if err := os.WriteFile(filepath.Join(keysDir, "id_ed25519"), newPrivPEM, 0600); err != nil {
		s.LogError("Key rotation failed: writing new private key: %v", err)
		http.Error(w, "Failed to write new keys", http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(keysDir, "id_ed25519.pub"), newPubSSH, 0644); err != nil {
		s.LogError("Key rotation failed: writing new public key: %v", err)
		http.Error(w, "Failed to write new keys", http.StatusInternalServerError)
		return
	}

	// Update .well-known/polis with new public key
	wk, err := site.LoadWellKnown(s.DataDir)
	if err != nil {
		s.LogError("Key rotation failed: loading .well-known/polis: %v", err)
		http.Error(w, "Failed to update site identity", http.StatusInternalServerError)
		return
	}
	wk.PublicKey = newPubKey
	if err := site.SaveWellKnown(s.DataDir, wk); err != nil {
		s.LogError("Key rotation failed: saving .well-known/polis: %v", err)
		http.Error(w, "Failed to update site identity", http.StatusInternalServerError)
		return
	}

	// Reload in-memory keys
	s.LoadKeys()

	// Re-render site (non-fatal if fails)
	if err := s.RenderSite(); err != nil {
		log.Printf("[warning] post-rotation render failed: %v", err)
	}

	s.LogEvent("pub.polis.site.key_rotate", nil)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"public_key": newPubKey,
	})
}

// Comment API handlers

func (s *Server) handleCommentDrafts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// List comment drafts
		drafts, err := comment.ListDrafts(s.DataDir)
		if err != nil {
			s.LogError("failed to list drafts: %v", err)
			http.Error(w, "Failed to list drafts", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"drafts": drafts,
		})

	case http.MethodPost:
		// Save comment draft
		var req struct {
			ID        string `json:"id"`
			InReplyTo string `json:"in_reply_to"`
			RootPost  string `json:"root_post"`
			Content   string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			if isBodyTooLargeError(err) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if len(req.Content) > MaxCommentBodySize {
			http.Error(w, "Comment content too large (max 64KB)", http.StatusRequestEntityTooLarge)
			return
		}

		if req.InReplyTo == "" {
			http.Error(w, "in_reply_to is required", http.StatusBadRequest)
			return
		}

		draft := &comment.CommentDraft{
			ID:        req.ID,
			InReplyTo: polisurl.NormalizeToMD(req.InReplyTo),
			RootPost:  polisurl.NormalizeToMD(req.RootPost),
			Content:   req.Content,
		}

		if err := comment.SaveDraft(s.DataDir, draft); err != nil {
			s.LogError("failed to save draft: %v", err)
			http.Error(w, "Failed to save draft", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"id":      draft.ID,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCommentDraft(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /api/comments/drafts/{id}
	id := strings.TrimPrefix(r.URL.Path, "/api/comments/drafts/")
	if id == "" {
		http.Error(w, "Draft ID required", http.StatusBadRequest)
		return
	}

	// Sanitize ID to prevent path traversal (matches post draft pattern)
	id = draftIDSanitizer.ReplaceAllString(id, "-")

	switch r.Method {
	case http.MethodGet:
		draft, err := comment.LoadDraft(s.DataDir, id)
		if err != nil {
			http.Error(w, "Draft not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(draft)

	case http.MethodDelete:
		if err := comment.DeleteDraft(s.DataDir, id); err != nil {
			s.LogError("failed to delete draft: %v", err)
			http.Error(w, "Failed to delete draft", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCommentSign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Not configured - please complete setup first", http.StatusBadRequest)
		return
	}

	var req struct {
		DraftID   string `json:"draft_id"`
		InReplyTo string `json:"in_reply_to"`
		RootPost  string `json:"root_post"`
		Content   string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if len(req.Content) > MaxCommentBodySize {
		http.Error(w, "Comment content too large (max 64KB)", http.StatusRequestEntityTooLarge)
		return
	}

	// Load draft if ID provided, otherwise use inline content
	var draft *comment.CommentDraft
	if req.DraftID != "" {
		var err error
		draft, err = comment.LoadDraft(s.DataDir, req.DraftID)
		if err != nil {
			http.Error(w, "Draft not found", http.StatusNotFound)
			return
		}
	} else {
		if req.InReplyTo == "" {
			http.Error(w, "in_reply_to is required", http.StatusBadRequest)
			return
		}
		draft = &comment.CommentDraft{
			InReplyTo: polisurl.NormalizeToMD(req.InReplyTo),
			RootPost:  polisurl.NormalizeToMD(req.RootPost),
			Content:   req.Content,
		}
	}

	// Get author domain from .well-known/polis (domain is the public identity)
	authorDomain := s.GetAuthorDomain()
	if authorDomain == "" {
		http.Error(w, "Author identity not configured - set domain in .well-known/polis or POLIS_BASE_URL in .env", http.StatusBadRequest)
		return
	}

	// Get site URL from POLIS_BASE_URL env var (authoritative source, matches bash CLI)
	siteURL := s.GetBaseURL()
	if siteURL == "" {
		http.Error(w, "POLIS_BASE_URL not configured - set it in .env file", http.StatusBadRequest)
		return
	}

	signed, err := comment.SignComment(s.DataDir, draft, authorDomain, siteURL, s.PrivateKey)
	if err != nil {
		s.LogError("failed to sign comment: %v", err)
		http.Error(w, "Failed to sign comment", http.StatusInternalServerError)
		return
	}

	s.LogEvent("pub.polis.comment.sign", map[string]interface{}{
		"in_reply_to": draft.InReplyTo,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"comment":   signed.Meta,
		"signature": signed.Signature,
	})
}

func (s *Server) handleCommentBeseech(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CommentID string `json:"comment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.CommentID == "" {
		http.Error(w, "comment_id is required", http.StatusBadRequest)
		return
	}

	// Business logic is in the comment package (pass per-tenant config for hosted safety)
	result, err := comment.BeseechComment(s.DataDir, req.CommentID, s.PrivateKey, &comment.DiscoveryConfig{
		DiscoveryURL: s.DiscoveryURL,
		DiscoveryKey: s.DiscoveryKey,
		BaseURL:      s.GetBaseURL(),
	})

	// Always re-render after beseech attempt — PublishComment() runs early inside
	// BeseechComment, so the .md may already be on disk even if DS registration
	// fails afterward. Without this, the comment HTML and index.html are never generated.
	if renderErr := s.RenderSite(); renderErr != nil {
		log.Printf("[warning] post-beseech render failed: %v", renderErr)
	}

	if err != nil {
		s.LogError("beseech failed: %v", err)
		// Config issues → 400, runtime errors → 500
		status := http.StatusInternalServerError
		errMsg := err.Error()
		if strings.Contains(errMsg, "not configured") || strings.Contains(errMsg, "not found in pending") {
			status = http.StatusBadRequest
		}
		http.Error(w, errMsg, status)
		return
	}

	// Run hooks if auto-blessed
	if result.AutoBlessed && s.EnableHooks {
		hc := s.getHookConfig()
		payload := &hooks.HookPayload{
			Event:         hooks.EventPostComment,
			Path:          fmt.Sprintf("comments/blessed/%s.md", req.CommentID),
			Title:         result.Comment.InReplyTo,
			Version:       result.Comment.CommentVersion,
			Timestamp:     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
			CommitMessage: hooks.GenerateCommitMessage(hooks.EventPostComment, result.Comment.InReplyTo),
		}
		hooks.RunHook(s.DataDir, hc, payload)
	}

	s.LogEvent("pub.polis.comment.beseech", map[string]interface{}{
		"comment_id": req.CommentID,
		"status":     result.Status,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": result.Success,
		"status":  result.Status,
		"message": result.Message,
	})
}

func (s *Server) handleCommentsPending(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	comments, err := comment.ListComments(s.DataDir, comment.StatusPending)
	if err != nil {
		s.LogError("failed to list pending comments: %v", err)
		http.Error(w, "Failed to list pending comments", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"comments": comments,
	})
}

func (s *Server) handleCommentsBlessed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	comments, err := comment.ListComments(s.DataDir, comment.StatusBlessed)
	if err != nil {
		s.LogError("failed to list blessed comments: %v", err)
		http.Error(w, "Failed to list blessed comments", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"comments": comments,
	})
}

func (s *Server) handleCommentsDenied(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	comments, err := comment.ListComments(s.DataDir, comment.StatusDenied)
	if err != nil {
		s.LogError("failed to list denied comments: %v", err)
		http.Error(w, "Failed to list denied comments", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"comments": comments,
	})
}

// handleCommentByStatus handles GET /api/comments/{status}/{id}
func (s *Server) handleCommentByStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract status and ID from URL: /api/comments/{status}/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/comments/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		http.Error(w, "Comment ID required", http.StatusBadRequest)
		return
	}

	status := parts[0]
	commentID := parts[1]

	// Validate status
	if status != comment.StatusPending && status != comment.StatusBlessed && status != comment.StatusDenied {
		http.Error(w, "Invalid status", http.StatusBadRequest)
		return
	}

	result, err := comment.GetComment(s.DataDir, commentID, status)
	if err != nil {
		http.Error(w, "Comment not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"comment": map[string]interface{}{
			"id":          result.Meta.ID,
			"title":       result.Meta.Title,
			"in_reply_to": result.Meta.InReplyTo,
			"root_post":   result.Meta.RootPost,
			"comment_url": result.Meta.CommentURL,
			"timestamp":   result.Meta.Timestamp,
			"status":      result.Meta.Status,
			"content":     result.Content,
		},
	})
}

func (s *Server) handleCommentsSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.DiscoveryURL == "" {
		http.Error(w, "Discovery service not configured", http.StatusBadRequest)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Private key not configured", http.StatusBadRequest)
		return
	}

	// Create authenticated discovery client (needed for pending/denied queries)
	myDomain := discovery.ExtractDomainFromURL(s.GetBaseURL())
	client := s.NewAuthDSClient(r, myDomain)

	// Sync pending comments (pass nil hookConfig when hooks disabled to prevent execution)
	hc := s.getHookConfig()
	result, err := comment.SyncPendingComments(s.DataDir, s.GetBaseURL(), client, hc)
	if err != nil {
		log.Printf("handleCommentsSync: failed for %s: %v", myDomain, err)
		s.LogError("failed to sync comments: %v", err)
		http.Error(w, "Failed to sync comments", http.StatusInternalServerError)
		return
	}

	// Re-render site so HTML reflects updated comment statuses (blessed/denied)
	if err := s.RenderSite(); err != nil {
		log.Printf("[warning] post-comment-sync render failed: %v", err)
	}

	s.LogEvent("pub.polis.comment.sync", map[string]interface{}{
		"blessed": len(result.Blessed),
		"denied":  len(result.Denied),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// Blessing API handlers (ON MY POSTS - incoming blessing requests)

func (s *Server) handleBlessingRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.DiscoveryURL == "" {
		http.Error(w, "Discovery service not configured", http.StatusBadRequest)
		return
	}

	if s.PrivateKey == nil {
		log.Printf("handleBlessingRequests: private key not configured for %s", s.GetBaseURL())
		http.Error(w, "Private key not configured", http.StatusBadRequest)
		return
	}

	// Create authenticated discovery client (needed for status=pending queries)
	myDomain := discovery.ExtractDomainFromURL(s.GetBaseURL())
	client := s.NewAuthDSClient(r, myDomain)

	// Fetch pending blessing requests (actor must be full domain, not subdomain)
	requests, err := blessing.FetchPendingRequests(client, myDomain)
	if err != nil {
		log.Printf("handleBlessingRequests: failed to fetch for %s: %v", myDomain, err)
		s.LogError("failed to fetch requests: %v", err)
		http.Error(w, fmt.Sprintf("Failed to fetch requests: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"requests": requests,
	})
}

func (s *Server) handleBlessingGrant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.DiscoveryURL == "" {
		http.Error(w, "Discovery service not configured", http.StatusBadRequest)
		return
	}

	if !s.IsRegisteredWithDS() {
		http.Error(w, "Site not registered with discovery service", http.StatusPreconditionFailed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Private key not configured", http.StatusBadRequest)
		return
	}

	var req struct {
		CommentVersion string `json:"comment_version"`
		CommentURL     string `json:"comment_url"`
		InReplyTo      string `json:"in_reply_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.CommentURL == "" {
		http.Error(w, "comment_url is required", http.StatusBadRequest)
		return
	}

	// Create discovery client
	client := s.NewDSClient(r)

	// If comment_version is missing (old DS records without metadata), look it up
	if req.CommentVersion == "" {
		if check, err := client.CheckContent("pub.polis.comment", req.CommentURL); err == nil && check.Exists {
			req.CommentVersion = check.Version
		}
	}

	// Grant the blessing (with signed request)
	// Normalize URLs to .md format for consistent storage
	s.LogDebug("Granting blessing for comment version: %s", req.CommentVersion)
	bhc := s.getHookConfig()
	result, err := blessing.GrantByVersion(
		s.DataDir,
		req.CommentVersion,
		polisurl.NormalizeToMD(req.CommentURL),
		polisurl.NormalizeToMD(req.InReplyTo),
		client,
		bhc,
		s.PrivateKey,
	)
	if err != nil {
		s.LogError("Failed to grant blessing: %v", err)
		http.Error(w, "Failed to grant blessing", http.StatusInternalServerError)
		return
	}
	s.LogEvent("pub.polis.comment.blessing.grant", map[string]interface{}{
		"comment_url": req.CommentURL,
	})

	// Fetch the remote comment markdown and save it locally so the renderer
	// can display the comment body on the post page. The comment .md file
	// lives on the commenter's site, not ours.
	if commentRelPath := extractCommentRelPath(req.CommentURL); commentRelPath != "" {
		localPath := filepath.Join(s.DataDir, commentRelPath)
		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			rc := s.NewRemoteClient()
			mdURL := polisurl.NormalizeToMD(req.CommentURL)
			content, fetchErr := rc.FetchContent(commentSourceURL(mdURL))
			if fetchErr != nil {
				// Fallback: try the mount path directly (non-hosted sites may serve .md there)
				content, fetchErr = rc.FetchContent(mdURL)
			}
			if fetchErr == nil {
				if err := os.MkdirAll(filepath.Dir(localPath), 0755); err == nil {
					os.WriteFile(localPath, []byte(content), 0644)
				}
			} else {
				log.Printf("[warning] could not fetch remote comment %s: %v", req.CommentURL, fetchErr)
			}
		}
	}

	// Render site to include the newly blessed comment
	if err := s.RenderSite(); err != nil {
		// Log but don't fail - the blessing was granted successfully
		log.Printf("[warning] post-blessing render failed: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleBlessingDeny(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.DiscoveryURL == "" {
		http.Error(w, "Discovery service not configured", http.StatusBadRequest)
		return
	}

	if !s.IsRegisteredWithDS() {
		http.Error(w, "Site not registered with discovery service", http.StatusPreconditionFailed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Private key not configured", http.StatusBadRequest)
		return
	}

	var req struct {
		CommentURL string `json:"comment_url"`
		InReplyTo  string `json:"in_reply_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.CommentURL == "" || req.InReplyTo == "" {
		http.Error(w, "comment_url and in_reply_to are required", http.StatusBadRequest)
		return
	}

	// Create discovery client
	client := s.NewDSClient(r)

	// Deny the blessing (with signed request)
	s.LogDebug("Denying blessing for comment: %s", req.CommentURL)
	result, err := blessing.Deny(req.CommentURL, req.InReplyTo, client, s.PrivateKey)
	if err != nil {
		s.LogError("Failed to deny blessing: %v", err)
		http.Error(w, "Failed to deny blessing", http.StatusInternalServerError)
		return
	}
	s.LogEvent("pub.polis.comment.blessing.deny", map[string]interface{}{
		"comment_url": req.CommentURL,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleBlessedComments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Load blessed comments from local metadata
	bc, err := metadata.LoadBlessedComments(s.DataDir)
	if err != nil {
		// Return empty list if file doesn't exist
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"version":  "",
			"comments": []interface{}{},
		})
		return
	}

	// Enrich each blessed comment with an excerpt from the locally saved markdown
	// and resolve the post title from the local post file
	type enrichedComment struct {
		URL       string `json:"url"`
		Version   string `json:"version"`
		BlessedAt string `json:"blessed_at"`
		Excerpt   string `json:"excerpt,omitempty"`
	}
	type enrichedPostComments struct {
		Post      string            `json:"post"`
		PostTitle string            `json:"post_title,omitempty"`
		Blessed   []enrichedComment `json:"blessed"`
	}
	enriched := make([]enrichedPostComments, 0, len(bc.Comments))
	for _, pc := range bc.Comments {
		epc := enrichedPostComments{Post: pc.Post}

		// Resolve post title — try local file first, then derive from path
		postPath := pc.Post
		// Try local post file (works for posts on our own site)
		localPostPath := postPath
		if idx := strings.Index(localPostPath, "/posts/"); idx >= 0 {
			localPostPath = "content/pub.polis.core/post/" + localPostPath[idx+len("/posts/"):]
		}
		localPostPath = strings.TrimSuffix(localPostPath, ".html")
		if !strings.HasSuffix(localPostPath, ".md") {
			localPostPath += ".md"
		}
		if raw, err := os.ReadFile(filepath.Join(s.DataDir, localPostPath)); err == nil {
			epc.PostTitle = extractFrontmatterTitle(string(raw))
		}
		// Derive a readable title from the URL/path if no frontmatter title found
		if epc.PostTitle == "" {
			slug := postPath
			if lastSlash := strings.LastIndex(slug, "/"); lastSlash >= 0 {
				slug = slug[lastSlash+1:]
			}
			slug = strings.TrimSuffix(slug, ".md")
			slug = strings.TrimSuffix(slug, ".html")
			slug = strings.ReplaceAll(slug, "-", " ")
			if slug != "" {
				// Title case the first letter
				epc.PostTitle = strings.ToUpper(slug[:1]) + slug[1:]
			}
		}

		for _, c := range pc.Blessed {
			ec := enrichedComment{
				URL:       c.URL,
				Version:   c.Version,
				BlessedAt: c.BlessedAt,
			}
			// Try to read local copy of the comment markdown.
			// Normalize URL to .md and try multiple path forms.
			commentURL := c.URL
			if strings.HasSuffix(commentURL, ".html") {
				commentURL = strings.TrimSuffix(commentURL, ".html") + ".md"
			}
			if relPath := extractCommentRelPath(commentURL); relPath != "" {
				localPath := filepath.Join(s.DataDir, relPath)
				if raw, err := os.ReadFile(localPath); err == nil {
					body := stripFrontmatter(string(raw))
					ec.Excerpt = makeExcerpt(body, 140)
				}
			}
			epc.Blessed = append(epc.Blessed, ec)
		}
		enriched = append(enriched, epc)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"version":  bc.Version,
		"comments": enriched,
	})
}

func (s *Server) handleBlessingRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CommentURL string `json:"comment_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.CommentURL == "" {
		http.Error(w, "comment_url is required", http.StatusBadRequest)
		return
	}

	// Normalize URL to .md format for consistent lookup
	normalizedURL := polisurl.NormalizeToMD(req.CommentURL)

	// Remove from blessed-comments.json
	if err := metadata.RemoveBlessedComment(s.DataDir, normalizedURL); err != nil {
		s.LogError("failed to revoke blessing: %v", err)
		http.Error(w, "Failed to revoke blessing", http.StatusInternalServerError)
		return
	}
	s.LogEvent("pub.polis.comment.blessing.revoke", map[string]interface{}{
		"comment_url": normalizedURL,
	})

	// Render site to remove the comment from pages
	if err := s.RenderSite(); err != nil {
		// Log but don't fail - the revoke was successful
		log.Printf("[warning] post-revoke render failed: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"comment_url": normalizedURL,
	})
}

// Settings and Automation API handlers

// Automation represents a configured automation (hook)
type Automation struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Event       string `json:"event"`
	ScriptPath  string `json:"script_path"`
	Enabled     bool   `json:"enabled"`
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Build site info from .well-known/polis and config
	subdomain := ""
	publicKey := ""
	discoveryURL := s.DiscoveryURL
	discoveryConfigured := s.DiscoveryURL != ""
	siteTitle := s.GetSiteTitle() // From .well-known/polis with fallback to base_url
	showFrontmatter := true       // Default to showing frontmatter
	baseURL := ""

	if s.Config != nil {
		subdomain = s.GetSubdomain()
		if s.Config.ShowFrontmatter != nil {
			showFrontmatter = *s.Config.ShowFrontmatter
		}
	}
	if s.PublicKey != nil {
		publicKey = strings.TrimSpace(string(s.PublicKey))
	}

	// Get base URL from POLIS_BASE_URL env var (matches bash CLI behavior)
	baseURL = s.GetBaseURL()

	// Build automations list from hooks config
	automations := s.getAutomations()

	// Check which hook files exist
	existingHooks := s.getExistingHooks()

	setupWizardDismissed := false
	if s.Config != nil {
		setupWizardDismissed = s.Config.SetupWizardDismissed
	}

	// Webapp theme preference
	webappTheme := "dark"
	if s.Config != nil && s.Config.WebappTheme != "" {
		webappTheme = s.Config.WebappTheme
	}

	// Editor panel mode preference
	editorPanelMode := "wysiwyg"
	if s.Config != nil && s.Config.EditorPanelMode != "" {
		editorPanelMode = s.Config.EditorPanelMode
	}

	// Load theme data
	themes, _ := theme.ListThemesWithPalettes(s.DataDir, s.CLIThemesDir)
	activeTheme, _ := theme.GetActiveTheme(s.DataDir)

	// Load avatar and author_name from .well-known/polis
	var avatarConfig *site.AvatarConfig
	var authorName string
	if wk, err := site.LoadWellKnown(s.DataDir); err == nil {
		avatarConfig = wk.Avatar
		authorName = wk.AuthorName
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"site": map[string]interface{}{
			"subdomain":            subdomain,
			"site_title":           siteTitle,
			"public_key":           publicKey,
			"data_dir":             s.DataDir,
			"discovery_url":        discoveryURL,
			"discovery_configured": discoveryConfigured,
			"show_frontmatter":     showFrontmatter,
			"base_url":             baseURL,
			"avatar":               avatarConfig,
			"author_name":          authorName,
		},
		"automations":             automations,
		"existing_hooks":          existingHooks,
		"setup_wizard_dismissed":  setupWizardDismissed,
		"hide_read":               s.Config != nil && s.Config.HideRead,
		"webapp_theme":            webappTheme,
		"editor_panel_mode":       editorPanelMode,
		"active_theme":            activeTheme,
		"themes":                  themes,
	})
}

func (s *Server) getAutomations() []Automation {
	var automations []Automation
	if !s.EnableHooks {
		return automations
	}
	hc := s.getHookConfig()

	type hookInfo struct {
		event       hooks.HookEvent
		id          string
		name        string
		description string
	}
	allHooks := []hookInfo{
		{hooks.EventPostPublish, "post-publish", "Post-publish hook", "Runs after each publish"},
		{hooks.EventPostRepublish, "post-republish", "Post-republish hook", "Runs after each republish"},
		{hooks.EventPostComment, "post-comment", "Post-comment hook", "Runs when a comment becomes blessed (grant, sync, or auto-bless)"},
	}

	for _, h := range allHooks {
		path := hooks.GetHookPathWithDiscovery(hc, h.event, s.DataDir)
		if path != "" {
			automations = append(automations, Automation{
				ID:          h.id,
				Name:        h.name,
				Description: h.description,
				Event:       string(h.event),
				ScriptPath:  path,
				Enabled:     true,
			})
		}
	}

	return automations
}

func (s *Server) handleAutomations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		automations := s.getAutomations()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"automations": automations,
		})

	case http.MethodPost:
		if !s.EnableHooks {
			http.Error(w, "Hooks are not available in this mode", http.StatusForbidden)
			return
		}
		// Create a new automation
		var req struct {
			TemplateID string `json:"template_id"`
			HookType   string `json:"hook_type"`
			Script     string `json:"script"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			if isBodyTooLargeError(err) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if len(req.Script) > MaxHookBodySize {
			http.Error(w, "Hook script too large (max 32KB)", http.StatusRequestEntityTooLarge)
			return
		}

		// Default to post-publish if not specified
		hookType := req.HookType
		if hookType == "" {
			hookType = "post-publish"
		}

		// Validate hook type
		validTypes := map[string]bool{
			"post-publish":   true,
			"post-republish": true,
			"post-comment":   true,
		}
		if !validTypes[hookType] {
			http.Error(w, "Invalid hook type", http.StatusBadRequest)
			return
		}

		// Get script from template or use provided script
		script := req.Script
		if req.TemplateID != "" {
			template, ok := hooks.GetTemplate(req.TemplateID)
			if !ok {
				http.Error(w, "Unknown template ID", http.StatusBadRequest)
				return
			}
			script = template.Script
		}

		if script == "" {
			http.Error(w, "Script is required", http.StatusBadRequest)
			return
		}

		// Create the hook script
		scriptPath, err := s.createHookScript(script, hookType)
		if err != nil {
			s.LogError("failed to create hook: %v", err)
			http.Error(w, "Failed to create hook", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"script_path": scriptPath,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAutomationsQuick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.EnableHooks {
		http.Error(w, "Hooks are not available in this mode", http.StatusForbidden)
		return
	}

	// Use the vercel template (default for quick create)
	template, _ := hooks.GetTemplate("vercel")

	scriptPath, err := s.createHookScript(template.Script, "post-publish")
	if err != nil {
		s.LogError("failed to create hook: %v", err)
		http.Error(w, "Failed to create hook", http.StatusInternalServerError)
		return
	}
	_ = scriptPath // suppress unused variable warning

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"script_path": ".polis/webapp/hooks/post-publish.sh",
		"message":     "Created post-publish hook at .polis/webapp/hooks/post-publish.sh",
	})
}

func (s *Server) createHookScript(script string, hookType string) (string, error) {
	// Create hooks directory (.polis/webapp/hooks — restricted)
	hooksDir := filepath.Join(s.DataDir, ".polis", "webapp", "hooks")
	if err := os.MkdirAll(hooksDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create hooks directory: %w", err)
	}

	// Write the script file
	relativePath := ".polis/webapp/hooks/" + hookType + ".sh"
	scriptPath := filepath.Join(hooksDir, hookType+".sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", fmt.Errorf("failed to write hook script: %w", err)
	}

	// Update config to use this hook
	if s.Config == nil {
		s.Config = &Config{}
	}
	if s.Config.Hooks == nil {
		s.Config.Hooks = &hooks.HookConfig{}
	}

	switch hookType {
	case "post-publish":
		s.Config.Hooks.PostPublish = relativePath
	case "post-republish":
		s.Config.Hooks.PostRepublish = relativePath
	case "post-comment":
		s.Config.Hooks.PostComment = relativePath
	}

	if err := s.SaveConfig(); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}

	return relativePath, nil
}

func (s *Server) handleAutomation(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /api/automations/{id}
	id := strings.TrimPrefix(r.URL.Path, "/api/automations/")
	if id == "" {
		http.Error(w, "Automation ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if !s.EnableHooks {
			http.Error(w, "Hooks are not available in this mode", http.StatusForbidden)
			return
		}
		// Remove the automation
		if s.Config == nil || s.Config.Hooks == nil {
			http.Error(w, "No automations configured", http.StatusNotFound)
			return
		}

		var scriptPath string
		switch id {
		case "post-publish":
			scriptPath = s.Config.Hooks.PostPublish
			s.Config.Hooks.PostPublish = ""
		case "post-republish":
			scriptPath = s.Config.Hooks.PostRepublish
			s.Config.Hooks.PostRepublish = ""
		case "post-comment":
			scriptPath = s.Config.Hooks.PostComment
			s.Config.Hooks.PostComment = ""
		default:
			http.Error(w, "Unknown automation ID", http.StatusNotFound)
			return
		}

		// Save the updated config
		if err := s.SaveConfig(); err != nil {
			s.LogError("failed to save config: %v", err)
			http.Error(w, "Failed to save config", http.StatusInternalServerError)
			return
		}

		// Optionally delete the script file (only if it's in our hooks directory)
		if scriptPath != "" && strings.HasPrefix(scriptPath, ".polis/webapp/hooks/") {
			fullPath := filepath.Join(s.DataDir, scriptPath)
			os.Remove(fullPath) // Ignore error - file might not exist
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	templates := hooks.ListTemplates()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"templates": templates,
	})
}

// getExistingHooks returns a list of hook types that have existing hook files
func (s *Server) getExistingHooks() []string {
	var existing []string
	hooksDir := filepath.Join(s.DataDir, ".polis", "webapp", "hooks")

	hookTypes := []string{"post-publish", "post-republish", "post-comment"}
	for _, hookType := range hookTypes {
		scriptPath := filepath.Join(hooksDir, hookType+".sh")
		if _, err := os.Stat(scriptPath); err == nil {
			existing = append(existing, hookType)
		}
	}
	return existing
}

// handleHooksGenerate handles POST /api/hooks/generate to create an empty hook script
func (s *Server) handleHooksGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.EnableHooks {
		http.Error(w, "Hooks are not available in this mode", http.StatusForbidden)
		return
	}

	var req struct {
		HookType string `json:"hook_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Validate hook type
	validTypes := map[string]bool{
		"post-publish":   true,
		"post-republish": true,
		"post-comment":   true,
	}
	if !validTypes[req.HookType] {
		http.Error(w, "Invalid hook type. Must be one of: post-publish, post-republish, post-comment", http.StatusBadRequest)
		return
	}

	// Create hooks directory (.polis/webapp/hooks — restricted)
	hooksDir := filepath.Join(s.DataDir, ".polis", "webapp", "hooks")
	if err := os.MkdirAll(hooksDir, 0700); err != nil {
		s.LogError("failed to create hooks directory: %v", err)
		http.Error(w, "Failed to create hooks directory", http.StatusInternalServerError)
		return
	}

	// Check if file already exists
	scriptPath := filepath.Join(hooksDir, req.HookType+".sh")
	if _, err := os.Stat(scriptPath); err == nil {
		http.Error(w, "Hook file already exists: .polis/webapp/hooks/"+req.HookType+".sh", http.StatusConflict)
		return
	}

	// Create empty hook script with boilerplate
	script := fmt.Sprintf(`#!/bin/bash
set -e
# %s hook
# Available environment variables:
# POLIS_SITE_DIR - path to your site directory
# POLIS_PATH - relative path to the published file
# POLIS_TITLE - title of the post
# POLIS_COMMIT_MESSAGE - suggested commit message
# POLIS_EVENT - event type (%s)
# POLIS_VERSION - content hash
# POLIS_TIMESTAMP - ISO timestamp

# Add your custom logic below:
echo "Hook triggered: %s"
`, req.HookType, req.HookType, req.HookType)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		s.LogError("failed to write hook script: %v", err)
		http.Error(w, "Failed to write hook script", http.StatusInternalServerError)
		return
	}

	// Update config to use this hook
	if s.Config == nil {
		s.Config = &Config{}
	}
	if s.Config.Hooks == nil {
		s.Config.Hooks = &hooks.HookConfig{}
	}

	relativePath := ".polis/webapp/hooks/" + req.HookType + ".sh"
	switch req.HookType {
	case "post-publish":
		s.Config.Hooks.PostPublish = relativePath
	case "post-republish":
		s.Config.Hooks.PostRepublish = relativePath
	case "post-comment":
		s.Config.Hooks.PostComment = relativePath
	}

	if err := s.SaveConfig(); err != nil {
		s.LogError("failed to save config: %v", err)
		http.Error(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"hook_type":   req.HookType,
		"script_path": relativePath,
		"message":     "Created " + req.HookType + " hook at " + relativePath,
	})
}

// handleThemeSwitch handles POST /api/settings/theme to switch the site theme.
func (s *Server) handleThemeSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Theme string `json:"theme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Theme == "" {
		http.Error(w, "theme is required", http.StatusBadRequest)
		return
	}
	if req.Theme == "sols" {
		http.Error(w, "sols is reserved as the system theme and cannot be selected as a personal theme", http.StatusBadRequest)
		return
	}

	// Validate that the theme exists
	themes, err := theme.ListThemes(s.DataDir, s.CLIThemesDir)
	if err != nil {
		s.LogError("theme switch: failed to list themes: %v", err)
		http.Error(w, "Failed to list themes", http.StatusInternalServerError)
		return
	}
	found := false
	for _, t := range themes {
		if t == req.Theme {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Unknown theme: "+req.Theme, http.StatusBadRequest)
		return
	}

	// Update manifest and copy CSS
	if err := theme.SetActiveTheme(s.DataDir, req.Theme); err != nil {
		s.LogError("theme switch: set active theme failed: %v", err)
		http.Error(w, "Failed to set theme: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := theme.CopyCSS(s.DataDir, s.CLIThemesDir, req.Theme); err != nil {
		s.LogError("theme switch: copy CSS failed: %v", err)
		http.Error(w, "Failed to copy theme CSS: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-render entire site with new theme
	if err := s.RenderSite(); err != nil {
		s.LogError("theme switch: render site failed: %v", err)
		// Non-fatal — theme files are updated, render can be retried
	}

	s.LogEvent("pub.polis.site.theme_switch", map[string]interface{}{
		"theme": req.Theme,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"theme":   req.Theme,
	})
}

// handleShowFrontmatter handles POST /api/settings/show-frontmatter to toggle frontmatter visibility
func (s *Server) handleShowFrontmatter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ShowFrontmatter bool `json:"show_frontmatter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Ensure config exists
	if s.Config == nil {
		s.Config = &Config{}
	}

	// Update and save
	s.Config.ShowFrontmatter = &req.ShowFrontmatter
	if err := s.SaveConfig(); err != nil {
		s.LogError("failed to save config: %v", err)
		http.Error(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":          true,
		"show_frontmatter": req.ShowFrontmatter,
	})
}

func (s *Server) handleHideRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		HideRead bool `json:"hide_read"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Ensure config exists
	if s.Config == nil {
		s.Config = &Config{}
	}

	// Update and save
	s.Config.HideRead = req.HideRead
	if err := s.SaveConfig(); err != nil {
		s.LogError("failed to save config: %v", err)
		http.Error(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"hide_read": req.HideRead,
	})
}

// handleWebappTheme handles POST /api/settings/webapp-theme to switch between light and dark themes.
func (s *Server) handleWebappTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Theme string `json:"theme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Theme != "light" && req.Theme != "dark" {
		http.Error(w, "Invalid theme: must be 'light' or 'dark'", http.StatusBadRequest)
		return
	}

	if s.Config == nil {
		s.Config = &Config{}
	}

	s.Config.WebappTheme = req.Theme
	if err := s.SaveConfig(); err != nil {
		s.LogError("failed to save config: %v", err)
		http.Error(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"webapp_theme": req.Theme,
	})
}

// handleEditorPanelMode handles POST /api/settings/editor-panel-mode to persist the right panel mode.
func (s *Server) handleEditorPanelMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	validModes := map[string]bool{"wysiwyg": true, "markdown": true, "help": true, "browse": true}
	if !validModes[req.Mode] {
		http.Error(w, "Invalid mode", http.StatusBadRequest)
		return
	}

	if s.Config == nil {
		s.Config = &Config{}
	}

	s.Config.EditorPanelMode = req.Mode
	if err := s.SaveConfig(); err != nil {
		s.LogError("failed to save config: %v", err)
		http.Error(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"editor_panel_mode": req.Mode,
	})
}

// handleUpdateSiteTitle handles POST /api/settings/site-title to update the site title.
func (s *Server) handleUpdateSiteTitle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SiteTitle string `json:"site_title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	wk, err := site.LoadWellKnown(s.DataDir)
	if err != nil {
		s.LogError("failed to load .well-known/polis: %v", err)
		http.Error(w, "Failed to load site config", http.StatusInternalServerError)
		return
	}

	wk.SiteTitle = strings.TrimSpace(req.SiteTitle)

	if err := site.SaveWellKnown(s.DataDir, wk); err != nil {
		s.LogError("failed to save .well-known/polis: %v", err)
		http.Error(w, "Failed to save site config", http.StatusInternalServerError)
		return
	}

	s.LogEvent("pub.polis.site.title_update", map[string]interface{}{
		"title": wk.SiteTitle,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"site_title": wk.SiteTitle,
	})
}

// Valid hex color pattern for avatar config
var hexColorRegex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Valid avatar pattern names
var validPatterns = map[string]bool{
	"none": true, "rings": true, "cross": true, "grid": true,
	"dots": true, "stripes": true, "diamond": true, "halves": true,
}

func (s *Server) handleUpdateAvatar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Avatar *site.AvatarConfig `json:"avatar"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	wk, err := site.LoadWellKnown(s.DataDir)
	if err != nil {
		s.LogError("failed to load .well-known/polis: %v", err)
		http.Error(w, "Failed to load site config", http.StatusInternalServerError)
		return
	}

	if req.Avatar != nil {
		// Validate hex colors
		for _, c := range []string{req.Avatar.BG, req.Avatar.FG} {
			if !hexColorRegex.MatchString(c) {
				http.Error(w, "Invalid color: must be #RRGGBB hex", http.StatusBadRequest)
				return
			}
		}
		for _, c := range []string{req.Avatar.Border, req.Avatar.PatternColor} {
			if c != "" && !hexColorRegex.MatchString(c) {
				http.Error(w, "Invalid color: must be #RRGGBB hex", http.StatusBadRequest)
				return
			}
		}
		// Validate border width
		if req.Avatar.BorderW < 0 || req.Avatar.BorderW > 3 {
			http.Error(w, "Invalid border_w: must be 0-3", http.StatusBadRequest)
			return
		}
		// Validate pattern
		if req.Avatar.Pattern != "" && !validPatterns[req.Avatar.Pattern] {
			http.Error(w, "Invalid pattern", http.StatusBadRequest)
			return
		}
	}

	wk.Avatar = req.Avatar

	if err := site.SaveWellKnown(s.DataDir, wk); err != nil {
		s.LogError("failed to save .well-known/polis: %v", err)
		http.Error(w, "Failed to save site config", http.StatusInternalServerError)
		return
	}

	s.LogEvent("pub.polis.site.avatar_update", map[string]interface{}{
		"has_avatar": req.Avatar != nil,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"avatar":  wk.Avatar,
	})
}

func (s *Server) handleUpdateAuthorName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		AuthorName string `json:"author_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(req.AuthorName)
	if len(name) > 50 {
		http.Error(w, "Display name must be 50 characters or less", http.StatusBadRequest)
		return
	}

	wk, err := site.LoadWellKnown(s.DataDir)
	if err != nil {
		s.LogError("failed to load .well-known/polis: %v", err)
		http.Error(w, "Failed to load site config", http.StatusInternalServerError)
		return
	}

	wk.AuthorName = name

	if err := site.SaveWellKnown(s.DataDir, wk); err != nil {
		s.LogError("failed to save .well-known/polis: %v", err)
		http.Error(w, "Failed to save site config", http.StatusInternalServerError)
		return
	}

	s.LogEvent("pub.polis.site.author_name_update", map[string]interface{}{
		"author_name": name,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"author_name": name,
	})
}

// handleContent handles GET /api/content/{path} for browser mode navigation
// handleContentRedirect redirects content source paths (.html) to their current
// mount paths. For example, /content/pub.polis.core/post/20260302/slug.html
// redirects to /posts/20260302/slug.html. Non-.html requests (like .md) are
// not handled here and fall through to 404 (the API serves markdown via /api/content/).
func (s *Server) handleContentRedirect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clean := strings.TrimPrefix(r.URL.Path, "/")
	if !strings.HasSuffix(clean, ".html") {
		http.NotFound(w, r)
		return
	}

	b := s.loadOrDefaultBundle()
	if mounted := b.SourceToMountPath(clean); mounted != clean {
		http.Redirect(w, r, "/"+mounted, http.StatusMovedPermanently)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract path from URL: /api/content/{path}
	contentPath := strings.TrimPrefix(r.URL.Path, "/api/content/")
	if contentPath == "" {
		http.Error(w, "Path required", http.StatusBadRequest)
		return
	}

	// Validate path to prevent directory traversal
	if err := validateContentPath(contentPath); err != nil {
		s.LogEvent("pub.polis.security.path_traversal", map[string]interface{}{
			"path": contentPath, "request_id": RequestIDFromContext(r.Context()),
		})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if this is an HTML file request
	if strings.HasSuffix(contentPath, ".html") {
		s.handleHTMLContent(w, contentPath)
		return
	}

	// Read the content file (markdown)
	fullPath := filepath.Join(s.DataDir, contentPath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "Content not found", http.StatusNotFound)
		return
	}

	// Determine content type and editability
	contentType := "page"
	editable := true // Default: own content is editable
	rawMarkdown := string(content)
	markdown := rawMarkdown // Start with raw, strip frontmatter for rendering

	if strings.HasPrefix(contentPath, "content/pub.polis.core/post/") {
		contentType = "post"
		editable = true // Own posts are editable
		// Strip frontmatter for rendering
		markdown = publish.StripFrontmatter(rawMarkdown)
	} else if strings.HasPrefix(contentPath, "content/pub.polis.core/comment/") {
		contentType = "blessed_comment"
		editable = false // Blessed comments from others are not editable
	} else if strings.HasPrefix(contentPath, ".polis/content/pub.polis.core/posts/drafts/") {
		contentType = "draft"
		editable = true // Own drafts are editable
	} else if strings.HasSuffix(contentPath, ".md") && !strings.Contains(contentPath, "/") {
		// Root-level markdown files (index.md, about.md, etc.)
		contentType = "page"
		editable = true // Own pages are editable
		// Strip frontmatter for rendering if present
		if publish.HasFrontmatter(rawMarkdown) {
			markdown = publish.StripFrontmatter(rawMarkdown)
		}
	}

	// Render markdown to HTML (without frontmatter)
	html, err := render.MarkdownToHTML(markdown)
	if err != nil {
		s.LogError("failed to render markdown: %v", err)
		http.Error(w, "Failed to render markdown", http.StatusInternalServerError)
		return
	}

	// Parse frontmatter for metadata
	frontmatter := publish.ParseFrontmatter(rawMarkdown)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"path":     contentPath,
		"markdown": rawMarkdown, // Return full content including frontmatter
		"html":     html,
		"editable": editable,
		"type":     contentType,
		"metadata": frontmatter,
	})
}

// handleHTMLContent serves pre-rendered HTML files for browser mode
func (s *Server) handleHTMLContent(w http.ResponseWriter, contentPath string) {
	// First check if the HTML file exists (to validate the path)
	fullPath := filepath.Join(s.DataDir, contentPath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		http.Error(w, "Content not found", http.StatusNotFound)
		return
	}

	// Try to find the corresponding .md source file
	mdPath := strings.TrimSuffix(contentPath, ".html") + ".md"
	fullMdPath := filepath.Join(s.DataDir, mdPath)
	markdown := ""
	editable := false
	var metadata map[string]string
	var html string

	mdContent, err := os.ReadFile(fullMdPath)
	if err == nil {
		// Found the source markdown - render it fresh for consistent preview styling
		markdown = string(mdContent)
		editable = true // Can edit if we have the source
		metadata = publish.ParseFrontmatter(markdown)
		// Strip frontmatter for HTML rendering only
		markdownForRender := markdown
		if publish.HasFrontmatter(markdown) {
			markdownForRender = publish.StripFrontmatter(markdown)
		}
		// Render markdown to HTML (same as editor preview)
		renderedHTML, renderErr := render.MarkdownToHTML(markdownForRender)
		if renderErr == nil {
			html = renderedHTML
		}
	}

	// If we couldn't render from markdown, fall back to the pre-rendered HTML
	if html == "" {
		htmlContent, err := os.ReadFile(fullPath)
		if err != nil {
			http.Error(w, "Content not found", http.StatusNotFound)
			return
		}
		html = string(htmlContent)
	}

	// Determine content type
	contentType := "page"
	if strings.HasPrefix(contentPath, "content/pub.polis.core/post/") {
		contentType = "post"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"path":       contentPath,
		"markdown":   markdown,
		"html":       html,
		"editable":   editable,
		"type":       contentType,
		"metadata":   metadata,
		"source_md":  mdPath,
		"has_source": markdown != "",
	})
}

// handleSiteRegistrationStatus returns the site's registration status with the discovery service.
func (s *Server) handleSiteRegistrationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if discovery service is configured
	if s.DiscoveryURL == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"configured": false,
			"error":      "Discovery service not configured",
		})
		return
	}

	// Extract domain from POLIS_BASE_URL
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"configured": true,
			"error":      "POLIS_BASE_URL not set",
		})
		return
	}

	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"configured": true,
			"error":      "Could not extract domain from POLIS_BASE_URL",
		})
		return
	}

	// Query discovery service for registration status
	client := s.NewDSClient(r)
	result, err := client.CheckSiteRegistration(domain)
	if err != nil {
		s.LogWarn("Failed to check registration status: %v", err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "ds_signature") {
			errMsg = "DS response signature verification failed — DS may need redeployment"
		} else if strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "no such host") {
			errMsg = "Discovery service unreachable"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"configured": true,
			"domain":     domain,
			"error":      errMsg,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"configured":    true,
		"domain":        domain,
		"is_registered": result.IsRegistered,
		"created_at":   result.CreatedAt,
		"registry_url": result.RegistryURL,
	})
}

// handleSiteRegister registers the site with the discovery service.
func (s *Server) handleSiteRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate discovery service is configured
	if s.DiscoveryURL == "" {
		http.Error(w, "Discovery service not configured", http.StatusBadRequest)
		return
	}

	// Validate private key is available
	if s.PrivateKey == nil {
		http.Error(w, "Private key not available", http.StatusBadRequest)
		return
	}

	// Extract domain from POLIS_BASE_URL
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		http.Error(w, "POLIS_BASE_URL not set", http.StatusBadRequest)
		return
	}

	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		http.Error(w, "Could not extract domain from POLIS_BASE_URL", http.StatusBadRequest)
		return
	}

	// Get author_name from .well-known/polis (email is private, not sent to DS)
	var authorName string
	if wk, err := site.LoadWellKnown(s.DataDir); err == nil {
		authorName = wk.Author
	}

	// Register with discovery service (email omitted — private by default)
	client := s.NewDSClient(r)
	result, err := client.RegisterSite(domain, s.PrivateKey, "", authorName)
	if err != nil {
		s.LogError("Failed to register site: %v", err)
		http.Error(w, "Registration failed", http.StatusInternalServerError)
		return
	}

	// Write local registration marker
	if err := discovery.WriteRegistrationMarker(s.DataDir, s.DiscoveryURL, domain); err != nil {
		s.LogWarn("Failed to write registration marker: %v", err)
	}

	s.LogEvent("pub.polis.site.register", map[string]interface{}{
		"domain": domain,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       result.Success,
		"domain":        domain,
		"created_at":   result.CreatedAt,
		"registry_url": result.RegistryURL,
	})
}

// handleSiteUnregister unregisters the site from the discovery service.
func (s *Server) handleSiteUnregister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate discovery service is configured
	if s.DiscoveryURL == "" {
		http.Error(w, "Discovery service not configured", http.StatusBadRequest)
		return
	}

	// Validate private key is available
	if s.PrivateKey == nil {
		http.Error(w, "Private key not available", http.StatusBadRequest)
		return
	}

	// Extract domain from POLIS_BASE_URL
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		http.Error(w, "POLIS_BASE_URL not set", http.StatusBadRequest)
		return
	}

	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		http.Error(w, "Could not extract domain from POLIS_BASE_URL", http.StatusBadRequest)
		return
	}

	// Block deregistration for hosted polis.pub domains
	if strings.HasSuffix(domain, ".polis.pub") {
		http.Error(w, "Cannot unregister hosted polis.pub sites", http.StatusForbidden)
		return
	}

	// Unregister from discovery service
	client := s.NewDSClient(r)
	result, err := client.UnregisterSite(domain, s.PrivateKey)
	if err != nil {
		s.LogError("Failed to unregister site: %v", err)
		http.Error(w, "Unregistration failed", http.StatusInternalServerError)
		return
	}

	// Remove local registration marker
	if err := discovery.RemoveRegistrationMarker(s.DataDir, s.DiscoveryURL); err != nil {
		s.LogWarn("Failed to remove registration marker: %v", err)
	}

	s.LogEvent("pub.polis.site.unregister", map[string]interface{}{
		"domain": domain,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": result.Success,
		"domain":  domain,
		"message": result.Message,
	})
}

// handleDeployCheck checks if the site is publicly accessible at its POLIS_BASE_URL.
func (s *Server) handleDeployCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	baseURL := s.GetBaseURL()
	if baseURL == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"deployed": false,
			"error":    "POLIS_BASE_URL not set",
		})
		return
	}

	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"deployed": false,
			"domain":   "",
			"error":    "Could not extract domain from POLIS_BASE_URL",
		})
		return
	}

	// Try to fetch .well-known/polis from the live domain
	checkURL := fmt.Sprintf("https://%s/.well-known/polis", domain)
	hc := s.SharedHTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := hc.Get(checkURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"deployed": false,
			"domain":   domain,
		})
		return
	}
	defer resp.Body.Close()

	deployed := resp.StatusCode == http.StatusOK

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"deployed": deployed,
		"domain":   domain,
	})
}

// handleSetupWizardDismiss marks the setup wizard as dismissed in config.
func (s *Server) handleSetupWizardDismiss(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.Config == nil {
		s.Config = &Config{}
	}
	s.Config.SetupWizardDismissed = true
	if err := s.SaveConfig(); err != nil {
		s.LogError("Failed to save config after dismissing setup wizard: %v", err)
		http.Error(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// About page handler

// defaultAboutContent is the fallback text for sites without snippets/about.md.
const defaultAboutContent = "Welcome to my polis space. This site runs on *polis*\u2014signed markdown on your own domain. No platform, no middleman, just you and your words.\n"

// handleAbout handles GET/POST /api/about for the about page editor.
func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		aboutPath := filepath.Join(s.DataDir, "site", "snippets", "about.md")
		data, err := os.ReadFile(aboutPath)
		if err != nil {
			// File doesn't exist — return default content
			defaultHTML, _ := render.MarkdownToHTML(defaultAboutContent)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"content":      defaultAboutContent,
				"content_html": defaultHTML,
				"has_custom":   false,
			})
			return
		}
		contentHTML, _ := render.MarkdownToHTML(string(data))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"content":      string(data),
			"content_html": contentHTML,
			"has_custom":   true,
		})

	case http.MethodPost:
		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			if isBodyTooLargeError(err) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if len(req.Content) > MaxSnippetBodySize {
			http.Error(w, "About content too large (max 64KB)", http.StatusRequestEntityTooLarge)
			return
		}

		// Ensure snippets directory exists
		snippetsDir := filepath.Join(s.DataDir, "site", "snippets")
		if err := os.MkdirAll(snippetsDir, 0755); err != nil {
			s.LogError("failed to create snippets dir: %v", err)
			http.Error(w, "Failed to create snippets directory", http.StatusInternalServerError)
			return
		}

		aboutPath := filepath.Join(snippetsDir, "about.md")
		if err := os.WriteFile(aboutPath, []byte(req.Content), 0644); err != nil {
			s.LogError("failed to write about.md: %v", err)
			http.Error(w, "Failed to save about content", http.StatusInternalServerError)
			return
		}

		// Re-render all pages so the about section updates
		if err := s.RenderSite(); err != nil {
			s.LogWarn("about: render failed: %v", err)
		}

		s.LogEvent("pub.polis.site.about_update", nil)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// Snippets API handlers

// handleSnippets handles GET /api/snippets?path={subdir} and POST /api/snippets
func (s *Server) handleSnippets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// List snippets at path
		path := r.URL.Query().Get("path")
		filter := r.URL.Query().Get("filter") // "all", "global", or "theme"

		// List snippets from both local site/themes/ and CLI themes (fallback)
		tree, err := snippet.ListSnippets(s.DataDir, s.CLIThemesDir, "", path, filter)
		if err != nil {
			s.LogError("failed to list snippets: %v", err)
			http.Error(w, "Failed to list snippets", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tree)

	case http.MethodPost:
		// Create new global snippet
		var req struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			if isBodyTooLargeError(err) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if len(req.Content) > MaxSnippetBodySize {
			http.Error(w, "Snippet content too large (max 64KB)", http.StatusRequestEntityTooLarge)
			return
		}

		if req.Path == "" {
			http.Error(w, "path is required", http.StatusBadRequest)
			return
		}

		if err := snippet.CreateSnippet(s.DataDir, req.Path, req.Content); err != nil {
			s.LogError("failed to create snippet: %v", err)
			http.Error(w, "Failed to create snippet", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"path":    req.Path,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSnippet handles GET/PUT/DELETE /api/snippets/{path}
func (s *Server) handleSnippet(w http.ResponseWriter, r *http.Request) {
	// Extract path from URL: /api/snippets/{path}
	snippetPath := strings.TrimPrefix(r.URL.Path, "/api/snippets/")
	if snippetPath == "" {
		http.Error(w, "Snippet path required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get snippet content
		source := r.URL.Query().Get("source")
		if source == "" {
			source = "global" // Default to global
		}

		// Read from local site/themes/ or CLI themes (fallback)
		content, err := snippet.ReadSnippet(s.DataDir, s.CLIThemesDir, "", snippetPath, source)
		if err != nil {
			// Try the other source if not found
			if source == "global" {
				content, err = snippet.ReadSnippet(s.DataDir, s.CLIThemesDir, "", snippetPath, "theme")
			}
			if err != nil {
				http.Error(w, "Snippet not found", http.StatusNotFound)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(content)

	case http.MethodPut:
		// Update snippet
		var req struct {
			Content string `json:"content"`
			Source  string `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			if isBodyTooLargeError(err) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if len(req.Content) > MaxSnippetBodySize {
			http.Error(w, "Snippet content too large (max 64KB)", http.StatusRequestEntityTooLarge)
			return
		}

		if req.Source == "" {
			req.Source = "global" // Default to global
		}

		// Write to local site/themes/ or CLI themes (fallback)
		if err := snippet.WriteSnippet(s.DataDir, s.CLIThemesDir, "", snippetPath, req.Content, req.Source); err != nil {
			s.LogError("failed to save snippet: %v", err)
			http.Error(w, "Failed to save snippet", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"path":    snippetPath,
			"source":  req.Source,
		})

	case http.MethodDelete:
		// Delete snippet (global only)
		if err := snippet.DeleteSnippet(s.DataDir, snippetPath); err != nil {
			s.LogError("failed to delete snippet: %v", err)
			http.Error(w, "Failed to delete snippet", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"path":    snippetPath,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleRenderPage handles POST /api/render-page to re-render pages using Go packages.
// This is used for snippet editing workflow - after saving a snippet, re-render
// the current page to see the changes.
func (s *Server) handleRenderPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Get site base URL from POLIS_BASE_URL env var (matches bash CLI behavior)
	baseURL := s.GetBaseURL()

	// Load bundle for source/mount path resolution
	coreBundle := s.loadOrDefaultBundle()
	postsSource, _ := coreBundle.ContentDir("pub.polis.post")
	postsMountDir, _ := coreBundle.MountDir("pub.polis.post")
	commentsSource, _ := coreBundle.ContentDir("pub.polis.comment")
	commentsMountDir, _ := coreBundle.MountDir("pub.polis.comment")

	// Create page renderer using Go packages
	renderer, err := render.NewPageRenderer(render.PageConfig{
		DataDir:           s.DataDir,
		CLIThemesDir:      s.CLIThemesDir,
		BaseURL:           baseURL,
		RenderMarkers:     true, // Enable snippet markers for editing
		PostsSourceDir:    postsSource,
		PostsMountDir:     postsMountDir,
		CommentsSourceDir: commentsSource,
		CommentsMountDir:  commentsMountDir,
	})
	if err != nil {
		s.LogError("Render page: failed to create renderer: %v", err)
		http.Error(w, "Failed to create renderer", http.StatusInternalServerError)
		return
	}

	// Render all pages with force=true to ensure snippets are updated
	stats, err := renderer.RenderAll(true)
	if err != nil {
		s.LogError("Render page: render failed: %v", err)
		http.Error(w, "Render failed", http.StatusInternalServerError)
		return
	}

	s.LogEvent("pub.polis.site.render_full", map[string]interface{}{
		"posts":    stats.PostsRendered,
		"comments": stats.CommentsRendered,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"path":              req.Path,
		"posts_rendered":    stats.PostsRendered,
		"comments_rendered": stats.CommentsRendered,
	})
}

// ============================================================================
// Social handlers (following, feed, remote post)
// ============================================================================

// handleFollowing manages the following list.
// GET: returns the list of followed authors.
// POST: follows a new author (with blessing side-effect).
// DELETE: unfollows an author (with denial side-effect).
func (s *Server) handleFollowing(w http.ResponseWriter, r *http.Request) {
	followingPath := following.DefaultPath(s.DataDir)

	switch r.Method {
	case http.MethodGet:
		f, err := following.Load(followingPath)
		if err != nil {
			s.LogError("following load failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Backfill metadata for entries missing site_title/author_name (cap 3 per request)
		missing := f.EntriesMissingMetadata()
		if len(missing) > 3 {
			missing = missing[:3]
		}
		if len(missing) > 0 {
			rc := s.NewRemoteClient()
			dirty := false
			for _, m := range missing {
				wk, err := rc.FetchWellKnown(m.URL)
				if err != nil {
					s.LogDebug("following backfill: failed to fetch %s: %v", m.URL, err)
					continue
				}
				if f.UpdateMetadata(m.URL, wk.SiteTitle, wk.Author) {
					dirty = true
				}
			}
			if dirty {
				if err := following.Save(followingPath, f); err != nil {
					s.LogError("following backfill: save failed: %v", err)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"following": f.All(),
			"count":     f.Count(),
		})

	case http.MethodPost:
		if s.PrivateKey == nil {
			http.Error(w, "Not configured: no private key", http.StatusBadRequest)
			return
		}

		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Normalize domain to lowercase for consistent storage and display
		req.URL = strings.ToLower(req.URL)

		if len(req.URL) < 8 || req.URL[:8] != "https://" {
			http.Error(w, "Author URL must use HTTPS", http.StatusBadRequest)
			return
		}

		// Prevent self-follow
		ownDomain := discovery.ExtractDomainFromURL(s.GetBaseURL())
		targetDomain := discovery.ExtractDomainFromURL(req.URL)
		if ownDomain != "" && targetDomain != "" && ownDomain == targetDomain {
			http.Error(w, "Cannot follow your own site", http.StatusBadRequest)
			return
		}

		followDomain := ownDomain
		discoveryClient := s.NewAuthDSClient(r, followDomain)
		remoteClient := s.NewRemoteClient()

		privPath, pubPath := policy.DefaultPaths(s.DataDir)
		policies, _ := policy.LoadPolicies(privPath, pubPath)

		result, err := following.FollowWithBlessing(followingPath, req.URL, discoveryClient, remoteClient, s.PrivateKey, policies)
		if err != nil {
			s.LogError("follow failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.LogEvent("pub.polis.follow.add", map[string]interface{}{
			"url":              req.URL,
			"comments_blessed": result.CommentsBlessed,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    result,
		})

		// Backfill historical posts from the new author, then sync feed
		// for any recent events across all followed authors.
		go func() {
			newDomain := discovery.ExtractDomainFromURL(req.URL)
			if newDomain != "" {
				s.backfillFeedForAuthor(newDomain)
			}
			s.syncFeed()
		}()

	case http.MethodDelete:
		if s.PrivateKey == nil {
			http.Error(w, "Not configured: no private key", http.StatusBadRequest)
			return
		}

		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if len(req.URL) < 8 || req.URL[:8] != "https://" {
			http.Error(w, "Author URL must use HTTPS", http.StatusBadRequest)
			return
		}

		unfollowDomain := discovery.ExtractDomainFromURL(s.GetBaseURL())
		discoveryClient := s.NewAuthDSClient(r, unfollowDomain)
		remoteClient := s.NewRemoteClient()

		privPath2, pubPath2 := policy.DefaultPaths(s.DataDir)
		unfollowPolicies, _ := policy.LoadPolicies(privPath2, pubPath2)

		result, err := following.UnfollowWithDenial(followingPath, req.URL, discoveryClient, remoteClient, s.PrivateKey, unfollowPolicies)
		if err != nil {
			s.LogError("unfollow failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.LogEvent("pub.polis.follow.remove", map[string]interface{}{
			"url":             req.URL,
			"comments_denied": result.CommentsDenied,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    result,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleFeed returns cached feed items (instant, no network).
// GET /api/feed?type=post|comment&status=read|unread
func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	scope := r.URL.Query().Get("scope")
	if scope != "" && !validFeedScopes[scope] {
		http.Error(w, "Invalid scope", http.StatusBadRequest)
		return
	}
	cm := s.feedCacheForScope(scope)
	typeFilter := r.URL.Query().Get("type")
	statusFilter := r.URL.Query().Get("status")

	items, err := cm.ListFiltered(feed.FilterOptions{
		Type:   typeFilter,
		Status: statusFilter,
	})
	if err != nil {
		s.LogError("feed list failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	unread := 0
	for _, item := range items {
		if item.ReadAt == "" {
			unread++
		}
	}

	stale, _ := cm.IsStale()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"items":        items,
		"total":        len(items),
		"unread":       unread,
		"stale":        stale,
		"last_refresh": cm.LastUpdated(),
	})
}

// handleFeedRefresh triggers a stream-based feed sync and returns the updated cache.
// POST /api/feed/refresh
func (s *Server) handleFeedRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	scope := r.URL.Query().Get("scope")
	if scope != "" && !validFeedScopes[scope] {
		http.Error(w, "Invalid scope", http.StatusBadRequest)
		return
	}

	// Count items before sync to determine how many are actually new
	cm := s.feedCacheForScope(scope)
	beforeItems, _ := cm.List()
	beforeCount := len(beforeItems)

	// Trigger stream-based sync (feed)
	if scope == "" || scope == "network" {
		s.syncFeed()
	} else {
		s.syncFeedScoped(scope)
	}

	items, _ := cm.List()
	unread := 0
	for _, item := range items {
		if item.ReadAt == "" {
			unread++
		}
	}

	newItems := len(items) - beforeCount
	if newItems < 0 {
		newItems = 0
	}

	s.LogEvent("pub.polis.feed.refresh", map[string]interface{}{
		"new_items": newItems,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"items":        items,
		"total":        len(items),
		"unread":       unread,
		"new_items":    newItems,
		"stale":        false,
		"last_refresh": cm.LastUpdated(),
	})
}

// handleFeedRead marks a feed item as read (viewport-based auto-marking).
// POST /api/feed/read
// Body: {"id":"x"}
func (s *Server) handleFeedRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		http.Error(w, "Missing id", http.StatusBadRequest)
		return
	}

	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)

	if err := cm.MarkRead(req.ID); err != nil {
		// Item not found is OK — may have been pruned
		if err.Error() != "item not found: "+req.ID {
			s.LogError("feed read failed: %v", err)
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// handleFeedCounts returns lightweight feed counts for sidebar badge.
// GET /api/feed/counts
func (s *Server) handleFeedCounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)

	items, err := cm.List()
	if err != nil {
		s.LogError("feed counts failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	unread := 0
	newestCachedAt := ""
	for _, item := range items {
		if item.ReadAt == "" {
			unread++
		}
		if item.CachedAt > newestCachedAt {
			newestCachedAt = item.CachedAt
		}
	}

	stale, _ := cm.IsStale()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":            len(items),
		"unread":           unread,
		"stale":            stale,
		"newest_cached_at": newestCachedAt,
	})
}

// handleFeedVisited records the timestamp of the user's last feed page visit.
// POST /api/feed/visited  Body: {"at":"2026-03-28T01:00:00Z"}
func (s *Server) handleFeedVisited(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		At string `json:"at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.At == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	s.Config.FeedLastVisit = req.At
	if err := s.SaveConfig(); err != nil {
		s.LogError("failed to save feed visit: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// handleFeedGrouped returns feed items grouped by post URL.
// Comments are grouped with their target post; posts without comments appear as solo groups.
// GET /api/feed/grouped
func (s *Server) handleFeedGrouped(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	scope := r.URL.Query().Get("scope")
	if scope != "" && !validFeedScopes[scope] {
		http.Error(w, "Invalid scope", http.StatusBadRequest)
		return
	}
	cm := s.feedCacheForScope(scope)

	items, err := cm.List()
	if err != nil {
		s.LogError("feed grouped failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build followed domains set
	followingPath := following.DefaultPath(s.DataDir)
	f, _ := following.Load(followingPath)
	followedDomains := make(map[string]bool)
	if f != nil {
		for _, entry := range f.Following {
			// Extract domain from URL (e.g. "https://alice.polis.pub" -> "alice.polis.pub")
			domain := strings.TrimPrefix(entry.URL, "https://")
			domain = strings.TrimPrefix(domain, "http://")
			domain = strings.TrimSuffix(domain, "/")
			followedDomains[domain] = true
		}
	}

	// Group items by post URL
	type commentDetail struct {
		URL          string `json:"url"`
		AuthorDomain string `json:"author_domain"`
		Title        string `json:"title,omitempty"`
		Excerpt      string `json:"excerpt,omitempty"`
		Published    string `json:"published"`
		Unread       bool   `json:"unread"`
	}
	type feedGroup struct {
		PostURL          string           `json:"post_url"`
		PostTitle        string           `json:"post_title"`
		PostDomain       string           `json:"post_domain"`
		PostPublished    string           `json:"post_published"`
		PostExcerpt      string           `json:"post_excerpt,omitempty"`
		HasPost          bool             `json:"has_post"`
		TotalComments    int              `json:"total_comments"`
		NetworkComments  int              `json:"network_comments"`
		ExternalComments int              `json:"external_comments"`
		UnreadComments   int              `json:"unread_comments"`
		LastActivity     string           `json:"last_activity"`
		PostUnread       bool             `json:"post_unread"`
		ItemIDs          []string         `json:"item_ids"`
		Comments         []*commentDetail `json:"comments,omitempty"`
	}

	type announcementItem struct {
		ID           string `json:"id"`
		EventType    string `json:"event_type"`
		AuthorDomain string `json:"author_domain"`
		TargetDomain string `json:"target_domain,omitempty"`
		Title        string `json:"title,omitempty"`
		URL          string `json:"url,omitempty"`
		Published    string `json:"published"`
		Unread       bool   `json:"unread"`
	}

	groups := make(map[string]*feedGroup)
	groupOrder := []string{} // track insertion order for stable iteration
	var announcements []announcementItem

	totalUnread := 0
	for _, item := range items {
		if item.ReadAt == "" {
			totalUnread++
		}

		if item.Type == "announcement" {
			announcements = append(announcements, announcementItem{
				ID:           item.ID,
				EventType:    item.EventType,
				AuthorDomain: item.AuthorDomain,
				TargetDomain: item.TargetDomain,
				Title:        item.Title,
				URL:          item.URL,
				Published:    item.Published,
				Unread:       item.ReadAt == "",
			})
			continue
		} else if item.Type == "post" {
			key := item.URL
			g, exists := groups[key]
			if !exists {
				g = &feedGroup{
					PostURL:       item.URL,
					PostTitle:     item.Title,
					PostDomain:    item.AuthorDomain,
					PostPublished: item.Published,
					LastActivity:  item.Published,
					ItemIDs:       []string{},
				}
				groups[key] = g
				groupOrder = append(groupOrder, key)
			}
			g.HasPost = true
			g.PostUnread = item.ReadAt == ""
			if item.Title != "" {
				g.PostTitle = item.Title
			}
			if item.AuthorDomain != "" {
				g.PostDomain = item.AuthorDomain
			}
			if item.Published != "" {
				g.PostPublished = item.Published
			}
			if item.Excerpt != "" {
				g.PostExcerpt = item.Excerpt
			}
			g.ItemIDs = append(g.ItemIDs, item.ID)
			if feed.PublishedBefore(g.LastActivity, item.Published) {
				g.LastActivity = item.Published
			}
		} else if item.Type == "comment" {
			key := item.TargetURL
			if key == "" {
				// Orphan comment (no target URL) — use its own URL as key
				key = item.URL
			}
			g, exists := groups[key]
			if !exists {
				g = &feedGroup{
					PostURL:      key,
					PostDomain:   item.TargetDomain,
					LastActivity: item.Published,
					ItemIDs:      []string{},
				}
				groups[key] = g
				groupOrder = append(groupOrder, key)
			}
			g.TotalComments++
			if followedDomains[item.AuthorDomain] {
				g.NetworkComments++
			} else {
				g.ExternalComments++
			}
			isUnread := item.ReadAt == ""
			if isUnread {
				g.UnreadComments++
			}
			g.Comments = append(g.Comments, &commentDetail{
				URL:          item.URL,
				AuthorDomain: item.AuthorDomain,
				Title:        item.Title,
				Excerpt:      item.Excerpt,
				Published:    item.Published,
				Unread:       isUnread,
			})
			g.ItemIDs = append(g.ItemIDs, item.ID)
			if feed.PublishedBefore(g.LastActivity, item.Published) {
				g.LastActivity = item.Published
			}
		}
	}

	// Build sorted slice
	result := make([]*feedGroup, 0, len(groups))
	for _, key := range groupOrder {
		result = append(result, groups[key])
	}

	// Sort by last_activity descending (parse timestamps to handle JS Date format)
	sort.Slice(result, func(i, j int) bool {
		return feed.PublishedBefore(result[j].LastActivity, result[i].LastActivity)
	})

	stale, _ := cm.IsStale()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"groups":        result,
		"announcements": announcements,
		"total_items":   len(items),
		"unread_items":  totalUnread,
		"stale":         stale,
	})

	// Background: fetch excerpts for items that don't have one yet
	var needExcerpts []int
	for i, item := range items {
		if item.Excerpt == "" && (item.Type == "post" || item.Type == "comment") && strings.HasPrefix(item.URL, "https://") {
			needExcerpts = append(needExcerpts, i)
		}
	}
	if len(needExcerpts) > 0 {
		go s.fetchFeedExcerpts(cm, items, needExcerpts)
	}
}

// fetchFeedExcerpts fetches remote post content and caches excerpts.
// Runs in background goroutine; errors are logged, never fatal.
func (s *Server) fetchFeedExcerpts(cm *feed.CacheManager, items []feed.CachedFeedItem, indices []int) {
	client := s.NewRemoteClient()
	updated := false

	for _, idx := range indices {
		item := &items[idx]
		content, err := client.FetchContent(item.URL)
		if err != nil {
			// Comment URLs use /comments/ for rendered HTML but the markdown
			// source lives at /content/pub.polis.core/comment/. Try that path.
			if strings.Contains(item.URL, "/comments/") {
				altURL := strings.Replace(item.URL, "/comments/", "/content/pub.polis.core/comment/", 1)
				content, err = client.FetchContent(altURL)
			}
			if err != nil {
				s.LogDebug("excerpt fetch failed for %s: %v", item.URL, err)
				continue
			}
		}
		// If HTML, try markdown source
		if looksLikeHTML(content) {
			altContent, _, altErr := client.TryAlternateExtension(item.URL)
			if altErr == nil && !looksLikeHTML(altContent) {
				content = altContent
			} else {
				continue // Can't extract excerpt from HTML
			}
		}
		body := stripFrontmatter(content)
		excerpt := makeExcerpt(body, 140)
		if excerpt != "" {
			item.Excerpt = excerpt
			updated = true
		}
	}

	if updated {
		if err := cm.SaveItems(items); err != nil {
			s.LogError("failed to save feed excerpts: %v", err)
		} else {
			s.LogDebug("cached %d feed excerpts", len(indices))
		}
	}
}

// handleRemoteAvatar fetches avatar and author_name from a remote site's .well-known/polis.
// GET /api/remote/avatar?domain=discover.polis.pub
func (s *Server) handleRemoteAvatar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	domain := r.URL.Query().Get("domain")
	if domain == "" {
		http.Error(w, "Missing 'domain' parameter", http.StatusBadRequest)
		return
	}

	// Validate domain: no slashes, no colons, basic sanity
	if strings.ContainsAny(domain, "/\\:@?#") || len(domain) > 253 {
		http.Error(w, "Invalid domain", http.StatusBadRequest)
		return
	}

	client := s.NewRemoteClient()
	client.HTTPClient.Timeout = 5 * time.Second

	wk, err := client.FetchWellKnown("https://" + domain)
	if err != nil {
		// Return empty result rather than error — no avatar is normal
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"domain": domain,
		})
		return
	}

	resp := map[string]interface{}{
		"domain": domain,
	}
	if wk.Avatar != nil {
		resp["avatar"] = wk.Avatar
	}
	if wk.AuthorName != "" {
		resp["author_name"] = wk.AuthorName
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleRemotePost fetches a remote post and returns it as rendered HTML.
// GET /api/remote/post?url=https://example.com/posts/hello.md
func (s *Server) handleRemotePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	postURL := r.URL.Query().Get("url")
	if postURL == "" {
		http.Error(w, "Missing 'url' parameter", http.StatusBadRequest)
		return
	}

	if len(postURL) < 8 || postURL[:8] != "https://" {
		http.Error(w, "URL must use HTTPS", http.StatusBadRequest)
		return
	}

	client := s.NewRemoteClient()

	// Try fetching the URL as-is first
	content, err := client.FetchContent(postURL)
	fetchedURL := postURL

	if err != nil {
		s.LogError("remote post fetch failed: %v", err)
		http.Error(w, "Failed to fetch remote post: "+err.Error(), http.StatusBadGateway)
		return
	}

	// If the response looks like HTML (not markdown), the host likely served
	// the rendered page instead of the raw source. Try the alternate extension.
	if looksLikeHTML(content) {
		altContent, altURL, altErr := client.TryAlternateExtension(postURL)
		if altErr == nil && !looksLikeHTML(altContent) {
			content = altContent
			fetchedURL = altURL
		}
		// If both extensions return HTML, use the original content as-is
	}

	var body, htmlContent string
	if looksLikeHTML(content) {
		// Content is already HTML — serve it directly (strip full page shell if present)
		htmlContent = extractHTMLBody(content)
		body = content
	} else {
		// Content is markdown — strip frontmatter and render
		body = stripFrontmatter(content)
		rendered, renderErr := render.MarkdownToHTML(body)
		if renderErr != nil {
			s.LogError("remote post render failed: %v", renderErr)
			http.Error(w, "Failed to render post", http.StatusInternalServerError)
			return
		}
		htmlContent = rendered
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"url":     fetchedURL,
		"content": htmlContent,
		"raw":     body,
	})
}

// makeExcerpt strips markdown syntax and truncates to maxLen at a word boundary.
func makeExcerpt(markdown string, maxLen int) string {
	// Remove headings
	lines := strings.Split(markdown, "\n")
	var clean []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		// Strip images, links, bold, italic, code
		s := trimmed
		// Remove images ![alt](url)
		for {
			idx := strings.Index(s, "![")
			if idx < 0 {
				break
			}
			end := strings.Index(s[idx:], ")")
			if end < 0 {
				break
			}
			s = s[:idx] + s[idx+end+1:]
		}
		// Remove links [text](url) -> text
		for {
			idx := strings.Index(s, "](")
			if idx < 0 {
				break
			}
			start := strings.LastIndex(s[:idx], "[")
			end := strings.Index(s[idx:], ")")
			if start < 0 || end < 0 {
				break
			}
			s = s[:start] + s[start+1:idx] + s[idx+end+1:]
		}
		s = strings.ReplaceAll(s, "**", "")
		s = strings.ReplaceAll(s, "__", "")
		s = strings.ReplaceAll(s, "*", "")
		s = strings.ReplaceAll(s, "_", "")
		s = strings.ReplaceAll(s, "`", "")
		s = strings.TrimSpace(s)
		if s != "" {
			clean = append(clean, s)
		}
	}
	text := strings.Join(clean, " ")
	if len(text) <= maxLen {
		return text
	}
	// Truncate at word boundary
	cut := strings.LastIndex(text[:maxLen], " ")
	if cut < maxLen/2 {
		cut = maxLen
	}
	return text[:cut] + "…"
}

// extractFrontmatterTitle extracts the title field from YAML frontmatter.
func extractFrontmatterTitle(content string) string {
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		return ""
	}
	idx := strings.Index(content, "---")
	if idx < 0 {
		return ""
	}
	end := strings.Index(content[idx+3:], "---")
	if end < 0 {
		return ""
	}
	fm := content[idx+3 : idx+3+end]
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "title:") {
			t := strings.TrimSpace(strings.TrimPrefix(line, "title:"))
			return strings.Trim(t, "\"'")
		}
	}
	return ""
}

// stripFrontmatter removes YAML frontmatter (---...---) from content.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}
	// Find the closing ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return content
	}
	// Return everything after the closing ---
	after := rest[idx+4:]
	return strings.TrimLeft(after, "\n")
}

// looksLikeHTML checks if content appears to be HTML rather than markdown.
func looksLikeHTML(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "<!DOCTYPE") ||
		strings.HasPrefix(trimmed, "<!doctype") ||
		strings.HasPrefix(trimmed, "<html") ||
		strings.HasPrefix(trimmed, "<HTML")
}

// extractHTMLBody extracts content between <body> and </body> tags,
// or between <main> and </main> tags, falling back to the full content.
// extractCommentRelPath extracts the relative content path (e.g. "content/pub.polis.core/comment/20260222/id.md")
// from a full comment URL. Returns empty string if the URL doesn't contain /comments/.
func extractCommentRelPath(commentURL string) string {
	idx := strings.Index(commentURL, "/comments/")
	if idx < 0 {
		return ""
	}
	// URL mount path is /comments/, map to content source path
	return "content/pub.polis.core/comment/" + commentURL[idx+len("/comments/"):]
}

// commentSourceURL converts a comment mount-path URL to the content source path URL.
// e.g. "https://alice.polis.pub/comments/20260222/id.md" -> "https://alice.polis.pub/content/pub.polis.core/comment/20260222/id.md"
// Returns the original URL if it doesn't contain /comments/.
func commentSourceURL(commentURL string) string {
	idx := strings.Index(commentURL, "/comments/")
	if idx < 0 {
		return commentURL
	}
	return commentURL[:idx] + "/content/pub.polis.core/comment/" + commentURL[idx+len("/comments/"):]
}

func extractHTMLBody(content string) string {
	lower := strings.ToLower(content)

	// Try <main>...</main> first (most specific)
	if mainStart := strings.Index(lower, "<main"); mainStart >= 0 {
		// Find end of opening tag
		tagEnd := strings.Index(content[mainStart:], ">")
		if tagEnd >= 0 {
			innerStart := mainStart + tagEnd + 1
			if mainEnd := strings.Index(lower[innerStart:], "</main>"); mainEnd >= 0 {
				return strings.TrimSpace(content[innerStart : innerStart+mainEnd])
			}
		}
	}

	// Try <body>...</body>
	if bodyStart := strings.Index(lower, "<body"); bodyStart >= 0 {
		tagEnd := strings.Index(content[bodyStart:], ">")
		if tagEnd >= 0 {
			innerStart := bodyStart + tagEnd + 1
			if bodyEnd := strings.Index(lower[innerStart:], "</body>"); bodyEnd >= 0 {
				return strings.TrimSpace(content[innerStart : innerStart+bodyEnd])
			}
		}
	}

	return content
}

// ConversationComment is a comment in a conversation thread.
type ConversationComment struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Published string `json:"published"`
	Unread    bool   `json:"unread"`
}

// CommentThread is a group of comments from a single author.
type CommentThread struct {
	AuthorDomain string                `json:"author_domain"`
	Comments     []ConversationComment `json:"comments"`
}

// BlessingActivityEntry is a blessing event for the conversations view.
type BlessingActivityEntry struct {
	Domain    string `json:"domain"`
	Status    string `json:"status"`
	TargetURL string `json:"target_url"`
	SourceURL string `json:"source_url"`
	UpdatedAt string `json:"updated_at"`
}

// ConversationsResponse is the JSON shape returned by GET /api/conversations.
type ConversationsResponse struct {
	CommentThreads []CommentThread `json:"comment_threads"`
	OnYourPosts    struct {
		PendingCount int                     `json:"pending_count"`
		BlessedCount int                     `json:"blessed_count"`
		Recent       []BlessingActivityEntry `json:"recent"`
	} `json:"on_your_posts"`
}

// handleConversations returns comment threads and blessing activity from local cache.
// GET /api/conversations
func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var resp ConversationsResponse

	// 1. Comment threads from feed cache (type=comment, last 30 days)
	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)
	items, err := cm.List()
	if err != nil {
		items = nil
	}

	cutoff30d := time.Now().AddDate(0, 0, -30)
	threadMap := make(map[string][]ConversationComment)
	for _, item := range items {
		if item.Type != "comment" {
			continue
		}
		pub, err := time.Parse(time.RFC3339, item.Published)
		if err != nil || pub.Before(cutoff30d) {
			continue
		}
		domain := item.AuthorDomain
		if domain == "" {
			continue
		}
		threadMap[domain] = append(threadMap[domain], ConversationComment{
			Title:     item.Title,
			URL:       item.URL,
			Published: item.Published,
			Unread:    item.ReadAt == "",
		})
	}

	// Sort threads by most recent comment, cap at 10 threads / 5 comments each
	type threadEntry struct {
		domain    string
		comments  []ConversationComment
		mostRecent time.Time
	}
	var threads []threadEntry
	for domain, comments := range threadMap {
		var mostRecent time.Time
		for _, c := range comments {
			if t, err := time.Parse(time.RFC3339, c.Published); err == nil && t.After(mostRecent) {
				mostRecent = t
			}
		}
		// Sort comments newest first
		sort.Slice(comments, func(i, j int) bool {
			return comments[i].Published > comments[j].Published
		})
		if len(comments) > 5 {
			comments = comments[:5]
		}
		threads = append(threads, threadEntry{domain, comments, mostRecent})
	}
	sort.Slice(threads, func(i, j int) bool {
		return threads[i].mostRecent.After(threads[j].mostRecent)
	})
	if len(threads) > 10 {
		threads = threads[:10]
	}

	resp.CommentThreads = make([]CommentThread, len(threads))
	for i, t := range threads {
		resp.CommentThreads[i] = CommentThread{
			AuthorDomain: t.domain,
			Comments:     t.comments,
		}
	}

	// 2. Blessing activity from cached state
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")
	var blessingState stream.BlessingState
	_ = store.LoadState("pub.polis.comment.blessing", &blessingState)

	pendingCount := 0
	blessedCount := 0
	for _, b := range blessingState.Blessings {
		switch b.Status {
		case "pending":
			pendingCount++
		case "granted":
			blessedCount++
		}
	}
	resp.OnYourPosts.PendingCount = pendingCount
	resp.OnYourPosts.BlessedCount = blessedCount

	// Recent blessing entries (up to 10, sorted by updated_at desc)
	blessings := make([]stream.BlessingEntry, len(blessingState.Blessings))
	copy(blessings, blessingState.Blessings)
	sort.Slice(blessings, func(i, j int) bool {
		return blessings[i].UpdatedAt > blessings[j].UpdatedAt
	})
	if len(blessings) > 10 {
		blessings = blessings[:10]
	}

	recentBlessings := make([]BlessingActivityEntry, len(blessings))
	for i, b := range blessings {
		recentBlessings[i] = BlessingActivityEntry{
			Domain:    b.Actor,
			Status:    b.Status,
			TargetURL: b.TargetURL,
			SourceURL: b.SourceURL,
			UpdatedAt: b.UpdatedAt,
		}
	}
	resp.OnYourPosts.Recent = recentBlessings
	if resp.OnYourPosts.Recent == nil {
		resp.OnYourPosts.Recent = []BlessingActivityEntry{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// PulseHighlight is a recent feed item for the pulse dashboard.
type PulseHighlight struct {
	Type         string `json:"type"`
	Title        string `json:"title"`
	AuthorDomain string `json:"author_domain"`
	Published    string `json:"published"`
	Unread       bool   `json:"unread"`
}

// PulseAuthor is an author activity summary for the pulse dashboard.
type PulseAuthor struct {
	Domain       string `json:"domain"`
	PostCount    int    `json:"post_count"`
	CommentCount int    `json:"comment_count"`
}

// PulseResponse is the JSON shape returned by GET /api/pulse.
type PulseResponse struct {
	Network struct {
		Following      int `json:"following"`
		Followers      int `json:"followers"`
		FeedUnread     int `json:"feed_unread"`
		IncomingPending int `json:"incoming_pending"`
	} `json:"network"`
	Recent     []PulseHighlight `json:"recent"`
	TopAuthors []PulseAuthor    `json:"top_authors"`
	Site       struct {
		Posts           int `json:"posts"`
		IncomingBlessed int `json:"incoming_blessed"`
		IncomingPending int `json:"incoming_pending"`
	} `json:"site"`
}

// handlePulse returns an aggregated community pulse dashboard.
// All data comes from local cached state — no DS queries.
// GET /api/pulse
func (s *Server) handlePulse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	counts := s.computeAllCounts()

	var resp PulseResponse
	resp.Network.Following = counts.Following
	resp.Network.Followers = counts.Followers
	resp.Network.FeedUnread = counts.FeedUnread
	resp.Network.IncomingPending = counts.IncomingPending
	resp.Site.Posts = counts.Posts
	resp.Site.IncomingBlessed = counts.IncomingBlessed
	resp.Site.IncomingPending = counts.IncomingPending

	// Recent highlights: top 5 feed items from last 7 days
	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)
	items, err := cm.List()
	if err != nil {
		items = nil
	}

	cutoff7d := time.Now().AddDate(0, 0, -7)
	var recent []PulseHighlight
	for _, item := range items {
		if len(recent) >= 5 {
			break
		}
		pub, err := time.Parse(time.RFC3339, item.Published)
		if err != nil {
			continue
		}
		if pub.Before(cutoff7d) {
			continue
		}
		recent = append(recent, PulseHighlight{
			Type:         item.Type,
			Title:        item.Title,
			AuthorDomain: item.AuthorDomain,
			Published:    item.Published,
			Unread:       item.ReadAt == "",
		})
	}
	resp.Recent = recent
	if resp.Recent == nil {
		resp.Recent = []PulseHighlight{}
	}

	// Most active authors: top 5 by activity in last 30 days
	cutoff30d := time.Now().AddDate(0, 0, -30)
	type authorStats struct {
		posts    int
		comments int
	}
	authorMap := make(map[string]*authorStats)
	for _, item := range items {
		pub, err := time.Parse(time.RFC3339, item.Published)
		if err != nil || pub.Before(cutoff30d) {
			continue
		}
		domain := item.AuthorDomain
		if domain == "" {
			continue
		}
		stats, ok := authorMap[domain]
		if !ok {
			stats = &authorStats{}
			authorMap[domain] = stats
		}
		if item.Type == "post" {
			stats.posts++
		} else if item.Type == "comment" {
			stats.comments++
		}
	}

	// Sort by total activity descending, take top 5
	type authorEntry struct {
		domain string
		total  int
		stats  *authorStats
	}
	var authorList []authorEntry
	for domain, stats := range authorMap {
		authorList = append(authorList, authorEntry{domain, stats.posts + stats.comments, stats})
	}
	sort.Slice(authorList, func(i, j int) bool {
		return authorList[i].total > authorList[j].total
	})
	var topAuthors []PulseAuthor
	for i, a := range authorList {
		if i >= 5 {
			break
		}
		topAuthors = append(topAuthors, PulseAuthor{
			Domain:       a.domain,
			PostCount:    a.stats.posts,
			CommentCount: a.stats.comments,
		})
	}
	resp.TopAuthors = topAuthors
	if resp.TopAuthors == nil {
		resp.TopAuthors = []PulseAuthor{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleFollowerCount returns the current follower count from cached state.
// The unified sync loop keeps polis.follow.json up to date, so this handler
// only reads from disk (no DS queries).
// GET /api/followers/count?refresh=false
func (s *Server) handleFollowerCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// If refresh requested, trigger an immediate sync
	if r.URL.Query().Get("refresh") == "true" {
		s.TriggerSync()
	}

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	var state stream.FollowerState
	_ = store.LoadState("pub.polis.follow", &state)

	followers := state.Followers
	if followers == nil {
		followers = []string{}
	}
	// Normalize follower domains to lowercase for consistent display
	for i, f := range followers {
		followers[i] = strings.ToLower(f)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"count":     state.Count,
		"followers": followers,
	})
}

// handleNotifications returns a paginated list of notifications.
// GET /api/notifications?offset=0&limit=20&include_read=false
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mgr := notification.NewManager(s.DataDir, s.GetDiscoveryDomain())

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	includeRead := r.URL.Query().Get("include_read") == "true"

	items, total, err := mgr.ListPaginated(offset, limit, includeRead)
	if err != nil {
		s.LogError("Failed to list notifications: %v", err)
		http.Error(w, "Failed to list notifications", http.StatusInternalServerError)
		return
	}

	if items == nil {
		items = []notification.StateEntry{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"notifications": items,
		"total":         total,
		"offset":        offset,
		"limit":         limit,
	})
}

// handleNotificationCount returns the unread notification count from cached state.
// The unified sync loop keeps polis.notification.jsonl up to date.
// GET /api/notifications/count
func (s *Server) handleNotificationCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mgr := notification.NewManager(s.DataDir, s.GetDiscoveryDomain())
	unread, err := mgr.CountUnread()
	if err != nil {
		s.LogError("Failed to count notifications: %v", err)
		http.Error(w, "Failed to count notifications", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"unread": unread,
	})
}

// handleNotificationRead marks notifications as read.
// POST /api/notifications/read  body: {"ids": [...]} or {"all": true}
func (s *Server) handleNotificationRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	mgr := notification.NewManager(s.DataDir, s.GetDiscoveryDomain())
	marked, err := mgr.MarkRead(req.IDs, req.All)
	if err != nil {
		s.LogError("Failed to mark notifications as read: %v", err)
		http.Error(w, "Failed to mark as read", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"marked":  marked,
	})
}

// ============================================================================
// WIDGET API HANDLERS
// ============================================================================

// POST /api/widget/publish — Sign and publish content via widget token.
// Generic endpoint dispatched by "type" field. Currently supports "comment".
func (s *Server) handleWidgetPublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Not configured", http.StatusBadRequest)
		return
	}

	var req struct {
		Type     string                 `json:"type"`
		Target   string                 `json:"target"`
		Text     string                 `json:"text"`
		Metadata map[string]interface{} `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if len(req.Text) > MaxCommentBodySize {
		http.Error(w, "Comment text too large (max 64KB)", http.StatusRequestEntityTooLarge)
		return
	}

	switch req.Type {
	case "comment":
		s.handleWidgetPublishComment(w, r, req.Target, req.Text)
	default:
		http.Error(w, "Unsupported content type", http.StatusBadRequest)
	}
}

// POST /api/widget/comment — Direct comment endpoint for the widget.
// Accepts {target, text} without requiring a type field.
func (s *Server) handleWidgetComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Not configured", http.StatusBadRequest)
		return
	}

	var req struct {
		Target string `json:"target"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if len(req.Text) > MaxCommentBodySize {
		http.Error(w, "Comment text too large (max 64KB)", http.StatusRequestEntityTooLarge)
		return
	}

	s.handleWidgetPublishComment(w, r, req.Target, req.Text)
}

// handleWidgetPublishComment handles the comment type for widget publish.
func (s *Server) handleWidgetPublishComment(w http.ResponseWriter, r *http.Request, target, text string) {
	if target == "" || text == "" {
		http.Error(w, "target and text are required", http.StatusBadRequest)
		return
	}

	authorDomain := s.GetAuthorDomain()
	if authorDomain == "" {
		http.Error(w, "Author identity not configured", http.StatusBadRequest)
		return
	}

	siteURL := s.GetBaseURL()
	if siteURL == "" {
		http.Error(w, "POLIS_BASE_URL not configured", http.StatusBadRequest)
		return
	}

	draft := &comment.CommentDraft{
		InReplyTo: polisurl.NormalizeToMD(target),
		Content:   text,
	}

	signed, err := comment.SignComment(s.DataDir, draft, authorDomain, siteURL, s.PrivateKey)
	if err != nil {
		s.LogError("widget publish comment: sign failed: %v", err)
		http.Error(w, "Failed to sign comment", http.StatusInternalServerError)
		return
	}

	// Attempt to send blessing request (beseech) — pass per-tenant config for hosted safety
	blessingStatus := "pending"
	result, err := comment.BeseechComment(s.DataDir, signed.Meta.ID, s.PrivateKey, &comment.DiscoveryConfig{
		DiscoveryURL: s.DiscoveryURL,
		DiscoveryKey: s.DiscoveryKey,
		BaseURL:      s.GetBaseURL(),
	})
	if err != nil {
		s.LogError("widget publish comment: beseech failed: %v", err)
		blessingStatus = "error"
	} else if result.AutoBlessed {
		blessingStatus = "granted"
	}

	// Always re-render after beseech attempt — PublishComment() runs early inside
	// BeseechComment, so the .md is on disk even if DS registration fails or the
	// comment is not auto-blessed. Without this, the comment HTML and index.html
	// are never generated.
	if renderErr := s.RenderSite(); renderErr != nil {
		s.LogError("widget publish comment: render failed: %v", renderErr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":         true,
		"content_url":     signed.Meta.CommentURL,
		"comment_id":      signed.Meta.ID,
		"blessing_status": blessingStatus,
	})
}

// POST /api/widget/follow — Add author to following.json via widget token.
// DELETE /api/widget/follow — Remove author from following.json via widget token.
func (s *Server) handleWidgetFollow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "Not configured", http.StatusBadRequest)
		return
	}

	var req struct {
		Author string `json:"author"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Author == "" {
		http.Error(w, "author is required", http.StatusBadRequest)
		return
	}

	// Normalize author to URL if it's just a domain
	authorURL := req.Author
	if !strings.HasPrefix(authorURL, "https://") {
		authorURL = "https://" + authorURL
	}

	followingPath := following.DefaultPath(s.DataDir)
	followDomain := discovery.ExtractDomainFromURL(s.GetBaseURL())
	discoveryClient := s.NewAuthDSClient(r, followDomain)
	remoteClient := s.NewRemoteClient()

	switch r.Method {
	case http.MethodPost:
		privPath, pubPath := policy.DefaultPaths(s.DataDir)
		widgetPolicies, _ := policy.LoadPolicies(privPath, pubPath)

		result, err := following.FollowWithBlessing(followingPath, authorURL, discoveryClient, remoteClient, s.PrivateKey, widgetPolicies)
		if err != nil {
			s.LogError("widget follow failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.LogEvent("pub.polis.follow.widget_add", map[string]interface{}{
			"url": authorURL,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    result,
		})

	case http.MethodDelete:
		privPath, pubPath := policy.DefaultPaths(s.DataDir)
		widgetUnfollowPolicies, _ := policy.LoadPolicies(privPath, pubPath)

		result, err := following.UnfollowWithDenial(followingPath, authorURL, discoveryClient, remoteClient, s.PrivateKey, widgetUnfollowPolicies)
		if err != nil {
			s.LogError("widget unfollow failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.LogEvent("pub.polis.follow.widget_remove", map[string]interface{}{
			"url":             authorURL,
			"comments_denied": result.CommentsDenied,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    result,
		})

	}
}

// GET /api/widget/connect — Issue widget token from session and redirect.
// Query params: return=<url>
// This endpoint is same-origin (dashboard) — session cookie auth is valid here.
func (s *Server) handleWidgetConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// This endpoint needs no special handling in the server package —
	// widget token issuance happens in the hosted service layer.
	// The server-side handler is a no-op placeholder that the hosted
	// layer intercepts before it reaches here.
	http.Error(w, "Widget connect is handled by the hosted service", http.StatusNotImplemented)
}

// Download rate limiting: one download per 10 minutes per server instance.
var (
	downloadMu       sync.Mutex
	lastDownloadTime time.Time
)

// WriteZipArchive writes a ZIP archive of dataDir to w, excluding the named directories.
// Directory names in excludeDirs are relative to dataDir (e.g. "logs", ".polis/keys").
func WriteZipArchive(w io.Writer, dataDir string, excludeDirs []string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()

	dataDir = filepath.Clean(dataDir)

	// Build a set of excluded directory paths for quick lookup
	excludeSet := make(map[string]bool, len(excludeDirs))
	for _, d := range excludeDirs {
		excludeSet[filepath.Clean(d)] = true
	}

	return filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip files we can't read
		}

		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return nil
		}

		// Skip excluded directories
		if info.IsDir() {
			if excludeSet[rel] {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip files inside excluded directories (safety check)
		for _, d := range excludeDirs {
			if strings.HasPrefix(rel, d+string(filepath.Separator)) {
				return nil
			}
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return nil
		}
		header.Name = rel
		header.Method = zip.Deflate

		writer, err := zw.CreateHeader(header)
		if err != nil {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		io.Copy(writer, f)
		return nil
	})
}

// handleDownloadSite handles GET /api/download-site — streams a zip archive of the site.
// Includes cryptographic keys. Only excludes logs.
func (s *Server) handleDownloadSite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limit
	downloadMu.Lock()
	if time.Since(lastDownloadTime) < 10*time.Minute {
		downloadMu.Unlock()
		http.Error(w, "Please wait before downloading again", http.StatusTooManyRequests)
		return
	}
	lastDownloadTime = time.Now()
	downloadMu.Unlock()

	s.LogEvent("pub.polis.site.download", nil)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="polis-site.zip"`)

	WriteZipArchive(w, s.DataDir, []string{filepath.Join(".polis", "logs")})
}

// ============================================================================
// SSE AND COUNTS HANDLERS
// ============================================================================

// handleSSE provides a Server-Sent Events endpoint for real-time count updates.
// GET /_/api/sse
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	// Register this client
	ch := make(chan SSEEvent, 10)
	s.addSSEClient(ch)
	defer s.removeSSEClient(ch)

	// Send initial counts immediately
	counts := s.computeAllCounts()
	if data, err := json.Marshal(counts); err == nil {
		fmt.Fprintf(w, "event: counts\ndata: %s\n\n", data)
		flusher.Flush()
	}

	// Stream events until client disconnects
	ctx := r.Context()
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Event, evt.Data)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// handleNavState returns nav state for the avatar menu and nav widget.
// GET /api/nav/state
func (s *Server) handleNavState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	counts := s.computeAllCounts()

	// Read author name + avatar from .well-known/polis
	authorName := ""
	var avatarConfig map[string]interface{}
	wkPath := filepath.Join(s.DataDir, ".well-known", "polis")
	if data, err := os.ReadFile(wkPath); err == nil {
		var wk map[string]interface{}
		if json.Unmarshal(data, &wk) == nil {
			if name, ok := wk["author_name"].(string); ok {
				authorName = name
			}
			if av, ok := wk["avatar"].(map[string]interface{}); ok {
				avatarConfig = av
			}
		}
	}

	// Build handle from base URL
	handle := ""
	baseURL := s.GetBaseURL()
	if baseURL != "" {
		if u, err := url.Parse(baseURL); err == nil {
			parts := strings.SplitN(u.Hostname(), ".", 2)
			if len(parts) > 0 {
				handle = parts[0]
			}
		}
	}

	resp := map[string]interface{}{
		"handle":      handle,
		"author_name": authorName,
		"home_url":    baseURL,
		"counts": map[string]int{
			"posts":              counts.Posts,
			"following":          counts.Following,
			"followers":          counts.Followers,
			"feed_unread":        counts.FeedUnread,
			"dm_unread":          counts.DMUnread,
			"blessing_requests":  counts.BlessingRequests,
		},
	}
	if avatarConfig != nil {
		resp["avatar"] = avatarConfig
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleCounts returns all badge counts in a single response.
// Replaces the need for 13 parallel API calls from loadAllCounts().
// GET /_/api/counts
func (s *Server) handleCounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	counts := s.computeAllCounts()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(counts)
}

// handleDMConversations returns the list of DM conversations.
func (s *Server) handleDMConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	store, err := s.dmStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	idx, err := store.LoadIndex()
	if err != nil {
		s.LogError("dm conversations: %v", err)
		http.Error(w, "Failed to load conversations", http.StatusInternalServerError)
		return
	}
	store.DecryptIndexPreviews(idx)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"conversations": idx.Conversations,
		"count":         len(idx.Conversations),
	})
}

// handleDMConversation returns a single conversation with decrypted messages.
// Also auto-marks the conversation as read.
func (s *Server) handleDMConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		s.handleDMDeleteConversation(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	convID := strings.TrimPrefix(r.URL.Path, "/api/dm/conversations/")
	if convID == "" {
		http.Error(w, "Missing conversation ID", http.StatusBadRequest)
		return
	}

	store, err := s.dmStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conv, err := store.LoadConversation(convID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			s.LogError("dm conversation load: %v", err)
			http.Error(w, "Failed to load conversation", http.StatusInternalServerError)
		}
		return
	}

	// Decrypt messages
	type decryptedMsg struct {
		ID        string `json:"id"`
		From      string `json:"from"`
		To        string `json:"to"`
		Content   string `json:"content"`
		ReplyToID string `json:"reply_to_id,omitempty"`
		Timestamp string `json:"timestamp"`
		ReadAt    string `json:"read_at,omitempty"`
		Status    string `json:"status"`
	}
	msgs := make([]decryptedMsg, 0, len(conv.Messages))
	for _, msg := range conv.Messages {
		plaintext, err := store.DecryptMessage(&msg)
		if err != nil {
			s.LogError("dm decrypt message %s: %v", msg.ID, err)
			plaintext = "[decryption failed]"
		}
		msgs = append(msgs, decryptedMsg{
			ID:        msg.ID,
			From:      msg.From,
			To:        msg.To,
			Content:   plaintext,
			ReplyToID: msg.ReplyToID,
			Timestamp: msg.Timestamp,
			ReadAt:    msg.ReadAt,
			Status:    msg.Status,
		})
	}

	// Auto-mark as read
	if err := store.MarkRead(convID); err != nil {
		s.LogError("dm mark read: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"peer_domain": conv.PeerDomain,
		"peer_url":    conv.PeerURL,
		"messages":    msgs,
	})
}

// handleDMDeleteConversation deletes a DM conversation.
func (s *Server) handleDMDeleteConversation(w http.ResponseWriter, r *http.Request) {
	convID := strings.TrimPrefix(r.URL.Path, "/api/dm/conversations/")
	if convID == "" {
		http.Error(w, "Missing conversation ID", http.StatusBadRequest)
		return
	}

	store, err := s.dmStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := store.DeleteConversation(convID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			s.LogError("dm delete: %v", err)
			http.Error(w, "Failed to delete conversation", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// handleDMSend sends a new DM to a remote recipient.
func (s *Server) handleDMSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	store, err := s.dmStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		RecipientURL string `json:"recipient_url"`
		Content      string `json:"content"`
		ReplyToID    string `json:"reply_to_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.RecipientURL == "" {
		http.Error(w, "recipient_url is required", http.StatusBadRequest)
		return
	}
	if req.Content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	// Validate recipient URL
	if _, err := url.Parse(req.RecipientURL); err != nil {
		http.Error(w, "Invalid recipient URL", http.StatusBadRequest)
		return
	}

	domain := s.GetBaseURL()
	if domain == "" {
		http.Error(w, "Site base URL not configured", http.StatusBadRequest)
		return
	}

	sender := dm.NewSenderWithHTTP(s.PrivateKey, s.PublicKey, dm.ExtractDomainFromURL(domain), store, s.SharedHTTPClient)

	msg, err := sender.SendMessage(req.RecipientURL, req.Content, req.ReplyToID)
	if err != nil {
		// Even on delivery failure, the message may have been saved as "unsent"
		if msg != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"message_id": msg.ID,
				"status":     msg.Status,
				"warning":    err.Error(),
			})
			return
		}
		s.LogError("dm send: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message_id": msg.ID,
		"status":     msg.Status,
	})
}

// handleDMMarkRead marks a conversation as read.
func (s *Server) handleDMMarkRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	store, err := s.dmStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ConversationID == "" {
		http.Error(w, "conversation_id is required", http.StatusBadRequest)
		return
	}

	if err := store.MarkRead(req.ConversationID); err != nil {
		s.LogError("dm mark read: %v", err)
		http.Error(w, "Failed to mark read", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// handleDMRetry returns unsent messages (retry is handled client-side by re-sending).
func (s *Server) handleDMRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	store, err := s.dmStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	unsent, err := store.GetUnsentMessages()
	if err != nil {
		s.LogError("dm retry: %v", err)
		http.Error(w, "Failed to get unsent messages", http.StatusInternalServerError)
		return
	}

	type unsentItem struct {
		ConvID    string `json:"conv_id"`
		MessageID string `json:"message_id"`
		To        string `json:"to"`
		Timestamp string `json:"timestamp"`
	}
	items := make([]unsentItem, 0, len(unsent))
	for _, u := range unsent {
		items = append(items, unsentItem{
			ConvID:    u.ConvID,
			MessageID: u.Message.ID,
			To:        u.Message.To,
			Timestamp: u.Message.Timestamp,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"unsent_count": len(items),
		"unsent":       items,
	})
}

// handleDMRecipients returns the following list with DM eligibility per recipient.
// For each person we follow:
//  1. Check their public policies — if explicit DM allow/deny, use that.
//  2. If no public DM rules, fetch their following.json — if they follow us, allow.
//
// Side effects: updates local follower state and following metadata when remote data is fetched.
func (s *Server) handleDMRecipients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.PrivateKey == nil {
		http.Error(w, "No private key configured", http.StatusBadRequest)
		return
	}

	followingPath := following.DefaultPath(s.DataDir)
	f, err := following.Load(followingPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"recipients": []interface{}{},
		})
		return
	}

	myDomain := discovery.ExtractDomainFromURL(s.GetBaseURL())

	type recipient struct {
		Domain     string `json:"domain"`
		URL        string `json:"url"`
		AuthorName string `json:"author_name,omitempty"`
		Status     string `json:"status"`           // "open", "no-dm", "no-follow", "unknown"
		Reason     string `json:"reason,omitempty"`  // human-readable explanation
		FollowsUs  bool   `json:"follows_us"`        // whether they follow us
	}

	entries := f.All()
	results := make([]recipient, len(entries))

	type checkResult struct {
		idx    int
		result policycheck.Result
	}
	resultCh := make(chan checkResult, len(entries))
	sem := make(chan struct{}, 5)
	start := time.Now()

	pendingCount := 0
	for i, entry := range entries {
		domain := dm.ExtractDomainFromURL(entry.URL)
		results[i] = recipient{
			Domain:     domain,
			URL:        entry.URL,
			AuthorName: entry.AuthorName,
		}

		// Check cache
		cacheKey := domain + ":" + myDomain
		if cached := s.getPolicyCacheEntry(cacheKey); cached != nil {
			results[i].Status = cached.Status
			results[i].Reason = cached.Reason
			results[i].FollowsUs = cached.FollowsUs
			continue
		}

		pendingCount++
		idx := i
		url := entry.URL
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			client := s.NewRemoteClient()
			res := policycheck.CheckDMEligibilityURL(client, url, myDomain)
			resultCh <- checkResult{idx: idx, result: res}
		}()
	}

	// Collect results
	followingChanged := false
	for range pendingCount {
		cr := <-resultCh
		results[cr.idx].Status = cr.result.Status
		results[cr.idx].Reason = cr.result.Reason
		results[cr.idx].FollowsUs = cr.result.FollowsUs

		// Cache
		cacheKey := results[cr.idx].Domain + ":" + myDomain
		s.setPolicyCacheEntry(cacheKey, cr.result.Status, cr.result.Reason, cr.result.FollowsUs)

		// Update local follower state from fetched data
		if cr.result.FetchedFollowing != nil {
			s.syncFollowerState(results[cr.idx].Domain, cr.result.FollowsUs)
		}

		// Opportunistic metadata sync: update author_name/site_title in following.json
		// if the remote following.json was fetched and we can get .well-known metadata
		// (skipped — would require extra fetch; metadata comes from other sync paths)
	}

	if followingChanged {
		following.Save(followingPath, f)
	}

	// Log aggregate stats
	duration := time.Since(start)
	openCount, blockedCount, noFollowCount, unknownCount := 0, 0, 0, 0
	for _, rec := range results {
		switch rec.Status {
		case "open":
			openCount++
		case "no-dm":
			blockedCount++
		case "no-follow":
			noFollowCount++
		case "unknown":
			unknownCount++
		}
	}
	s.LogEvent("pub.polis.dm.recipients_checked", map[string]interface{}{
		"request_id":      RequestIDFromContext(r.Context()),
		"total":           len(results),
		"open_count":      openCount,
		"blocked_count":   blockedCount,
		"no_follow_count": noFollowCount,
		"unknown_count":   unknownCount,
		"duration_ms":     duration.Milliseconds(),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"recipients": results,
	})
}

// getPolicyCacheEntry returns a cached policy check result, or nil if expired/missing.
func (s *Server) getPolicyCacheEntry(key string) *policyCacheEntry {
	s.policyCacheMu.Lock()
	defer s.policyCacheMu.Unlock()

	if s.policyCache == nil {
		return nil
	}
	entry, ok := s.policyCache[key]
	if !ok || time.Now().After(entry.ExpiresAt) {
		delete(s.policyCache, key)
		return nil
	}
	return entry
}

// setPolicyCacheEntry stores a policy check result in the cache.
func (s *Server) setPolicyCacheEntry(key, status, reason string, followsUs bool) {
	s.policyCacheMu.Lock()
	defer s.policyCacheMu.Unlock()

	if s.policyCache == nil {
		s.policyCache = make(map[string]*policyCacheEntry)
	}
	s.policyCache[key] = &policyCacheEntry{
		Status:    status,
		Reason:    reason,
		FollowsUs: followsUs,
		ExpiresAt: time.Now().Add(policyCheckCacheTTL),
	}
}

// syncFollowerState updates local follower state if remote data disagrees.
func (s *Server) syncFollowerState(remoteDomain string, theyFollowUs bool) {
	dsDomain := discovery.ExtractDomainFromURL(s.DiscoveryURL)
	if dsDomain == "" {
		return
	}
	store := stream.NewStore(s.DataDir, dsDomain, "pub.polis.core")
	var fs stream.FollowerState
	if err := store.LoadState("pub.polis.follow", &fs); err != nil {
		return
	}

	remoteLower := strings.ToLower(remoteDomain)
	isLocal := false
	for _, f := range fs.Followers {
		if strings.ToLower(f) == remoteLower {
			isLocal = true
			break
		}
	}

	if theyFollowUs && !isLocal {
		fs.Followers = append(fs.Followers, remoteLower)
		fs.Count = len(fs.Followers)
		store.SaveState("pub.polis.follow", &fs)
	} else if !theyFollowUs && isLocal {
		filtered := fs.Followers[:0]
		for _, f := range fs.Followers {
			if strings.ToLower(f) != remoteLower {
				filtered = append(filtered, f)
			}
		}
		fs.Followers = filtered
		fs.Count = len(fs.Followers)
		store.SaveState("pub.polis.follow", &fs)
	}
}

// handleTags handles GET (list all tags), POST (apply tag), and DELETE (remove target) for /api/tags.
func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleTagsList(w, r)
	case http.MethodPost:
		s.handleTagApply(w, r)
	case http.MethodDelete:
		s.handleTagRemove(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTagsList returns all tag files as JSON.
func (s *Server) handleTagsList(w http.ResponseWriter, r *http.Request) {
	tags, err := tag.ListTags(s.DataDir)
	if err != nil {
		s.LogError("list tags: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tags == nil {
		tags = []tag.TagFile{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tags": tags,
	})
}

// handleTagApply applies a tag to a target URI.
func (s *Server) handleTagApply(w http.ResponseWriter, r *http.Request) {
	if s.PrivateKey == nil {
		http.Error(w, "Not configured: no signing key", http.StatusBadRequest)
		return
	}

	var req struct {
		Tag       string `json:"tag"`
		TargetURI string `json:"target_uri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Tag == "" || req.TargetURI == "" {
		http.Error(w, "tag and target_uri are required", http.StatusBadRequest)
		return
	}

	tf, err := tag.ApplyTag(s.DataDir, req.Tag, req.TargetURI, s.PrivateKey)
	if err != nil {
		s.LogError("apply tag: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Sync with discovery service
	dsCfg := s.tagDiscoveryConfig()
	if dsCfg != nil {
		if err := tag.SyncTag(s.DataDir, tf, s.PrivateKey, dsCfg); err != nil {
			s.LogWarn("tag DS sync failed: %v", err)
		}
	}

	s.LogEvent("pub.polis.tag.applied", map[string]interface{}{
		"tag":        req.Tag,
		"target_uri": req.TargetURI,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"tag":     tf,
	})
}

// handleTagRemove removes a target URI from a tag.
func (s *Server) handleTagRemove(w http.ResponseWriter, r *http.Request) {
	if s.PrivateKey == nil {
		http.Error(w, "Not configured: no signing key", http.StatusBadRequest)
		return
	}

	var req struct {
		Tag       string `json:"tag"`
		TargetURI string `json:"target_uri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Tag == "" || req.TargetURI == "" {
		http.Error(w, "tag and target_uri are required", http.StatusBadRequest)
		return
	}

	tf, err := tag.RemoveTarget(s.DataDir, req.Tag, req.TargetURI, s.PrivateKey)
	if err != nil {
		s.LogError("remove tag target: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Unregister from discovery service
	dsCfg := s.tagDiscoveryConfig()
	if dsCfg != nil {
		if err := tag.UnregisterTarget(req.Tag, req.TargetURI, s.PrivateKey, dsCfg); err != nil {
			s.LogWarn("tag DS unregister failed: %v", err)
		}
	}

	s.LogEvent("pub.polis.tag.removed", map[string]interface{}{
		"tag":        req.Tag,
		"target_uri": req.TargetURI,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"tag":     tf,
	})
}

// tagDiscoveryConfig returns a tag.DiscoveryConfig for the current server,
// or nil if discovery is not configured.
func (s *Server) tagDiscoveryConfig() *tag.DiscoveryConfig {
	if s.DiscoveryURL == "" || s.BaseURL == "" {
		return nil
	}
	return &tag.DiscoveryConfig{
		DiscoveryURL: s.DiscoveryURL,
		DiscoveryKey: s.DiscoveryKey,
		BaseURL:      s.BaseURL,
		HTTPClient:   s.SharedHTTPClient,
	}
}
