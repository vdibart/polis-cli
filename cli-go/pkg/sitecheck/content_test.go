package sitecheck

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// signedPost builds a post exactly the way the publish path does: canonicalize
// the body, hash it, write the unsigned frontmatter, sign the whole unsigned
// document, then splice the signature back in.
func signedPost(t *testing.T, privKey []byte, body string, isComment bool) string {
	t.Helper()

	canonical := signing.CanonicalizeContent(body)
	hash := SHA256Hex([]byte(canonical))

	head := "---\ntitle: Test\npublished: 2026-01-15T10:00:00Z\n"
	if isComment {
		head += "type: comment\n"
	}
	unsignedFM := head + fmt.Sprintf("current-version: sha256:%s\n---", hash)

	sig, err := signing.SignContent([]byte(signing.CanonicalizeContent(unsignedFM+"\n\n"+canonical)), privKey)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}
	sigB64 := extractSigBase64(sig)

	finalFM := head + fmt.Sprintf("current-version: sha256:%s\nsignature: %s\n---", hash, sigB64)
	if isComment {
		// author: is injected AFTER signing — the whole reason comments need
		// their own signing-base extraction.
		finalFM = head + fmt.Sprintf("current-version: sha256:%s\nauthor: alice.example\nsignature: %s\n---", hash, sigB64)
	}
	return finalFM + "\n\n" + canonical
}

// typeFor maps the test helper's bool to the frontmatter discriminator the
// real verifiers read.
func typeFor(isComment bool) string {
	if isComment {
		return "comment"
	}
	return "post"
}

func extractSigBase64(pemSig string) string {
	var b strings.Builder
	for _, line := range strings.Split(pemSig, "\n") {
		if strings.HasPrefix(line, "-----") || line == "" {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

func TestVerifyContent_SignedPostAndCommentVerify(t *testing.T) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	for _, isComment := range []bool{false, true} {
		content := signedPost(t, priv, "Hello, world.\n", isComment)
		res := VerifyContent(content, pub, signing.MarkdownObjectTypeFor(typeFor(isComment), false))
		if res.ParseError != nil {
			t.Fatalf("isComment=%v: parse: %v", isComment, res.ParseError)
		}
		if res.Signature != SigValid {
			t.Errorf("isComment=%v: signature = %s (%v), want valid", isComment, res.Signature, res.SigError)
		}
		if res.Hash != HashValid {
			t.Errorf("isComment=%v: hash = %s, want valid", isComment, res.Hash)
		}
		if !res.OK() {
			t.Errorf("isComment=%v: OK() = false", isComment)
		}
	}
}

// Unsigned is a FACT, not a failure (D5). Most artifacts on most sites carry no
// signature, and a validator that reads absent-as-broken tells nearly every
// self-hoster their site is broken.
func TestVerifyContent_UnsignedIsNotAFailure(t *testing.T) {
	content := "---\ntitle: Test\n---\n\nbody\n"
	res := VerifyContent(content, nil, signing.TypePost)
	if res.Signature != SigUnsigned {
		t.Errorf("signature = %s, want unsigned", res.Signature)
	}
	if res.Hash != HashAbsent {
		t.Errorf("hash = %s, want absent", res.Hash)
	}
	if !res.OK() {
		t.Error("an unsigned, unhashed artifact must not read as a failure")
	}
}

func TestVerifyContent_TamperedBodyFailsBothSignatureAndHash(t *testing.T) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	content := signedPost(t, priv, "Hello, world.\n", false)
	tampered := strings.Replace(content, "Hello, world.", "Goodbye, world.", 1)

	res := VerifyContent(tampered, pub, signing.TypePost)
	if res.Signature != SigInvalid {
		t.Errorf("signature = %s, want invalid", res.Signature)
	}
	if res.Hash != HashMismatch {
		t.Errorf("hash = %s, want mismatch", res.Hash)
	}
	if res.OK() {
		t.Error("tampered content must not read as OK")
	}
}

func TestVerifyContent_WrongKeyIsInvalidNotUnsigned(t *testing.T) {
	priv, _, _ := signing.GenerateKeypair()
	_, otherPub, _ := signing.GenerateKeypair()

	res := VerifyContent(signedPost(t, priv, "Hello.\n", false), otherPub, signing.TypePost)
	if res.Signature != SigInvalid {
		t.Errorf("signature = %s, want invalid — a signature checked against the wrong key is a finding, not an absence", res.Signature)
	}
}

// An empty key must NOT become a third "could not check" status inside the
// predicate: Patrol reports the verifier's own error text and Judge counts the
// item failed. Whether a key exists at all is a site-level question.
func TestVerifyContent_EmptyKeyIsInvalidWithAnError(t *testing.T) {
	priv, _, _ := signing.GenerateKeypair()
	res := VerifyContent(signedPost(t, priv, "Hello.\n", false), nil, signing.TypePost)
	if res.Signature != SigInvalid {
		t.Errorf("signature = %s, want invalid", res.Signature)
	}
	if res.SigError == nil {
		t.Error("want an error explaining why it could not verify")
	}
}

func TestVerifyContent_NoFrontmatterIsAParseError(t *testing.T) {
	res := VerifyContent("just a body, no frontmatter", nil, signing.TypePost)
	if res.ParseError == nil {
		t.Fatal("want a parse error")
	}
	if res.OK() {
		t.Error("unparseable content must not read as OK")
	}
}

// The raw-byte fallback is the ACTORS' behaviour and must stay until it is
// retired deliberately — pkg/verify already dropped it (R20-C-F9) and the two
// have not been reconciled.
func TestVerifyHash_AcceptsCanonicalAndRawBytes(t *testing.T) {
	body := "line one\r\nline two   \n"
	canonical := signing.CanonicalizeContent(body)

	if !VerifyHash(body, SHA256Hex([]byte(canonical))) {
		t.Error("canonical hash should match")
	}
	if !VerifyHash(body, SHA256Hex([]byte(body))) {
		t.Error("raw-byte hash should still match — Patrol and Judge both accept it")
	}
	if VerifyHash(body, strings.Repeat("0", 64)) {
		t.Error("an unrelated hash must not match")
	}
}

func TestReconstructSSHSignature_RoundTripsThroughTheVerifier(t *testing.T) {
	priv, pub, _ := signing.GenerateKeypair()
	pem, err := signing.SignContent([]byte("hello"), priv)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := ReconstructSSHSignature(extractSigBase64(pem))
	ok, err := signing.VerifySignature([]byte("hello"), pub, rebuilt)
	if err != nil || !ok {
		t.Fatalf("rewrapped signature did not verify: ok=%v err=%v", ok, err)
	}
}

// WrapBase64 with a non-positive width used to be an infinite loop waiting for
// a caller. It is exported now, so it must terminate.
func TestWrapBase64_NonPositiveWidthReturnsInput(t *testing.T) {
	if got := WrapBase64("abcdef", 0); got != "abcdef" {
		t.Errorf("WrapBase64(_, 0) = %q", got)
	}
}

func TestParseFrontmatter_ReadsSignatureAndVersionAndSplitsBody(t *testing.T) {
	content := "---\ntitle: T\ncurrent-version: sha256:abc\nsignature: SIGNATURE\n---\n\nthe body\n"
	fm, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Signature != "SIGNATURE" || fm.CurrentVersion != "sha256:abc" {
		t.Errorf("fm = %+v", fm)
	}
	if body != "the body\n" {
		t.Errorf("body = %q", body)
	}
}
