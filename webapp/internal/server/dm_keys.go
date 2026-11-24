package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// The messages key's last two moves (v5 spec 2.3, gap M2), both done in the
// owner's browser, which sends only wrapped blobs: a fresh key when both the
// password and the recovery phrase are lost, and wrapping an earlier key under
// the current password so its messages stay open ("Keep unlocked").

// handleDMNewKey starts a fresh key: a new epoch becomes current, its key is
// published, and every earlier epoch keeps its wraps (its messages wait,
// sealed, for the old password or phrase).
func (s *Server) handleDMNewKey(w http.ResponseWriter, r *http.Request) {
	if s.PrivateKey == nil {
		writeJSONError(w, http.StatusBadRequest, "this site has no key to sign its messages key with")
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
		writeJSONError(w, http.StatusBadRequest, "body must be the new epoch, as the owner's app builds it")
		return
	}
	s.dmKeyringMu.Lock()
	defer s.dmKeyringMu.Unlock()
	kr, ok := s.loadKeyringFor(w)
	if !ok {
		return
	}
	baseRev := kr.Revision
	id, err := kr.AddFreshPasswordEpoch(req.PublicKeyMessages, req.WrappedDEK, req.WrappedDEKRecovery, req.KDF, req.RecoveryKDF)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.saveKeyringFor(w, kr, baseRev) {
		return
	}
	// Advertise the new key, signed, so senders seal to it from now on.
	if err := site.PublishMessagesKey(s.Site(), s.PrivateKey); err != nil {
		s.LogError("dm new key publish: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "the fresh key is kept, but it could not be published just now; try again")
		return
	}
	s.LogEvent("pub.polis.dm.new_key", map[string]interface{}{"epoch": id})
	writeJSON(w, map[string]interface{}{"epoch_id": id, "current_epoch": id})
}

// handleDMRewrapEarlier wraps an earlier epoch's key again (in practice under
// the current password). Only that epoch's password wrap changes.
func (s *Server) handleDMRewrapEarlier(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Epoch      *int          `json:"epoch"`
		WrappedDEK string        `json:"wrapped_dek"`
		KDF        *dm.KDFParams `json:"kdf"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Epoch == nil {
		writeJSONError(w, http.StatusBadRequest, `body must be JSON: {"epoch": n, "wrapped_dek": "...", "kdf": {...}}`)
		return
	}
	s.dmKeyringMu.Lock()
	defer s.dmKeyringMu.Unlock()
	kr, ok := s.loadKeyringFor(w)
	if !ok {
		return
	}
	baseRev := kr.Revision
	if err := kr.RewrapEarlierEpoch(*req.Epoch, req.WrappedDEK, req.KDF); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.saveKeyringFor(w, kr, baseRev) {
		return
	}
	s.LogEvent("pub.polis.dm.earlier_rewrapped", ops.RequestFields(r.Context(), map[string]any{"epoch": *req.Epoch, "revision": kr.Revision}))
	writeJSON(w, map[string]interface{}{"epoch": *req.Epoch, "revision": kr.Revision})
}

func (s *Server) loadKeyringFor(w http.ResponseWriter) (*dm.Keyring, bool) {
	kr, err := dm.LoadKeyring(s.Site())
	if errors.Is(err, fs.ErrNotExist) {
		writeJSONError(w, http.StatusNotFound, "this site has no messages key yet")
		return nil, false
	}
	if err != nil {
		s.LogError("dm keyring load: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "could not read the messages key just now")
		return nil, false
	}
	return kr, true
}

func (s *Server) saveKeyringFor(w http.ResponseWriter, kr *dm.Keyring, baseRev int) bool {
	if err := kr.SaveCAS(s.Site(), baseRev); err != nil {
		if errors.Is(err, dm.ErrRevisionConflict) {
			writeJSONError(w, http.StatusConflict, "the messages key changed meanwhile; reload and try again")
			return false
		}
		s.LogError("dm keyring save: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "could not save the messages key just now")
		return false
	}
	return true
}
