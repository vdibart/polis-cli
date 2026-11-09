package secret

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The secret used throughout. ⭐ Distinctive enough that a leak through any
// path shows up as this exact string rather than something plausible.
const canary = "sk-live-CANARY-must-never-appear-7f3a9c"

// ⭐⭐ THE CENTRAL TEST OF THIS PACKAGE. Never-logged was the one property with
// no implementation anywhere in the codebase, and every verb below is a way
// somebody has leaked a secret in some other project.
func TestEveryFormattingVerbRedacts(t *testing.T) {
	s := New(canary)

	for _, verb := range []string{"%v", "%s", "%q", "%#v", "%+v", "%x", "%X"} {
		got := fmt.Sprintf(verb, s)
		if strings.Contains(got, canary) {
			t.Errorf("%s leaked the secret: %s", verb, got)
		}
	}

	// A Value nested in a struct, which is how it will actually travel.
	type carrier struct {
		Name string
		Key  Value
	}
	c := carrier{Name: "resend", Key: s}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if got := fmt.Sprintf(verb, c); strings.Contains(got, canary) {
			t.Errorf("nested %s leaked the secret: %s", verb, got)
		}
	}

	// And in the map shape the loggers use for event fields.
	fields := map[string]interface{}{"key": s}
	if got := fmt.Sprintf("%v", fields); strings.Contains(got, canary) {
		t.Errorf("map value leaked the secret: %s", got)
	}
}

func TestJSONAndTextMarshalRedact(t *testing.T) {
	s := New(canary)

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), canary) {
		t.Errorf("MarshalJSON leaked the secret: %s", b)
	}

	// ⚠️ The nested case is the one that matters: an event payload is a struct
	// or map that happens to contain a secret, never the secret alone.
	type event struct {
		Action string `json:"action"`
		Key    Value  `json:"key"`
	}
	b, err = json.Marshal(event{Action: "boot", Key: s})
	if err != nil {
		t.Fatalf("marshal nested: %v", err)
	}
	if strings.Contains(string(b), canary) {
		t.Errorf("nested MarshalJSON leaked the secret: %s", b)
	}

	tb, err := s.MarshalText()
	if err != nil {
		t.Fatalf("marshal text: %v", err)
	}
	if strings.Contains(string(tb), canary) {
		t.Errorf("MarshalText leaked the secret: %s", tb)
	}
}

// ⛔ A secret arrives from the platform, never from a document we parsed. If a
// Value could be unmarshalled, a request body could set one.
func TestValueCannotBeUnmarshalled(t *testing.T) {
	var s Value
	if err := json.Unmarshal([]byte(`"`+canary+`"`), &s); err == nil {
		t.Fatal("unmarshalling a secret succeeded; it must be refused")
	}
	if s.Expose() != "" {
		t.Errorf("a refused unmarshal still set the value: %q", s.Expose())
	}
}

// Expose is the only way out, and it must actually work — a redaction that
// also breaks the legitimate read would get removed by the next person.
func TestExposeReturnsTheValue(t *testing.T) {
	if got := New(canary).Expose(); got != canary {
		t.Errorf("Expose() = %q, want the original value", got)
	}
	if !New("").IsZero() || New("x").IsZero() {
		t.Error("IsZero does not track emptiness")
	}
}
