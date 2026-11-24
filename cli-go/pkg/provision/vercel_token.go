package provision

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	secretpkg "github.com/vdibart/polis-cli/cli-go/pkg/secret"
)

// ⭐⭐ THE DEPLOY TOKEN IS HANDED OVER, NEVER HELD BY THE PROJECT. A
// server-signed instance on Vercel publishes by deploying, so it needs a token
// that can create its project's deployments. The provisioner mints one scoped
// to that project alone, and cannot write it where it belongs (the instance's
// private store, which only the running deployment can open). So it sets it as
// a WRITE-ONLY project setting; the instance moves it into its private store
// at its first deploy and deletes the setting with the token itself.
//
// ⚠️ A deployment keeps the settings it was created with: the deployment that
// carried the setting still holds it until a newer one replaces it, which the
// instance's own first publish does.

// DeployTokenLifetime is the expiry asked for: a year, the ruled default.
// Vercel accepted it (measured 2026-10-14); longer is not measured.
const DeployTokenLifetime = 365 * 24 * time.Hour

// DeployTokenGrant describes a token IssueDeployToken handed over. ⛔ It never
// carries the token.
type DeployTokenGrant struct {
	ID        string
	ExpiresAt time.Time

	// Pending are the ids of earlier handed-over tokens this renewal
	// replaces. ⭐ They stay live until the code deployed after this handover
	// is activated, and are revoked then (DeployTokensRetired reports how
	// that went).
	Pending []string
}

// DeployTokenRetirement is what Activate did with the tokens a renewal
// replaced. ⛔ It never carries a token.
type DeployTokenRetirement struct {
	// Revoked are the ids of earlier tokens revoked.
	Revoked []string

	// Unrevoked are the ids of earlier tokens that could not be revoked. ⭐
	// They stay recorded, so the next renewal tries again; an operator can
	// revoke them by hand.
	Unrevoked []string

	// Err is why the record could not be read, when it could not; nothing
	// was revoked then, and the next renewal tries again.
	Err error
}

// tokenRenewal is a handover this process made, waiting for a deployment
// that carries it to be activated.
type tokenRenewal struct {
	head     string          // the id of the token handed over
	releases map[string]bool // deployments created after the handover
}

// deployTokenIDSetting records, as a plain project setting, the id of the
// token most recently handed over and then any earlier ones not yet revoked,
// space-separated. ⭐ An id is not a credential; it is how a renewal in a later
// process knows which token it replaces. The instance never reads it.
const deployTokenIDSetting = "POLIS_DEPLOY_TOKEN_ID" //nolint:gosec // G101: a setting's or path's name, not a credential

// IssueDeployToken mints a project-only token for inst with the provisioner's
// own credential and sets it as the write-only setting the instance adopts.
//
// ⭐ IT IS ALSO THE RENEWAL. Issuing again mints a new token, the upsert
// replaces the setting, and every earlier token this provisioner handed over
// to the project (recorded by id in POLIS_DEPLOY_TOKEN_ID) is revoked, so a
// re-run leaves one live deploy token. ⭐ The revocation waits for the new
// code: PutCode after this handover creates a deployment that carries the new
// token, and Activate revokes the earlier ones once that deployment is
// production. Until then the live deployment keeps publishing with the token
// it has, and a deploy that fails leaves it working. ⚠️ This process must do
// all three (the hand-over, PutCode, Activate): a renewal whose deploy never
// succeeds here leaves the earlier tokens recorded, and the next renewal's
// successful deploy revokes them.
//
// ⛔ The token is never returned, logged or kept: it goes from the mint
// response straight into the setting. ⛔ A token whose setting could not be
// written is revoked, so a failure leaves no live credential nobody holds.
func (v *Vercel) IssueDeployToken(ctx context.Context, inst Instance, expiresAt time.Time) (DeployTokenGrant, error) {
	if inst.ID == "" {
		return DeployTokenGrant{}, fmt.Errorf("provision: issuing a deploy token needs the project")
	}
	if !expiresAt.After(time.Now()) {
		return DeployTokenGrant{}, fmt.Errorf("provision: a deploy token must expire in the future")
	}
	// ⭐ Read before minting: a project whose record cannot be read is not
	// renewed, rather than renewed leaving an old token live.
	earlier, err := v.handedOverTokenIDs(ctx, inst.ID)
	if err != nil {
		return DeployTokenGrant{}, fmt.Errorf("provision: reading which deploy tokens were handed over before: %w", err)
	}
	name := "polis deploy"
	if inst.Name != "" {
		name += " " + inst.Name
	}
	var minted struct {
		Token struct {
			ID        string `json:"id"`
			ExpiresAt int64  `json:"expiresAt"`
		} `json:"token"`
		BearerToken string `json:"bearerToken"`
	}
	if err := v.call(ctx, http.MethodPost, "/v3/user/tokens", map[string]any{
		"name":      name,
		"projectId": inst.ID,
		"expiresAt": expiresAt.UnixMilli(),
	}, &minted); err != nil {
		return DeployTokenGrant{}, fmt.Errorf("provision: minting the deploy token: %w", err)
	}
	if strings.TrimSpace(minted.BearerToken) == "" || minted.Token.ID == "" {
		return DeployTokenGrant{}, fmt.Errorf("provision: Vercel minted no usable deploy token")
	}
	grant := DeployTokenGrant{ID: minted.Token.ID, ExpiresAt: expiresAt}
	if minted.Token.ExpiresAt > 0 {
		grant.ExpiresAt = time.UnixMilli(minted.Token.ExpiresAt).UTC()
	}
	// revokeNew undoes the mint, so a failure leaves no live credential
	// nobody holds.
	revokeNew := func(step string, err error) (DeployTokenGrant, error) {
		if rerr := v.RevokeToken(ctx, grant.ID); rerr != nil {
			return DeployTokenGrant{}, fmt.Errorf("provision: %s: %w; and revoking the unused token failed, so revoke token %s by hand: %v", step, err, grant.ID, rerr)
		}
		return DeployTokenGrant{}, fmt.Errorf("provision: %s: %w (the token was revoked)", step, err)
	}
	// ⭐ THE ID IS RECORDED BEFORE THE TOKEN IS HANDED OVER, beside the ids
	// still to revoke, so no failure from here on leaves a live token that a
	// later renewal does not know about.
	if err := v.setPlain(ctx, inst.ID, deployTokenIDSetting, strings.Join(append([]string{grant.ID}, earlier...), " ")); err != nil {
		minted.BearerToken = ""
		return revokeNew("recording "+deployTokenIDSetting, err)
	}
	// ⭐ upsert: a renewal replaces a setting an earlier handover left.
	path := fmt.Sprintf("/v10/projects/%s/env?upsert=true", url.PathEscape(inst.ID))
	err = v.call(ctx, http.MethodPost, path, map[string]any{
		"key":        secretpkg.EnvDeployToken,
		"value":      minted.BearerToken,
		"type":       "sensitive",
		"visibility": "secret",
		"target":     []string{"production"},
	}, nil)
	minted.BearerToken = ""
	if err != nil {
		return revokeNew("setting "+secretpkg.EnvDeployToken, err)
	}
	// ⭐⭐ THE EARLIER TOKENS ARE NOT REVOKED HERE. The live deployment still
	// publishes with one of them, and keeps doing so until a deployment
	// carrying the new token is production. So they are revoked by Activate,
	// once the code deployed after this handover is activated
	// (retireDeployTokens); a deploy that fails leaves them working.
	for _, old := range earlier {
		if old != grant.ID {
			grant.Pending = append(grant.Pending, old)
		}
	}
	v.mu.Lock()
	if v.renewals == nil {
		v.renewals = map[string]*tokenRenewal{}
	}
	v.renewals[inst.ID] = &tokenRenewal{head: grant.ID, releases: map[string]bool{}}
	v.mu.Unlock()
	return grant, nil
}

// handedOverTokenIDs reads the ids recorded in POLIS_DEPLOY_TOKEN_ID. A
// project without the setting has none.
func (v *Vercel) handedOverTokenIDs(ctx context.Context, project string) ([]string, error) {
	envs, err := v.listEnv(ctx, project)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range envs {
		if e.Key != deployTokenIDSetting || e.ID == "" {
			continue
		}
		var got struct {
			Value string `json:"value"`
		}
		if err := v.call(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/env/"+url.PathEscape(e.ID), nil, &got); err != nil {
			return nil, err
		}
		for _, id := range strings.Fields(got.Value) {
			if !contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

// VercelDeployTokenHandover is a ready-made handover (SeededProvision.Handover,
// or a caller's own flow between SetConfig and PutCode): it issues a
// project-only deploy token expiring after lifetime (DeployTokenLifetime when
// zero) and sets it as the write-only POLIS_DEPLOY_TOKEN. ⭐ A re-run renews,
// and the tokens earlier handovers left are revoked once the code deployed
// after it is activated.
//
// ⛔ Run it BEFORE PutCode: a deployment keeps the settings it was created
// with. ⛔ No token value is logged, returned or kept.
func VercelDeployTokenHandover(lifetime time.Duration) func(ctx context.Context, p Provisioner, inst Instance) error {
	if lifetime <= 0 {
		lifetime = DeployTokenLifetime
	}
	return func(ctx context.Context, p Provisioner, inst Instance) error {
		v, ok := p.(*Vercel)
		if !ok {
			return fmt.Errorf("provision: the deploy-token handover is Vercel's, and the platform is %s", p.Name())
		}
		_, err := v.IssueDeployToken(ctx, inst, time.Now().Add(lifetime))
		return err
	}
}

// RevokeToken deletes a token by its id.
func (v *Vercel) RevokeToken(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("provision: revoking a token needs its id")
	}
	return v.call(ctx, http.MethodDelete, "/v3/user/tokens/"+url.PathEscape(id), nil, nil)
}

// DeleteEnv deletes every setting named key from a project, and reports how
// many it deleted. ⭐ Values are never asked for: the listing names settings,
// and a write-only one could not be read back anyway.
func (v *Vercel) DeleteEnv(ctx context.Context, project, key string) (int, error) {
	if project == "" || key == "" {
		return 0, fmt.Errorf("provision: deleting a setting needs the project and its name")
	}
	var listed struct {
		Envs []struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"envs"`
	}
	base := "/v9/projects/" + url.PathEscape(project) + "/env"
	if err := v.call(ctx, http.MethodGet, base, nil, &listed); err != nil {
		return 0, fmt.Errorf("provision: listing %s's settings: %w", project, err)
	}
	n := 0
	for _, e := range listed.Envs {
		if e.Key != key || e.ID == "" {
			continue
		}
		if err := v.call(ctx, http.MethodDelete, base+"/"+url.PathEscape(e.ID), nil, nil); err != nil {
			return n, fmt.Errorf("provision: deleting %s: %w", key, err)
		}
		n++
	}
	return n, nil
}

// noteDeployment records that rel was created for inst after this process's
// handover, so it carries the handed-over token.
func (v *Vercel) noteDeployment(inst Instance, rel Release) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if r := v.renewals[inst.ID]; r != nil && rel.ID != "" {
		r.releases[rel.ID] = true
	}
}

// retireDeployTokens revokes the tokens a renewal replaced, once rel, a
// deployment carrying the new token, is production. ⭐ Nothing happens for a
// release this process did not deploy after a handover: an older deployment
// carries an older token, and activating it must not revoke what it holds.
//
// ⭐ Best effort, because the release is live by now and its activation must
// not be reported as failed: ids that could not be revoked stay recorded, and
// the next renewal tries again.
func (v *Vercel) retireDeployTokens(ctx context.Context, inst Instance, rel Release) {
	v.mu.Lock()
	r := v.renewals[inst.ID]
	if r == nil || !r.releases[rel.ID] {
		v.mu.Unlock()
		return
	}
	delete(v.renewals, inst.ID)
	v.mu.Unlock()

	var out DeployTokenRetirement
	defer func() {
		v.mu.Lock()
		if v.retired == nil {
			v.retired = map[string]DeployTokenRetirement{}
		}
		v.retired[inst.ID] = out
		v.mu.Unlock()
	}()
	// ⭐ Read again rather than trusting what the handover saw: a later
	// handover in another process may have recorded more.
	ids, err := v.handedOverTokenIDs(ctx, inst.ID)
	if err != nil {
		out.Err = fmt.Errorf("provision: reading which deploy tokens to revoke: %w", err)
		return
	}
	keep := []string{r.head}
	for _, id := range ids {
		if id == r.head {
			continue
		}
		rerr := v.RevokeToken(ctx, id)
		if rerr == nil || isStatus(rerr, http.StatusNotFound) {
			out.Revoked = append(out.Revoked, id)
			continue
		}
		out.Unrevoked = append(out.Unrevoked, id)
		keep = append(keep, id)
	}
	if len(out.Revoked) > 0 {
		// ⚠️ Best effort: if this fails the record still names the revoked
		// ids, and a later renewal finds them gone (404), which is success.
		_ = v.setPlain(ctx, inst.ID, deployTokenIDSetting, strings.Join(keep, " "))
	}
}

// DeployTokensRetired reports what the last Activate of inst in this process
// did with the deploy tokens a renewal replaced; ok is false when it retired
// none (no renewal, or none activated yet).
func (v *Vercel) DeployTokensRetired(inst Instance) (DeployTokenRetirement, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	r, ok := v.retired[inst.ID]
	return r, ok
}
