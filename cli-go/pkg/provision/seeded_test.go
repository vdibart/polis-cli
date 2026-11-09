package provision

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// ⭐ A seeded provision runs Create, Seed, SetConfig, the handover, then the
// first deployment, and the configuration holds the owner's recovery key
// (equal to the branch output's recovery.pub) BEFORE the first deployment.
func TestASeededProvisionSetsTheOwnerKeyBeforeTheFirstDeployment(t *testing.T) {
	ctx := context.Background()
	src := branchedSite(t)
	want, err := src.Store().Get(ctx, owner.RecoveryPublicKeyKey)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakePlatform{}
	var atHandover string
	inst, rel, err := ProvisionSeeded(ctx, p, SeededProvision{
		Create: CreateRequest{Name: "seeded"},
		Site:   src,
		Config: Config{Values: map[string]string{"POLIS_BASE_URL": "https://seed.example"}, Plain: []string{"POLIS_BASE_URL"}},
		Handover: func(ctx context.Context, _ Provisioner, _ Instance) error {
			p.calls = append(p.calls, "Handover")
			atHandover = p.config.Values[owner.RecoveryKeyEnv]
			return nil
		},
		Code: Code{Archive: []byte("x"), Format: FormatTarGz},
	})
	if err != nil {
		t.Fatal(err)
	}
	if inst.ID == "" || rel.ID == "" {
		t.Fatalf("instance %+v release %+v", inst, rel)
	}
	order := []string{"Create", "Seed", "SetConfig", "Handover", "PutCode", "Activate"}
	if !reflect.DeepEqual(p.calls, order) {
		t.Fatalf("calls = %v, want %v", p.calls, order)
	}
	if atHandover != strings.TrimSpace(string(want)) {
		t.Errorf("before the first deployment POLIS_RECOVERY_KEY = %q, want the site's recovery.pub", atHandover)
	}
	if p.config.Values["POLIS_BASE_URL"] != "https://seed.example" {
		t.Error("the caller's configuration was lost in the merge")
	}
	if ok, _ := p.store.Exists(ctx, site.WellKnownKey); !ok {
		t.Error("the instance's store was not seeded")
	}
}

// ⛔ A site with no recovery key provisions nothing at all.
func TestASeededProvisionWithoutAnOwnerKeyCreatesNothing(t *testing.T) {
	src := branchedSite(t)
	if err := src.Store().Delete(context.Background(), owner.RecoveryPublicKeyKey); err != nil {
		t.Fatal(err)
	}
	p := &fakePlatform{}
	_, _, err := ProvisionSeeded(context.Background(), p, SeededProvision{Create: CreateRequest{Name: "x"}, Site: src})
	if !errors.Is(err, ErrNoOwnerKey) {
		t.Fatalf("err = %v, want ErrNoOwnerKey", err)
	}
	if len(p.calls) != 0 {
		t.Errorf("calls = %v; nothing may be created for a site anyone could claim", p.calls)
	}
}

// ⛔ A caller value for the recovery key that differs from the site's is
// refused, not resolved either way.
func TestASeededProvisionRefusesAConflictingOwnerKey(t *testing.T) {
	p := &fakePlatform{}
	_, _, err := ProvisionSeeded(context.Background(), p, SeededProvision{
		Create: CreateRequest{Name: "x"},
		Site:   branchedSite(t),
		Config: Config{Values: map[string]string{owner.RecoveryKeyEnv: "ssh-ed25519 AAAA other"}, Plain: []string{owner.RecoveryKeyEnv}},
	})
	if err == nil || strings.Contains(err.Error(), "AAAA") {
		t.Fatalf("err = %v, want a refusal naming the key only", err)
	}
	if len(p.calls) != 0 {
		t.Errorf("calls = %v", p.calls)
	}
}

// ⭐ A failure after Create destroys what was created, and nothing is deployed.
func TestAFailedSeedDestroysTheInstance(t *testing.T) {
	p := &fakePlatform{failSeed: true}
	_, _, err := ProvisionSeeded(context.Background(), p, SeededProvision{
		Create: CreateRequest{Name: "x"}, Site: branchedSite(t),
		Code: Code{Archive: []byte("x"), Format: FormatTarGz},
	})
	if err == nil {
		t.Fatal("a failed seed provisioned an instance")
	}
	if want := []string{"Create", "Seed", "Destroy"}; !reflect.DeepEqual(p.calls, want) {
		t.Errorf("calls = %v, want %v", p.calls, want)
	}
}

// ⭐ The configuration that depends on the created instance is built from it,
// and set with the rest in the one SetConfig before the first deployment.
// ⛔ A key it sets differently from the caller is refused, and the instance it
// was built for is destroyed before anything is seeded.
func TestASeededProvisionBuildsTheInstanceConfigurationFromTheInstance(t *testing.T) {
	p := &fakePlatform{}
	_, _, err := ProvisionSeeded(context.Background(), p, SeededProvision{
		Create: CreateRequest{Name: "seeded"},
		Site:   branchedSite(t),
		ConfigFor: func(inst Instance) (Config, error) {
			return Config{Values: map[string]string{"INSTANCE": inst.ID}, Plain: []string{"INSTANCE"}}, nil
		},
		Code: Code{Archive: []byte("x"), Format: FormatTarGz},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.config.Values["INSTANCE"] != "fake-1" || p.config.Values[owner.RecoveryKeyEnv] == "" {
		t.Errorf("SetConfig got %v, want the instance's value and the owner key", p.config.Values)
	}

	p = &fakePlatform{}
	_, _, err = ProvisionSeeded(context.Background(), p, SeededProvision{
		Create: CreateRequest{Name: "seeded"},
		Site:   branchedSite(t),
		Config: Config{Values: map[string]string{"INSTANCE": "mine"}, Plain: []string{"INSTANCE"}},
		ConfigFor: func(inst Instance) (Config, error) {
			return Config{Values: map[string]string{"INSTANCE": "theirs"}, Plain: []string{"INSTANCE"}}, nil
		},
		Code: Code{Archive: []byte("x"), Format: FormatTarGz},
	})
	if err == nil || strings.Contains(err.Error(), "theirs") || strings.Contains(err.Error(), "mine") {
		t.Fatalf("a conflicting key: %v, want a refusal naming the key only", err)
	}
	if want := []string{"Create", "Destroy"}; !reflect.DeepEqual(p.calls, want) {
		t.Errorf("calls = %v, want %v", p.calls, want)
	}
}

// ⛔ Once the first deployment exists the instance may be running and writing
// to its store, so a failed activation leaves it in place, says so, and never
// empties the store under it (measured 2026-10-19: the store was left half
// deleted beside a live instance).
func TestAFailedActivationDoesNotDestroyTheInstance(t *testing.T) {
	p := &fakePlatform{failActivate: true}
	inst, _, err := ProvisionSeeded(context.Background(), p, SeededProvision{
		Create: CreateRequest{Name: "live"}, Site: branchedSite(t),
		Code: Code{Archive: []byte("x"), Format: FormatTarGz},
	})
	if err == nil {
		t.Fatal("a failed activation reported success")
	}
	if p.destroyed {
		t.Fatal("a failed activation destroyed an instance that may be running")
	}
	if !strings.Contains(err.Error(), "left in place") || !strings.Contains(err.Error(), "fake-1") || inst.ID != "fake-1" {
		t.Errorf("err = %v, inst = %+v; want the instance named and left in place", err, inst)
	}
	if ok, _ := p.store.Exists(context.Background(), site.WellKnownKey); !ok {
		t.Error("the store was emptied")
	}
}
