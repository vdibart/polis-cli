package following

import (
	"fmt"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// ReconcileFollowsToDS reconciles the DS follow graph UP to the user's LOCAL
// view: following.json is the source of truth. It tallies the DS net per target
// (pub.polis.follow.announced +1, pub.polis.follow.removed -1) and re-announces
// any locally-followed target whose net is below 1 — the targets the DS is
// missing.
//
// This closes the "dangling removed" gap at its source. A follow made while the
// site was unregistered has its announce suppressed (stream.PublishEvent no-ops
// without a registration marker; see following.FollowWithBlessing's
// LogSuppressedEmit branch), and an unfollow→refollow can otherwise leave the DS
// one announce short. Either way the DS ends up below local; calling this right
// after the site (re)registers brings the two back into agreement immediately,
// instead of waiting for the hosted Chaplain's periodic repair (which self-
// hosters don't run at all). The DS follower projection is set / last-write-
// wins, so a fresh announced restores the edge; re-announcing a target the user
// genuinely follows is always a valid statement.
//
// `ds` is the client used for the net-tally queries; `dsCfg` carries the config
// used to PUBLISH the re-announce events — which may point at a different DS URL
// than the query client (as on the hosted platform, where the public URL must
// match the registration marker domain). Returns the target domains that were
// re-announced. Query failures are fatal (a partial net could cause spurious
// re-announces); a single publish failure is non-fatal and that target is
// skipped (the next reconcile/Chaplain cycle retries it).
//
// Net-based netting here matches how clerk/parity_follow.go measures drift, so
// the reconcile converges. (The hosted Chaplain's repairFollows applies the same
// net rule — keep the two in step if either changes.)
func ReconcileFollowsToDS(followingPath, domain string, ds *discovery.Client, privKey []byte, dsCfg *stream.DiscoveryConfig) ([]string, error) {
	f, err := Load(followingPath)
	if err != nil {
		return nil, fmt.Errorf("load following.json: %w", err)
	}

	net := make(map[string]int)
	tally := func(eventType string, delta int) error {
		cursor := ""
		for {
			resp, err := ds.StreamQuery(cursor, 1000, eventType, domain, "")
			if err != nil {
				return err
			}
			if resp == nil {
				break
			}
			for _, evt := range resp.Events {
				if td, ok := evt.Payload["target_domain"].(string); ok && td != "" {
					net[td] += delta
				}
			}
			if !resp.HasMore || resp.Cursor == "" {
				break
			}
			cursor = resp.Cursor
		}
		return nil
	}
	if err := tally("pub.polis.follow.announced", +1); err != nil {
		return nil, fmt.Errorf("ds query (announced): %w", err)
	}
	if err := tally("pub.polis.follow.removed", -1); err != nil {
		return nil, fmt.Errorf("ds query (removed): %w", err)
	}

	var reannounced []string
	for _, td := range followsNeedingReannounce(f.Following, net) {
		if err := stream.PublishEvent("pub.polis.follow.announced", map[string]interface{}{
			"target_domain": td,
		}, privKey, dsCfg); err != nil {
			// Non-fatal: skip this target, keep reconciling the rest.
			continue
		}
		reannounced = append(reannounced, td)
	}
	return reannounced, nil
}

// followsNeedingReannounce returns the target domains (from following.json
// entries) whose DS net is below 1 — locally-followed targets the DS projection
// is missing. Pure decision logic, split out for testing: net < 1 captures both
// never-announced targets (net 0) and dangling-removed targets (announced then
// removed, net 0 or below). Targets with net >= 1 already have the edge and are
// left alone, so the reconcile is bounded to genuine gaps and converges.
func followsNeedingReannounce(entries []FollowingEntry, net map[string]int) []string {
	var out []string
	for _, entry := range entries {
		td := discovery.ExtractDomainFromURL(entry.URL)
		if td == "" {
			continue
		}
		if net[td] < 1 {
			out = append(out, td)
		}
	}
	return out
}
