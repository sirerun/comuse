package jsonwire

import (
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
