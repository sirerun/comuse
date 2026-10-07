package jsonwire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStrictDecode(t *testing.T) {
	type value struct {
		Scope struct {
			PID int `json:"pid"`
		} `json:"scope"`
	}
	for _, text := range []string{`{"scope":{"pid":1,"pid":2}}`, `{"scope":{"pid":1},"extra":1}`, `{"scope":{"pid":1}} {}`, `null null`, `{"scope":{"pid":"x"}}`, `{"scope":{"pid":1},"bad":"` + string([]byte{255}) + `"}`, strings.Repeat("[", 66) + strings.Repeat("]", 66)} {
		var v value
		if Decode(strings.NewReader(text), 32768, &v) == nil {
			t.Fatalf("accepted invalid request %q", text)
		}
	}
	var v value
	if e := Decode(strings.NewReader(`{"scope":{"pid":1}}`), 32768, &v); e != nil || v.Scope.PID != 1 {
		t.Fatalf("valid: %+v %v", v, e)
	}
	if Decode(strings.NewReader(strings.Repeat(" ", 33)), 32, &v) == nil {
		t.Fatal("accepted oversized request")
	}
}

func TestStrictDecodeRequiresExactJSONTagSpellingRecursively(t *testing.T) {
	t.Parallel()
	type nested struct {
		Value int `json:"value"`
	}
	type value struct {
		Outer   nested          `json:"outer"`
		Maybe   *bool           `json:"maybe"`
		Payload json.RawMessage `json:"payload"`
	}
	for _, input := range []string{
		`{"Outer":{"value":1}}`,
		`{"outer":{"Value":1}}`,
	} {
		var got value
		if err := Decode(strings.NewReader(input), 32768, &got); err == nil {
			t.Fatalf("accepted case-aliased field: %s", input)
		}
	}
	var got value
	if err := Decode(strings.NewReader(`{"outer":{"value":1},"maybe":null,"payload":{"arbitrary":"fields"}}`), 32768, &got); err != nil {
		t.Fatalf("nullable pointer/RawMessage payload rejected: %v", err)
	}
	if got.Maybe != nil || string(got.Payload) != `{"arbitrary":"fields"}` {
		t.Fatalf("nullable/raw payload changed: %+v", got)
	}
}

func TestStrictDecodeExactTagsWithEmbeddedStruct(t *testing.T) {
	t.Parallel()
	type embedded struct {
		ID string `json:"id"`
	}
	type value struct {
		embedded
		Name string `json:"name"`
	}
	var got value
	if err := Decode(strings.NewReader(`{"id":"x","name":"y"}`), 32768, &got); err != nil {
		t.Fatalf("valid flattened embedded fields rejected: %v", err)
	}
	if err := Decode(strings.NewReader(`{"ID":"x","name":"y"}`), 32768, &got); err == nil {
		t.Fatal("accepted case alias on flattened embedded field")
	}
}
