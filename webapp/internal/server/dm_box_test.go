package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"golang.org/x/crypto/nacl/box"
)

// ⭐ A message under the owner's password comes back from dm.get with its box
// (v5 spec 2.3, gap M1), so the owner's browser, holding the key once
// unlocked, can open it; the instance itself still cannot.
func TestDMGetCarriesTheBoxOfAMessageItCannotOpen(t *testing.T) {
	s, h := queryRoutes(t)
	site := s.Site()
	kr, err := dm.LoadKeyring(site)
	if errors.Is(err, fs.ErrNotExist) {
		kr = &dm.Keyring{}
		if _, err := kr.AddBootstrapEpoch(); err != nil {
			t.Fatal(err)
		}
		if err := kr.SaveNew(site); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if _, _, err := kr.SetPassword([]byte("correct horse")); err != nil {
		t.Fatal(err)
	}
	if err := kr.Save(site); err != nil {
		t.Fatal(err)
	}
	ep, _ := kr.CurrentEpoch()
	var myPub [32]byte
	pb, _ := base64.StdEncoding.DecodeString(ep.PublicKeyMessages)
	copy(myPub[:], pb)

	peerPub, peerPriv, _ := box.GenerateKey(rand.Reader)
	var nonce [24]byte
	_, _ = rand.Read(nonce[:])
	ct := box.Seal(nil, []byte("only the owner reads this"), &nonce, &myPub, peerPriv)
	if _, err := dm.NewMailbox(site).AppendReceived("ana.example", "ana.example", ep.ID, ct, nonce, *peerPub, ""); err != nil {
		t.Fatal(err)
	}

	full := mint(t, s)
	w := get(t, h, "/v1/content/dm", full)
	var list struct {
		Conversations []struct {
			ID string `json:"id"`
		} `json:"conversations"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Conversations) != 1 {
		t.Fatalf("conversations = %s", w.Body.String())
	}
	w = get(t, h, "/v1/content/dm/"+list.Conversations[0].ID, full)
	var conv struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &conv)
	if len(conv.Messages) != 1 {
		t.Fatalf("messages = %s", w.Body.String())
	}
	m := conv.Messages[0]
	if m["locked"] != true || m["content"] != "" {
		t.Fatalf("message = %v, want locked with no text", m)
	}
	if m["ciphertext"] != base64.StdEncoding.EncodeToString(ct) || m["nonce"] != base64.StdEncoding.EncodeToString(nonce[:]) ||
		m["box_pub"] != base64.StdEncoding.EncodeToString(peerPub[:]) {
		t.Errorf("message = %v, want its box", m)
	}
	// An agent never reads messages, box or not.
	if code := get(t, h, "/v1/content/dm/"+list.Conversations[0].ID, mint(t, s, "read")).Code; code != 403 {
		t.Errorf("an agent's read of a conversation = %d, want 403", code)
	}
}
