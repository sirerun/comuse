//go:build darwin && cgo

// seamprobe is a minimal real Go/cgo/dlopen client. It must run on the process
// main goroutine because Open and Pump verify pthread_main_np().
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/sirerun/comuse/spikes/bridgeclient"
)

func init() { runtime.LockOSThread() }

func main() {
	library := flag.String("library", "", "path to BridgeProbe dylib")
	cancelBeforePump := flag.Bool("cancel-before-pump", false, "cancel doctor before the owner pumps main-queue work")
	flag.Parse()
	if *library == "" {
		fmt.Fprintln(os.Stderr, "-library is required")
		os.Exit(2)
	}
	client, err := bridgeclient.Open(*library)
	if err != nil {
		fatal(err)
	}
	request := []byte(`{"schema_version":1,"request_id":"seamprobe-1","op":"doctor"}`)
	ctx, cancel := context.WithCancel(context.Background())
	if *cancelBeforePump {
		cancel()
	}
	result := make(chan struct {
		data []byte
		err  error
	}, 1)
	go func() {
		data, callErr := client.Call(ctx, request)
		result <- struct {
			data []byte
			err  error
		}{data, callErr}
	}()
	var callResult struct {
		data []byte
		err  error
	}
	for {
		select {
		case callResult = <-result:
			goto finished
		default:
		}
		if err := client.Pump(10 * time.Millisecond); err != nil {
			cancel()
			fatalClose(client, err)
		}
	}
finished:
	cancel()
	if *cancelBeforePump && callResult.err == nil {
		fatalClose(client, fmt.Errorf("cancel-before-pump unexpectedly completed: %s", callResult.data))
	}
	if !*cancelBeforePump && callResult.err != nil {
		fatalClose(client, callResult.err)
	}
	if len(callResult.data) != 0 {
		fmt.Println(string(callResult.data))
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer closeCancel()
	if err := client.Close(closeCtx); err != nil {
		fatal(err)
	}
	if _, err := bridgeclient.Open(*library); err == nil {
		fatal(fmt.Errorf("bridgeclient reopened a second activated image in one process"))
	} else if !strings.Contains(err.Error(), "native status 8") {
		fatal(fmt.Errorf("second image rejection was not the process image-capacity status: %w", err))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func fatalClose(client *bridgeclient.Client, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%v (close: %v)\n", cause, err)
		os.Exit(1)
	}
	fatal(cause)
}
