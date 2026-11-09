package provision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	secretpkg "github.com/vdibart/polis-cli/cli-go/pkg/secret"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func siteWithRecoveryKey(t *testing.T, body string) storage.Site {
	t.Helper()
	s := storage.At(t.TempDir())
	if body != "" {
		if err := s.Store().Put(context.Background(), owner.RecoveryPublicKeyKey, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// ⭐ The site's recovery public key becomes POLIS_RECOVERY_KEY, as plain
// configuration a platform will accept, and the instance can build its
// recovery from it.
func TestOwnerConfigSetsTheRecoveryKey(t *testing.T) {
	_, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := OwnerConfig(siteWithRecoveryKey(t, string(pub)))
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Values[owner.RecoveryKeyEnv]
	if got != strings.TrimSpace(string(pub)) || len(cfg.Values) != 1 {
		t.Fatalf("config = %v", cfg.Values)
	}
	secret, err := cfg.Classify(secretpkg.IsSecretName)
	if err != nil {
		t.Fatalf("SetConfig would refuse it: %v", err)
	}
	if secret[owner.RecoveryKeyEnv] {
		t.Error("a public key was declared a secret")
	}
	if _, err := owner.NewRecovery(got, "https://alice.example"); err != nil {
		t.Errorf("the instance would refuse the value at boot: %v", err)
	}
}

// ⛔ Absent or malformed refuses, naming the file and never the value.
func TestOwnerConfigRefuses(t *testing.T) {
	if _, err := OwnerConfig(siteWithRecoveryKey(t, "")); !errors.Is(err, ErrNoOwnerKey) {
		t.Errorf("absent: err = %v, want ErrNoOwnerKey", err)
	}
	priv, pub, _ := signing.GenerateKeypair()
	for name, body := range map[string]string{
		"not a key":   "ssh-ed25519 not-base64-at-all",
		"private key": string(priv),
		"two keys":    string(pub) + string(pub),
	} {
		_, err := OwnerConfig(siteWithRecoveryKey(t, body))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), "not-base64") {
			t.Errorf("%s: the error echoes the value: %v", name, err)
		}
	}
}
