//go:build darwin && cgo

package main

type request struct {
	SchemaVersion int    `json:"schema_version"`
	RequestID     string `json:"request_id"`
	Op            string `json:"op"`
}

type completion struct {
	handle uint64
	bytes  []byte
	err    error
}
