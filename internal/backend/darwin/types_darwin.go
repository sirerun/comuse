//go:build darwin && cgo

package darwin

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/sirerun/comuse/internal/backend"
)

const (
	abiVersion         = 1
	maximumProcesses   = 32
	maximumInflight    = 32
	maximumRefBytes    = 128
	maximumTextBytes   = 8192
	maximumResponse    = 64 * 1024
	ownerPumpTimeoutMS = 10
)

type nativeConfig struct {
	SchemaVersion int         `json:"schema_version"`
	Scope         nativeScope `json:"scope"`
	AllowValues   bool        `json:"allow_values"`
}

type nativeScope struct {
	Processes          []backend.ProcessIdentity `json:"processes"`
	ExpiresAtUnixMilli int64                     `json:"expires_at_unix_milli"`
}

type nativeRequest struct {
	SchemaVersion int             `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Operation     string          `json:"operation"`
	WindowRef     string          `json:"window_ref,omitempty"`
	ElementRef    string          `json:"element_ref,omitempty"`
	StateID       string          `json:"state_id,omitempty"`
	Budget        *backend.Budget `json:"budget,omitempty"`
	Action        *nativeAction   `json:"action,omitempty"`
}

// nativeAction is the Darwin transport projection. It preserves required zero
// values and an empty replace text while emitting only fields permitted for
// the selected action kind; the shared backend DTO remains unchanged.
type nativeAction map[string]any

func actionTransport(action backend.Action) nativeAction {
	value := nativeAction{"id": action.ID, "window_ref": action.WindowRef, "element_ref": action.ElementRef,
		"state_id": action.StateID, "kind": action.Kind}
	switch action.Kind {
	case backend.ActionReplace, backend.ActionInsert:
		value["text"] = action.Text
	case backend.ActionScroll:
		value["direction"], value["amount"] = action.Direction, action.Amount
	case backend.ActionClick:
		value["x"], value["y"], value["button"], value["count"], value["hold_ms"] = action.X, action.Y, action.Button, action.Count, action.HoldMS
	case backend.ActionTypeText:
		value["text"], value["delay_ms"] = action.Text, action.DelayMS
	case backend.ActionPressKey:
		value["keys"], value["hold_ms"] = action.Keys, action.HoldMS
	case backend.ActionCoordinateScroll:
		value["x"], value["y"], value["dx"], value["dy"] = action.X, action.Y, action.DX, action.DY
	case backend.ActionDrag:
		value["x"], value["y"], value["end_x"], value["end_y"] = action.X, action.Y, action.EndX, action.EndY
		value["steps"], value["duration_ms"] = action.Steps, action.DurationMS
	}
	return value
}

type nativeEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Status        string          `json:"status"`
	Result        json.RawMessage `json:"result"`
	Error         *string         `json:"error"`
}

type callResult struct {
	value json.RawMessage
	err   error
}

type ownerCommand struct {
	request []byte
	ctx     context.Context
	reply   chan callResult
	close   bool
}

type pendingRequest struct {
	nativeID  uint64
	ctx       context.Context
	reply     chan callResult
	request   []byte
	cancelled bool
}

type nativeTransport interface {
	isProcessMain() bool
	open([]byte) (uint64, []byte, error)
	pump(uint64, uint32) error
	closeRuntime(uint64) error
	start(uint64, []byte, uint64, chan nativeCompletion) (uint64, error)
	cancel(uint64, uint64) error
	close()
}

type runtimeOwner struct {
	lib           nativeTransport
	runtimeID     uint64
	commands      chan ownerCommand
	completions   chan nativeCompletion
	backend       *nativeBackend
	pending       map[uint64]pendingRequest
	workerDone    <-chan error
	workerErr     error
	workerEnded   bool
	closing       bool
	closed        bool
	closeWaiters  []chan callResult
	closeAttempts int
	retryAt       time.Time
	drainDeadline time.Time
}

type nativeBackend struct {
	owner          *runtimeOwner
	boundScope     backend.Scope
	allowValues    bool
	qualifiedInput bool // private admission; zero-valued in every production constructor
	closing        atomic.Bool
	closed         atomic.Bool
	inflight       atomic.Int32
}
