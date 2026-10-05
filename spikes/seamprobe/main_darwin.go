//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -I${SRCDIR}/../macos/bridge/include
#include <stdint.h>
#include <stdlib.h>
#include "comuse_spike.h"
int32_t seam_open(const char *path);
void seam_close(void);
uint32_t seam_version(void);
int32_t seam_start(const uint8_t *request, size_t length, uint64_t token, uint64_t *handle);
int32_t seam_cancel(uint64_t handle);
int32_t seam_drain(uint64_t handle);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"
)

const abiVersion = 1

var runMu sync.Mutex

func runHello(ctx context.Context, library string) (response, error) {
	runMu.Lock()
	defer runMu.Unlock()
	if err := ctx.Err(); err != nil {
		return response{}, err
	}
	path := C.CString(library)
	defer C.free(unsafe.Pointer(path))
	if status := C.seam_open(path); status != 0 {
		return response{}, fmt.Errorf("loading native bridge (status %d)", int32(status))
	}
	nativeStateDrained := true
	defer func() {
		if nativeStateDrained {
			C.seam_close()
		}
	}()
	if version := uint32(C.seam_version()); version != abiVersion {
		return response{}, fmt.Errorf("native ABI mismatch: want %d, got %d", abiVersion, version)
	}
	body, err := json.Marshal(request{SchemaVersion: 1, RequestID: "seamprobe-1", Op: "hello"})
	if err != nil {
		return response{}, fmt.Errorf("encoding hello request: %w", err)
	}
	if len(body) > 32*1024 {
		return response{}, errors.New("hello request exceeds 32768 bytes")
	}
	result := make(chan completion, 1)
	token := cgo.NewHandle(result)
	defer func() {
		if nativeStateDrained {
			token.Delete()
		}
	}()
	var handle C.uint64_t
	if status := C.seam_start((*C.uint8_t)(unsafe.Pointer(&body[0])), C.size_t(len(body)), C.uint64_t(token), &handle); status != 0 {
		return response{}, fmt.Errorf("starting native hello (status %d)", int32(status))
	}
	waitForCallbackAndDrain := func() error {
		deadline := time.Now().Add(2 * time.Second)
		for {
			status := int32(C.seam_drain(handle))
			if status == 0 {
				nativeStateDrained = true
				return nil
			}
			if status != 6 {
				return fmt.Errorf("draining native request (status %d)", status)
			}
			if time.Now().After(deadline) {
				return errors.New("native callback did not become drainable within two seconds")
			}
			time.Sleep(time.Millisecond)
		}
	}
	nativeStateDrained = false
	select {
	case delivered := <-result:
		drainErr := waitForCallbackAndDrain()
		if drainErr != nil {
			return response{}, drainErr
		}
		if err := validateCompletionHandle(delivered, uint64(handle)); err != nil {
			return response{}, err
		}
		if delivered.err != nil {
			return response{}, delivered.err
		}
		return parseHelloResponse(delivered.bytes)
	case <-ctx.Done():
		cancelStatus := int32(C.seam_cancel(handle))
		// The callback token and library remain alive until terminal delivery and drain.
		var delivered completion
		select {
		case delivered = <-result:
		case <-time.After(2 * time.Second):
			return response{}, errors.New("native terminal callback did not arrive within two seconds; retained its token and library")
		}
		drainErr := waitForCallbackAndDrain()
		if drainErr != nil {
			return response{}, drainErr
		}
		if err := validateCompletionHandle(delivered, uint64(handle)); err != nil {
			return response{}, err
		}
		if delivered.err != nil {
			return response{}, delivered.err
		}
		return resolveTerminalAfterCancel(delivered.bytes, ctx.Err(), cancelStatus)
	}
}

//export goComuseCompletion
func goComuseCompletion(handle C.uint64_t, bytes *C.uint8_t, length C.size_t, token C.uint64_t) {
	channel := cgo.Handle(token).Value().(chan completion)
	if uint64(length) > 64*1024 || (length > 0 && bytes == nil) {
		select {
		case channel <- completion{handle: uint64(handle), err: errors.New("native response exceeds 65536 bytes or has a null buffer")}:
		default:
		}
		return
	}
	copyOfBytes := C.GoBytes(unsafe.Pointer(bytes), C.int(length))
	select {
	case channel <- completion{handle: uint64(handle), bytes: copyOfBytes}:
	default:
	}
}

func main() {
	library := flag.String("library", "", "path to the built BridgeProbe dynamic library")
	flag.Parse()
	if *library == "" {
		fmt.Fprintln(os.Stderr, "seamprobe: -library is required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runHello(ctx, *library)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seamprobe:", err)
		os.Exit(1)
	}
	path := C.CString(*library)
	defer C.free(unsafe.Pointer(path))
	if status := int32(C.seam_open(path)); status != -4 {
		fmt.Fprintf(os.Stderr, "seamprobe: expected process-pinned image status -4 on reopen, got %d\n", status)
		os.Exit(1)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seamprobe: encoding response:", err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}
