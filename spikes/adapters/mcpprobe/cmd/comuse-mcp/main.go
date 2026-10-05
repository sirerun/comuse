// Command comuse-mcp is the host-launched stdio MCP spike. All scope values
// come from its trusted launch configuration; MCP tool arguments cannot alter them.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/sirerun/comuse/spikes/adapters/mcpprobe"
	"github.com/sirerun/comuse/spikes/bridgeclient"
)

func init() { runtime.LockOSThread() }

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var config mcpprobe.Config
	flag.StringVar(&config.NativeLibraryPath, "native-library", "", "trusted absolute BridgeProbe library path")
	flag.IntVar(&config.FixturePID, "fixture-pid", 0, "trusted PID of the launched fixture")
	flag.StringVar(&config.FixtureBundleID, "fixture-bundle-id", "", "trusted fixture bundle identifier")
	flag.StringVar(&config.FixtureNonce, "fixture-nonce", "", "trusted fixture launch nonce")
	flag.StringVar(&config.ProcessLaunchGeneration, "process-launch-generation", "", "trusted host launch generation")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if err := mcpprobe.ValidateConfig(config); err != nil {
		return err
	}

	// Open pins this goroutine to the actual process main thread. It must remain
	// here to pump native callbacks while the MCP SDK runs on its worker goroutine.
	native, err := bridgeclient.Open(config.NativeLibraryPath)
	if err != nil {
		return err
	}
	backend, err := mcpprobe.NewNativeBackend(native)
	if err != nil {
		closeNative(native)
		return err
	}
	server, err := mcpprobe.New(config, backend, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		closeNative(native)
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.RunStdio(ctx) }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	stop := ctx.Done()
	var serveErr error

loop:
	for {
		select {
		case serveErr = <-serveDone:
			break loop
		case <-stop:
			cancel()
			stop = nil
		case <-ticker.C:
			if pumpErr := native.Pump(10 * time.Millisecond); pumpErr != nil {
				cancel()
				serveErr = pumpErr
				<-serveDone
				break loop
			}
		}
	}
	cancel()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	closeErr := native.Close(closeCtx)
	if errors.Is(serveErr, context.Canceled) {
		serveErr = nil
	}
	return errors.Join(serveErr, closeErr)
}

func closeNative(native *bridgeclient.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = native.Close(ctx)
}
