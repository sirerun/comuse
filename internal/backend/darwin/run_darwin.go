//go:build darwin && cgo

package darwin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sirerun/comuse/internal/backend"
	"github.com/sirerun/comuse/internal/jsonwire"
)

var retainedOwnerMu sync.Mutex
var retainedOwner *runtimeOwner

// ResolvedScope returns the exact process generations bound during native Open.
// It is valid only for the backend passed to Run's callback.
func ResolvedScope(value backend.Backend) (backend.Scope, error) {
	current, ok := value.(*nativeBackend)
	if !ok || current == nil || current.closed.Load() || len(current.boundScope.Processes) == 0 {
		return backend.Scope{}, backendError("backend_unavailable")
	}
	return copyScope(current.boundScope), nil
}

// Run owns native Open/Pump/Close on the actual process-main thread. It runs fn
// on a worker so AX work can return to the main actor while the owner pumps.
func Run(ctx context.Context, config backend.Config, fn func(backend.Backend) error) error {
	if ctx == nil || fn == nil {
		return backendError("invalid_request")
	}
	if err := validateConfig(config); err != nil {
		return err
	}
	runtime.LockOSThread()
	unlockThread := true
	defer func() {
		if unlockThread {
			runtime.UnlockOSThread()
		}
	}()
	if hasRetainedOwner() {
		return backendError("desktop_busy")
	}
	if !currentIsProcessMain() {
		return backendError("backend_unavailable")
	}
	lib, err := loadNativeLibrary(config.LibraryPath)
	if err != nil {
		return err
	}

	configJSON, err := json.Marshal(nativeConfig{SchemaVersion: abiVersion, Scope: nativeScope{
		Processes: copyScope(config.Scope).Processes, ExpiresAtUnixMilli: config.Scope.ExpiresAt.UnixMilli(),
	}, AllowValues: config.AllowValues})
	if err != nil || len(configJSON) == 0 || len(configJSON) > maxNativeRequest {
		lib.close()
		return backendError("invalid_request")
	}
	runtimeID, scopeJSON, err := lib.open(configJSON)
	if err != nil {
		if runtimeID != 0 {
			cleanupErr := cleanupOpenFailure(lib, runtimeID, err)
			unlockThread = !hasRetainedOwner()
			return cleanupErr
		}
		lib.close()
		return err
	}
	var bound nativeScope
	if err = jsonwire.Decode(strings.NewReader(string(scopeJSON)), maxNativeRequest, &bound); err != nil {
		cleanupErr := cleanupOpenFailure(lib, runtimeID, backendError("backend_unavailable"))
		unlockThread = !hasRetainedOwner()
		return cleanupErr
	}
	resolved := backend.Scope{Processes: copyScope(backend.Scope{Processes: bound.Processes}).Processes,
		ExpiresAt: time.UnixMilli(bound.ExpiresAtUnixMilli)}
	if err = validateBoundScope(config.Scope, resolved); err != nil {
		cleanupErr := cleanupOpenFailure(lib, runtimeID, err)
		unlockThread = !hasRetainedOwner()
		return cleanupErr
	}
	owner := &runtimeOwner{lib: lib, runtimeID: runtimeID,
		commands: make(chan ownerCommand), completions: make(chan nativeCompletion, 64), pending: make(map[uint64]pendingRequest)}
	bridge := &nativeBackend{owner: owner, boundScope: resolved, allowValues: config.AllowValues}
	owner.backend = bridge
	workerDone := make(chan error, 1)
	go func() { workerDone <- fn(bridge) }()
	owner.workerDone = workerDone
	err = owner.loop(ctx)
	unlockThread = !hasRetainedOwner()
	return err
}

func cleanupOpenFailure(lib nativeTransport, runtimeID uint64, original error) error {
	for attempt := 0; attempt < ownerCloseAttempts; attempt++ {
		_ = lib.pump(runtimeID, ownerPumpTimeoutMS)
		if err := lib.closeRuntime(runtimeID); err == nil {
			lib.close()
			return original
		}
		time.Sleep(50 * time.Millisecond)
	}
	retainOwner(&runtimeOwner{lib: lib, runtimeID: runtimeID, pending: make(map[uint64]pendingRequest),
		commands: make(chan ownerCommand), completions: make(chan nativeCompletion, 64), closing: true, workerEnded: true})
	return backendError("backend_unavailable")
}

const ownerCloseAttempts = 3
const ownerDrainTimeout = 5 * time.Second

// RetryPendingClose must be called on the same pinned process-main goroutine
// that called Run. Run keeps its OS-thread pin if native close remains uncertain.
func RetryPendingClose(ctx context.Context) error {
	if ctx == nil {
		return backendError("invalid_request")
	}
	retainedOwnerMu.Lock()
	owner := retainedOwner
	retainedOwnerMu.Unlock()
	if owner == nil {
		return nil
	}
	if !owner.lib.isProcessMain() {
		return backendError("backend_unavailable")
	}
	owner.closeAttempts = 0
	owner.retryAt = time.Time{}
	owner.drainDeadline = boundedDrainDeadline(ctx)
	err := owner.loop(ctx)
	if !hasRetainedOwner() {
		runtime.UnlockOSThread()
	}
	return err
}

func retainOwner(owner *runtimeOwner) {
	retainedOwnerMu.Lock()
	retainedOwner = owner
	retainedOwnerMu.Unlock()
}

func clearRetainedOwner(owner *runtimeOwner) {
	retainedOwnerMu.Lock()
	if retainedOwner == owner {
		retainedOwner = nil
	}
	retainedOwnerMu.Unlock()
}

func hasRetainedOwner() bool {
	retainedOwnerMu.Lock()
	defer retainedOwnerMu.Unlock()
	return retainedOwner != nil
}

func boundedDrainDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(ownerDrainTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		return contextDeadline
	}
	return deadline
}

func (owner *runtimeOwner) loop(ctx context.Context) error {
	if owner.pending == nil {
		owner.pending = make(map[uint64]pendingRequest)
	}
	if owner.drainDeadline.IsZero() {
		owner.drainDeadline = boundedDrainDeadline(ctx)
	}
	ctxDone := ctx.Done()
	for {
		if !owner.closed {
			if pumpErr := owner.lib.pump(owner.runtimeID, ownerPumpTimeoutMS); pumpErr != nil {
				owner.closing = true
				if owner.workerErr == nil {
					owner.workerErr = pumpErr
				}
				if owner.backend != nil {
					owner.backend.closing.Store(true)
				}
				if owner.drainDeadline.IsZero() {
					owner.drainDeadline = boundedDrainDeadline(ctx)
				}
			}
		}
		select {
		case event := <-owner.completions:
			request, ok := owner.pending[event.callbackID]
			if !ok {
				continue
			}
			delete(owner.pending, event.callbackID)
			result := callResult{}
			switch {
			case request.ctx.Err() != nil:
				result.err = request.ctx.Err()
			case event.requestID != request.nativeID || event.status != 0:
				result.err = backendError("backend_unavailable")
			default:
				result.value, result.err = validateEnvelope(event.bytes, requestIDFromBytes(request.request))
			}
			request.reply <- result
		case command := <-owner.commands:
			if command.close {
				owner.closing = true
				owner.closeWaiters = append(owner.closeWaiters, command.reply)
				owner.drainDeadline = boundedDrainDeadline(command.ctx)
				owner.cancelPending(owner.pending)
			} else if owner.closing || owner.closed {
				command.reply <- callResult{err: backendError("session_closed")}
			} else if command.ctx.Err() != nil {
				command.reply <- callResult{err: command.ctx.Err()}
			} else {
				owner.start(command, owner.pending)
			}
		case workerErr := <-owner.workerDone:
			if workerErr != nil {
				owner.workerErr = workerErr
			}
			owner.workerEnded = true
			owner.workerDone = nil
			owner.closing = true
			if owner.backend != nil {
				owner.backend.closing.Store(true)
			}
			owner.drainDeadline = boundedDrainDeadline(ctx)
			owner.cancelPending(owner.pending)
		case <-ctxDone:
			ctxDone = nil
			owner.closing = true
			if owner.workerErr == nil {
				owner.workerErr = ctx.Err()
			}
			if owner.backend != nil {
				owner.backend.closing.Store(true)
			}
			owner.drainDeadline = boundedDrainDeadline(ctx)
			owner.cancelPending(owner.pending)
		case <-time.After(10 * time.Millisecond):
			for callbackID, request := range owner.pending {
				if request.ctx.Err() != nil && !request.cancelled {
					if cancelErr := owner.lib.cancel(owner.runtimeID, request.nativeID); cancelErr == nil {
						request.cancelled = true
						owner.pending[callbackID] = request
					}
				}
			}
			if owner.closing && len(owner.pending) == 0 && !owner.closed &&
				(owner.retryAt.IsZero() || !time.Now().Before(owner.retryAt)) {
				owner.closeAttempts++
				closeErr := owner.lib.closeRuntime(owner.runtimeID)
				if closeErr == nil {
					owner.closed = true
					if owner.backend != nil {
						owner.backend.closed.Store(true)
					}
					owner.lib.close()
					for _, waiter := range owner.closeWaiters {
						waiter <- callResult{}
					}
					owner.closeWaiters = nil
				} else {
					owner.retryAt = time.Now().Add(50 * time.Millisecond)
					if owner.closeAttempts >= ownerCloseAttempts {
						for _, waiter := range owner.closeWaiters {
							waiter <- callResult{err: closeErr}
						}
						owner.closeWaiters = nil
						retainOwner(owner)
						return backendError("backend_unavailable")
					}
				}
			}
		}
		if owner.closing && len(owner.pending) > 0 && !time.Now().Before(owner.drainDeadline) {
			retainOwner(owner)
			return backendError("backend_unavailable")
		}
		if owner.closed && owner.workerEnded {
			clearRetainedOwner(owner)
			return owner.workerErr
		}
		if owner.closed && !time.Now().Before(owner.drainDeadline) {
			clearRetainedOwner(owner)
			return backendError("backend_unavailable")
		}
	}
}

func (owner *runtimeOwner) cancelPending(pending map[uint64]pendingRequest) {
	for callbackID, request := range pending {
		if !request.cancelled {
			if err := owner.lib.cancel(owner.runtimeID, request.nativeID); err == nil {
				request.cancelled = true
				pending[callbackID] = request
			}
		}
	}
}

func validateConfig(config backend.Config) error {
	if !filepath.IsAbs(config.LibraryPath) || filepath.Clean(config.LibraryPath) != config.LibraryPath || len(config.LibraryPath) > 4096 {
		return backendError("invalid_request")
	}
	for path := config.LibraryPath; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return backendError("backend_unavailable")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != uint32(os.Getuid()) && stat.Uid != 0) || info.Mode().Perm()&0022 != 0 {
			return backendError("policy_refused")
		}
		if path == string(filepath.Separator) {
			break
		}
	}
	fileInfo, err := os.Lstat(config.LibraryPath)
	if err != nil || !fileInfo.Mode().IsRegular() {
		return backendError("backend_unavailable")
	}
	if len(config.Scope.Processes) == 0 || len(config.Scope.Processes) > maximumProcesses || !config.Scope.ExpiresAt.After(time.Now()) {
		return backendError("invalid_request")
	}
	seen := make(map[int32]struct{}, len(config.Scope.Processes))
	for _, process := range config.Scope.Processes {
		if process.PID <= 0 || !validBundleID(process.BundleID) || len(process.LaunchID) > maximumRefBytes ||
			(process.LaunchID != "" && !validOpaque(process.LaunchID)) {
			return backendError("invalid_request")
		}
		if _, exists := seen[process.PID]; exists {
			return backendError("invalid_request")
		}
		seen[process.PID] = struct{}{}
	}
	return nil
}

func validateBoundScope(requested backend.Scope, resolved backend.Scope) error {
	// The v1 native config carries expiry at millisecond precision. Compare at
	// that wire precision while requiring that the native side never extends it.
	if len(requested.Processes) != len(resolved.Processes) ||
		resolved.ExpiresAt.UnixMilli() != requested.ExpiresAt.UnixMilli() ||
		resolved.ExpiresAt.After(requested.ExpiresAt) {
		return backendError("backend_unavailable")
	}
	wanted := make(map[int32]backend.ProcessIdentity, len(requested.Processes))
	for _, process := range requested.Processes {
		wanted[process.PID] = process
	}
	for _, process := range resolved.Processes {
		prior, ok := wanted[process.PID]
		if !ok || prior.BundleID != process.BundleID || process.LaunchID == "" ||
			(prior.LaunchID != "" && prior.LaunchID != process.LaunchID) || !validOpaque(process.LaunchID) {
			return backendError("backend_unavailable")
		}
	}
	return nil
}

func copyScope(scope backend.Scope) backend.Scope {
	return backend.Scope{Processes: append([]backend.ProcessIdentity(nil), scope.Processes...), ExpiresAt: scope.ExpiresAt}
}

func validBundleID(value string) bool {
	if len(value) == 0 || len(value) > 255 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" {
			return false
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' {
				return false
			}
		}
	}
	return true
}

func validOpaque(value string) bool {
	if value == "" || len(value) > maximumRefBytes {
		return false
	}
	for _, ch := range value {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '.' && ch != '_' && ch != '-' {
			return false
		}
	}
	return true
}

func scopeContains(scope backend.Scope, process backend.ProcessIdentity) bool {
	for _, candidate := range scope.Processes {
		if candidate == process {
			return true
		}
	}
	return false
}
