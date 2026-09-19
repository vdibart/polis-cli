package attestation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestPredicateAndPinFormatsMatchTheSharedVectors holds predicateRe and
// versionPinRe to the vector file the DS's validateRegister is tested against
// (discovery-service/core/attestation_vectors_test.ts). The two regexes are
// byte-identical today with nothing else holding them together: a record the
// writer accepts and the DS refuses is issued and never indexed. A format
// change edits the vector file first, and both tests follow.
//
// Format only — `pub.polis.attestation.withdrawal` is a valid predicate here
// even though Issue refuses it, so this asserts the regexes, not Issue.
func TestPredicateAndPinFormatsMatchTheSharedVectors(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..",
		"discovery-service", "core", "contract-fixtures", "attestation-predicate-vectors.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if _, derr := os.Stat(filepath.Join(filepath.Dir(file), "..", "..", "..", "discovery-service")); os.IsNotExist(derr) {
			t.Skip("the shared contract fixtures live with the discovery service source, which is not in this repository")
		}
		t.Fatalf("read shared vectors: %v", err)
	}
	type set struct {
		Valid   []string `json:"valid"`
		Invalid []string `json:"invalid"`
	}
	var v struct {
		Predicate      set `json:"predicate"`
		SubjectVersion set `json:"subject_version"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parse shared vectors: %v", err)
	}
	if len(v.Predicate.Valid) == 0 || len(v.Predicate.Invalid) == 0 ||
		len(v.SubjectVersion.Valid) == 0 || len(v.SubjectVersion.Invalid) == 0 {
		t.Fatal("shared vector file has an empty section; the test would pass vacuously")
	}
	for _, s := range v.Predicate.Valid {
		if !predicateRe.MatchString(s) {
			t.Errorf("predicate %q: shared vectors say valid, predicateRe refuses it", s)
		}
	}
	for _, s := range v.Predicate.Invalid {
		if predicateRe.MatchString(s) {
			t.Errorf("predicate %q: shared vectors say invalid, predicateRe accepts it", s)
		}
	}
	for _, s := range v.SubjectVersion.Valid {
		if !versionPinRe.MatchString(s) {
			t.Errorf("subject_version %q: shared vectors say valid, versionPinRe refuses it", s)
		}
	}
	for _, s := range v.SubjectVersion.Invalid {
		if versionPinRe.MatchString(s) {
			t.Errorf("subject_version %q: shared vectors say invalid, versionPinRe accepts it", s)
		}
	}
}
