package server

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/webapp/internal/api"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/blessing"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/policycheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// handleValidate returns the validation status of the site directory.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	validation := site.Validate(s.Site())

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(validation)
}

// writeEnvFile writes or updates key=value pairs in the .env file at envKey.
// If the file exists, it updates existing keys in place and appends new ones.
// Otherwise it creates a new file with all pairs.
func (s *Server) writeEnvFile(envKey string, vars map[string]string) {
	data, err := s.readKey(envKey)
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
		if err := s.writeKey(envKey, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			s.LogWarn("Failed to update .env: %v", err)
		}
	} else {
		// Create new .env file
		var b strings.Builder
		b.WriteString("# polis configuration\n")
		// Write in a stable order
		for _, key := range []string{"POLIS_BASE_URL", "DISCOVERY_SERVICE_URL", "LOG_LEVEL", "LOG_RETENTION_DAYS"} {
			if val, ok := vars[key]; ok {
				b.WriteString(key + "=" + val + "\n")
			}
		}
		if err := s.writeKey(envKey, []byte(b.String()), 0644); err != nil {
			s.LogWarn("Failed to create .env: %v", err)
		}
	}
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

	// ⭐ What this rotation starts from: a fresh keypair, or an earlier
	// rotation's staged key to resume (the discovery service may already hold
	// it), signing with whichever key .well-known/polis publishes as current.
	// The keys are read from disk, not from s.PrivateKey: after an interrupted
	// rotation the key in memory is the INSTALLED one, which never signs its
	// own handover. ⛔ It refuses when the key on disk is not the published one.
	keys, err := site.PrepareRotation(r.Context(), s.Site())
	if err != nil {
		s.LogError("Key rotation not started: %v", err)
		status := http.StatusInternalServerError
		if errors.Is(err, site.ErrKeyNotPublished) || errors.Is(err, site.ErrRotationUnfinishable) ||
			errors.Is(err, site.ErrRotationPending) {
			status = http.StatusConflict
		}
		http.Error(w, "Key rotation not started: "+err.Error(), status)
		return
	}
	resuming := keys.Resuming
	oldPubKey, oldPrivKey := keys.OldPub, keys.OldPriv
	newPrivPEM, newPubSSH, newPubKey := keys.NewPriv, keys.NewPubSSH, keys.NewPub

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
	// ⛔ STAGE FIRST: the new key is durably on disk before anyone is told.
	if !resuming {
		if err := site.StageRotationKey(r.Context(), s.Site(), newPrivPEM); err != nil {
			s.LogError("Key rotation not started: %v", err)
			status := http.StatusInternalServerError // a full disk is not a conflict
			if errors.Is(err, site.ErrRotationPending) {
				status = http.StatusConflict
			}
			http.Error(w, "Key rotation not started: "+err.Error(), status)
			return
		}
	}
	dsClient := s.NewDSClient(r)
	// ⛔ Only a refusal drops the staged key; a lost answer keeps it
	// (site.AnnounceRotation).
	witness, _, err := site.AnnounceRotation(r.Context(), s.Site(), dsClient, discovery.KeyRotationRequest{
		Domain:        domain,
		OldKey:        oldPubKey,
		NewKey:        newPubKey,
		TransitionSig: transitionSig,
		Timestamp:     timestamp,
	}, resuming)
	if errors.Is(err, site.ErrRotationDiverged) {
		s.LogError("Key rotation not finished: %v", err)
		http.Error(w, "Key rotation not finished: the discovery service holds neither this site's key nor the staged "+
			"one. Nothing was changed.", http.StatusConflict)
		return
	}
	if errors.Is(err, site.ErrRotationPending) {
		s.LogError("Key rotation not finished: %v", err)
		http.Error(w, "Key rotation not finished: the discovery service's answer was lost, so the new key is kept. "+
			"Rotate again to finish: it checks which key the discovery service holds.", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		s.LogError("Key rotation failed: DS notification: %v", err)
		http.Error(w, "Discovery service rejected key rotation", http.StatusBadGateway)
		return
	}
	// SIGNET epic 32: the DS's countersignature, carried in the new entry below.
	var witnesses []discovery.Witness
	if witness != nil {
		witnesses = append(witnesses, *witness)
	}

	// No .old backup is made. The published key history replaces it — see the
	// same note in cli-go/pkg/cmd/rotate_key.go. Existing .old files are left in
	// place and reaper.go still excludes them from a departing user's archive.
	// ⭐ Install the staged key: each file lands by rename, and the private key
	// always ends 0600. The retired key and the staged copy stay until the
	// rotation is recorded below.
	if err := site.InstallRotationKey(r.Context(), s.Site(), newPrivPEM, newPubSSH); err != nil {
		s.LogError("Key rotation failed: installing the new key (staged at %s): %v", site.StagedKeyKey, err)
		http.Error(w, "Failed to write new keys", http.StatusInternalServerError)
		return
	}

	// Update .well-known/polis: the new public_key AND the appended history
	// entry, in one write, through the shared seam.
	//
	// ⛔ This handler is a SECOND, independent implementation of rotation — the
	// CLI has its own. Both must append, or a site rotated through one of them
	// publishes a key its own history never learned about, and every artifact
	// signed under it becomes unverifiable against that history. Neither path
	// touches public_key or public_key_history directly; site.RecordKeyRotation
	// writes both in one go, so they cannot drift apart. It preserves unmodelled
	// fields (public_key_messages), as every well-known writer does.
	//
	// timestamp is passed verbatim: the old key signed it, and it becomes the new
	// entry's valid_from. Reformatting it breaks the transition signature.
	if err := site.RecordKeyRotation(s.Site(), newPubKey, transitionSig, timestamp, domain, witnesses...); err != nil {
		s.LogError("Key rotation failed: recording the rotation in .well-known/polis: %v", err)
		http.Error(w, "Failed to update site identity", http.StatusInternalServerError)
		return
	}
	// ⛔ Only now: the retired key and the staged copy are what a resume would
	// sign the handover with. Not fatal; the next rotation removes leftovers.
	if err := site.FinishRotation(r.Context(), s.Site()); err != nil {
		s.LogError("Key rotation: %v", err)
	}
	keyEpoch := 0
	if block, herr := site.LoadKeyHistory(s.Site()); herr == nil && block != nil {
		keyEpoch = block.Current.Epoch
	}

	// Re-sign + re-publish the DM messages key under the NEW identity key. The
	// preserved block is still signed by the OLD key, which Judge would flag as
	// forged (judge.alert.messages_key_forged); re-publishing re-signs it. Tenants
	// without a keyring (pre-DM) have nothing to publish — skip silently.
	if _, err := dm.LoadKeyring(s.Site()); err == nil {
		if err := site.PublishMessagesKey(s.Site(), newPrivPEM); err != nil {
			s.LogError("Key rotation: failed to re-publish messages key under new identity: %v", err)
		}
	}

	// Republish the DID Document so it carries the new key and, since SIGNET
	// epic 16, the retired ones.
	//
	// ⚠️ This branch used to do nothing, leaving did.json naming a RETIRED key
	// until Medic's next hourly sweep healed it — and a self-hosted webapp has no
	// Medic at all. A stale derived artifact answers 200 with a key the site no
	// longer holds, which a resolver cannot tell from a good one; an absent one
	// fails honestly. So: republish, and if that fails, remove. Never fatal —
	// the rotation has already happened and been announced, and the document is
	// derived, so `polis did --write` rebuilds it.
	if err := site.PublishDIDDocument(s.Site(), domain); err != nil {
		removed := s.Site().Store().Delete(context.Background(), site.DIDDocumentKey) == nil
		s.LogError("Key rotation: did.json republish failed (removed=%v): %v", removed, err)
	}

	// Reload in-memory keys
	s.LoadKeys()

	// Re-sign the follow file and the blessing list under the NEW key. Neither
	// carries a signing time, so each verifies against the current key only, and
	// leaving them would turn both invalid until the next follow or bless.
	//
	// ⛔ Only a file that verifies under the OLD key is re-signed, so a rotation
	// can never launder a list that was already failing (signing.ResignOutcome).
	//
	// ⚠️ AFTER LoadKeys, deliberately. Until then s.PrivateKey is the old key, and
	// sync or Rosie may write the blessing list with it; that write passes the
	// old-key gate and is re-signed here. Anything written after LoadKeys already
	// carries the new key. Never fatal: a file not re-signed is the file it was.
	followResign, err := following.ResignAfterRotation(s.Site(), []byte(oldPubKey), newPrivPEM)
	if err != nil {
		s.LogError("Key rotation: follow file not re-signed (%s): %v", followResign, err)
	}
	blessedResign, err := metadata.ResignBlessedAfterRotation(s.Site(), []byte(oldPubKey), newPrivPEM)
	if err != nil {
		s.LogError("Key rotation: blessing list not re-signed (%s): %v", blessedResign, err)
	}

	// Re-render site (non-fatal if fails)
	if err := s.RenderSite(); err != nil {
		log.Printf("[warning] post-rotation render failed: %v", err)
	}

	s.LogEvent("pub.polis.site.key_rotate", map[string]interface{}{
		"key_history_epoch": keyEpoch,
		"following_resign":  string(followResign),
		"blessed_resign":    string(blessedResign),
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"public_key":        newPubKey,
		"key_history_epoch": keyEpoch,
		"following_resign":  string(followResign),
		"blessed_resign":    string(blessedResign),
	})
}

// Comment API handlers

// Blessing API handlers (ON MY POSTS - incoming blessing requests)

// BlessComment blesses a comment on one of the owner's posts: the blessing is
// signed, recorded with the discovery service, the comment is verified into
// the blessed cache, and the site is rebuilt to show it. ⭐ THE ONE
// IMPLEMENTATION: the browser app's /api/blessing/grant and the API's
// comment.bless both call it. A failure comes with the HTTP status to answer.
func (s *Server) BlessComment(r *http.Request, commentURL, commentVersion, inReplyTo string) (interface{}, int, error) {
	if s.DiscoveryURL == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("Discovery service not configured")
	}
	if !s.IsRegisteredWithDS() {
		return nil, http.StatusPreconditionFailed, fmt.Errorf("Site not registered with discovery service")
	}
	if s.PrivateKey == nil {
		return nil, http.StatusBadRequest, fmt.Errorf("Private key not configured")
	}
	if commentURL == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("comment_url is required")
	}

	// Create discovery client
	client := s.NewDSClient(r)

	// If comment_version is missing (old DS records without metadata), look it up
	if commentVersion == "" {
		if check, err := client.CheckContent("pub.polis.comment", commentURL); err == nil && check.Exists {
			commentVersion = check.Version
		}
	}

	// Grant the blessing (with signed request)
	// Normalize URLs to .md format for consistent storage
	s.LogDebug("Granting blessing for comment version: %s", commentVersion)
	bhc := s.getHookConfig()
	var result *blessing.GrantResult
	var err error
	if live := ops.GrantFrom(r.Context()); live != nil {
		// ⭐ An agent's decision, under the grant its token presents: marked
		// inside the relationship request and the blessed.json entry.
		result, err = blessing.GrantAsAgent(s.Site(), &blessing.IncomingRequest{
			CommentVersion: commentVersion,
			CommentURL:     polisurl.NormalizeToMD(commentURL),
			InReplyTo:      polisurl.NormalizeToMD(inReplyTo),
		}, client, bhc, s.PrivateKey, live)
	} else {
		result, err = blessing.GrantByVersion(
			s.Site(),
			commentVersion,
			polisurl.NormalizeToMD(commentURL),
			polisurl.NormalizeToMD(inReplyTo),
			client,
			bhc,
			s.PrivateKey,
		)
	}
	if err != nil {
		s.LogError("Failed to grant blessing: %v", err)
		return nil, http.StatusInternalServerError, fmt.Errorf("Failed to grant blessing")
	}
	s.LogEvent("pub.polis.comment.blessing.grant", map[string]interface{}{
		"comment_url": commentURL,
	})

	// Verify + cache the remote comment in the ISOLATED blessed cache (NEVER
	// under our content/ tree — Defect 3) so the renderer can display the body on
	// the post page. Shared with the background sync handler via cacheBlessedComment
	// so the two Defect-3 write sites can't drift.
	s.cacheBlessedComment(commentURL)

	// Render site to include the newly blessed comment
	if err := s.RenderSite(); err != nil {
		// Log but don't fail - the blessing was granted successfully
		log.Printf("[warning] post-blessing render failed: %v", err)
	}
	return result, http.StatusOK, nil
}

// DenyComment turns a waiting comment away from one of the owner's posts
// ("not here"), by a signed request to the discovery service. ⭐ The one
// implementation: /api/blessing/deny and the API's comment.deny call it.
func (s *Server) DenyComment(r *http.Request, commentURL, inReplyTo string) (interface{}, int, error) {
	if s.DiscoveryURL == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("Discovery service not configured")
	}
	if !s.IsRegisteredWithDS() {
		return nil, http.StatusPreconditionFailed, fmt.Errorf("Site not registered with discovery service")
	}
	if s.PrivateKey == nil {
		return nil, http.StatusBadRequest, fmt.Errorf("Private key not configured")
	}
	if commentURL == "" || inReplyTo == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("comment_url and in_reply_to are required")
	}

	// Create discovery client
	client := s.NewDSClient(r)

	// Deny the blessing (with signed request). Normalise to .md like grant: the
	// DS matches the relationship on its exact URLs, and the v4 stream hands the
	// SPA the rendered .html forms.
	s.LogDebug("Denying blessing for comment: %s", commentURL)
	var result *blessing.DenyResult
	var err error
	if live := ops.GrantFrom(r.Context()); live != nil {
		result, err = blessing.DenyAsAgent(polisurl.NormalizeToMD(commentURL), polisurl.NormalizeToMD(inReplyTo), client, s.PrivateKey, live)
	} else {
		result, err = blessing.Deny(polisurl.NormalizeToMD(commentURL), polisurl.NormalizeToMD(inReplyTo), client, s.PrivateKey)
	}
	if err != nil {
		s.LogError("Failed to deny blessing: %v", err)
		return nil, http.StatusInternalServerError, fmt.Errorf("Failed to deny blessing")
	}
	s.LogEvent("pub.polis.comment.blessing.deny", map[string]interface{}{
		"comment_url": commentURL,
	})
	return result, http.StatusOK, nil
}

// UnblessComment takes a blessed comment off one of the owner's posts: the
// signed blessing list drops it and the site is rebuilt; it leaves at the
// next sync. ⭐ The one implementation: /api/blessing/revoke and the API's
// comment.unbless call it.
func (s *Server) UnblessComment(commentURL string) (interface{}, int, error) {
	if commentURL == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("comment_url is required")
	}

	// Normalize URL to .md format for consistent lookup
	normalizedURL := polisurl.NormalizeToMD(commentURL)

	// Remove from blessed.json, SIGNED — an unbless is as much an authored act as
	// a bless (SIGNET epic 14). A nil key writes unsigned, which is the safe
	// direction: unsigned is a fact, a stale signature is a false alarm.
	if err := metadata.RemoveBlessedCommentSigned(s.Site(), normalizedURL, s.PrivateKey); err != nil {
		s.LogError("failed to revoke blessing: %v", err)
		return nil, http.StatusInternalServerError, fmt.Errorf("Failed to revoke blessing")
	}
	s.LogEvent("pub.polis.comment.blessing.revoke", map[string]interface{}{
		"comment_url": normalizedURL,
	})

	// Render site to remove the comment from pages
	if err := s.RenderSite(); err != nil {
		// Log but don't fail - the revoke was successful
		log.Printf("[warning] post-revoke render failed: %v", err)
	}
	return map[string]interface{}{
		"success":     true,
		"comment_url": normalizedURL,
	}, http.StatusOK, nil
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
	// Reserved themes cannot be selected as a personal theme. sols is the
	// logged-out landing theme; stardust is reserved for why.polis.pub. (A
	// tenant can still be put on a reserved theme via registry.json directly —
	// this only blocks user selection.)
	if theme.IsReserved(req.Theme) {
		http.Error(w, req.Theme+" is reserved and cannot be selected as a personal theme", http.StatusBadRequest)
		return
	}

	// Validate that the theme exists
	themes, err := theme.ListThemes(s.Site(), s.CLIThemesDir)
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
	// ⛔ And one this site's shape can draw, as the picker offers: checked
	// before anything is changed, so a refused switch leaves the site as it was.
	shape, _ := bundle.GetActiveShapeName(s.Site())
	if !theme.OfferedForShape(req.Theme, shape) {
		http.Error(w, req.Theme+" is not offered for this site's shape ("+shape+")", http.StatusBadRequest)
		return
	}

	// Update manifest and copy CSS
	if err := theme.SetActiveTheme(s.Site(), req.Theme); err != nil {
		s.LogError("theme switch: set active theme failed: %v", err)
		http.Error(w, "Failed to set theme: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := theme.CopyCSS(s.Site(), s.CLIThemesDir, req.Theme); err != nil {
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
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"theme":   req.Theme,
	})
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
	if req.Avatar != nil {
		// A request body is not an authored avatar block: members the settings
		// UI does not send are not written into the public identity document.
		req.Avatar.Extra = nil
	}

	if err := site.ValidateAvatar(req.Avatar); err != nil {
		http.Error(w, "Invalid avatar: "+err.Error(), http.StatusBadRequest)
		return
	}

	// site.SetAvatar saves through the one lossless writer (every member the
	// struct does not model survives) and regenerates the favicon.
	faviconErr, err := site.SetAvatar(s.Site(), req.Avatar)
	if err != nil {
		s.LogError("failed to save .well-known/polis: %v", err)
		http.Error(w, "Failed to save site config", http.StatusInternalServerError)
		return
	}
	if faviconErr != nil {
		s.LogWarn("failed to regenerate favicon: %v", faviconErr)
	}

	s.LogEvent("pub.polis.site.avatar_update", map[string]interface{}{
		"has_avatar": req.Avatar != nil,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"avatar":  req.Avatar,
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

	if _, err := site.ValidateAuthorName(req.AuthorName); err != nil {
		http.Error(w, "Display name must be 50 characters or less", http.StatusBadRequest)
		return
	}

	// The one lossless writer, shared with `polis site set author-name`.
	name, err := site.SetAuthorName(s.Site(), req.AuthorName)
	if err != nil {
		s.LogError("failed to save .well-known/polis: %v", err)
		http.Error(w, "Failed to save site config", http.StatusInternalServerError)
		return
	}

	s.LogEvent("pub.polis.site.author_name_update", map[string]interface{}{
		"author_name": name,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"author_name": name,
	})
}

// handleFavicon serves the generated favicon.svg from the data directory.
// Falls back to 404 if no favicon has been generated yet.
func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	faviconPath := siteKey("favicon.svg")
	data, err := s.readKey(faviconPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
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
		http.Redirect(w, r, "/"+mounted, http.StatusMovedPermanently) //nolint:gosec // G710: the target is built from the session's own handle and the base domain, a validated return URL, or the site's own mount map
		return
	}

	http.NotFound(w, r)
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
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"configured": false,
			"error":      "Discovery service not configured",
		})
		return
	}

	// Extract domain from POLIS_BASE_URL
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"configured": true,
			"error":      "POLIS_BASE_URL not set",
		})
		return
	}

	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"configured": true,
			"domain":     domain,
			"error":      errMsg,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"configured":    true,
		"domain":        domain,
		"is_registered": result.IsRegistered,
		"created_at":    result.CreatedAt,
		"registry_url":  result.RegistryURL,
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
	if wk, err := site.LoadWellKnown(s.Site()); err == nil {
		authorName = wk.AuthorName
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
	if err := discovery.WriteRegistrationMarker(s.Site(), s.DiscoveryURL, domain, result.ServiceAttestation); err != nil {
		s.LogWarn("Failed to write registration marker: %v", err)
	}

	// Upstream follow-drift guard. A follow made while this site was
	// unregistered had its announce suppressed; now that we're registered,
	// reconcile the DS follow graph up to local so those follows are announced
	// instead of lingering as a "dangling removed" until a Chaplain cycle (and
	// so self-hosters, who have no Chaplain, recover at all). Best-effort;
	// mirrors the cli register path.
	reconciledFollows := 0
	if reannounced, rerr := following.ReconcileFollowsToDS(
		s.Site(), domain, client, s.PrivateKey,
		&stream.DiscoveryConfig{
			DiscoveryURL: s.DiscoveryURL,
			BaseURL:      baseURL,
			Site:         s.Site(),
		},
	); rerr != nil {
		s.LogWarn("follow reconcile after register skipped: %v", rerr)
	} else {
		reconciledFollows = len(reannounced)
		if reconciledFollows > 0 {
			s.LogInfo("reconciled %d follow(s) to DS after register", reconciledFollows)
		}
	}

	s.LogEvent("pub.polis.site.register", map[string]interface{}{
		"domain": domain,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":            result.Success,
		"domain":             domain,
		"created_at":         result.CreatedAt,
		"registry_url":       result.RegistryURL,
		"reconciled_follows": reconciledFollows,
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
	if err := discovery.RemoveRegistrationMarker(s.Site(), s.DiscoveryURL); err != nil {
		s.LogWarn("Failed to remove registration marker: %v", err)
	}

	s.LogEvent("pub.polis.site.unregister", map[string]interface{}{
		"domain": domain,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": result.Success,
		"domain":  domain,
		"message": result.Message,
	})
}

// About page handler

// defaultAboutContent is the fallback text for sites without snippets/about.md.
const defaultAboutContent = "Hi \u2014 I'm just getting set up here. A real about page is coming soon.\n\nThis site runs on *polis*: signed markdown on my own domain. No platform, no middleman.\n"

// handleAbout handles GET/POST /api/about for the about page editor.
func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		aboutPath := siteKey("site", "snippets", "about.md")
		data, err := s.readKey(aboutPath)
		if err != nil {
			// File doesn't exist — return default content
			defaultHTML, _ := render.MarkdownToHTML(defaultAboutContent)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"content":      defaultAboutContent,
				"content_html": defaultHTML,
				"has_custom":   false,
			})
			return
		}
		contentHTML, _ := render.MarkdownToHTML(string(data))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
		snippetsDir := siteKey("site", "snippets")

		aboutPath := siteKey(snippetsDir, "about.md")
		if err := s.writeKey(aboutPath, []byte(req.Content), 0644); err != nil {
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
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// Snippets API handlers

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
		Site:              s.Site(),
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

	// ⭐ One render event, with its mode (no dashboard or monitor keyed on the
	// old pub.polis.site.render_full name; checked before it was retired).
	s.LogEvent("pub.polis.site.render", map[string]interface{}{
		"mode":     ops.RenderModeFull,
		"trigger":  "rebuild",
		"posts":    stats.PostsRendered,
		"comments": stats.CommentsRendered,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"path":              req.Path,
		"posts_rendered":    stats.PostsRendered,
		"comments_rendered": stats.CommentsRendered,
	})
}

// ============================================================================
// Social handlers (following, feed, remote post)
// ============================================================================

// followErrorStatus maps a follow failure to its status. A target that is not
// a polis site is the caller's mistake (422); a target that errored or could
// not be reached is upstream (502); anything else failed here (500). An API
// client that retries on 5xx must not retry a URL that will never be a site.
func followErrorStatus(err error) int {
	switch {
	case remote.TargetIsNotPolis(err):
		return http.StatusUnprocessableEntity
	case remote.TargetUnavailable(err):
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

// followFailure is what a failed follow or unfollow says: plain words that
// name the site and what went wrong. ⛔ Never the underlying error's text,
// which carries resolver and transport detail (the machine's own DNS
// resolver's address among it) to the owner's screen and to any agent; that
// detail is logged where the failure is.
func followFailure(verb, site string, err error) error {
	if site == "" {
		site = "that site"
	}
	switch {
	case remote.TargetIsNotPolis(err):
		return fmt.Errorf("%s isn't a polis site, so it can't be %sed: it publishes no polis identity at /.well-known/polis", site, verb)
	case remote.TargetUnavailable(err):
		return fmt.Errorf("%s could not be reached just now (its /.well-known/polis did not answer), so it wasn't %sed; try again later", site, verb)
	}
	return fmt.Errorf("%s could not be %sed just now; try again later", site, verb)
}

// Follow follows a site in the owner's name: the signed follow list gains it,
// the discovery service is told, comments it already made on the owner's
// posts are blessed where the owner's rules say so, and its posts are
// backfilled into the feed. ⭐ THE ONE IMPLEMENTATION: the browser app's POST
// /api/following and the API's follow.create both call it.
func (s *Server) Follow(r *http.Request, url string) (interface{}, int, error) {
	// ⭐ The one implementation (ops.Follow): its checks, the site's rules,
	// the follow and its event. What follows is this host's own: the feed.
	result, err := ops.Follow(r.Context(), s.followSite(r), url)
	if err != nil {
		if errors.Is(err, ops.ErrFollowInvalid) {
			return nil, http.StatusBadRequest, err
		}
		s.LogError("follow failed: %v", err)
		return nil, followErrorStatus(err), followFailure("follow", discovery.ExtractDomainFromURL(url), err)
	}

	// Backfill historical posts from the new author, then sync feed
	// for any recent events across all followed authors.
	targetDomain := discovery.ExtractDomainFromURL(strings.ToLower(url))
	go func() {
		if targetDomain != "" {
			s.backfillFeedForAuthor(targetDomain)
		}
		s.syncFeed()
	}()
	return result, http.StatusOK, nil
}

// followSite is this site as ops.Follow and ops.Unfollow need it.
func (s *Server) followSite(r *http.Request) ops.FollowSite {
	return ops.FollowSite{
		Site: s.Site(), PrivateKey: s.PrivateKey, BaseURL: s.BaseURL, DiscoveryURL: s.DiscoveryURL,
		DS:     s.NewAuthDSClient(r, discovery.ExtractDomainFromURL(s.GetBaseURL())),
		Remote: s.NewRemoteClient(),
		Emit:   s.LogEvent,
	}
}

// Unfollow unfollows a site in the owner's name: the signed follow list drops
// it, the discovery service is told, and comments blessed only because the
// owner followed it are turned away where the owner's rules say so. ⭐ The one
// implementation: DELETE /api/following and the API's follow.delete call it.
func (s *Server) Unfollow(r *http.Request, url string) (interface{}, int, error) {
	// ⭐ The one implementation (ops.Unfollow).
	result, err := ops.Unfollow(r.Context(), s.followSite(r), url)
	if err != nil {
		if errors.Is(err, ops.ErrFollowInvalid) {
			return nil, http.StatusBadRequest, err
		}
		s.LogError("unfollow failed: %v", err)
		return nil, followErrorStatus(err), followFailure("unfollow", discovery.ExtractDomainFromURL(url), err)
	}
	return result, http.StatusOK, nil
}

// excerptFetchIndices returns the indices of items that need an
// excerpt fetched: missing Excerpt + post/comment type + https URL.
// Pure function — no side effects. Extracted so tests can verify the
// filter logic without firing a goroutine (which would race against
// network or require sleep-and-poll patterns to assert behavior).
func excerptFetchIndices(items []feed.CachedFeedItem) []int {
	var out []int
	for i, item := range items {
		if item.Excerpt == "" && (item.Type == "post" || item.Type == "comment") && strings.HasPrefix(item.URL, "https://") {
			out = append(out, i)
		}
	}
	return out
}

// kickOffExcerptFetch scans `items` for entries missing an excerpt and,
// if any are found, launches a background fetch. Shared by:
//   - handleFeedGrouped (fallback — fires on dashboard load)
//   - feedSyncHandler.Process (primary — fires on sync, step-06/6.l)
//
// Idempotent: items that already have an Excerpt or non-https URLs
// are skipped, so calling repeatedly costs only the index scan when
// the cache is fully hydrated.
func (s *Server) kickOffExcerptFetch(cm *feed.CacheManager, items []feed.CachedFeedItem) {
	indices := excerptFetchIndices(items)
	if len(indices) > 0 {
		go s.fetchFeedExcerpts(cm, items, indices)
	}
}

// fetchFeedExcerpts fetches remote post content and caches excerpts.
// Runs in background goroutine; errors are logged, never fatal.
//
// step-06/6.l: parallelized with a 5-worker concurrency cap. Without
// the cap, a sync that brings in N new items would fire N concurrent
// remote-fetch goroutines, stampeding the origin servers. The cap
// keeps fan-out bounded while still parallelizing enough to be useful
// (serial was ~2-3 sec/item; 5-way parallel is closer to N/5 sec).
//
// step-06 final-review item #4: excerpts collected into a per-item
// map, persisted via cm.UpdateExcerpts (per-item, race-free) instead
// of cm.SaveItems (full-slice overwrite, races with parallel
// MergeItems). Items slice still owns the read snapshot, but
// persistence reads-modifies-writes under the cache mutex so
// concurrent merges aren't lost.
//
// Concurrency safety:
//   - Each worker writes to a unique key in `excerpts` map (with mu).
//   - sync.Mutex guards map writes.
//   - cm.UpdateExcerpts atomically applies updates under cm.mu.
func (s *Server) fetchFeedExcerpts(cm *feed.CacheManager, items []feed.CachedFeedItem, indices []int) {
	const maxWorkers = 5
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	excerpts := make(map[string]string, len(indices))

	client := s.NewRemoteClient()

	for _, idx := range indices {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer func() {
				// Recover from panics in fetch / parse / excerpt code so
				// one bad item doesn't crash the process. Five concurrent
				// workers means five concurrent panic-attractors; without
				// this, a single panic in any one tears down everything
				// (the dashboard, the SPA, hosted-mode siblings). LogError
				// surfaces the panic via the standard structured-event
				// pipeline.
				if r := recover(); r != nil {
					s.LogError("excerpt fetch worker panic for index %d: %v", i, r)
				}
				<-sem
				wg.Done()
			}()

			item := &items[i]
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
					return
				}
			}
			// If HTML, try markdown source
			if looksLikeHTML(content) {
				altContent, _, altErr := client.TryAlternateExtension(item.URL)
				if altErr == nil && !looksLikeHTML(altContent) {
					content = altContent
				} else {
					return // Can't extract excerpt from HTML
				}
			}
			body := stripFrontmatter(content)
			excerpt := makeExcerpt(body, feed.ExcerptCharCap)
			if excerpt != "" {
				mu.Lock()
				excerpts[item.ID] = excerpt
				mu.Unlock()
			}
		}(idx)
	}
	wg.Wait()

	if len(excerpts) > 0 {
		if applied, err := cm.UpdateExcerpts(excerpts); err != nil {
			s.LogError("failed to save feed excerpts: %v", err)
		} else {
			s.LogDebug("cached %d feed excerpts (of %d fetched)", applied, len(excerpts))
		}
	}
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

// stripFrontmatter removes YAML frontmatter (---...---) from content.
//
// It is deliberately NOT replaced by publish.StripFrontmatter, which reads
// worse on markdown polis did not write: it leaves the frontmatter of a CRLF
// file in place (its pattern needs "---\n"), so a remote post's frontmatter,
// signature line included, would render as body; and it trims trailing
// whitespace per line, dropping markdown hard breaks. Several callers here
// read other sites' content, so the duplicate stays.
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

// commentSourceURL converts a comment URL to the content source-path URL.
// A mount-path URL (/comments/<…>) is rewritten to /content/pub.polis.core/
// comment/<…>; a URL already in canonical source form is returned unchanged
// (the /comments/ segment is absent). e.g.
// "https://a.polis.pub/comments/20260222/id.md" ->
// "https://a.polis.pub/content/pub.polis.core/comment/20260222/id.md".
func commentSourceURL(commentURL string) string {
	idx := strings.Index(commentURL, "/comments/")
	if idx < 0 {
		return commentURL
	}
	return commentURL[:idx] + "/content/pub.polis.core/comment/" + commentURL[idx+len("/comments/"):]
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
		Following       int `json:"following"`
		Followers       int `json:"followers"`
		FeedUnread      int `json:"feed_unread"`
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

// ============================================================================
// WIDGET API HANDLERS
// ============================================================================

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

// widgetRequest is r marked as the widget's, so the shared function's event
// says who made the write.
func widgetRequest(r *http.Request) *http.Request {
	return r.WithContext(ops.WithRequestMeta(r.Context(), &ops.RequestMeta{Surface: ops.SurfaceWidget}))
}

// handleWidgetPublishComment handles the comment type for widget publish.
func (s *Server) handleWidgetPublishComment(w http.ResponseWriter, r *http.Request, target, text string) {
	// Both widget comment routes (comment, and publish with type comment) land here.
	if s.switchedOff(api.SwitchWidgetComments) {
		api.WriteSwitchedOff(w)
		return
	}
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

	// ⭐ The one implementation of writing a comment (ops.WriteComment): the
	// same author, generator and send as the API's comment.create.
	signed, sent, err := ops.WriteComment(ops.CommentSite{
		Site: s.Site(), PrivateKey: s.PrivateKey, BaseURL: siteURL, DiscoveryURL: s.DiscoveryURL,
	}, &comment.CommentDraft{InReplyTo: target, Content: text})
	if err != nil {
		s.LogError("widget publish comment: sign failed: %v", err)
		http.Error(w, "Failed to sign comment", http.StatusInternalServerError)
		return
	}

	blessingStatus := "pending"
	if sent.Err != nil {
		s.LogError("widget publish comment: beseech failed: %v", sent.Err)
		blessingStatus = "error"
	} else if sent.AutoBlessed {
		// Only a comment the post author had ALREADY blessed (a re-registration).
		// A new comment is always "pending": the post author's own site decides
		// it later, and the widget must not promise otherwise (Signet epic 45 D8).
		blessingStatus = "granted"
	}

	// Always re-render after beseech attempt — PublishComment() runs early inside
	// BeseechComment, so the .md is on disk even if DS registration fails or the
	// comment is still pending. Without this, the comment HTML and index.html
	// are never generated.
	if renderErr := s.RenderSite(); renderErr != nil {
		s.LogError("widget publish comment: render failed: %v", renderErr)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
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

	switch r.Method {
	case http.MethodPost:
		// ⭐ The shared follow (/api/following and /v1 use it): its checks
		// (https only, no self-follow), its feed backfill and its event, which
		// says the widget made it. The widget used to call the package itself
		// and skipped all three.
		result, status, err := s.Follow(widgetRequest(r), authorURL)
		if err != nil {
			s.LogError("widget follow failed: %v", err)
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    result,
		})

	case http.MethodDelete:
		result, status, err := s.Unfollow(widgetRequest(r), authorURL)
		if err != nil {
			s.LogError("widget unfollow failed: %v", err)
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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

const (
	maxExportFileSize  = 50 << 20  // 50MB per file
	maxExportTotalSize = 500 << 20 // 500MB total
)

// WriteZipArchive writes a ZIP archive of dataDir to w, excluding the named directories.
// Directory names in excludeDirs are relative to dataDir (e.g. "logs", ".polis/keys").
// Files exceeding maxExportFileSize are skipped. Returns an error if the total exceeds
// maxExportTotalSize.
func WriteZipArchive(w io.Writer, dataDir string, excludeDirs []string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()

	dataDir = filepath.Clean(dataDir)

	// Build a set of excluded directory paths for quick lookup
	excludeSet := make(map[string]bool, len(excludeDirs))
	for _, d := range excludeDirs {
		excludeSet[filepath.Clean(d)] = true
	}

	var totalWritten int64

	// NOT the storage interface, for this walk and the os.Open below: this is
	// the site EXPORT, which streams every file into a zip. The interface is
	// whole-file over []byte because polis artifacts are small; a whole site
	// is not one.
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

		// ⛔ REGULAR FILES ONLY. Walk reports a symlink without following it,
		// but os.Open below would: a link in one site pointing at another
		// tenant's .polis/keys/id_ed25519 put that key in this site's export.
		if !info.Mode().IsRegular() {
			return nil
		}

		// Skip files inside excluded directories (safety check)
		for _, d := range excludeDirs {
			if strings.HasPrefix(rel, d+string(filepath.Separator)) {
				return nil
			}
		}

		// Skip oversized files
		if info.Size() > maxExportFileSize {
			return nil
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

		f, err := os.Open(path) //nolint:gosec // G304: a file this walk found under the site
		if err != nil {
			return nil
		}
		defer f.Close()

		n, _ := io.Copy(writer, io.LimitReader(f, maxExportFileSize))
		totalWritten += n
		if totalWritten > maxExportTotalSize {
			return fmt.Errorf("export exceeded total size limit of %d bytes", maxExportTotalSize)
		}
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

	if err := WriteZipArchive(w, s.DataDir, []string{filepath.Join(".polis", "logs")}); err != nil {
		// The headers are sent, so the download cannot be turned into an error
		// now; it ends truncated, which the zip reader will refuse.
		s.LogWarn("site download incomplete: %v", err)
	}
}

// ============================================================================
// MESSAGES KEY HANDLERS
// ============================================================================

// handleDMKeyring returns the DM keyring's browser-safe view: epoch pubkeys + the wrapped
// DEK blobs + KDF params the SPA needs to derive a KEK from the user's password and unwrap
// a password epoch's DEK in-browser. The server-held bootstrap DEK (server_dek) is stripped
// (BrowserView) — the browser never receives the bootstrap private key.
func (s *Server) handleDMKeyring(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	kr, err := dm.LoadKeyring(s.Site())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "No DM keyring (not provisioned)", http.StatusNotFound)
			return
		}
		s.LogError("dm keyring load: %v", err)
		http.Error(w, "Failed to load keyring", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(kr.BrowserView())
}

// handleDMSetPassword performs the bootstrap→epoch-1 upgrade. The browser mints the epoch
// keypair, generates the recovery phrase, derives the password + recovery KEKs, and wraps
// the DEK twice — then POSTs only the wrapped blobs + KDF params + the epoch pubkey here.
// The server appends the epoch (CAS on revision) and re-signs/publishes the messages-key
// block. It never sees the password, KEK, DEK, or recovery phrase. Requires the current
// epoch to be bootstrap (a later change is a re-wrap — see change-password).
func (s *Server) handleDMSetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.PrivateKey == nil {
		http.Error(w, "no private key configured", http.StatusBadRequest)
		return
	}

	var req struct {
		PublicKeyMessages  string       `json:"public_key_messages"`
		WrappedDEK         string       `json:"wrapped_dek"`
		WrappedDEKRecovery string       `json:"wrapped_dek_recovery"`
		KDF                dm.KDFParams `json:"kdf"`
		RecoveryKDF        dm.KDFParams `json:"recovery_kdf"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// DM-8: serialize the whole load→mutate→SaveCAS sequence per tenant (covers the
	// SaveCAS read-back/write TOCTOU and the two-step server_dek clear below).
	s.dmKeyringMu.Lock()
	defer s.dmKeyringMu.Unlock()

	kr, err := dm.LoadKeyring(s.Site())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "No DM keyring (not provisioned)", http.StatusNotFound)
			return
		}
		s.LogError("dm set-password load: %v", err)
		http.Error(w, "Failed to load keyring", http.StatusInternalServerError)
		return
	}
	baseRev := kr.Revision

	id, err := kr.AddPasswordEpoch(req.PublicKeyMessages, req.WrappedDEK, req.WrappedDEKRecovery, req.KDF, req.RecoveryKDF)
	if err != nil {
		// "already exists" = the user already has a password epoch.
		if strings.Contains(err.Error(), "already exists") {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := kr.SaveCAS(s.Site(), baseRev); err != nil {
		if errors.Is(err, dm.ErrRevisionConflict) {
			http.Error(w, "keyring changed concurrently; reload and retry", http.StatusConflict)
			return
		}
		s.LogError("dm set-password save: %v", err)
		http.Error(w, "Failed to save keyring", http.StatusInternalServerError)
		return
	}

	// Re-sign + publish the messages-key block so the new epoch's pubkey is advertised.
	if err := site.PublishMessagesKey(s.Site(), s.PrivateKey); err != nil {
		s.LogError("dm set-password publish: %v", err)
		http.Error(w, "Failed to publish messages key", http.StatusInternalServerError)
		return
	}

	// Close the bootstrap window: re-seal prior bootstrap-epoch messages FORWARD to the new
	// password epoch (the server still holds the bootstrap DEK here, so it can), then drop
	// the operator-readable bootstrap key (`server_dek`). After this, earlier messages are
	// readable only under the user's password and the operator holds no readable DM key.
	// Best-effort: the password is already set + functional, so a failure here is logged but
	// never fails the response (it is retriable, and leaving server_dek loses no data —
	// those messages simply stay readable until the re-seal succeeds). The clear runs only
	// if the re-seal fully succeeded, so a message is never orphaned.
	if bootEp, bootErr := kr.EpochByID(id - 1); bootErr == nil && bootEp.Kind == dm.EpochKindBootstrap {
		if bootDEK, ok, derr := bootEp.ServerDEKBytes(); derr == nil && ok {
			newEp, _ := kr.CurrentEpoch()
			newPubRaw, perr := base64.StdEncoding.DecodeString(newEp.PublicKeyMessages)
			if perr == nil && len(newPubRaw) == 32 {
				var newPub [32]byte
				copy(newPub[:], newPubRaw)
				mb := dm.NewMailbox(s.Site())
				if n, rerr := mb.ReencryptBootstrapForward(bootEp.ID, bootDEK, newEp.ID, newPub); rerr != nil {
					s.LogError("dm set-password: re-seal bootstrap messages forward failed (server_dek retained, retriable): %v", rerr)
				} else {
					if kr.ClearBootstrapServerDEK() {
						if err := kr.SaveCAS(s.Site(), baseRev+1); err != nil {
							s.LogError("dm set-password: clear server_dek save failed (retriable): %v", err)
						}
					}
					s.LogInfo("dm set-password: re-sealed %d bootstrap message(s) forward to epoch %d; bootstrap key cleared", n, newEp.ID)
				}
			}
		}
	}

	s.LogEvent("pub.polis.dm.password_set", ops.RequestFields(r.Context(), map[string]any{"epoch_id": id}))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"epoch_id": id, "current_epoch": id})
}

// handleDMRewrapPassword performs the O(1) re-wrap (phase 4.8): the browser
// unwraps the current-epoch DEK with the old KEK and re-wraps it under a new
// password and/or a new recovery-phrase KEK over fresh salts, then POSTs only the
// new wrapped blob(s) + KDF params here. The server overwrites the current epoch's
// wrap material (CAS on revision). The DEK + epoch pubkey are unchanged, so — unlike
// set-password — there is NO messages-key republish. Requires an existing password
// epoch (set a password first).
func (s *Server) handleDMRewrapPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		WrappedDEK         string        `json:"wrapped_dek"`
		KDF                *dm.KDFParams `json:"kdf"`
		WrappedDEKRecovery string        `json:"wrapped_dek_recovery"`
		RecoveryKDF        *dm.KDFParams `json:"recovery_kdf"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// DM-8: serialize per-tenant keyring mutation (see set-password).
	s.dmKeyringMu.Lock()
	defer s.dmKeyringMu.Unlock()

	kr, err := dm.LoadKeyring(s.Site())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "No DM keyring (not provisioned)", http.StatusNotFound)
			return
		}
		s.LogError("dm rewrap load: %v", err)
		http.Error(w, "Failed to load keyring", http.StatusInternalServerError)
		return
	}
	baseRev := kr.Revision

	if err := kr.RewrapCurrentEpoch(req.WrappedDEK, req.KDF, req.WrappedDEKRecovery, req.RecoveryKDF); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := kr.SaveCAS(s.Site(), baseRev); err != nil {
		if errors.Is(err, dm.ErrRevisionConflict) {
			http.Error(w, "keyring changed concurrently; reload and retry", http.StatusConflict)
			return
		}
		s.LogError("dm rewrap save: %v", err)
		http.Error(w, "Failed to save keyring", http.StatusInternalServerError)
		return
	}

	s.LogEvent("pub.polis.dm.password_change", ops.RequestFields(r.Context(), map[string]any{"revision": kr.Revision}))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "revision": kr.Revision})
}

// handleDMRecipientKey resolves a recipient's verified X25519 messages key so the
// SPA can seal a password-epoch box to it in-browser (phase 4.6). The server does
// the fetch + signature verification (it holds no secret here); the browser holds
// the sender's DEK and does the sealing. Read-only.
func (s *Server) handleDMRecipientKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.PrivateKey == nil {
		http.Error(w, "no private key configured", http.StatusBadRequest)
		return
	}
	recipientURL := r.URL.Query().Get("recipient_url")
	if recipientURL == "" {
		http.Error(w, "recipient_url is required", http.StatusBadRequest)
		return
	}
	if _, err := url.Parse(recipientURL); err != nil {
		http.Error(w, "Invalid recipient URL", http.StatusBadRequest)
		return
	}
	if !s.allowPrivateDMRecipients() {
		if err := dm.ValidateRecipientURL(recipientURL); err != nil {
			http.Error(w, "Invalid recipient URL: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	domain := s.GetBaseURL()
	if domain == "" {
		http.Error(w, "Site base URL not configured", http.StatusBadRequest)
		return
	}
	sender := dm.NewSenderWithHTTP(s.PrivateKey, s.PublicKey, dm.ExtractDomainFromURL(domain), s.Site(), s.dmHTTPClient())
	sender.Logger = dm.EventFunc(s.LogEvent)
	pubB64, epoch, err := sender.ResolveRecipientKey(recipientURL)
	if err != nil {
		// Recipient unreachable / no published key / bad signature — the SPA
		// can't seal to them. 502: the failure is upstream, not the request.
		s.LogWarn("dm recipient-key resolve failed (%s): %v", recipientURL, err)
		http.Error(w, "Could not resolve recipient's message key: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"public_key_messages": pubB64,
		"recipient_epoch":     epoch,
	})
}

// handleDMSendSealed delivers a message whose boxes were sealed in the browser
// from the sender's unlocked password-epoch DEK (phase 4.6). The server seals
// nothing — it builds the wire envelope from the browser's delivery box, signs
// the instance-to-instance deliver request, and stores the sender's pre-sealed
// box-to-self copy. Bootstrap-epoch sends still go through handleDMSend.
func (s *Server) handleDMSendSealed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.PrivateKey == nil {
		http.Error(w, "no private key configured", http.StatusBadRequest)
		return
	}
	var req struct {
		RecipientURL   string `json:"recipient_url"`
		SenderEpoch    int    `json:"sender_epoch"`
		RecipientEpoch int    `json:"recipient_epoch"`
		BoxPub         string `json:"box_pub"`
		Delivery       struct {
			Ciphertext string `json:"ciphertext"`
			Nonce      string `json:"nonce"`
		} `json:"delivery"`
		Sent struct {
			Ciphertext string `json:"ciphertext"`
			Nonce      string `json:"nonce"`
		} `json:"sent"`
		ReplyToID string `json:"reply_to_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.handleBodyTooLarge(w, r, err) {
			return
		}
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.RecipientURL == "" || req.BoxPub == "" ||
		req.Delivery.Ciphertext == "" || req.Delivery.Nonce == "" ||
		req.Sent.Ciphertext == "" || req.Sent.Nonce == "" {
		http.Error(w, "recipient_url, box_pub, delivery and sent boxes are required", http.StatusBadRequest)
		return
	}
	if _, err := url.Parse(req.RecipientURL); err != nil {
		http.Error(w, "Invalid recipient URL", http.StatusBadRequest)
		return
	}
	if !s.allowPrivateDMRecipients() {
		if err := dm.ValidateRecipientURL(req.RecipientURL); err != nil {
			http.Error(w, "Invalid recipient URL: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	domain := s.GetBaseURL()
	if domain == "" {
		http.Error(w, "Site base URL not configured", http.StatusBadRequest)
		return
	}
	sender := dm.NewSenderWithHTTP(s.PrivateKey, s.PublicKey, dm.ExtractDomainFromURL(domain), s.Site(), s.dmHTTPClient())
	sender.Logger = dm.EventFunc(s.LogEvent)
	msg, err := sender.SendSealed(req.RecipientURL, req.SenderEpoch, req.RecipientEpoch, req.BoxPub,
		dm.SealedBox{Ciphertext: req.Delivery.Ciphertext, Nonce: req.Delivery.Nonce},
		dm.SealedBox{Ciphertext: req.Sent.Ciphertext, Nonce: req.Sent.Nonce}, req.ReplyToID)
	if err != nil {
		if msg != nil {
			// Saved as "unsent" — delivery failed but the box is stored.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message_id": msg.ID,
				"status":     msg.Status,
				"warning":    err.Error(),
			})
			return
		}
		s.LogError("dm send-sealed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message_id": msg.ID,
		"status":     msg.Status,
	})
}

// handleDMProtectionStatus exposes a recipient's signed protection_status to the
// SPA (phase 4.10). The browser can't make the signed, mutuals-gated query — the
// sender's SERVER does it (Sender.FetchProtectionStatus verifies the recipient's
// identity-key signature). Any failure (recipient unreachable, not a mutual, bad
// signature) collapses to {status:"unknown"} so the SPA can soft-warn ("couldn't
// confirm…") rather than mislead. A clean answer returns the single protected bit.
func (s *Server) handleDMProtectionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.PrivateKey == nil {
		http.Error(w, "no private key configured", http.StatusBadRequest)
		return
	}
	recipientURL := r.URL.Query().Get("recipient_url")
	if recipientURL == "" {
		http.Error(w, "recipient_url is required", http.StatusBadRequest)
		return
	}
	if _, err := url.Parse(recipientURL); err != nil {
		http.Error(w, "Invalid recipient URL", http.StatusBadRequest)
		return
	}
	if !s.allowPrivateDMRecipients() {
		if err := dm.ValidateRecipientURL(recipientURL); err != nil {
			http.Error(w, "Invalid recipient URL: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	domain := s.GetBaseURL()
	if domain == "" {
		http.Error(w, "Site base URL not configured", http.StatusBadRequest)
		return
	}
	sender := dm.NewSenderWithHTTP(s.PrivateKey, s.PublicKey, dm.ExtractDomainFromURL(domain), s.Site(), s.dmHTTPClient())
	sender.Logger = dm.EventFunc(s.LogEvent)
	ps, err := sender.FetchProtectionStatus(recipientURL)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		// Not an HTTP error: "unknown" is a legitimate UX state (the SPA warns softly).
		s.LogDebug("dm protection-status unknown for %s: %v", recipientURL, err)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "unknown"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"protected": ps.Protected,
		"domain":    ps.Domain,
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

	f, err := following.LoadSite(s.Site())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"recipients": []interface{}{},
		})
		return
	}

	myDomain := discovery.ExtractDomainFromURL(s.GetBaseURL())

	type recipient struct {
		Domain     string               `json:"domain"`
		URL        string               `json:"url"`
		AuthorName string               `json:"author_name,omitempty"`
		Status     string               `json:"status"`           // "open", "no-dm", "no-follow", "unknown"
		Reason     string               `json:"reason,omitempty"` // human-readable explanation
		FollowsUs  bool                 `json:"follows_us"`       // whether they follow us
		Avatar     *render.AvatarConfig `json:"avatar,omitempty"` // custom avatar, or deterministic hue fallback
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

	// Resolve each recipient's avatar for the composer list. Uses the shared
	// 6h author-avatar cache (commentAuthorAvatar → render.LoadOrFetchAuthorAvatar)
	// — the same cache the stream warms when it renders these authors, so this
	// is almost always cache hits with no network. Cold entries fetch
	// /.well-known/polis once; bounded to 5-wide concurrency so opening the
	// composer never serializes N well-known fetches. commentAuthorAvatar always
	// returns a non-nil config (custom avatar, or a deterministic hue fallback),
	// so the client renders a colored avatar for everyone.
	var avatarWG sync.WaitGroup
	avatarSem := make(chan struct{}, 5)
	for i := range results {
		avatarWG.Add(1)
		go func(i int) {
			defer avatarWG.Done()
			avatarSem <- struct{}{}
			defer func() { <-avatarSem }()
			results[i].Avatar = s.commentAuthorAvatar(results[i].Domain)
		}(i)
	}
	avatarWG.Wait()

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
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
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

// getCommentCountCacheEntry returns the cached count for a post URL, or
// (0, false) if expired or missing. Lazy expiry on read.
func (s *Server) getCommentCountCacheEntry(url string) (int, bool) {
	s.commentCountCacheMu.Lock()
	defer s.commentCountCacheMu.Unlock()
	if s.commentCountCache == nil {
		return 0, false
	}
	entry, ok := s.commentCountCache[url]
	if !ok || time.Now().After(entry.ExpiresAt) {
		if ok {
			delete(s.commentCountCache, url)
		}
		return 0, false
	}
	return entry.Total, true
}

// setCommentCountCacheEntries stores a batch of post-URL → count mappings.
// Single-mutex acquisition for a batch is cheaper than locking per entry.
func (s *Server) setCommentCountCacheEntries(counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	s.commentCountCacheMu.Lock()
	defer s.commentCountCacheMu.Unlock()
	if s.commentCountCache == nil {
		s.commentCountCache = make(map[string]*commentCountCacheEntry, len(counts))
	}
	expiresAt := time.Now().Add(commentCountCacheTTL)
	for url, total := range counts {
		s.commentCountCache[url] = &commentCountCacheEntry{
			Total:     total,
			ExpiresAt: expiresAt,
		}
	}
}

// syncFollowerState updates local follower state if remote data disagrees.
func (s *Server) syncFollowerState(remoteDomain string, theyFollowUs bool) {
	dsDomain := discovery.ExtractDomainFromURL(s.DiscoveryURL)
	if dsDomain == "" {
		return
	}
	store := stream.NewStore(s.Site(), dsDomain, "pub.polis.core")
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
		if err := store.SaveState("pub.polis.follow", &fs); err != nil {
			s.LogWarn("follower state for %s not saved: %v", remoteLower, err)
		}
	} else if !theyFollowUs && isLocal {
		filtered := fs.Followers[:0]
		for _, f := range fs.Followers {
			if strings.ToLower(f) != remoteLower {
				filtered = append(filtered, f)
			}
		}
		fs.Followers = filtered
		fs.Count = len(fs.Followers)
		if err := store.SaveState("pub.polis.follow", &fs); err != nil {
			s.LogWarn("follower state for %s not saved: %v", remoteLower, err)
		}
	}
}

// siteKey joins site-relative elements into a key. It does no filesystem
// work: the store refuses a key that escapes the site, as the RelKey of a
// joined path did.
func siteKey(elem ...string) string {
	return filepath.ToSlash(filepath.Join(elem...))
}

// The key-addressed file helpers: each goes through the site's own store, so
// it reaches wherever the site is stored.
func (s *Server) readKey(key string) ([]byte, error) {
	return s.Site().Store().Get(context.Background(), key)
}

func (s *Server) writeKey(key string, data []byte, perm fs.FileMode) error {
	return s.Site().Store().Put(context.Background(), key, data, storage.WithMode(perm))
}

// listKey lists the direct children of key. A missing prefix yields no
// entries, which is how every caller here already treated one.
func (s *Server) listKey(key string) []storage.Entry {
	entries, err := s.Site().Store().List(context.Background(), key)
	if err != nil {
		return nil
	}
	return entries
}
