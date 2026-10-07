// Package cli provides the shared, envelope-only command dispatcher used by
// the production CLI and external protocol fixtures.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/sirerun/comuse"
	"github.com/sirerun/comuse/internal/jsonwire"
)

const maxRequestBytes = 32768

// Run executes one command or a persistent serve session. The caller owns
// trusted session construction and host configuration.
func Run(ctx context.Context, session *comuse.Session, args []string, in io.Reader, out, diagnostics io.Writer) int {
	if len(args) != 1 {
		return writeRejection(out, "invalid_request")
	}
	operation, ok := operationFor(args[0])
	if !ok {
		return writeRejection(out, "invalid_request")
	}
	if operation == "serve" {
		if err := Serve(ctx, session, in, out); err != nil {
			return exitFor(comuse.ErrorCode(err))
		}
		return 0
	}
	raw, err := io.ReadAll(io.LimitReader(in, maxRequestBytes+1))
	if err != nil || len(raw) > maxRequestBytes {
		return writeRejection(out, "invalid_request")
	}
	return Dispatch(ctx, session, operation, raw, out)
}

// Dispatch decodes an operation-specific flat request through the shared wire
// decoder, invokes the shared Session.Call path, and writes one envelope.
func Dispatch(ctx context.Context, session *comuse.Session, operation comuse.Operation, raw []byte, out io.Writer) int {
	envelope, code := dispatch(ctx, session, operation, raw)
	if err := json.NewEncoder(out).Encode(envelope); err != nil {
		return 1
	}
	return code
}

func dispatch(ctx context.Context, session *comuse.Session, operation comuse.Operation, raw []byte) (comuse.ResultEnvelope, int) {
	request, err := comuse.DecodeRequest(operation, raw)
	if err != nil {
		return rejection("invalid_request")
	}
	envelope, callErr := session.Call(ctx, request)
	if callErr != nil && envelope.Error == nil {
		return rejection("internal_error")
	}
	if envelope.Error != nil {
		return envelope, exitFor(envelope.Error.Code)
	}
	return envelope, 0
}

// Serve reads bounded, newline-delimited flat operation requests. Each line
// uses Dispatch, the same decoder, session call, and envelope serialization as
// one-shot commands.
func Serve(ctx context.Context, session *comuse.Session, in io.Reader, out io.Writer) error {
	if ctx == nil || session == nil || in == nil || out == nil {
		return cliError("invalid_request")
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
			if closer, ok := in.(io.Closer); ok {
				_ = closer.Close()
			}
		}
	}()

	reader := bufio.NewReaderSize(in, maxRequestBytes+1)
	for {
		if ctx.Err() != nil {
			return cliError("cancelled")
		}
		line, readErr := reader.ReadSlice('\n')
		if ctx.Err() != nil {
			return cliError("cancelled")
		}
		if errors.Is(readErr, bufio.ErrBufferFull) || len(line) > maxRequestBytes {
			if writeRejection(out, "invalid_request") == 1 {
				return cliError("internal_error")
			}
			return cliError("invalid_request")
		}
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			if writeRejection(out, "invalid_request") == 1 {
				return cliError("internal_error")
			}
			return cliError("invalid_request")
		}

		operation, raw, decodeErr := decodeServeFrame(line)
		if decodeErr != nil {
			if writeRejection(out, "invalid_request") == 1 {
				return cliError("internal_error")
			}
		} else {
			envelope, _ := dispatch(ctx, session, operation, raw)
			if err := json.NewEncoder(out).Encode(envelope); err != nil {
				return cliError("internal_error")
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
	}
}

type serveFrame struct {
	Command json.RawMessage `json:"command"`
}

func decodeServeFrame(line []byte) (comuse.Operation, []byte, error) {
	var fields map[string]json.RawMessage
	if err := jsonwire.Decode(bytes.NewReader(line), maxRequestBytes, &fields); err != nil {
		return "", nil, errors.New("invalid_request")
	}
	commandRaw, ok := fields["command"]
	if !ok || len(commandRaw) == 0 || bytes.Equal(bytes.TrimSpace(commandRaw), []byte("null")) {
		return "", nil, errors.New("invalid_request")
	}
	var frame serveFrame
	if err := json.Unmarshal([]byte(`{"command":`+string(commandRaw)+`}`), &frame); err != nil || len(frame.Command) == 0 {
		return "", nil, errors.New("invalid_request")
	}
	var command string
	if err := json.Unmarshal(frame.Command, &command); err != nil || command == "" {
		return "", nil, errors.New("invalid_request")
	}
	operation, ok := operationFor(command)
	if !ok || operation == "serve" {
		return "", nil, errors.New("invalid_request")
	}
	delete(fields, "command")
	raw, err := json.Marshal(fields)
	if err != nil {
		return "", nil, errors.New("invalid_request")
	}
	return operation, raw, nil
}

func operationFor(command string) (comuse.Operation, bool) {
	switch command {
	case "doctor":
		return comuse.OperationDoctor, true
	case "state":
		return comuse.OperationState, true
	case "windows":
		return comuse.OperationWindows, true
	case "a11y":
		return comuse.OperationObserve, true
	case "read-element":
		return comuse.OperationReadElement, true
	case "wait":
		return comuse.OperationWait, true
	case "click-element":
		return comuse.OperationClickElement, true
	case "element-action":
		return comuse.OperationElementAction, true
	case "write-element":
		return comuse.OperationWriteElement, true
	case "scroll-element":
		return comuse.OperationScrollElement, true
	case "click":
		return comuse.OperationClick, true
	case "type-text":
		return comuse.OperationTypeText, true
	case "press-key":
		return comuse.OperationPressKey, true
	case "scroll":
		return comuse.OperationScroll, true
	case "drag":
		return comuse.OperationDrag, true
	case "focus-window":
		return comuse.OperationFocusWindow, true
	case "serve":
		return "serve", true
	default:
		return "", false
	}
}

// IsCommand reports whether a positional CLI token names a supported route.
func IsCommand(command string) bool {
	_, ok := operationFor(command)
	return ok
}

func writeRejection(out io.Writer, code string) int {
	envelope, status := rejection(code)
	if json.NewEncoder(out).Encode(envelope) != nil {
		return 1
	}
	return status
}

func rejection(code string) (comuse.ResultEnvelope, int) {
	envelope, err := comuse.RejectionEnvelope(code)
	if err != nil {
		return comuse.ResultEnvelope{}, 1
	}
	if envelope.Error != nil {
		return envelope, exitFor(envelope.Error.Code)
	}
	return envelope, 1
}

func exitFor(code string) int {
	switch code {
	case "invalid_request", "policy_refused", "approval_required":
		return 2
	case "permission_denied", "unsupported", "backend_unavailable":
		return 3
	case "desktop_busy":
		return 4
	default:
		return 1
	}
}

func cliError(code string) error { return &comuse.Error{Code: code, Message: code} }
