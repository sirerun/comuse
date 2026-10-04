package main

import (
	"strings"
	"testing"
)

func TestParseHelloResponse(t *testing.T) {
	t.Parallel()
	valid := []byte(`{"schema_version":1,"request_id":"seamprobe-1","status":"completed","result":"hello"}`)
	out, err := parseHelloResponse(valid)
	if err != nil {
		t.Fatal(err)
	}
	if out.Result == nil || *out.Result != "hello" {
		t.Fatalf("result = %v, want hello", out.Result)
	}
}

func TestParseHelloResponseRejectsInvalidOrUnexpectedEnvelopes(t *testing.T) {
	t.Parallel()
	cases := map[string][]byte{
		"malformed":    []byte("{"),
		"wrong schema": []byte(`{"schema_version":2,"request_id":"seamprobe-1","status":"completed","result":"hello"}`),
		"cancelled":    []byte(`{"schema_version":1,"request_id":"seamprobe-1","status":"cancelled","error":"cancelled"}`),
		"wrong id":     []byte(`{"schema_version":1,"request_id":"other","status":"completed","result":"hello"}`),
		"wrong result": []byte(`{"schema_version":1,"request_id":"seamprobe-1","status":"completed","result":"fabricated"}`),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseHelloResponse(payload); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	if _, err := parseHelloResponse([]byte(strings.Repeat("x", 64*1024+1))); err == nil {
		t.Fatal("expected oversized response rejection")
	}
}
