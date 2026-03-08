package stream

import (
	"encoding/json"
	"time"
)

// VerificationState tracks signature verification failures across sync cycles.
type VerificationState struct {
	DSFailures     DSFailureStats               `json:"ds_failures"`
	AuthorFailures map[string]*AuthorFailureStat `json:"author_failures,omitempty"`
}

// DSFailureStats tracks discovery service envelope verification failures.
type DSFailureStats struct {
	Total       int        `json:"total"`
	LastFailure *time.Time `json:"last_failure"`
	Consecutive int        `json:"consecutive"`
}

// AuthorFailureStat tracks per-domain author signature verification failures.
type AuthorFailureStat struct {
	Total       int        `json:"total"`
	LastFailure *time.Time `json:"last_failure"`
	EventTypes  []string   `json:"event_types"`
}

// RecordDSFailure increments DS failure counters.
func (v *VerificationState) RecordDSFailure() {
	now := time.Now().UTC()
	v.DSFailures.Total++
	v.DSFailures.Consecutive++
	v.DSFailures.LastFailure = &now
}

// RecordDSSuccess resets the consecutive failure counter.
func (v *VerificationState) RecordDSSuccess() {
	v.DSFailures.Consecutive = 0
}

// RecordAuthorFailure increments per-domain author failure counters.
func (v *VerificationState) RecordAuthorFailure(domain, eventType string) {
	if v.AuthorFailures == nil {
		v.AuthorFailures = make(map[string]*AuthorFailureStat)
	}
	stat, ok := v.AuthorFailures[domain]
	if !ok {
		stat = &AuthorFailureStat{}
		v.AuthorFailures[domain] = stat
	}
	now := time.Now().UTC()
	stat.Total++
	stat.LastFailure = &now

	// Add event type if not already tracked
	for _, t := range stat.EventTypes {
		if t == eventType {
			return
		}
	}
	stat.EventTypes = append(stat.EventTypes, eventType)
}

// RecentAuthorFailures returns domains with failures in the last duration.
func (v *VerificationState) RecentAuthorFailures(window time.Duration) map[string]*AuthorFailureStat {
	cutoff := time.Now().UTC().Add(-window)
	result := make(map[string]*AuthorFailureStat)
	for domain, stat := range v.AuthorFailures {
		if stat.LastFailure != nil && stat.LastFailure.After(cutoff) {
			result[domain] = stat
		}
	}
	return result
}

// ShouldSuspendSync returns true if consecutive DS failures exceed the threshold.
func (v *VerificationState) ShouldSuspendSync(maxConsecutive int) bool {
	if maxConsecutive <= 0 {
		maxConsecutive = 3
	}
	return v.DSFailures.Consecutive >= maxConsecutive
}

// Load reads verification state from a store.
func (v *VerificationState) Load(store *Store) error {
	return store.LoadState("verification", v)
}

// Save writes verification state to a store.
func (v *VerificationState) Save(store *Store) error {
	return store.SaveState("verification", v)
}

// MarshalJSON implements json.Marshaler (ensures AuthorFailures is {} not null).
func (v VerificationState) MarshalJSON() ([]byte, error) {
	type Alias VerificationState
	if v.AuthorFailures == nil {
		v.AuthorFailures = make(map[string]*AuthorFailureStat)
	}
	return json.Marshal(Alias(v))
}

// VerificationConfig holds user-configurable verification settings.
// Stored at .polis/ds/<domain>/pub.polis.core/config/verification.json
type VerificationConfig struct {
	RequireAuthorSignatures bool `json:"require_author_signatures"`
	AuthorKeyCacheTTLMin    int  `json:"author_key_cache_ttl_minutes,omitempty"`
	DSKeyCacheTTLMin        int  `json:"ds_key_cache_ttl_minutes,omitempty"`
	MaxConsecutiveDSFails   int  `json:"max_consecutive_ds_failures,omitempty"`
}

// DefaultVerificationConfig returns the default verification config.
func DefaultVerificationConfig() VerificationConfig {
	return VerificationConfig{
		RequireAuthorSignatures: false, // will flip to true after legacy events drain
		AuthorKeyCacheTTLMin:    60,
		DSKeyCacheTTLMin:        60,
		MaxConsecutiveDSFails:   3,
	}
}

// LoadVerificationConfig loads the verification config from a store, returning defaults if absent.
func LoadVerificationConfig(store *Store) VerificationConfig {
	var cfg VerificationConfig
	if err := store.LoadConfig("verification", &cfg); err != nil {
		return DefaultVerificationConfig()
	}
	// Apply defaults for zero values
	if cfg.AuthorKeyCacheTTLMin <= 0 {
		cfg.AuthorKeyCacheTTLMin = 60
	}
	if cfg.DSKeyCacheTTLMin <= 0 {
		cfg.DSKeyCacheTTLMin = 60
	}
	if cfg.MaxConsecutiveDSFails <= 0 {
		cfg.MaxConsecutiveDSFails = 3
	}
	return cfg
}
