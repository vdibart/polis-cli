package owner

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ⛔⛔ A CONTENT SIGNATURE IS NEVER A RECOVERY PROOF. Every content signature
// is made under the "file" namespace, and something that signs content for its
// caller (the render preview once did) could be handed a challenge message. A
// recovery proof is verified only under its own namespace.
func TestAContentSignatureOverTheChallengeIsNotARecoveryProof(t *testing.T) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRecovery(string(pub), "alice.example")
	if err != nil {
		t.Fatal(err)
	}
	inst := remoteInstance(t)
	ch, err := r.Issue(inst)
	if err != nil {
		t.Fatal(err)
	}
	content, err := signing.SignContent([]byte(ch.Message), priv) // namespace "file"
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(inst, ch.Nonce, content); err == nil {
		t.Fatal("⛔ a content-namespace signature over the challenge verified as a recovery proof")
	}
	ch2, _ := r.Issue(inst)
	proof, _ := signing.SignContentNS([]byte(ch2.Message), priv, RecoveryNamespace)
	if err := r.Verify(inst, ch2.Nonce, proof); err != nil {
		t.Errorf("a proper recovery proof was refused: %v", err)
	}
	if ch2.Namespace != RecoveryNamespace || ch2.SignWith == "" {
		t.Errorf("the challenge does not tell the owner how to sign: %+v", ch2)
	}
}
