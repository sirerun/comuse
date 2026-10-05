//go:build darwin && cgo

package bridgeclient

/*
#cgo CFLAGS: -I${SRCDIR}/../macos/bridge/include
#include <stdint.h>
#include <stdlib.h>
#include "loader.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"
)

const maxPumpDuration = 250 * time.Millisecond
const callbackDrainTimeout = 2 * time.Second

type completion struct {
	handle uint64
	bytes  []byte
	err    error
}

type pendingRequest struct {
	mu           sync.Mutex
	result       completion
	terminal     chan struct{}
	terminalOnce sync.Once
	drainMu      sync.Mutex
	drained      chan struct{}
	drainedOnce  sync.Once
	nativeHandle uint64
	token        cgo.Handle
	client       *Client
}

func newPending(client *Client) *pendingRequest {
	return &pendingRequest{terminal: make(chan struct{}), drained: make(chan struct{}), client: client}
}

func (p *pendingRequest) publish(value completion) {
	p.terminalOnce.Do(func() {
		p.mu.Lock()
		p.result = value
		p.mu.Unlock()
		close(p.terminal)
	})
}

func (p *pendingRequest) completion() completion {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.result
}

type Client struct {
	nativeMu  sync.Mutex // serializes every C call against library retirement
	closeMu   sync.Mutex
	mu        sync.Mutex
	library   *C.comuse_bridge_library
	runtimeID uint64
	pending   map[uint64]*pendingRequest
	closing   bool
	closed    bool
}

// Open must be called by a goroutine already running on the process main OS thread.
// It pins that goroutine, and the native runtime verifies pthread_main_np itself.
// Only one activated Swift image is permitted per process; restart the host to
// upgrade or load a different image.
func Open(path string) (*Client, error) {
	if path == "" {
		return nil, errors.New("native bridge library path is required")
	}
	runtime.LockOSThread()
	if C.bridge_is_main_thread() == 0 {
		runtime.UnlockOSThread()
		return nil, ErrNotMainThread
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var library *C.comuse_bridge_library
	if status := int32(C.bridge_library_open(cPath, &library)); status != 0 {
		runtime.UnlockOSThread()
		return nil, nativeStatusError("loading native bridge", status)
	}
	var runtimeID C.uint64_t
	if status := int32(C.bridge_runtime_open(library, &runtimeID)); status != 0 {
		C.bridge_library_close(library)
		runtime.UnlockOSThread()
		return nil, nativeStatusError("opening native runtime", status)
	}
	return &Client{library: library, runtimeID: uint64(runtimeID), pending: make(map[uint64]*pendingRequest)}, nil
}

// Call may run only from a worker goroutine; the process main goroutine must keep pumping.
func (c *Client) Call(ctx context.Context, requestJSON []byte) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("Call requires a context")
	}
	request, err := validateRequest(requestJSON)
	if err != nil {
		return nil, err
	}
	return c.callValidated(ctx, requestJSON, request.RequestID, HostInputRequest{}, false)
}

// InputCall is an additive trusted-host transport. It accepts typed fields only; public readonly Call cannot route input operations.
func (c *Client) InputCall(ctx context.Context, request HostInputRequest) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("InputCall requires a context")
	}
	requestJSON, err := marshalHostInputRequest(request)
	if err != nil {
		return nil, err
	}
	return c.callValidated(ctx, requestJSON, request.RequestID, request, true)
}

func (c *Client) callValidated(ctx context.Context, requestJSON []byte, requestID string, input HostInputRequest, hostInput bool) ([]byte, error) {
	if C.bridge_is_main_thread() != 0 {
		return nil, ErrCallOnMainThread
	}

	c.nativeMu.Lock()
	c.mu.Lock()
	if c.closing || c.closed || c.library == nil {
		c.mu.Unlock()
		c.nativeMu.Unlock()
		return nil, ErrClosed
	}
	state := newPending(c)
	state.token = cgo.NewHandle(state)
	var nativeHandle C.uint64_t
	var status int32
	if hostInput {
		status = int32(C.bridge_input_request_start(c.library, C.uint64_t(c.runtimeID), (*C.uint8_t)(unsafe.Pointer(&requestJSON[0])), C.size_t(len(requestJSON)), C.uint64_t(state.token), &nativeHandle))
	} else {
		status = int32(C.bridge_request_start(c.library, (*C.uint8_t)(unsafe.Pointer(&requestJSON[0])), C.size_t(len(requestJSON)), C.uint64_t(state.token), &nativeHandle))
	}
	if status != 0 {
		state.token.Delete()
		c.mu.Unlock()
		c.nativeMu.Unlock()
		return nil, nativeStatusError("starting native request", status)
	}
	state.nativeHandle = uint64(nativeHandle)
	c.pending[state.nativeHandle] = state
	c.mu.Unlock()
	c.nativeMu.Unlock()

	contextErr := error(nil)
	select {
	case <-state.terminal:
	case <-ctx.Done():
		contextErr = ctx.Err()
		if err := c.cancelNative(state); err != nil {
			return nil, err
		}
		select {
		case <-state.terminal:
		case <-time.After(callbackDrainTimeout):
			return nil, errors.New("native terminal callback timed out; callback handle and dylib remain retained")
		}
	}

	completed := state.completion()
	if completed.handle != state.nativeHandle {
		return nil, fmt.Errorf("native callback handle mismatch: want %d, got %d", state.nativeHandle, completed.handle)
	}
	// A completed callback can precede return of its queued Swift closure.
	// Retain the handle when it is not yet drainable; Close pumps and reaps it.
	if err := c.drainOnce(state); err != nil && !errors.Is(err, errNotDrainable) {
		return nil, err
	}
	if completed.err != nil {
		return nil, completed.err
	}
	if hostInput {
		_, validateErr := validateHostInputResponse(completed.bytes, input)
		if validateErr != nil {
			return completed.bytes, validateErr
		}
		return completed.bytes, nil
	}
	response, err := validateResponse(completed.bytes, requestID)
	if err != nil {
		return completed.bytes, err
	}
	switch response.Status {
	case StatusCompleted, StatusPartial:
		return completed.bytes, nil
	case StatusCancelled:
		if contextErr != nil {
			return completed.bytes, fmt.Errorf("native request status cancelled: %w", contextErr)
		}
		return completed.bytes, &NativeError{Status: string(response.Status), Detail: string(response.Error)}
	case StatusError:
		return completed.bytes, &NativeError{Status: string(response.Status), Detail: string(response.Error)}
	default:
		return completed.bytes, fmt.Errorf("unexpected native response status %q", response.Status)
	}
}

// Pump services native main-thread work. Call it from the goroutine that called Open.
func (c *Client) Pump(duration time.Duration) error {
	if C.bridge_is_main_thread() == 0 {
		return ErrNotMainThread
	}
	if duration <= 0 || duration > maxPumpDuration {
		return fmt.Errorf("Pump duration must be in (0, %s]", maxPumpDuration)
	}
	c.nativeMu.Lock()
	defer c.nativeMu.Unlock()
	c.mu.Lock()
	if c.closed || c.library == nil {
		c.mu.Unlock()
		return ErrClosed
	}
	library, runtimeID := c.library, c.runtimeID
	c.mu.Unlock()
	milliseconds := uint32((duration + time.Millisecond - 1) / time.Millisecond)
	status := int32(C.bridge_runtime_pump(library, C.uint64_t(runtimeID), C.uint32_t(milliseconds)))
	if status != 0 {
		return nativeStatusError("pumping native runtime", status)
	}
	return nil
}

// Close stops admission, cancels active requests, and drains callbacks before
// retiring the runtime. It releases the C wrapper but keeps the activated Swift
// image mapped until process exit. On deadline or not-drainable status it keeps
// the wrapper and all reachable state alive for a retry.
func (c *Client) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("Close requires a context")
	}
	if C.bridge_is_main_thread() == 0 {
		return ErrNotMainThread
	}
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closing = true
	states := make([]*pendingRequest, 0, len(c.pending))
	for _, state := range c.pending {
		states = append(states, state)
	}
	runtimeID := c.runtimeID
	c.mu.Unlock()

	for _, state := range states {
		if err := c.cancelNative(state); err != nil {
			return err
		}
	}
	for _, state := range states {
		select {
		case <-state.terminal:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	for {
		// The owner thread must execute queued native closures before their handles
		// can be drained. Pumping also lets cancellation-before-dispatch retire safely.
		if err := c.Pump(time.Millisecond); err != nil {
			return err
		}
		allDrained := true
		for _, state := range states {
			if err := c.drainOnce(state); err != nil {
				if !errors.Is(err, errNotDrainable) {
					return err
				}
				allDrained = false
			}
		}
		if !allDrained {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			continue
		}
		c.nativeMu.Lock()
		c.mu.Lock()
		library := c.library
		c.mu.Unlock()
		if library == nil {
			c.nativeMu.Unlock()
			return ErrClosed
		}
		status := int32(C.bridge_runtime_close(library, C.uint64_t(runtimeID)))
		if status == 0 {
			c.mu.Lock()
			if c.library != nil {
				C.bridge_library_close(c.library)
				c.library = nil
			}
			c.closed = true
			c.mu.Unlock()
			c.nativeMu.Unlock()
			runtime.UnlockOSThread()
			return nil
		}
		c.nativeMu.Unlock()
		if status != 6 {
			return nativeStatusError("closing native runtime", status)
		}
		if err := c.Pump(time.Millisecond); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

var errNotDrainable = errors.New("native request is not drainable yet")

func (c *Client) drainOnce(state *pendingRequest) error {
	state.drainMu.Lock()
	defer state.drainMu.Unlock()
	select {
	case <-state.drained:
		return nil
	default:
	}
	c.nativeMu.Lock()
	defer c.nativeMu.Unlock()
	c.mu.Lock()
	library := c.library
	c.mu.Unlock()
	if library == nil {
		return ErrClosed
	}
	status := int32(C.bridge_request_drain(library, C.uint64_t(state.nativeHandle)))
	if status == 6 {
		return errNotDrainable
	}
	if status != 0 {
		return nativeStatusError("draining native callback", status)
	}
	state.drainedOnce.Do(func() {
		state.token.Delete()
		close(state.drained)
	})
	c.mu.Lock()
	delete(c.pending, state.nativeHandle)
	c.mu.Unlock()
	return nil
}

// cancelNative serializes request cancellation against drain and dlclose. A
// request already drained by Close has necessarily published its completion,
// so the terminal result wins over the caller's context cancellation.
func (c *Client) cancelNative(state *pendingRequest) error {
	select {
	case <-state.terminal:
		return nil
	default:
	}
	c.nativeMu.Lock()
	defer c.nativeMu.Unlock()
	c.mu.Lock()
	library := c.library
	pending := c.pending[state.nativeHandle]
	c.mu.Unlock()
	if library == nil || pending != state {
		return nil
	}
	status := int32(C.bridge_request_cancel(library, C.uint64_t(state.nativeHandle)))
	if status != 0 && status != 5 {
		select {
		case <-state.terminal:
			return nil
		default:
		}
		return nativeStatusError("cancelling native request", status)
	}
	return nil
}

//export goBridgeCompletion
func goBridgeCompletion(handle C.uint64_t, bytes *C.uint8_t, length C.size_t, token C.uint64_t) {
	state := cgo.Handle(token).Value().(*pendingRequest)
	if uint64(length) > maxResponseBytes || (length > 0 && bytes == nil) {
		state.publish(completion{handle: uint64(handle), err: errors.New("native response exceeds 65536 bytes or has a null pointer")})
		return
	}
	copied := C.GoBytes(unsafe.Pointer(bytes), C.int(length))
	state.publish(completion{handle: uint64(handle), bytes: copied})
}

func nativeStatusError(action string, status int32) error {
	switch status {
	case 9:
		return fmt.Errorf("%s: %w", action, ErrNotMainThread)
	case 10:
		return fmt.Errorf("%s: native runtime required", action)
	case 11:
		return fmt.Errorf("%s: %w", action, ErrClosed)
	default:
		return fmt.Errorf("%s: native status %d", action, status)
	}
}
