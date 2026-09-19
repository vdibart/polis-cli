package jsonextra

import (
	"encoding/json"
	"testing"
)

type sample struct {
	Zed   string                     `json:"zed"`
	Alpha string                     `json:"alpha,omitempty"`
	Mid   int                        `json:"mid"`
	Extra map[string]json.RawMessage `json:"-"`
}

func TestMarshalKeepsDeclarationOrderThenSortedExtras(t *testing.T) {
	s := sample{Zed: "z", Alpha: "a", Mid: 1, Extra: map[string]json.RawMessage{
		"yak": json.RawMessage(`{ "b": 1 }`),
		"ant": json.RawMessage(`[1, 2]`),
	}}
	got, err := Marshal(s, s.Extra)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"zed":"z","alpha":"a","mid":1,"ant":[1,2],"yak":{"b":1}}` {
		t.Fatalf("got %s", got)
	}
}

func TestMarshalWithNoExtrasIsExactlyJSONMarshal(t *testing.T) {
	s := sample{Zed: "z", Mid: 2}
	got, err := Marshal(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(s)
	if string(got) != string(want) {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestMarshalIntoAnEmptyObject(t *testing.T) {
	var empty struct {
		A string `json:"a,omitempty"`
	}
	got, err := Marshal(empty, map[string]json.RawMessage{"x": json.RawMessage(`1`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"x":1}` {
		t.Fatalf("got %s", got)
	}
}

func TestADeclaredNameIsNeverResurrectedFromExtra(t *testing.T) {
	s := sample{Zed: "z"}
	extra := map[string]json.RawMessage{"alpha": json.RawMessage(`"stale"`), "ALPHA": json.RawMessage(`"stale"`)}
	got, err := Marshal(s, extra)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(got, &m)
	if _, ok := m["alpha"]; ok {
		t.Fatalf("a cleared declared field came back from Extra: %s", got)
	}
	if _, ok := m["ALPHA"]; ok {
		t.Fatalf("a case-folded declared field came back from Extra: %s", got)
	}
}

func TestUnmarshalFilesOnlyUndeclaredMembers(t *testing.T) {
	type alias sample
	var a alias
	extra, err := Unmarshal([]byte(`{"zed":"z","Alpha":"a","mid":3,"new":true}`), &a)
	if err != nil {
		t.Fatal(err)
	}
	if a.Zed != "z" || a.Alpha != "a" || a.Mid != 3 {
		t.Fatalf("declared fields not decoded: %+v", a)
	}
	if len(extra) != 1 || string(extra["new"]) != "true" {
		t.Fatalf("extra = %v", extra)
	}
}

func TestUnmarshalOfNullKeepsNothing(t *testing.T) {
	type alias sample
	var a alias
	extra, err := Unmarshal([]byte(`null`), &a)
	if err != nil || extra != nil {
		t.Fatalf("extra=%v err=%v", extra, err)
	}
}

func TestMarshalSortedSortsEverything(t *testing.T) {
	var plain struct {
		Zed string `json:"zed"`
		Ant string `json:"ant"`
	}
	plain.Zed, plain.Ant = "z", "a"
	got, err := MarshalSorted(plain, map[string]json.RawMessage{"mid": json.RawMessage(`1`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ant":"a","mid":1,"zed":"z"}` {
		t.Fatalf("got %s", got)
	}
}
