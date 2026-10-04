package main

import (
	"context"
	"errors"
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
		"native error": []byte(`{"schema_version":1,"request_id":"seamprobe-1","status":"error","error":"bridge failed"}`),
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

func TestValidateCompletionHandle(t *testing.T) {
	t.Parallel()
	if err := validateCompletionHandle(completion{handle: 71}, 71); err != nil {
		t.Fatalf("matching callback handle rejected: %v", err)
	}
	if err := validateCompletionHandle(completion{handle: 72}, 71); err == nil {
		t.Fatal("mismatched callback handle was accepted")
	}
}

func TestResolveTerminalAfterCancel(t *testing.T) {
	t.Parallel()
	contextErr := context.DeadlineExceeded
	tests := []struct {
		name        string
		payload     []byte
		wantStatus  string
		wantContext bool
		wantErr     bool
	}{
		{
			name:       "completed wins over cancellation",
			payload:    []byte(`{"schema_version":1,"request_id":"seamprobe-1","status":"completed","result":"hello"}`),
			wantStatus: "completed",
		},
		{
			name:        "native cancelled preserves context error",
			payload:     []byte(`{"schema_version":1,"request_id":"seamprobe-1","status":"cancelled","error":"cancelled"}`),
			wantStatus:  "cancelled",
			wantContext: true,
			wantErr:     true,
		},
		{
			name:    "malformed response is not mapped to cancellation",
			payload: []byte("{"),
			wantErr: true,
		},
		{
			name:    "wrong request id is not mapped to cancellation",
			payload: []byte(`{"schema_version":1,"request_id":"other","status":"completed","result":"hello"}`),
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			out, err := resolveTerminalAfterCancel(test.payload, contextErr, 0)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error %t", err, test.wantErr)
			}
			if test.wantErr && test.wantContext && !errors.Is(err, contextErr) {
				t.Fatalf("error %v does not wrap %v", err, contextErr)
			}
			if out.Status != test.wantStatus {
				t.Fatalf("status = %q, want %q", out.Status, test.wantStatus)
			}
		})
	}
}
