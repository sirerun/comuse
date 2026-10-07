// Command comuse exposes the trusted host's bounded semantic session.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/sirerun/comuse"
	"github.com/sirerun/comuse/internal/backend"
	"github.com/sirerun/comuse/internal/backend/darwin"
	"github.com/sirerun/comuse/internal/cli"
	"github.com/sirerun/comuse/internal/jsonwire"
	comusemcp "github.com/sirerun/comuse/mcp"
)

// The runtime initializes main on the initial thread when init locks it.
func init() { runtime.LockOSThread() }

type hostConfig struct {
	LibraryPath string        `json:"library_path"`
	Scope       comuse.Scope  `json:"scope"`
	Budget      comuse.Budget `json:"budget"`
	AllowValues bool          `json:"allow_values"`
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
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) int {
	fs := flag.NewFlagSet("comuse", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "trusted scope and native library JSON file")
	if err := fs.Parse(args); err != nil {
		return emitRejection(out, "invalid_request")
	}
	if (*configPath == "") || (fs.NArg() != 1 && fs.NArg() != 2) {
		return emitRejection(out, "invalid_request")
	}
	command := fs.Arg(0)
	ledgerFlag := fs.NArg() == 2 && command == "state" && fs.Arg(1) == "--ledger"
	if fs.NArg() == 2 && !ledgerFlag {
		return emitRejection(out, "invalid_request")
	}
	if command != "mcp" && !cli.IsCommand(command) {
		return emitRejection(out, "invalid_request")
	}
	if command == "mcp" {
		if fs.NArg() != 1 {
			return emitRejection(out, "invalid_request")
		}
		cfg, err := loadConfig(*configPath)
		if err != nil {
			return emitRejection(out, "invalid_request")
		}
		return runMCP(ctx, cfg, in, out, diagnostics)
	}
	var commandCode int
	commandStarted := false
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return emitRejection(out, "invalid_request")
	}
	err = darwin.Run(ctx, backend.Config{Scope: cfg.Scope, LibraryPath: cfg.LibraryPath, AllowValues: cfg.AllowValues}, func(b backend.Backend) (callErr error) {
		resolvedScope, scopeErr := darwin.ResolvedScope(b)
		if scopeErr != nil {
			return scopeErr
		}
		session, newErr := comuse.NewSession(comuse.Config{Backend: b, Scope: resolvedScope, Budget: cfg.Budget, AllowValues: cfg.AllowValues})
		if newErr != nil {
			return newErr
		}
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if closeErr := session.Close(closeCtx); closeErr != nil {
				callErr = closeErr
			}
		}()
		commandStarted = true
		commandArgs := []string{command}
		if ledgerFlag {
			commandArgs = append(commandArgs, "--ledger")
		}
		commandCode = cli.Run(ctx, session, commandArgs, in, out, diagnostics)
		return nil
	})
	if err != nil {
		code := comuse.ErrorCode(err)
		if commandStarted {
			return cliExitFor(code)
		}
		if commandCode != 0 {
			return commandCode
		}
		return emitRejection(out, code)
	}
	return commandCode
}

func runMCP(ctx context.Context, cfg hostConfig, in io.Reader, out, diagnostics io.Writer) int {
	err := darwin.Run(ctx, backend.Config{Scope: cfg.Scope, LibraryPath: cfg.LibraryPath, AllowValues: cfg.AllowValues}, func(b backend.Backend) (callErr error) {
		resolvedScope, scopeErr := darwin.ResolvedScope(b)
		if scopeErr != nil {
			return scopeErr
		}
		session, newErr := comuse.NewSession(comuse.Config{Backend: b, Scope: resolvedScope, Budget: cfg.Budget, AllowValues: cfg.AllowValues})
		if newErr != nil {
			return newErr
		}
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if closeErr := session.Close(closeCtx); closeErr != nil {
				callErr = closeErr
			}
		}()
		return comusemcp.Serve(ctx, session, stdio{in, out})
	})
	if err != nil {
		_, _ = io.WriteString(diagnostics, "comuse: "+comuse.ErrorCode(err)+"\n")
		return cliExitFor(comuse.ErrorCode(err))
	}
	return 0
}

func loadConfig(path string) (hostConfig, error) {
	var c hostConfig
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	if err = decodeConfig(f, &c); err != nil {
		return c, err
	}
	if c.LibraryPath == "" || !filepath.IsAbs(c.LibraryPath) {
		return c, errors.New("invalid library path")
	}
	return c, nil
}

func decodeConfig(f io.ReadCloser, c *hostConfig) error {
	if err := jsonwire.Decode(f, 32768, c); err != nil {
		if f.Close() != nil {
			return errors.New("config close failed")
		}
		return err
	}
	if closeErr := f.Close(); closeErr != nil {
		return errors.New("config close failed")
	}
	return nil
}

func emitRejection(out io.Writer, code string) int {
	envelope, err := comuse.RejectionEnvelope(code)
	if err != nil {
		return 1
	}
	if json.NewEncoder(out).Encode(envelope) != nil {
		return 1
	}
	return cliExitFor(envelope.Error.Code)
}

func cliExitFor(code string) int {
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
