//go:build darwin && cgo

package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirerun/comuse/internal/backend"
)

const (
	nativeSmokeModeEnv = "COMUSE_NATIVE_SMOKE"
	nativeSmokeLibEnv  = "COMUSE_NATIVE_LIBRARY"
	nativeSmokePIDEnv  = "COMUSE_FIXTURE_PID"
	nativeSmokeTmpEnv  = "COMUSE_NATIVE_SMOKE_TMP"
	fixtureBundleID    = "com.sirerun.comuse.fixture"
)

var nativeSmokeErr error

func init() {
	// TestMain runs this opt-in probe directly on the initial test-binary
	// goroutine. Pin that goroutine before testing starts; ordinary Test funcs
	// are not assumed to run on the process-main thread.
	if os.Getenv(nativeSmokeModeEnv) == "fixture" {
		runtime.LockOSThread()
	}
}

func TestMain(m *testing.M) {
	if os.Getenv(nativeSmokeModeEnv) == "fixture" {
		nativeSmokeErr = runControlledNativeSmoke()
		if nativeSmokeErr != nil {
			if _, writeErr := fmt.Fprintln(os.Stderr, nativeSmokeErr); writeErr != nil {
				os.Exit(1)
			}
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func TestNativeABISmokeControlledFixture(t *testing.T) {
	if os.Getenv(nativeSmokeModeEnv) != "fixture" {
		t.Skip("set COMUSE_NATIVE_SMOKE=fixture to run the controlled native smoke probe")
	}
	if nativeSmokeErr != nil {
		t.Fatal(nativeSmokeErr)
	}
	t.Log("controlled fixture native ABI smoke completed")
}

func runControlledNativeSmoke() error {
	libraryPath, pid, tempRoot, err := nativeSmokeInputs()
	if err != nil {
		return err
	}

	lib, err := loadNativeLibrary(libraryPath)
	if err != nil {
		return smokeError("native library load", backend.ErrorCode(err))
	}
	runtimeID, resolvedScope, err := openSmokeRuntime(lib, pid)
	if err != nil {
		if runtimeID != 0 {
			_ = closeSmokeRuntime(lib, runtimeID)
		}
		lib.close()
		return smokeError("native runtime open", backend.ErrorCode(err))
	}
	if err := validateSmokeResolvedScope(resolvedScope, pid); err != nil {
		_ = closeSmokeRuntime(lib, runtimeID)
		lib.close()
		return err
	}

	if err := smokeDoctorCallback(lib, runtimeID, false); err != nil {
		_ = closeSmokeRuntime(lib, runtimeID)
		lib.close()
		return err
	}
	if err := smokeDoctorCallback(lib, runtimeID, true); err != nil {
		_ = closeSmokeRuntime(lib, runtimeID)
		lib.close()
		return err
	}
	if err := closeSmokeRuntime(lib, runtimeID); err != nil {
		lib.close()
		return smokeError("native runtime close/drain", backend.ErrorCode(err))
	}
	lib.close()

	copyPath, cleanup, err := copyNativeImage(libraryPath, tempRoot)
	if err != nil {
		return err
	}
	otherImage, imageErr := loadNativeLibrary(copyPath)
	if otherImage != nil {
		otherImage.close()
	}
	cleanupErr := cleanup()
	if imageErr == nil {
		return errors.New("second native image unexpectedly loaded")
	}
	if backend.ErrorCode(imageErr) != "unsupported" {
		return smokeError("second native image rejection", backend.ErrorCode(imageErr))
	}
	if cleanupErr != nil {
		return smokeError("temporary native image cleanup", "internal_error")
	}

	// The original image remains process-pinned after runtime close; two new
	// owner runtimes must still open, deliver Doctor, drain and close.
	if err := smokeRunDoctor(libraryPath, pid); err != nil {
		return err
	}
	if err := smokeRunDoctor(libraryPath, pid); err != nil {
		return err
	}
	return nil
}

func nativeSmokeInputs() (string, int32, string, error) {
	libraryPath := os.Getenv(nativeSmokeLibEnv)
	if !filepath.IsAbs(libraryPath) || !underBuildOffload(libraryPath) {
		return "", 0, "", errors.New("COMUSE_NATIVE_LIBRARY must name a verified dylib on the external build volume")
	}
	resolvedLibrary, err := filepath.EvalSymlinks(libraryPath)
	if err != nil || !underBuildOffload(resolvedLibrary) {
		return "", 0, "", errors.New("COMUSE_NATIVE_LIBRARY must resolve to the external build volume")
	}
	libraryInfo, err := os.Lstat(resolvedLibrary)
	if err != nil || !libraryInfo.Mode().IsRegular() {
		return "", 0, "", errors.New("COMUSE_NATIVE_LIBRARY must be a regular dylib file")
	}
	pidValue, err := strconv.ParseInt(os.Getenv(nativeSmokePIDEnv), 10, 32)
	if err != nil || pidValue <= 0 {
		return "", 0, "", errors.New("COMUSE_FIXTURE_PID must be the confirmed fixture process id")
	}
	tempRoot := os.Getenv(nativeSmokeTmpEnv)
	if !filepath.IsAbs(tempRoot) || !underBuildOffload(tempRoot) {
		return "", 0, "", errors.New("COMUSE_NATIVE_SMOKE_TMP must be a task temp directory on the external build volume")
	}
	resolvedTemp, err := filepath.EvalSymlinks(tempRoot)
	if err != nil {
		return "", 0, "", errors.New("native smoke temp directory is unavailable")
	}
	info, err := os.Stat(resolvedTemp)
	if err != nil || !info.IsDir() {
		return "", 0, "", errors.New("native smoke temp path is not a directory")
	}
	return resolvedLibrary, int32(pidValue), resolvedTemp, nil
}

func underBuildOffload(path string) bool {
	clean := filepath.Clean(path)
	return clean == "/Volumes/BuildOffload" || strings.HasPrefix(clean, "/Volumes/BuildOffload"+string(os.PathSeparator))
}

func openSmokeRuntime(lib *nativeLibrary, pid int32) (uint64, []byte, error) {
	config, err := json.Marshal(nativeConfig{
		SchemaVersion: abiVersion,
		Scope: nativeScope{
			Processes:          []backend.ProcessIdentity{{PID: pid, BundleID: fixtureBundleID}},
			ExpiresAtUnixMilli: time.Now().Add(2 * time.Minute).UnixMilli(),
		},
	})
	if err != nil {
		return 0, nil, backendError("internal_error")
	}
	return lib.open(config)
}

func validateSmokeResolvedScope(data []byte, pid int32) error {
	var scope nativeScope
	if err := json.Unmarshal(data, &scope); err != nil || len(scope.Processes) != 1 {
		return errors.New("native runtime returned invalid resolved fixture scope")
	}
	process := scope.Processes[0]
	if process.PID != pid || process.BundleID != fixtureBundleID || process.LaunchID == "" || scope.ExpiresAtUnixMilli <= time.Now().UnixMilli() {
		return errors.New("native runtime resolved a different or unbound fixture scope")
	}
	return nil
}

func smokeDoctorCallback(lib *nativeLibrary, runtimeID uint64, cancelBeforePump bool) error {
	requestID := "native-smoke-doctor"
	if cancelBeforePump {
		requestID = "native-smoke-cancel"
	}
	request, err := json.Marshal(nativeRequest{SchemaVersion: abiVersion, RequestID: requestID, Operation: "doctor"})
	if err != nil {
		return errors.New("could not encode bounded native Doctor request")
	}
	callbackID := callbackSequence.Add(1)
	if callbackID == 0 {
		callbackID = callbackSequence.Add(1)
	}
	completions := make(chan nativeCompletion, 1)
	nativeID, err := lib.start(runtimeID, request, callbackID, completions)
	if err != nil {
		return smokeError("native request start", backend.ErrorCode(err))
	}
	callbackDrained := false
	defer func() {
		if callbackDrained {
			return
		}
		_ = lib.cancel(runtimeID, nativeID)
		drainUntil := time.Now().Add(3 * time.Second)
		for time.Now().Before(drainUntil) {
			select {
			case event := <-completions:
				if event.callbackID == callbackID && event.requestID == nativeID {
					callbackDrained = true
					return
				}
			default:
				_ = lib.pump(runtimeID, ownerPumpTimeoutMS)
			}
		}
	}()
	if cancelBeforePump {
		if err := lib.cancel(runtimeID, nativeID); err != nil {
			return smokeError("native request cancel", backend.ErrorCode(err))
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case event := <-completions:
			callbackDrained = true
			if event.callbackID != callbackID || event.requestID != nativeID || event.status != 0 {
				return errors.New("native callback identity or status did not match the started request")
			}
			var envelope nativeEnvelope
			if err := json.Unmarshal(event.bytes, &envelope); err != nil || envelope.RequestID != requestID || envelope.SchemaVersion != abiVersion {
				return errors.New("native callback returned an invalid response envelope")
			}
			if cancelBeforePump {
				if envelope.Status != "error" || envelope.Error == nil || *envelope.Error != "cancelled" {
					return errors.New("cancelled native request did not deliver a cancelled terminal envelope")
				}
				return nil
			}
			if envelope.Status != "ok" || envelope.Error != nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
				return errors.New("native Doctor did not deliver a successful result envelope")
			}
			var doctor backend.Doctor
			if err := json.Unmarshal(envelope.Result, &doctor); err != nil || doctor.Permissions == nil {
				return errors.New("native Doctor result was missing the bounded permission report")
			}
			return nil
		default:
			if err := lib.pump(runtimeID, ownerPumpTimeoutMS); err != nil {
				return smokeError("native main-thread pump", backend.ErrorCode(err))
			}
		}
	}
	return errors.New("native request callback did not drain before the smoke deadline")
}

func closeSmokeRuntime(lib *nativeLibrary, runtimeID uint64) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := lib.closeRuntime(runtimeID)
		if err == nil {
			return nil
		}
		if backend.ErrorCode(err) != "desktop_busy" {
			return err
		}
		if pumpErr := lib.pump(runtimeID, ownerPumpTimeoutMS); pumpErr != nil {
			return pumpErr
		}
	}
	return backendError("unknown_outcome")
}

func smokeRunDoctor(libraryPath string, pid int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config := backend.Config{
		LibraryPath: libraryPath,
		Scope: backend.Scope{
			Processes: []backend.ProcessIdentity{{PID: pid, BundleID: fixtureBundleID}},
			ExpiresAt: time.Now().Add(2 * time.Minute),
		},
	}
	err := Run(ctx, config, func(native backend.Backend) error {
		resolved, err := ResolvedScope(native)
		if err != nil || len(resolved.Processes) != 1 {
			return backendError("backend_unavailable")
		}
		process := resolved.Processes[0]
		if process.PID != pid || process.BundleID != fixtureBundleID || process.LaunchID == "" {
			return backendError("backend_unavailable")
		}
		doctor, err := native.Doctor(ctx)
		if err != nil {
			return err
		}
		if doctor.Permissions == nil {
			return backendError("backend_unavailable")
		}
		return nil
	})
	if err != nil {
		retryCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		retryErr := RetryPendingClose(retryCtx)
		cancel()
		if retryErr != nil {
			return smokeError("retry retained native runtime close", backend.ErrorCode(retryErr))
		}
		return smokeError("same-image close/reopen Doctor", backend.ErrorCode(err))
	}
	return nil
}

func copyNativeImage(source, tempRoot string) (string, func() error, error) {
	input, err := os.Open(source)
	if err != nil {
		return "", nil, errors.New("could not open verified native image for inode-rejection probe")
	}
	copyFile, err := os.CreateTemp(tempRoot, "comuse-native-image-copy-*")
	if err != nil {
		if input.Close() != nil {
			return "", nil, errors.New("could not clean up second-image source handle")
		}
		return "", nil, errors.New("could not create external-temp second-image probe")
	}
	path := copyFile.Name()
	_, copyErr := io.Copy(copyFile, input)
	inputCloseErr := input.Close()
	closeErr := copyFile.Close()
	if copyErr != nil || inputCloseErr != nil || closeErr != nil {
		if os.Remove(path) != nil {
			return "", nil, errors.New("could not clean up failed second-image probe")
		}
		return "", nil, errors.New("could not prepare second-image rejection probe")
	}
	return path, func() error { return os.Remove(path) }, nil
}

func smokeError(operation, code string) error {
	if code == "" {
		code = "internal_error"
	}
	return fmt.Errorf("%s failed (%s)", operation, code)
}
