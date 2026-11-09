package owner

import (
	"errors"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ⭐ The owner's signer, against the instance's verifier: a challenge this
// instance issued, signed by SignChallenge, is accepted, and only once.
func TestSignChallengeIsAcceptedByVerify(t *testing.T) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRecovery(string(pub), "https://alice.example")
	if err != nil {
		t.Fatal(err)
	}
	inst := remoteInstance(t)
	ch, err := r.Issue(inst)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := SignChallenge([]byte(ch.Message), priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(inst, ch.Nonce, sig); err != nil {
		t.Fatalf("a SignChallenge proof was refused: %v", err)
	}
	if err := r.Verify(inst, ch.Nonce, sig); err == nil {
		t.Error("the proof verified twice")
	}
}

// ⛔ Only a challenge message is signed: the signer must never be a general
// signer under the recovery namespace.
func TestSignChallengeRefusesAnythingElse(t *testing.T) {
	priv, _, _ := signing.GenerateKeypair()
	for _, in := range []string{
		"",
		"hello",
		"pub.polis.recovery.challenge.v1\n",
		"pub.polis.recovery.challenge.v1\nhttps://a.example\n",
		"pub.polis.recovery.challenge.v2\nhttps://a.example\nn\n",
		"pub.polis.recovery.challenge.v1\nhttps://a.example\nn\nextra\n",
		"pub.polis.recovery.challenge.v1\nhttps://a example\nn\n",
		`{"message":""}`,
		`{"nonce":"x"}`,
	} {
		if _, err := SignChallenge([]byte(in), priv); !errors.Is(err, ErrNotAChallenge) {
			t.Errorf("SignChallenge(%q) err = %v, want ErrNotAChallenge", in, err)
		}
	}
}
