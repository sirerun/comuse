//go:build darwin && cgo

package darwin

/*
#cgo CFLAGS: -I${SRCDIR}/../../../native/macos/include
#include "loader.h"
#include <stdlib.h>
*/
import "C"

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	maxNativeRequest  = 32 * 1024
	maxNativeResponse = 64 * 1024
)

type nativeCompletion struct {
	callbackID uint64
	requestID  uint64
	status     int32
	bytes      []byte
}

type nativeLibrary struct{ pointer *C.comuse_loader }

func currentIsProcessMain() bool { return C.comuse_loader_is_process_main() == 1 }

var callbackSequence atomic.Uint64
var callbackRegistry = struct {
	sync.Mutex
	items map[uint64]chan nativeCompletion
}{items: make(map[uint64]chan nativeCompletion)}

//export goComuseNativeCompletion
func goComuseNativeCompletion(callbackID C.uint64_t, requestID C.uint64_t, status C.int32_t,
	bytes *C.uint8_t, length C.size_t) {
	event := nativeCompletion{callbackID: uint64(callbackID), requestID: uint64(requestID), status: int32(status)}
	if length > maxNativeResponse || (length > 0 && bytes == nil) {
		event.status = -1
	} else if length > 0 {
		event.bytes = C.GoBytes(unsafe.Pointer(bytes), C.int(length))
	}
	callbackRegistry.Lock()
	ch := callbackRegistry.items[event.callbackID]
	delete(callbackRegistry.items, event.callbackID)
	callbackRegistry.Unlock()
	if ch != nil {
		// Owner admits at most 32 live callbacks; this queue holds 64. Since
		// pending is not retired until the owner consumes each event, capacity
		// cannot be exhausted and terminal delivery never uses a dropping send.
		ch <- event
	}
}

func loadNativeLibrary(path string) (*nativeLibrary, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var pointer *C.comuse_loader
	if status := C.comuse_loader_open(cPath, &pointer); status != 0 {
		return nil, nativeStatusError(int32(status))
	}
	lib := &nativeLibrary{pointer: pointer}
	var version C.uint32_t
	if status := C.comuse_loader_abi_version(pointer, &version); status != 0 || uint32(version) != 1 {
		lib.close()
		return nil, backendError("unsupported")
	}
	return lib, nil
}

func (l *nativeLibrary) isProcessMain() bool { return C.comuse_loader_is_process_main() == 1 }

func (l *nativeLibrary) open(config []byte) (uint64, []byte, error) {
	if len(config) == 0 || len(config) > maxNativeRequest {
		return 0, nil, backendError("invalid_request")
	}
	input := C.CBytes(config)
	defer C.free(input)
	capacity := C.size_t(maxNativeRequest)
	output := C.malloc(capacity)
	if output == nil {
		return 0, nil, backendError("backend_unavailable")
	}
	defer C.free(output)
	var runtimeID C.uint64_t
	var outputLength C.size_t
	status := C.comuse_loader_runtime_open(l.pointer, (*C.uint8_t)(input), C.size_t(len(config)), &runtimeID,
		(*C.uint8_t)(output), capacity, &outputLength)
	if status != 0 {
		return uint64(runtimeID), nil, nativeStatusError(int32(status))
	}
	if outputLength == 0 || outputLength > capacity {
		return uint64(runtimeID), nil, backendError("backend_unavailable")
	}
	return uint64(runtimeID), C.GoBytes(output, C.int(outputLength)), nil
}

func (l *nativeLibrary) pump(runtimeID uint64, timeoutMS uint32) error {
	if timeoutMS > 50 {
		timeoutMS = 50
	}
	status := C.comuse_loader_runtime_pump(l.pointer, C.uint64_t(runtimeID), C.uint32_t(timeoutMS))
	if status != 0 {
		return nativeStatusError(int32(status))
	}
	return nil
}

func (l *nativeLibrary) closeRuntime(runtimeID uint64) error {
	if status := C.comuse_loader_runtime_close(l.pointer, C.uint64_t(runtimeID)); status != 0 {
		return nativeStatusError(int32(status))
	}
	return nil
}

func (l *nativeLibrary) start(runtimeID uint64, request []byte, callbackID uint64, completion chan nativeCompletion) (uint64, error) {
	if len(request) == 0 || len(request) > maxNativeRequest {
		return 0, backendError("invalid_request")
	}
	callbackRegistry.Lock()
	if _, exists := callbackRegistry.items[callbackID]; exists {
		callbackRegistry.Unlock()
		return 0, backendError("desktop_busy")
	}
	callbackRegistry.items[callbackID] = completion
	callbackRegistry.Unlock()
	input := C.CBytes(request)
	defer C.free(input)
	var requestID C.uint64_t
	status := C.comuse_loader_request_start(l.pointer, C.uint64_t(runtimeID), (*C.uint8_t)(input),
		C.size_t(len(request)), C.uint64_t(callbackID), &requestID)
	if status != 0 {
		callbackRegistry.Lock()
		delete(callbackRegistry.items, callbackID)
		callbackRegistry.Unlock()
		return 0, nativeStatusError(int32(status))
	}
	return uint64(requestID), nil
}

func (l *nativeLibrary) cancel(runtimeID, requestID uint64) error {
	if status := C.comuse_loader_request_cancel(l.pointer, C.uint64_t(runtimeID), C.uint64_t(requestID)); status != 0 {
		return nativeStatusError(int32(status))
	}
	return nil
}

func (l *nativeLibrary) close() {
	if l.pointer != nil {
		C.comuse_loader_free(l.pointer)
		l.pointer = nil
	}
}

func nativeStatusError(status int32) error {
	switch status {
	case -5, -6:
		return backendError("unsupported")
	case -2:
		return backendError("backend_unavailable")
	case -1, -3, -4:
		return backendError("internal_error")
	case 1:
		return backendError("invalid_request")
	case 2:
		return backendError("budget_exceeded")
	case 3, 4:
		return backendError("backend_unavailable")
	case 5:
		return backendError("desktop_busy")
	case 6:
		return backendError("rate_limited")
	default:
		return backendError("internal_error")
	}
}
