package stream

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// LogSuppressedEmit emits a structured pub.polis.emit.suppressed record so that
// silent skips on the stream/DS publish path are visible in stderr → Fly →
// Axiom rather than vanishing.
//
// Background: prior to this helper, several code paths returned nil when a
// cross-boundary emit could not run (missing registration marker, missing
// config, etc.). Those silent returns hid a months-long incident in which
// every hosted tenant dropped every stream event because the local registration
// marker was never written. Every suppression must now leave a trail — on the
// HOSTED runtime, where being unregistered is anomalous.
//
// On the LOCAL runtime (self-hosted polis-server, CLI), being unregistered is
// the expected initial state of a fresh install: the user simply hasn't run
// `polis register` yet. Emitting a JSON suppression record on every publish/
// comment/follow gives them noise that looks like DS contact happened — when
// in fact the publish path correctly decided NOT to talk to DS. So the
// "not_registered_locally" reason is gated: it only fires when
// POLIS_DIAGNOSE_SUPPRESSIONS=1, which the hosted runtime
// (webapp/cmd/hosted/main.go) sets at startup. Other reasons (e.g.,
// "config_missing", which indicates a genuinely broken partial setup) still
// log unconditionally — those are useful diagnostic signal in any environment.
//
// The actor field is optional — pass empty string when not derivable.
// The context map carries event-type-specific extra fields.
//
// In CLI invocations this writes a JSON line to stderr; in the hosted runtime
// the Fly stdout/stderr collector forwards it to Axiom where the structured
// fields can be filtered.
func LogSuppressedEmit(eventType, reason, actor string, context map[string]interface{}) {
	// Gate the high-noise "not_registered_locally" reason on an explicit
	// opt-in env var. Local runtime: var unset → skip. Hosted runtime sets
	// the var so production prep-state still leaves a trail.
	if reason == "not_registered_locally" && os.Getenv("POLIS_DIAGNOSE_SUPPRESSIONS") != "1" {
		return
	}
	rec := map[string]interface{}{
		"ts":     time.Now().UTC().Format(time.RFC3339),
		"event":  "pub.polis.emit.suppressed",
		"source": "cli",
		"event_type": eventType,
		"reason": reason,
	}
	if actor != "" {
		rec["actor"] = actor
	}
	for k, v := range context {
		// Caller-controlled field names; namespace under "context_" only if
		// they collide with our reserved keys to avoid surprising overrides.
		if _, reserved := rec[k]; reserved {
			rec["context_"+k] = v
		} else {
			rec[k] = v
		}
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stderr, string(data))
}
