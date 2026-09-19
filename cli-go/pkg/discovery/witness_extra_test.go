package discovery

import (
	"encoding/json"
	"strings"
	"testing"
)

// A witness carries members this build does not model through a round-trip
// (witnesses.json is unsigned by the site; each record's DS signature covers
// only witnessFields), and an unmodelled member changes nothing it verifies.
func TestAWitnessKeepsAnUnmodelledMemberAndStillVerifies(t *testing.T) {
	w, dsKey := signedWitness(t, contentWitness())
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	withExtra := strings.TrimSuffix(string(b), "}") + `,"ds_note":{"from":"a newer DS"}}`

	var got Witness
	if err := json.Unmarshal([]byte(withExtra), &got); err != nil {
		t.Fatal(err)
	}
	if err := got.VerifySignature(dsKey); err != nil {
		t.Fatalf("an unmodelled member must not change what the DS signature covers: %v", err)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != withExtra {
		t.Fatalf("round-trip lost or moved a member:\n want %s\n got  %s", withExtra, out)
	}
}

// With nothing unmodelled, a witness encodes exactly as the plain struct did.
func TestAWitnessWithNothingUnmodelledEncodesAsBefore(t *testing.T) {
	w, _ := signedWitness(t, contentWitness())
	got, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(witnessMembers(w))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("encoding changed:\n want %s\n got  %s", want, got)
	}
}
