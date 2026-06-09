package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// protectionEnv builds a HandlerEnv for a responder site with a real identity key, a
// keyring containing a password epoch (so it reports protected), and a following.json
// listing the given followed domains.
func protectionEnv(t *testing.T, followed ...string) (HandlerEnv, []byte) {
	t.Helper()
	siteDir := t.TempDir()
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	// Keyring with a password epoch → protected.
	kr := &dm.Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := kr.SetPassword([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if err := kr.Save(dm.DMDir(siteDir)); err != nil {
		t.Fatal(err)
	}

	// following.json listing followed domains.
	var entries []following.FollowingEntry
	for _, d := range followed {
		entries = append(entries, following.FollowingEntry{URL: "https://" + d})
	}
	fpath := following.DefaultPath(siteDir)
	if err := os.MkdirAll(filepath.Dir(fpath), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(following.FollowingFile{Version: "1.0", Following: entries})
	if err := os.WriteFile(fpath, data, 0600); err != nil {
		t.Fatal(err)
	}

	env := HandlerEnv{
		Resolver:   resolve.New(siteDir, map[string]*bundle.Bundle{}),
		PrivateKey: privPEM,
		PublicKey:  pubSSH,
		BaseURL:    "https://bob.example.com",
	}
	return env, pubSSH
}

func TestProtectionStatusDM_FollowerGetsSignedAnswer(t *testing.T) {
	env, pubSSH := protectionEnv(t, "alice.example.com")
	h := &BuiltinCoreHandler{}

	res, err := h.protectionStatusDM(ActionRequest{
		Action:      "protection_status",
		ContentType: "pub.polis.dm",
		Payload:     map[string]any{"sender_domain": "alice.example.com"},
	}, env)
	if err != nil {
		t.Fatalf("protectionStatusDM: %v", err)
	}

	if protected, _ := res.Data["protected"].(bool); !protected {
		t.Error("expected protected=true")
	}
	ps := dm.ProtectionStatus{
		Domain:    res.Data["domain"].(string),
		Protected: res.Data["protected"].(bool),
		At:        res.Data["at"].(string),
		Sig:       res.Data["sig"].(string),
	}
	if ps.Domain != "bob.example.com" {
		t.Errorf("domain = %q, want bob.example.com", ps.Domain)
	}
	ok, err := dm.VerifyProtectionStatus(ps, pubSSH)
	if err != nil || !ok {
		t.Errorf("answer should verify against the responder identity key: ok=%v err=%v", ok, err)
	}
}

func TestProtectionStatusDM_NonFollowerForbidden(t *testing.T) {
	env, _ := protectionEnv(t, "alice.example.com")
	h := &BuiltinCoreHandler{}

	_, err := h.protectionStatusDM(ActionRequest{
		Action:      "protection_status",
		ContentType: "pub.polis.dm",
		Payload:     map[string]any{"sender_domain": "carol.example.com"},
	}, env)
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("non-follower: got err=%v, want a forbidden error", err)
	}
}

func TestProtectionStatusDM_MissingCaller(t *testing.T) {
	env, _ := protectionEnv(t)
	h := &BuiltinCoreHandler{}

	_, err := h.protectionStatusDM(ActionRequest{
		Action:      "protection_status",
		ContentType: "pub.polis.dm",
		Payload:     map[string]any{},
	}, env)
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing caller: got err=%v, want a required error", err)
	}
}
