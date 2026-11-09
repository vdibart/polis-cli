package provision

import (
	"context"
	"errors"
	"fmt"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// SeededProvision is what standing up an instance from an existing site needs.
type SeededProvision struct {
	// Create names the instance to create.
	Create CreateRequest

	// Site is the site the instance starts as: the output of a branch.
	Site storage.Site

	// Config is the instance's environment, without the owner's recovery
	// key: that is read from Site and merged in (OwnerConfig).
	Config Config

	// ConfigFor, where set, is the part of the environment that depends on
	// the instance Create made (its store, the name the platform gave it).
	// ⭐ Merged with Config and the owner's configuration under the same rule:
	// a key set twice with different values is refused.
	ConfigFor func(Instance) (Config, error)

	// Handover, where set, runs after SetConfig and before the first
	// deployment: a platform's credential handover, such as a deploy token
	// the instance adopts. ⭐ Before the deployment because a deployment keeps
	// the settings it was created with.
	Handover func(ctx context.Context, p Provisioner, inst Instance) error

	// Code is the instance's first release.
	Code Code
}

// ProvisionSeeded stands up an instance that starts as an existing site, in
// the order seeding needs: Create, Seed, SetConfig, the handover, then the
// first deployment (PutCode and Activate).
//
// ⛔ THE OWNER'S RECOVERY KEY IS SET BEFORE THE FIRST DEPLOYMENT, OR NOTHING
// IS PROVISIONED. It is read from the site before anything is created, and a
// site without one is refused (ErrNoOwnerKey): an instance started without it
// could be claimed by whoever reached it first.
//
// ⭐ A failure after Create destroys what it created (never an instance it
// adopted), so a failed provision leaves nothing behind and never a store half
// seeded. Seeding is not resumed: a seed refuses a store that holds a key.
func ProvisionSeeded(ctx context.Context, p Provisioner, req SeededProvision) (Instance, Release, error) {
	if p == nil {
		return Instance{}, Release{}, fmt.Errorf("provision: a seeded provision needs a platform")
	}
	cfg, err := withOwnerConfig(req.Config, req.Site)
	if err != nil {
		return Instance{}, Release{}, err
	}
	inst, err := p.Create(ctx, req.Create)
	if err != nil {
		return Instance{}, Release{}, err
	}
	fail := func(step string, err error) (Instance, Release, error) {
		if inst.Existing {
			return inst, Release{}, fmt.Errorf("provision: %s: %w", step, err)
		}
		if derr := p.Destroy(ctx, inst); derr != nil {
			return inst, Release{}, fmt.Errorf("provision: %s: %w; and destroying the half-provisioned instance failed, so remove it by hand: %v", step, err, derr)
		}
		return Instance{}, Release{}, fmt.Errorf("provision: %s: %w (the instance was destroyed)", step, err)
	}
	if req.ConfigFor != nil {
		more, err := req.ConfigFor(inst)
		if err != nil {
			return fail("building the configuration", err)
		}
		if cfg, err = mergeConfig(cfg, more); err != nil {
			return fail("building the configuration", err)
		}
	}
	if err := p.Seed(ctx, inst, req.Site); err != nil {
		return fail("seeding", err)
	}
	if err := p.SetConfig(ctx, inst, cfg); err != nil {
		return fail("setting the configuration", err)
	}
	if req.Handover != nil {
		if err := req.Handover(ctx, p, inst); err != nil {
			return fail("the handover", err)
		}
	}
	rel, err := p.PutCode(ctx, inst, req.Code)
	if err != nil {
		return fail("the first deployment", err)
	}
	if err := p.Activate(ctx, inst, rel); err != nil {
		// ⛔ NOT DESTROYED. The first deployment exists, so the instance may
		// already be running and writing to its store; emptying a store under
		// a running instance leaves it unable to work and the store half
		// deleted. Say what is there instead, and let a person remove it.
		if inst.Existing {
			return inst, Release{}, fmt.Errorf("provision: activating the first deployment: %w", err)
		}
		return inst, Release{}, fmt.Errorf("provision: activating the first deployment: %w; the instance %s (%s) was left in place, "+
			"because it may already be running: check it, then remove it by hand if it is not wanted", err, inst.Name, inst.ID)
	}
	return inst, rel, nil
}

// withOwnerConfig merges the site's owner configuration into cfg. ⛔ A caller
// value for the same key that differs is refused rather than either winning.
func withOwnerConfig(cfg Config, s storage.Site) (Config, error) {
	if !s.Valid() {
		return Config{}, fmt.Errorf("provision: a seeded provision needs the site to seed")
	}
	own, err := OwnerConfig(s)
	if errors.Is(err, ErrNoOwnerKey) {
		return Config{}, fmt.Errorf("%w; refusing to provision an instance anyone could claim first", err)
	}
	if err != nil {
		return Config{}, err
	}
	out, err := mergeConfig(cfg, own)
	if err != nil {
		return Config{}, fmt.Errorf("%w, and differs from the site's own", err)
	}
	return out, nil
}

// mergeConfig returns a with b's values and classifications added. ⛔ A key
// both set to different values is refused, naming the key, never either value.
func mergeConfig(a, b Config) (Config, error) {
	out := Config{
		Values: map[string]string{},
		Secret: append([]string(nil), a.Secret...),
		Plain:  append([]string(nil), a.Plain...),
	}
	for k, v := range a.Values {
		out.Values[k] = v
	}
	for k, v := range b.Values {
		if have, ok := out.Values[k]; ok {
			if have != v {
				return Config{}, fmt.Errorf("provision: %s is set twice", k)
			}
			continue
		}
		out.Values[k] = v
	}
	for _, k := range b.Plain {
		if !contains(out.Plain, k) {
			out.Plain = append(out.Plain, k)
		}
	}
	for _, k := range b.Secret {
		if !contains(out.Secret, k) {
			out.Secret = append(out.Secret, k)
		}
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
