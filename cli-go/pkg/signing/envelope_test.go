package signing

import (
	"crypto/ed25519"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeZeroedBare reproduces exactly what polis-cli-go 0.59.0 emitted: the real
// 64-byte signature with an all-zero embedded public key.
func makeZeroedBare(t *testing.T, realBare string) string {
	t.Helper()
	raw, err := decodeBareSignature(realBare)
	if err != nil {
		t.Fatalf("decode real bare: %v", err)
	}
	_, rawSig, err := walkEnvelope(raw)
	if err != nil {
		t.Fatalf("walk real envelope: %v", err)
	}
	zeroPEM, err := formatSSHSignature(make(ed25519.PublicKey, ed25519.PublicKeySize), rawSig)
	if err != nil {
		t.Fatalf("format zeroed: %v", err)
	}
	return bareFromPEM(zeroPEM)
}

func signBare(t *testing.T, content, privPEM []byte) string {
	t.Helper()
	pem, err := SignContent(content, privPEM)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}
	return bareFromPEM(pem)
}

func TestEnvelope_DetectAndRepair(t *testing.T) {
	content := []byte("envelope repair test content")
	privPEM, pubOpenSSH, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	priv, err := ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	realPub := priv.Public().(ed25519.PublicKey)

	realBare := signBare(t, content, privPEM)
	zeroBare := makeZeroedBare(t, realBare)

	// DETECT
	if z, err := IsZeroedEnvelope(realBare); err != nil || z {
		t.Errorf("real bare: IsZeroedEnvelope = %v, %v; want false, nil", z, err)
	}
	if z, err := IsZeroedEnvelope(zeroBare); err != nil || !z {
		t.Errorf("zeroed bare: IsZeroedEnvelope = %v, %v; want true, nil", z, err)
	}
	if got, err := EmbeddedKey(realBare); err != nil || !got.Equal(realPub) {
		t.Errorf("EmbeddedKey(real) mismatch: err=%v", err)
	}

	// REPAIR: zeroed -> corrected, byte-identical to the correctly-signed original.
	fixed, changed, err := RepairEnvelope(zeroBare, realPub)
	if err != nil {
		t.Fatalf("RepairEnvelope: %v", err)
	}
	if !changed {
		t.Error("RepairEnvelope on zeroed: changed=false, want true")
	}
	if fixed != realBare {
		t.Error("repaired bare != correctly-signed original (should be byte-identical)")
	}
	if z, _ := IsZeroedEnvelope(fixed); z {
		t.Error("repaired bare still zeroed")
	}
	if got, _ := EmbeddedKey(fixed); !got.Equal(realPub) {
		t.Error("repaired embedded key != real key")
	}

	// The 64-byte signature must be preserved byte-for-byte.
	_, zeroSig, _ := walkEnvelope(mustDecode(t, zeroBare))
	_, fixedSig, _ := walkEnvelope(mustDecode(t, fixed))
	if string(zeroSig) != string(fixedSig) {
		t.Error("raw 64-byte signature changed during repair")
	}

	// The repaired signature still verifies the content against the real key.
	if ok, err := VerifySignature(content, pubOpenSSH, bareToPEM(fixed)); err != nil || !ok {
		t.Errorf("repaired signature does not verify: ok=%v err=%v", ok, err)
	}

	// Idempotency: repairing an already-correct signature is a no-op.
	again, changed2, err := RepairEnvelope(fixed, realPub)
	if err != nil || changed2 || again != fixed {
		t.Errorf("repair not idempotent: changed=%v err=%v", changed2, err)
	}
	if _, changed3, _ := RepairEnvelope(realBare, realPub); changed3 {
		t.Error("repair of already-correct original reported changed=true")
	}
}

// TestEnvelope_SSHKeygenAcceptance is the interop assertion the whole remediation
// exists for: standard ssh-keygen rejects the zeroed envelope and accepts the
// repaired one. Skips if ssh-keygen is unavailable.
func TestEnvelope_SSHKeygenAcceptance(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	content := []byte("interop acceptance content")
	privPEM, pubOpenSSH, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	priv, _ := ParsePrivateKey(privPEM)
	realPub := priv.Public().(ed25519.PublicKey)

	realBare := signBare(t, content, privPEM)
	zeroBare := makeZeroedBare(t, realBare)
	fixed, _, err := RepairEnvelope(zeroBare, realPub)
	if err != nil {
		t.Fatalf("RepairEnvelope: %v", err)
	}

	dir := t.TempDir()
	msg := filepath.Join(dir, "msg")
	if err := os.WriteFile(msg, content, 0o644); err != nil {
		t.Fatal(err)
	}
	// allowed_signers: "<principal> <keytype> <base64> [comment]"
	allowed := filepath.Join(dir, "allowed_signers")
	if err := os.WriteFile(allowed, []byte("signer "+strings.TrimSpace(string(pubOpenSSH))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	verify := func(bare string) bool {
		sigFile := filepath.Join(dir, "s.sig")
		if err := os.WriteFile(sigFile, []byte(bareToPEM(bare)), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", "signer", "-n", "file", "-s", sigFile)
		in, _ := os.Open(msg)
		defer in.Close()
		cmd.Stdin = in
		return cmd.Run() == nil
	}

	if verify(zeroBare) {
		t.Error("ssh-keygen accepted the ZEROED envelope (should reject)")
	}
	if !verify(fixed) {
		t.Error("ssh-keygen rejected the REPAIRED envelope (should accept)")
	}
}

func mustDecode(t *testing.T, bare string) []byte {
	t.Helper()
	raw, err := decodeBareSignature(bare)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return raw
}

// bareToPEM re-armors a bare signature (test helper / inverse of bareFromPEM).
func bareToPEM(bare string) string {
	var b strings.Builder
	b.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	for i := 0; i < len(bare); i += 70 {
		end := i + 70
		if end > len(bare) {
			end = len(bare)
		}
		b.WriteString(bare[i:end])
		b.WriteString("\n")
	}
	b.WriteString("-----END SSH SIGNATURE-----\n")
	return b.String()
}
