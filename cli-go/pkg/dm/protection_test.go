package dm

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestIsProtected(t *testing.T) {
	// No keyring → not provisioned → unprotected.
	empty := t.TempDir()
	if got, err := IsProtected(empty); err != nil || got {
		t.Errorf("no keyring: got protected=%v err=%v, want false/nil", got, err)
	}

	// Bootstrap-only keyring → still unprotected (operator-readable by design).
	bootOnly := t.TempDir()
	kr := &Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	if err := kr.Save(DMDir(bootOnly)); err != nil {
		t.Fatal(err)
	}
	if got, err := IsProtected(bootOnly); err != nil || got {
		t.Errorf("bootstrap-only: got protected=%v err=%v, want false/nil", got, err)
	}

	// Password epoch → protected.
	withPw := t.TempDir()
	kr2 := &Keyring{}
	if _, err := kr2.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := kr2.SetPassword([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if err := kr2.Save(DMDir(withPw)); err != nil {
		t.Fatal(err)
	}
	if got, err := IsProtected(withPw); err != nil || !got {
		t.Errorf("password epoch: got protected=%v err=%v, want true/nil", got, err)
	}
}

func TestSignAndVerifyProtectionStatus(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	ps, err := SignProtectionStatus("bob.example.com", true, "2026-05-30T00:00:00Z", privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	ok, err := VerifyProtectionStatus(*ps, pubSSH)
	if err != nil || !ok {
		t.Fatalf("verify good: ok=%v err=%v", ok, err)
	}

	// Flipping the protected bit invalidates the signature.
	tampered := *ps
	tampered.Protected = false
	if ok, _ := VerifyProtectionStatus(tampered, pubSSH); ok {
		t.Error("tampered protected bit should not verify")
	}
	// Changing the timestamp invalidates the signature.
	tampered2 := *ps
	tampered2.At = "2099-01-01T00:00:00Z"
	if ok, _ := VerifyProtectionStatus(tampered2, pubSSH); ok {
		t.Error("tampered timestamp should not verify")
	}
	// A different identity key must not verify.
	_, otherPub, _ := signing.GenerateKeypair()
	if ok, _ := VerifyProtectionStatus(*ps, otherPub); ok {
		t.Error("answer should not verify against a different identity key")
	}
}

func TestBuildProtectionStatus(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, pubSSH, _ := signing.GenerateKeypair()
	kr := &Keyring{}
	kr.AddBootstrapEpoch()
	if _, _, err := kr.SetPassword([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if err := kr.Save(DMDir(siteDir)); err != nil {
		t.Fatal(err)
	}
	following := map[string]bool{"alice.example.com": true}

	// A caller in the follow relationship gets a signed, protected answer.
	ps, err := BuildProtectionStatus(siteDir, "bob.example.com", privPEM, "Alice.Example.com", following)
	if err != nil {
		t.Fatalf("follower build: %v", err)
	}
	if !ps.Protected {
		t.Error("expected protected=true (keyring has a password epoch)")
	}
	if ok, err := VerifyProtectionStatus(*ps, pubSSH); err != nil || !ok {
		t.Errorf("built answer should verify: ok=%v err=%v", ok, err)
	}

	// A non-follower is refused with the sentinel (handler maps to 403/404).
	if _, err := BuildProtectionStatus(siteDir, "bob.example.com", privPEM, "carol.example.com", following); !errors.Is(err, ErrProtectionStatusForbidden) {
		t.Errorf("non-follower: got err=%v, want ErrProtectionStatusForbidden", err)
	}
}

func TestFetchProtectionStatus_RoundTrip(t *testing.T) {
	recipientPriv, recipientPub, _ := signing.GenerateKeypair()
	kr := &Keyring{}
	kr.AddBootstrapEpoch()
	block, err := BuildMessagesKeyBlock(kr, recipientPriv)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"public_key":          string(recipientPub),
				"public_key_messages": block,
			})
		case "/v1/content/dm/actions/protection_status":
			ps, _ := SignProtectionStatus("recipient.example.com", true, nowRFC3339(), recipientPriv)
			json.NewEncoder(w).Encode(ps)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sender, _ := testSender(t)
	ps, err := sender.FetchProtectionStatus(srv.URL)
	if err != nil {
		t.Fatalf("FetchProtectionStatus: %v", err)
	}
	if !ps.Protected {
		t.Error("expected protected=true")
	}
}

func TestFetchProtectionStatus_RejectsForgedAnswer(t *testing.T) {
	// The well-known advertises recipientPub, but the answer is signed by an unrelated
	// key (an operator in the path trying to forge a reassuring "protected").
	recipientPriv, recipientPub, _ := signing.GenerateKeypair()
	forgerPriv, _, _ := signing.GenerateKeypair()
	kr := &Keyring{}
	kr.AddBootstrapEpoch()
	block, _ := BuildMessagesKeyBlock(kr, recipientPriv)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/polis":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"public_key":          string(recipientPub),
				"public_key_messages": block,
			})
		case "/v1/content/dm/actions/protection_status":
			ps, _ := SignProtectionStatus("recipient.example.com", true, nowRFC3339(), forgerPriv)
			json.NewEncoder(w).Encode(ps)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sender, _ := testSender(t)
	if _, err := sender.FetchProtectionStatus(srv.URL); err == nil {
		t.Fatal("expected rejection of an answer not signed by the recipient identity key")
	}
}
