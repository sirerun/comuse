// Command comuse exposes the trusted host's bounded semantic session.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/sirerun/comuse"
	"github.com/sirerun/comuse/internal/backend"
	"github.com/sirerun/comuse/internal/backend/darwin"
	"github.com/sirerun/comuse/internal/jsonwire"
	comusemcp "github.com/sirerun/comuse/mcp"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"
)

// The runtime initializes main on the initial thread when init locks it.
func init() { runtime.LockOSThread() }

type hostConfig struct {
	LibraryPath string        `json:"library_path"`
	Scope       comuse.Scope  `json:"scope"`
	Budget      comuse.Budget `json:"budget"`
	AllowValues bool          `json:"allow_values"`
}
type request struct {
	Command    string `json:"command,omitempty"`
	WindowRef  string `json:"window_ref"`
	ElementRef string `json:"element_ref"`
	StateID    string `json:"state_id"`
	TimeoutMS  int64  `json:"timeout_ms"`
}
type stdio struct {
	io.Reader
	io.Writer
}

func (s stdio) Close() error {
	var errs []error
	if c, ok := s.Reader.(io.Closer); ok {
		errs = append(errs, c.Close())
	}
	if c, ok := s.Writer.(io.Closer); ok {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
func run(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) int {
	fs := flag.NewFlagSet("comuse", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	configPath := fs.String("config", "", "trusted scope and native library JSON file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *configPath == "" {
		_, _ = fmt.Fprintln(diagnostics, "usage: comuse --config FILE doctor|state|windows|a11y|read-element|wait|serve|mcp")
		return 2
	}
	command := fs.Arg(0)
	switch command {
	case "doctor", "state", "windows", "a11y", "read-element", "wait", "serve", "mcp":
	default:
		return emitError(out, "unsupported")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return emitError(out, "invalid_request")
	}
	var req request
	if command == "a11y" || command == "read-element" || command == "wait" {
		if jsonwire.Decode(in, 32768, &req) != nil {
			return emitError(out, "invalid_request")
		}
		if req.Command != "" {
			return emitError(out, "invalid_request")
		}
		if req.WindowRef == "" {
			return emitError(out, "invalid_request")
		}
		if command != "read-element" && (req.ElementRef != "" || req.StateID != "") {
			return emitError(out, "invalid_request")
		}
		if command != "wait" && req.TimeoutMS != 0 {
			return emitError(out, "invalid_request")
		}
		if command == "wait" && (req.TimeoutMS < 1 || req.TimeoutMS > 30000) {
			return emitError(out, "invalid_request")
		}
		if command == "read-element" && (req.ElementRef == "" || req.StateID == "") {
			return emitError(out, "invalid_request")
		}
	}
	var result any
	err = darwin.Run(ctx, backend.Config{Scope: cfg.Scope, LibraryPath: cfg.LibraryPath, AllowValues: cfg.AllowValues}, func(b backend.Backend) (callErr error) {
		resolvedScope, scopeErr := darwin.ResolvedScope(b)
		if scopeErr != nil {
			return scopeErr
		}
		s, e := comuse.NewSession(comuse.Config{Backend: b, Scope: resolvedScope, Budget: cfg.Budget, AllowValues: cfg.AllowValues})
		if e != nil {
			return e
		}
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if closeErr := s.Close(closeCtx); closeErr != nil {
				callErr = closeErr
			}
		}()
		switch command {
		case "doctor":
			result, callErr = s.Doctor(ctx)
		case "state":
			result, callErr = s.State(ctx)
		case "windows":
			result, callErr = s.Windows(ctx)
		case "a11y":
			result, callErr = s.Observe(ctx, req.WindowRef)
		case "read-element":
			result, callErr = s.ReadElement(ctx, req.WindowRef, req.ElementRef, req.StateID)
		case "wait":
			result, callErr = s.Wait(ctx, req.WindowRef, time.Duration(req.TimeoutMS)*time.Millisecond)
		case "serve":
			callErr = serveCLI(ctx, s, in, out)
		case "mcp":
			callErr = comusemcp.Serve(ctx, s, stdio{in, out})
		}
		return callErr
	})
	if command == "mcp" || command == "serve" {
		if err != nil {
			_, _ = fmt.Fprintln(diagnostics, "comuse:", comuse.ErrorCode(err))
			return exitFor(comuse.ErrorCode(err))
		}
		return 0
	}
	if err != nil {
		return emitError(out, comuse.ErrorCode(err))
	}
	if e := json.NewEncoder(out).Encode(comuse.Envelope{SchemaVersion: comuse.SchemaVersion, Status: "ok", Result: result}); e != nil {
		return 1
	}
	return 0
}
func loadConfig(path string) (hostConfig, error) {
	var c hostConfig
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	if err = jsonwire.Decode(f, 32768, &c); err != nil {
		return c, err
	}
	if c.LibraryPath == "" || !filepath.IsAbs(c.LibraryPath) {
		return c, fmt.Errorf("invalid library path")
	}
	return c, nil
}
func safeCode(code string) string {
	switch code {
	case "invalid_request", "policy_refused", "approval_required", "element_stale", "state_expired", "permission_denied", "unsupported", "backend_unavailable", "desktop_busy", "rate_limited", "budget_exceeded", "cancelled", "session_closed", "unknown_outcome":
		return code
	default:
		return "internal_error"
	}
}
func errorEnvelope(code string) comuse.Envelope {
	code = safeCode(code)
	return comuse.Envelope{SchemaVersion: comuse.SchemaVersion, Status: "error", Error: &comuse.Error{Code: code, Message: code}}
}
func emitError(out io.Writer, code string) int {
	env := errorEnvelope(code)
	if json.NewEncoder(out).Encode(env) != nil {
		return 1
	}
	return exitFor(env.Error.Code)
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

// serveCLI keeps references and baselines within one host session.
func serveCLI(ctx context.Context, s *comuse.Session, in io.Reader, out io.Writer) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
			if c, ok := in.(io.Closer); ok {
				_ = c.Close()
			}
		}
	}()
	r := bufio.NewReaderSize(in, 32769)
	for {
		if ctx.Err() != nil {
			return &comuse.Error{Code: "cancelled", Message: "cancelled"}
		}
		line, e := r.ReadSlice('\n')
		if ctx.Err() != nil {
			return &comuse.Error{Code: "cancelled", Message: "cancelled"}
		}
		if errors.Is(e, bufio.ErrBufferFull) {
			return &comuse.Error{Code: "budget_exceeded", Message: "request exceeds limit"}
		}
		if len(line) == 0 && errors.Is(e, io.EOF) {
			if ctx.Err() != nil {
				return &comuse.Error{Code: "cancelled", Message: "cancelled"}
			}
			return nil
		}
		if e != nil && !errors.Is(e, io.EOF) {
			return &comuse.Error{Code: "invalid_request", Message: "cannot read request"}
		}
		var q request
		if jsonwire.Decode(bytes.NewReader(line), 32768, &q) != nil {
			if json.NewEncoder(out).Encode(errorEnvelope("invalid_request")) != nil {
				return &comuse.Error{Code: "internal_error", Message: "response unavailable"}
			}
			if errors.Is(e, io.EOF) {
				return nil
			}
			continue
		}
		result, callErr := invoke(ctx, s, q)
		env := comuse.Envelope{SchemaVersion: comuse.SchemaVersion, Status: "ok", Result: result}
		if callErr != nil {
			env = errorEnvelope(comuse.ErrorCode(callErr))
		}
		if json.NewEncoder(out).Encode(env) != nil {
			return &comuse.Error{Code: "internal_error", Message: "response unavailable"}
		}
		if errors.Is(e, io.EOF) {
			return nil
		}
	}
}
func invoke(ctx context.Context, s *comuse.Session, q request) (any, error) {
	invalid := func() (any, error) { return nil, &comuse.Error{Code: "invalid_request", Message: "invalid request"} }
	switch q.Command {
	case "doctor", "state", "windows":
		if q.WindowRef != "" || q.ElementRef != "" || q.StateID != "" || q.TimeoutMS != 0 {
			return invalid()
		}
		if q.Command == "windows" {
			return s.Windows(ctx)
		}
		if q.Command == "state" {
			return s.State(ctx)
		}
		return s.Doctor(ctx)
	case "a11y":
		if q.WindowRef == "" || q.ElementRef != "" || q.StateID != "" || q.TimeoutMS != 0 {
			return invalid()
		}
		return s.Observe(ctx, q.WindowRef)
	case "read-element":
		if q.WindowRef == "" || q.ElementRef == "" || q.StateID == "" || q.TimeoutMS != 0 {
			return invalid()
		}
		return s.ReadElement(ctx, q.WindowRef, q.ElementRef, q.StateID)
	case "wait":
		if q.WindowRef == "" || q.ElementRef != "" || q.StateID != "" || q.TimeoutMS < 1 || q.TimeoutMS > 30000 {
			return invalid()
		}
		return s.Wait(ctx, q.WindowRef, time.Duration(q.TimeoutMS)*time.Millisecond)
	default:
		return nil, &comuse.Error{Code: "unsupported", Message: "unsupported command"}
	}
}
