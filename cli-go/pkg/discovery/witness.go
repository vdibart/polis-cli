package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// A witness is a discovery service's countersignature over something it
// observed — SIGNET epic 32.
//
// It says: "at WitnessedAt, a party who could sign as this domain presented me
// these exact values, and the signature verified against the key the domain
// published then." The first fact is the prize: an INDEPENDENT date, the one
// thing nothing a site publishes about itself can supply.
//
// ⛔ EVIDENCE, NEVER A GATE. Nothing may require a witness. An artifact without
// one verifies on its own signature, exactly as before; a witness adds a claim,
// it never withholds one.
//
// ⛔ NEVER INSIDE THE ARTIFACT'S SIGNED BYTES. The artifact is signed, then
// witnessed; a witness inside the signing base would be circular. It is carried
// beside the thing witnessed: a rotation's witness inside the key-history entry
// it witnesses, a content witness in the site's published witness file.
//
// ⚠️ THE SIGNED FIELDS ARE A WIRE CONTRACT with discovery-service/core/witness.ts.
// witnessFields must list exactly what the DS signs for each action, and both
// sides pin the same golden bytes in discovery-service/core/contract-fixtures/
// witness-canonical.json.

// Witness actions — what was observed.
const (
	WitnessActionContent     = "content-registration"
	WitnessActionKeyRotation = "key-rotation"
	// WitnessActionRelationship is a blessing decision the post author signed
	// (SIGNET epic 45 D7). Decision is the request's action (grant/deny); the
	// marker fields and grant_state are present only for a user agent's act.
	WitnessActionRelationship = "relationship-update"
)

// MetadataArtifactHash is the registration metadata key carrying a hash of the
// whole signed artifact, which the DS lifts into its witness. See ArtifactHash.
const MetadataArtifactHash = "artifact_hash"

// Witness is one countersignature, as the DS returns it and as a site carries it.
type Witness struct {
	Action string `json:"action"`

	// content-registration
	Type         string `json:"type,omitempty"`
	URL          string `json:"url,omitempty"`
	Version      string `json:"version,omitempty"`
	Author       string `json:"author,omitempty"`
	Actor        string `json:"actor,omitempty"`
	PublicKey    string `json:"public_key,omitempty"`
	ArtifactHash string `json:"artifact_hash,omitempty"`

	// key-rotation (Timestamp is also the relationship request's)
	Domain        string `json:"domain,omitempty"`
	OldKey        string `json:"old_key,omitempty"`
	NewKey        string `json:"new_key,omitempty"`
	Timestamp     string `json:"timestamp,omitempty"`
	TransitionSig string `json:"transition_sig,omitempty"`

	// relationship-update (Type, Timestamp, Actor and PublicKey are shared).
	// Agent, Grant and GrantState are present only for a user agent's act.
	SourceURL  string `json:"source_url,omitempty"`
	TargetURL  string `json:"target_url,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Grant      string `json:"grant,omitempty"`
	GrantState string `json:"grant_state,omitempty"`

	// The witness itself. DS is the service's public base URL — where the key
	// named by DSKeyID is published. WitnessedAt is the DS's own clock, carried
	// verbatim; it is compared, never re-rendered.
	DS          string `json:"ds"`
	DSKeyID     string `json:"ds_key_id"`
	WitnessedAt string `json:"witnessed_at"`
	Signature   string `json:"signature"`

	// Extra carries members this build does not model, so a site that stores
	// a witness (witnesses.json, a key-history entry) keeps them — a newer DS
	// may add one, and the record may be the only copy (MergeWitnesses).
	//
	// ⭐ It changes nothing the signature covers: Canonical builds the signed
	// bytes from witnessFields by name and never re-marshals the struct. A
	// member the DS SIGNS but this build does not model therefore still fails
	// VerifySignature here — correctly, since this build cannot check it — and
	// keeping it is what lets a newer verifier check it later.
	Extra map[string]json.RawMessage `json:"-"`
}

// witnessMembers is Witness without its methods (see witness_extra.go).
type witnessMembers Witness

// witnessFields is the signed field list per action. An empty field is omitted
// from the canonical object rather than signed as "".
var witnessFields = map[string][]string{
	WitnessActionContent: {
		"action", "type", "url", "version", "author", "actor", "public_key", "artifact_hash",
		"ds", "ds_key_id", "witnessed_at",
	},
	WitnessActionKeyRotation: {
		"action", "domain", "old_key", "new_key", "timestamp", "transition_sig",
		"ds", "ds_key_id", "witnessed_at",
	},
	WitnessActionRelationship: {
		"action", "type", "source_url", "target_url", "decision", "timestamp", "actor", "public_key",
		"agent", "grant", "grant_state",
		"ds", "ds_key_id", "witnessed_at",
	},
}

func (w Witness) field(name string) string {
	switch name {
	case "action":
		return w.Action
	case "type":
		return w.Type
	case "url":
		return w.URL
	case "version":
		return w.Version
	case "author":
		return w.Author
	case "actor":
		return w.Actor
	case "public_key":
		return w.PublicKey
	case "artifact_hash":
		return w.ArtifactHash
	case "source_url":
		return w.SourceURL
	case "target_url":
		return w.TargetURL
	case "decision":
		return w.Decision
	case "agent":
		return w.Agent
	case "grant":
		return w.Grant
	case "grant_state":
		return w.GrantState
	case "domain":
		return w.Domain
	case "old_key":
		return w.OldKey
	case "new_key":
		return w.NewKey
	case "timestamp":
		return w.Timestamp
	case "transition_sig":
		return w.TransitionSig
	case "ds":
		return w.DS
	case "ds_key_id":
		return w.DSKeyID
	case "witnessed_at":
		return w.WitnessedAt
	}
	return ""
}

// Canonical returns the exact bytes the witness signature covers: the action's
// fields, empty ones dropped, keys sorted, no whitespace, no HTML escaping —
// the same form as every signed DS response (BuildCanonicalJSON).
func (w Witness) Canonical() ([]byte, error) {
	fields, ok := witnessFields[w.Action]
	if !ok {
		return nil, fmt.Errorf("unknown witness action %q", w.Action)
	}
	signable := make(map[string]interface{}, len(fields))
	for _, f := range fields {
		if v := w.field(f); v != "" {
			signable[f] = v
		}
	}
	return []byte(BuildCanonicalJSON(signable)), nil
}

// VerifySignature checks the witness signature against a DS public key (either
// ssh-ed25519 or raw base64 form). It says nothing about whether the witnessed
// values match any artifact — that is the caller's comparison.
func (w Witness) VerifySignature(dsPublicKey string) error {
	if w.Signature == "" {
		return fmt.Errorf("witness carries no signature")
	}
	canonical, err := w.Canonical()
	if err != nil {
		return err
	}
	key, err := ensureSSHFormat(dsPublicKey)
	if err != nil {
		return err
	}
	ok, err := signing.VerifySignature(canonical, []byte(key), w.Signature)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("witness signature does not verify against %s key %q", w.DS, w.DSKeyID)
	}
	return nil
}

// WitnessedTime parses WitnessedAt. The string itself is never rewritten.
func (w Witness) WitnessedTime() (time.Time, error) {
	return time.Parse(time.RFC3339Nano, w.WitnessedAt)
}

// ArtifactHash is the hash a registration carries as metadata.artifact_hash:
// sha256 over the artifact's SIGNING BASE — frontmatter and body exactly as the
// author's signature covers them (signing.MarkdownSigningBase for markdown).
//
// ⭐ WHY NOT `version`. For posts and comments `version` hashes the canonicalized
// BODY only, so a witness over `version` alone does not bind `title`, the
// `license:` block, or `published:` — the self-reported signing time a retired
// key's backdating turns on. This does. It rides inside `metadata`, which the
// registration signature already covers, so no wire field and no signed
// artifact byte changes.
func ArtifactHash(signingBase string) string {
	sum := sha256.Sum256([]byte(signingBase))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// witnessIdentity is what makes two witnesses the SAME testimony: the same DS
// saw the same thing. Which DS key signed it and when do not distinguish them.
func witnessIdentity(w Witness) string {
	switch w.Action {
	case WitnessActionKeyRotation:
		return strings.Join([]string{w.DS, w.Action, w.Domain, w.OldKey, w.NewKey, w.Timestamp}, "\x00")
	case WitnessActionRelationship:
		return strings.Join([]string{w.DS, w.Action, w.Type, w.SourceURL, w.TargetURL, w.Decision, w.Timestamp}, "\x00")
	default:
		return strings.Join([]string{w.DS, w.Action, w.Type, w.URL, w.Version, w.ArtifactHash}, "\x00")
	}
}

// earlier reports whether a was witnessed before b. Unparseable times compare as
// strings, which is still deterministic.
func earlier(a, b Witness) bool {
	ta, errA := a.WitnessedTime()
	tb, errB := b.WitnessedTime()
	if errA == nil && errB == nil {
		return ta.Before(tb)
	}
	return a.WitnessedAt < b.WitnessedAt
}

// MergeWitnesses adds witnesses to a set and reports whether it changed.
//
// ⛔ IT NEVER REMOVES ONE. A discovery service holds only the CURRENT version's
// witness (its rows are upserted), so anything a later source does not return is
// SUPERSEDED, not absent — replacing a set with what a DS knows today would
// silently delete every earlier version's testimony. A countersignature is a
// statement about the past and is never revoked.
//
// Two records of the same testimony (same DS, same observation) collapse to the
// EARLIEST witnessed_at: a later witness of the same bytes adds nothing, and
// repeated re-registration would otherwise grow the set forever.
func MergeWitnesses(existing []Witness, add ...Witness) ([]Witness, bool) {
	out := append([]Witness(nil), existing...)
	index := make(map[string]int, len(out))
	for i, w := range out {
		index[witnessIdentity(w)] = i
	}
	changed := false
	for _, w := range add {
		id := witnessIdentity(w)
		if i, ok := index[id]; ok {
			if earlier(w, out[i]) {
				out[i] = w
				changed = true
			}
			continue
		}
		index[id] = len(out)
		out = append(out, w)
		changed = true
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].WitnessedAt != out[j].WitnessedAt {
			return earlier(out[i], out[j])
		}
		return out[i].DS < out[j].DS
	})
	return out, changed
}

// FetchDSPublicKey returns the public key a discovery service publishes under
// keyID, from GET <ds>/v1/sites/public-key?key_id=<id>. That endpoint serves
// retired DS keys too, which is what keeps a witness verifiable after the DS
// rotates.
//
// ⚠️ This is the witness's equivalent of fetching a site's .well-known/polis:
// the key comes from the party that signed. A caller that cannot reach it must
// report the witness as NOT CHECKED — never as failed, and never as absent.
//
// requestID, when non-empty, is sent as X-Request-Id: the fetch crosses into the
// DS, and the baseline is that a correlation id travels across every boundary.
func FetchDSPublicKey(hc *http.Client, dsURL, keyID, requestID string) (string, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	endpoint := strings.TrimRight(dsURL, "/") + "/v1/sites/public-key?key_id=" + url.QueryEscape(keyID)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, endpoint)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}
	var result struct {
		PublicKey string `json:"public_key"`
		KeyID     string `json:"key_id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.PublicKey == "" {
		return "", fmt.Errorf("%s publishes no key %q", dsURL, keyID)
	}
	if result.KeyID != "" && result.KeyID != keyID {
		return "", fmt.Errorf("%s answered for key %q when asked for %q", dsURL, result.KeyID, keyID)
	}
	return ensureSSHFormat(result.PublicKey)
}
