package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// virtualAuthenticator is a software passkey: enough of a WebAuthn
// authenticator to register and assert with real signatures, so a test can run
// the whole ceremony through the real routes instead of writing the record a
// ceremony would leave behind.
type virtualAuthenticator struct {
	t       *testing.T
	key     *ecdsa.PrivateKey
	credID  []byte
	rpID    string
	origin  string
	counter uint32
	userID  []byte
}

func newVirtualAuthenticator(t *testing.T, origin, rpID string) *virtualAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &virtualAuthenticator{t: t, key: key, credID: id, rpID: rpID, origin: origin}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (a *virtualAuthenticator) clientData(kind string, challenge []byte) []byte {
	cd, _ := json.Marshal(map[string]any{
		"type": kind, "challenge": b64(challenge), "origin": a.origin, "crossOrigin": false,
	})
	return cd
}

// flags: user present + user verified.
const vaFlags = 0x01 | 0x04

func (a *virtualAuthenticator) authData(attested bool) []byte {
	rp := sha256.Sum256([]byte(a.rpID))
	out := append([]byte{}, rp[:]...)
	flags := byte(vaFlags)
	if attested {
		flags |= 0x40
	}
	out = append(out, flags)
	a.counter++
	var ctr [4]byte
	binary.BigEndian.PutUint32(ctr[:], a.counter)
	out = append(out, ctr[:]...)
	if attested {
		out = append(out, make([]byte, 16)...) // AAGUID
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(a.credID)))
		out = append(out, l[:]...)
		out = append(out, a.credID...)
		pub := webauthncose.EC2PublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
			Curve:         int64(webauthncose.P256),
			XCoord:        pad32(a.key.X.Bytes()),
			YCoord:        pad32(a.key.Y.Bytes()),
		}
		cose, err := webauthncbor.Marshal(pub)
		if err != nil {
			a.t.Fatal(err)
		}
		out = append(out, cose...)
	}
	return out
}

func pad32(b []byte) []byte {
	if len(b) >= 32 {
		return b
	}
	return append(make([]byte, 32-len(b)), b...)
}

// register answers a registration ceremony's options with a creation response.
func (a *virtualAuthenticator) register(options []byte) []byte {
	a.t.Helper()
	var cc protocol.CredentialCreation
	if err := json.Unmarshal(options, &cc); err != nil {
		a.t.Fatalf("registration options: %v (%s)", err, options)
	}
	switch id := cc.Response.User.ID.(type) {
	case string:
		raw, err := base64.RawURLEncoding.DecodeString(id)
		if err != nil {
			a.t.Fatalf("user id: %v", err)
		}
		a.userID = raw
	case []byte:
		a.userID = id
	}
	att, err := webauthncbor.Marshal(map[string]any{
		"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(true),
	})
	if err != nil {
		a.t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64(a.credID), "rawId": b64(a.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64(a.clientData("webauthn.create", cc.Response.Challenge)),
			"attestationObject": b64(att),
		},
	})
	return body
}

// assert answers a sign-in ceremony's options with an assertion.
func (a *virtualAuthenticator) assert(options []byte) []byte {
	a.t.Helper()
	var ca protocol.CredentialAssertion
	if err := json.Unmarshal(options, &ca); err != nil {
		a.t.Fatalf("assertion options: %v (%s)", err, options)
	}
	cd := a.clientData("webauthn.get", ca.Response.Challenge)
	ad := a.authData(false)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	resp := map[string]any{
		"clientDataJSON": b64(cd), "authenticatorData": b64(ad), "signature": b64(sig),
	}
	if a.userID != nil {
		resp["userHandle"] = b64(a.userID)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64(a.credID), "rawId": b64(a.credID), "type": "public-key", "response": resp,
	})
	return body
}
