// Package jsonwire rejects oversized, ambiguous and unknown JSON input.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

func Decode(r io.Reader, maxBytes int64, dst any) error {
	b, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return errors.New("cannot read JSON")
	}
	if !utf8.Valid(b) {
		return errors.New("invalid JSON encoding")
	}
	if int64(len(b)) > maxBytes {
		return errors.New("JSON exceeds limit")
	}
	if len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
		return errors.New("JSON object required")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if err = unique(d, 0); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return errors.New("extra JSON content")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(dst); err != nil {
		return errors.New("invalid JSON fields or types")
	}
	return nil
}
func unique(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds limit")
	}
	t, err := d.Token()
	if err != nil {
		return errors.New("invalid JSON")
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return errors.New("invalid JSON object")
			}
			name, yes := k.(string)
			if !yes || seen[name] {
				return errors.New("duplicate JSON field")
			}
			seen[name] = true
			if e = unique(d, depth+1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for d.More() {
			if err = unique(d, depth+1); err != nil {
				return err
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}
