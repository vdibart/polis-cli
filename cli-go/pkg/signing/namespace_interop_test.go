package signing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A signature made by SignContentNS is a standard SSHSIG under that namespace:
// ssh-keygen accepts it under the namespace and refuses it under "file", and
// VerifySignature (namespace "file") refuses it too. So the owner can check a
// recovery proof by hand, and a recovery proof never passes as content.
// Skips if ssh-keygen is unavailable.
func TestSignContentNSInteropsWithSSHKeygen(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	const ns = "polis-recovery"
	content := []byte("pub.polis.recovery.challenge.v1\nhttps://alice.example\nnonce\n")
	priv, pub, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := SignContentNS(content, priv, ns)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	msg := filepath.Join(dir, "msg")
	sigFile := filepath.Join(dir, "msg.sig")
	allowed := filepath.Join(dir, "allowed_signers")
	for p, b := range map[string]string{
		msg:     string(content),
		sigFile: sig,
		allowed: "owner " + strings.TrimSpace(string(pub)) + "\n",
	} {
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	verify := func(namespace string) bool {
		cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", "owner", "-n", namespace, "-s", sigFile)
		in, err := os.Open(msg)
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		cmd.Stdin = in
		return cmd.Run() == nil
	}
	if !verify(ns) {
		t.Errorf("ssh-keygen -Y verify -n %s refused the signature", ns)
	}
	if verify("file") {
		t.Error("⛔ ssh-keygen accepted the signature under the content namespace")
	}
	if ok, _ := VerifySignature(content, pub, sig); ok {
		t.Error("⛔ VerifySignature (namespace file) accepted a recovery-namespace signature")
	}
	if ok, err := VerifySignatureNS(content, pub, sig, ns); err != nil || !ok {
		t.Errorf("VerifySignatureNS refused its own namespace: %v", err)
	}
}
