package comuse

import (
	"errors"
	"sync"
	"time"
)

// JSONSafeIntegerMax is the largest integer accepted by the Phase 1 schema.
const JSONSafeIntegerMax uint64 = 1<<53 - 1

const maxRetainedBytes uint64 = 4 << 20

// Counter identifies one cumulative ledger counter. Gauges are updated with
// SetRetainedBytes and elapsed time is advanced from a monotonic clock.
type Counter uint8

const (
	CounterActions Counter = iota + 1
	CounterObservationScreenshot
	CounterObservationA11y
	CounterObservationState
	CounterSemanticSnapshot
	CounterSemanticDelta
	CounterSemanticUnchanged
	CounterBaselineResets
	CounterEncodedImageBytes
	CounterSerializedTextBytes
	CounterImages
)

var (
	ErrUnknownCounter = errors.New("unknown ledger counter")
	ErrRetainedBytes  = errors.New("retained bytes exceed the ledger limit")
	ErrCallFinished   = errors.New("metadata call already finished")
)

// Ledger is a concurrency-safe cumulative accounting engine. Snapshots are
// detached values and remain unchanged when later counters advance.
type Ledger struct {
	mu                  sync.Mutex
	started             time.Time
	actions             uint64
	observations        ObservationCounts
	semantic            SemanticResultCounts
	baselineResets      uint64
	encodedImageBytes   uint64
	serializedTextBytes uint64
	images              uint64
	retainedBytes       uint64
	elapsedMS           uint64
}

// ObservationCounts contains attempted native observation counters.
type ObservationCounts struct {
	Screenshot uint64 `json:"screenshot"`
	A11y       uint64 `json:"a11y"`
	State      uint64 `json:"state"`
}

// SemanticResultCounts contains successfully returned semantic results.
type SemanticResultCounts struct {
	Snapshot  uint64 `json:"snapshot"`
	Delta     uint64 `json:"delta"`
	Unchanged uint64 `json:"unchanged"`
}

// LedgerSnapshot is a defensive immutable-by-value view of cumulative usage.
// Session is supplied by the owning core and is opaque to this engine.
type LedgerSnapshot struct {
	Session             string               `json:"session"`
	Actions             uint64               `json:"actions"`
	Observations        ObservationCounts    `json:"observations"`
	SemanticResults     SemanticResultCounts `json:"semantic_results"`
	BaselineResets      uint64               `json:"baseline_resets"`
	EncodedImageBytes   uint64               `json:"encoded_image_bytes"`
	SerializedTextBytes uint64               `json:"serialized_text_bytes"`
	Images              uint64               `json:"images"`
	RetainedBytes       uint64               `json:"retained_bytes"`
	ElapsedMS           uint64               `json:"elapsed_ms"`
	ModelUsage          *ModelUsage          `json:"model_usage"`
}

// Usage is the per-call counter delta plus current gauges and duration.
type Usage struct {
	Actions             uint64               `json:"actions"`
	Observations        ObservationCounts    `json:"observations"`
	SemanticResults     SemanticResultCounts `json:"semantic_results"`
	BaselineResets      uint64               `json:"baseline_resets"`
	EncodedImageBytes   uint64               `json:"encoded_image_bytes"`
	SerializedTextBytes uint64               `json:"serialized_text_bytes"`
	Images              uint64               `json:"images"`
	RetainedBytes       uint64               `json:"retained_bytes"`
	ElapsedMS           uint64               `json:"elapsed_ms"`
	ModelUsage          *ModelUsage          `json:"model_usage"`
	DurationMS          uint64               `json:"duration_ms"`
}

// NewLedger creates a zeroed cumulative ledger. Session identity is attached
// only to snapshots, keeping the counter engine independent of session IDs.
func NewLedger() *Ledger { return &Ledger{started: time.Now()} }

// Add monotonically increments a known counter, saturating at the largest
// JSON-safe integer. It returns the resulting cumulative value.
func (l *Ledger) Add(counter Counter, amount uint64) (uint64, error) {
	if l == nil {
		return 0, errors.New("nil ledger")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	value, err := l.counter(counter)
	if err != nil {
		return 0, err
	}
	*value = saturatingAdd(*value, amount)
	return *value, nil
}

func (l *Ledger) counter(counter Counter) (*uint64, error) {
	switch counter {
	case CounterActions:
		return &l.actions, nil
	case CounterObservationScreenshot:
		return &l.observations.Screenshot, nil
	case CounterObservationA11y:
		return &l.observations.A11y, nil
	case CounterObservationState:
		return &l.observations.State, nil
	case CounterSemanticSnapshot:
		return &l.semantic.Snapshot, nil
	case CounterSemanticDelta:
		return &l.semantic.Delta, nil
	case CounterSemanticUnchanged:
		return &l.semantic.Unchanged, nil
	case CounterBaselineResets:
		return &l.baselineResets, nil
	case CounterEncodedImageBytes:
		return &l.encodedImageBytes, nil
	case CounterSerializedTextBytes:
		return &l.serializedTextBytes, nil
	case CounterImages:
		return &l.images, nil
	default:
		return nil, ErrUnknownCounter
	}
}

// SetRetainedBytes updates the current retained-state gauge, bounded by the
// schema limit. An oversized value leaves the current gauge unchanged.
func (l *Ledger) SetRetainedBytes(value uint64) error {
	if l == nil {
		return errors.New("nil ledger")
	}
	if value > maxRetainedBytes {
		return ErrRetainedBytes
	}
	l.mu.Lock()
	l.retainedBytes = value
	l.mu.Unlock()
	return nil
}

// AdvanceElapsed records monotonic session elapsed time without moving back.
func (l *Ledger) AdvanceElapsed(elapsed time.Duration) {
	if l == nil || elapsed < 0 {
		return
	}
	ms := uint64(elapsed / time.Millisecond)
	if ms > JSONSafeIntegerMax {
		ms = JSONSafeIntegerMax
	}
	l.mu.Lock()
	if ms > l.elapsedMS {
		l.elapsedMS = ms
	}
	l.mu.Unlock()
}

// Snapshot returns a detached view. No mutable reference from the live ledger
// is included.
func (l *Ledger) Snapshot(session string) LedgerSnapshot {
	if l == nil {
		return LedgerSnapshot{Session: session}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.advanceElapsedLocked(time.Since(l.started))
	return LedgerSnapshot{
		Session: session, Actions: l.actions, Observations: l.observations,
		SemanticResults: l.semantic, BaselineResets: l.baselineResets,
		EncodedImageBytes: l.encodedImageBytes, SerializedTextBytes: l.serializedTextBytes,
		Images: l.images, RetainedBytes: l.retainedBytes, ElapsedMS: l.elapsedMS,
	}
}

// ChargeLedgerSnapshot measures a standalone canonical Ledger representation
// from an immutable precharge snapshot, then charges those bytes to the live
// cumulative ledger. The supplied snapshot is never modified.
func (l *Ledger) ChargeLedgerSnapshot(snapshot LedgerSnapshot) (uint64, error) {
	if l == nil {
		return 0, errors.New("nil ledger")
	}
	if _, err := NewResultPayload(snapshot); err != nil {
		return 0, err
	}
	encoded, err := canonicalJSON(snapshot)
	if err != nil {
		return 0, err
	}
	charge := uint64(len(encoded))
	if charge > JSONSafeIntegerMax {
		charge = JSONSafeIntegerMax
	}
	if _, err := l.Add(CounterSerializedTextBytes, charge); err != nil {
		return 0, err
	}
	return charge, nil
}

// BeginCall captures an immutable counter baseline and monotonic start time.
func (l *Ledger) BeginCall() *CallSnapshot {
	if l == nil {
		return &CallSnapshot{started: time.Now()}
	}
	l.mu.Lock()
	l.advanceElapsedLocked(time.Since(l.started))
	baseline := l.valuesLocked()
	l.mu.Unlock()
	return &CallSnapshot{ledger: l, baseline: baseline, started: time.Now()}
}

type counterValues struct {
	actions                                                        uint64
	observations                                                   ObservationCounts
	semantic                                                       SemanticResultCounts
	baselineResets, encodedImageBytes, serializedTextBytes, images uint64
	retainedBytes, elapsedMS                                       uint64
}

// CallSnapshot is a one-call immutable baseline used by FinishCall.
type CallSnapshot struct {
	ledger     *Ledger
	baseline   counterValues
	callValues counterValues
	started    time.Time
	mu         sync.Mutex
	modelUsage *ModelUsage
	finished   bool
}

// ChargeLedgerSnapshot attributes a standalone Ledger read to this call using
// the exact immutable precharge record returned to the host.
func (c *CallSnapshot) ChargeLedgerSnapshot(snapshot LedgerSnapshot) (uint64, error) {
	if c == nil {
		return 0, errors.New("nil metadata call")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return 0, ErrCallFinished
	}
	if _, err := NewResultPayload(snapshot); err != nil {
		return 0, err
	}
	encoded, err := canonicalJSON(snapshot)
	if err != nil {
		return 0, err
	}
	charge := uint64(len(encoded))
	if charge > JSONSafeIntegerMax {
		charge = JSONSafeIntegerMax
	}
	c.callValues.serializedTextBytes = saturatingAdd(c.callValues.serializedTextBytes, charge)
	if c.ledger != nil {
		if _, err := c.ledger.Add(CounterSerializedTextBytes, charge); err != nil {
			return 0, err
		}
	}
	return charge, nil
}

// Add attributes a counter event to this logical call and updates the shared
// cumulative ledger atomically with respect to FinishCall.
func (c *CallSnapshot) Add(counter Counter, amount uint64) (uint64, error) {
	if c == nil {
		return 0, errors.New("nil metadata call")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return 0, ErrCallFinished
	}
	value, err := c.callCounter(counter)
	if err != nil {
		return 0, err
	}
	*value = saturatingAdd(*value, amount)
	if c.ledger == nil {
		return *value, nil
	}
	return c.ledger.Add(counter, amount)
}

func (c *CallSnapshot) callCounter(counter Counter) (*uint64, error) {
	switch counter {
	case CounterActions:
		return &c.callValues.actions, nil
	case CounterObservationScreenshot:
		return &c.callValues.observations.Screenshot, nil
	case CounterObservationA11y:
		return &c.callValues.observations.A11y, nil
	case CounterObservationState:
		return &c.callValues.observations.State, nil
	case CounterSemanticSnapshot:
		return &c.callValues.semantic.Snapshot, nil
	case CounterSemanticDelta:
		return &c.callValues.semantic.Delta, nil
	case CounterSemanticUnchanged:
		return &c.callValues.semantic.Unchanged, nil
	case CounterBaselineResets:
		return &c.callValues.baselineResets, nil
	case CounterEncodedImageBytes:
		return &c.callValues.encodedImageBytes, nil
	case CounterSerializedTextBytes:
		return &c.callValues.serializedTextBytes, nil
	case CounterImages:
		return &c.callValues.images, nil
	default:
		return nil, ErrUnknownCounter
	}
}

func (l *Ledger) valuesLocked() counterValues {
	return counterValues{actions: l.actions, observations: l.observations, semantic: l.semantic,
		baselineResets: l.baselineResets, encodedImageBytes: l.encodedImageBytes,
		serializedTextBytes: l.serializedTextBytes, images: l.images, retainedBytes: l.retainedBytes,
		elapsedMS: l.elapsedMS}
}

func (l *Ledger) advanceElapsedLocked(elapsed time.Duration) {
	if elapsed < 0 {
		return
	}
	ms := uint64(elapsed / time.Millisecond)
	if ms > JSONSafeIntegerMax {
		ms = JSONSafeIntegerMax
	}
	if ms > l.elapsedMS {
		l.elapsedMS = ms
	}
}

func saturatingAdd(value, amount uint64) uint64 {
	if value >= JSONSafeIntegerMax || amount >= JSONSafeIntegerMax-value {
		return JSONSafeIntegerMax
	}
	return value + amount
}

func saturatingDuration(start time.Time) uint64 {
	if start.IsZero() {
		return 0
	}
	d := time.Since(start)
	if d < 0 {
		return 0
	}
	ms := uint64(d / time.Millisecond)
	if ms > JSONSafeIntegerMax {
		return JSONSafeIntegerMax
	}
	return ms
}
