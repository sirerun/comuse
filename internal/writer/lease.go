// Package writer provides the durable writer admission boundary for a
// trusted native host. It records intent and replay outcomes; it does not post
// events or perform desktop actions itself.
package writer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ledgerVersion   = 1
	maxLedgerBytes  = 1 << 20
	maxActions      = 4096
	maxHeldInputs   = 64
	maxActionID     = 128
	maxReasonLength = 512
	terminalTTL     = 7 * 24 * time.Hour
	maxTombstones   = 16384
)

var (
	ErrUnsupportedLock   = errors.New("writer exclusion is unsupported on this platform")
	ErrDirty             = errors.New("writer host state is dirty; trusted reconciliation is required")
	ErrBindingMismatch   = errors.New("action ID was already used with a different binding commitment")
	ErrNoTicket          = errors.New("action is already recorded and cannot be dispatched again")
	ErrTicketUsed        = errors.New("dispatch ticket has already been consumed")
	ErrClosed            = errors.New("writer lease is closed")
	ErrCleanupPanic      = errors.New("writer cleanup callback panicked")
	ErrCloseNotPersisted = errors.New("writer close state was not durably persisted; writer lock remains held")
	ErrLeaseRetained     = errors.New("writer lease remains held for safe close retry")
)

type Outcome string

const (
	OutcomeApplied    Outcome = "applied"
	OutcomeNotApplied Outcome = "not_applied"
	OutcomePartial    Outcome = "partial"
	OutcomeUnknown    Outcome = "unknown"
)

// SafeActionMetadata is a compact, value-free replay summary. Its fields are
// closed enums so callers cannot persist native response text or secrets.
type SafeActionMetadata struct {
	Action             string `json:"action,omitempty"`
	Method             string `json:"method,omitempty"`
	CompletedSteps     string `json:"completed_steps,omitempty"`
	VerificationReason string `json:"verification_reason,omitempty"`
	Execution          string `json:"execution,omitempty"`
	Verification       string `json:"verification,omitempty"`
	StateStatus        string `json:"state_status,omitempty"`
	Cleanup            string `json:"cleanup,omitempty"`
	ErrorCode          string `json:"error_code,omitempty"`
}

type HeldStatus string

const (
	HeldComplete HeldStatus = "complete"
	HeldDirty    HeldStatus = "dirty"
	HeldUnknown  HeldStatus = "unknown"
)

type PriorOutcome struct {
	ActionID  string             `json:"action_id"`
	Outcome   Outcome            `json:"outcome"`
	UpdatedAt time.Time          `json:"updated_at"`
	Metadata  SafeActionMetadata `json:"metadata,omitempty"`
}

type actionRecord struct {
	Binding  string             `json:"binding_commitment"`
	Outcome  Outcome            `json:"outcome"`
	Started  time.Time          `json:"started_at"`
	Updated  time.Time          `json:"updated_at"`
	Metadata SafeActionMetadata `json:"metadata,omitempty"`
}

type heldRecord struct {
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

type actionTombstone struct {
	Binding  string             `json:"binding_commitment"`
	Outcome  Outcome            `json:"outcome"`
	Updated  time.Time          `json:"updated_at"`
	Metadata SafeActionMetadata `json:"metadata,omitempty"`
}

type ReconcileFunc func(context.Context, []HeldStatusRecord) error

type HeldStatusRecord struct {
	ID     string
	Status HeldStatus
}

type reconciliationRecord struct {
	VerifiedAt time.Time `json:"verified_at"`
}

type diskState struct {
	Version            int                        `json:"version"`
	Dirty              bool                       `json:"dirty"`
	DirtyReason        string                     `json:"dirty_reason,omitempty"`
	Actions            map[string]actionRecord    `json:"actions"`
	Tombstones         map[string]actionTombstone `json:"tombstones"`
	Held               map[string]heldRecord      `json:"held_inputs"`
	LastReconciliation *reconciliationRecord      `json:"last_reconciliation,omitempty"`
}

type CleanupFunc func(context.Context) error

type Lease struct {
	mu       sync.Mutex
	closeMu  sync.Mutex
	root     string
	lockFile *os.File
	state    diskState
	active   map[string]*cleanupEntry
	write    func(string, []byte) error
	closed   bool
	closing  bool
}

type cleanupEntry struct {
	fn      CleanupFunc
	running chan struct{}
	err     error
}

type Ticket struct {
	lease    *Lease
	actionID string
	binding  string
	mu       sync.Mutex
	consumed bool
}

type HeldInput struct {
	lease *Lease
	id    string
	mu    sync.Mutex
	done  bool
}

// Acquire creates or opens a private state root and holds a nonblocking
// cross-process writer lock until Close. The root is supplied by the trusted
// host and must be a private directory owned by the current user.
func Acquire(ctx context.Context, root string) (*Lease, error) {
	if ctx == nil {
		return nil, errors.New("Acquire requires a context")
	}
	if !writerLockAvailable() {
		return nil, ErrUnsupportedLock
	}
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create state root: %w", err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect state root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, errors.New("state root must be a real directory, not a symlink")
	}
	if err := verifyCurrentOwner(rootInfo); err != nil {
		return nil, fmt.Errorf("state root ownership: %w", err)
	}
	if rootInfo.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("state root permissions must exclude group and other access")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(root, ".writer.lock")
	if info, statErr := os.Lstat(lockPath); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("writer lock must be a regular file")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect writer lock: %w", statErr)
	}
	lockFile, err := openLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("open writer lock: %w", err)
	}
	lockInfo, err := lockFile.Stat()
	if err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("inspect writer lock: %w", err)
	}
	if !lockInfo.Mode().IsRegular() {
		_ = lockFile.Close()
		return nil, errors.New("writer lock must be a regular file")
	}
	if err := verifyCurrentOwner(lockInfo); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("writer lock ownership: %w", err)
	}
	if err := lockFile.Chmod(0o600); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("restrict writer lock permissions: %w", err)
	}
	if err = acquireFileLock(ctx, lockFile); err != nil {
		_ = lockFile.Close()
		return nil, err
	}
	lease := &Lease{root: root, lockFile: lockFile, active: make(map[string]*cleanupEntry), write: atomicWrite}
	lease.state, err = loadState(root)
	if err != nil {
		_ = releaseFileLock(lockFile)
		_ = lockFile.Close()
		return nil, err
	}
	if lease.state.Version != ledgerVersion {
		lease.releaseLock()
		return nil, fmt.Errorf("unsupported replay ledger version %d", lease.state.Version)
	}
	if lease.state.Actions == nil {
		lease.state.Actions = make(map[string]actionRecord)
	}
	if lease.state.Tombstones == nil {
		lease.state.Tombstones = make(map[string]actionTombstone)
	}
	if lease.state.Held == nil {
		lease.state.Held = make(map[string]heldRecord)
	}
	if err := lease.pruneExpired(time.Now()); err != nil {
		lease.releaseLock()
		return nil, err
	}
	for id, held := range lease.state.Held {
		if held.Status == "held" {
			held.Status = string(HeldUnknown)
			held.UpdatedAt = time.Now().UTC()
			lease.state.Held[id] = held
			lease.markDirtyLocked("unresolved_held_input_after_restart")
		}
	}
	for id, record := range lease.state.Actions {
		if record.Outcome == "inflight" {
			record.Outcome = OutcomeUnknown
			record.Updated = time.Now().UTC()
			lease.state.Actions[id] = record
			lease.markDirtyLocked("unresolved_action_after_restart")
		}
	}
	if err = lease.saveLocked(); err != nil {
		lease.releaseLock()
		return nil, fmt.Errorf("persist replay state on acquire: %w", err)
	}
	return lease, nil
}

// Begin durably admits one write intent. Existing IDs return their recorded
// outcome without a ticket; only a never-seen ID gets a dispatch ticket.
func (l *Lease) Begin(actionID string, bindingCommitment [32]byte) (*Ticket, *PriorOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.closing {
		return nil, nil, ErrClosed
	}
	if err := validateActionID(actionID); err != nil {
		return nil, nil, err
	}
	binding := base64.RawURLEncoding.EncodeToString(bindingCommitment[:])
	if existing, ok := l.state.Actions[actionID]; ok {
		if existing.Binding != binding {
			return nil, nil, ErrBindingMismatch
		}
		outcome := existing.Outcome
		if outcome == "inflight" {
			outcome = OutcomeUnknown
		}
		prior := &PriorOutcome{ActionID: actionID, Outcome: outcome, UpdatedAt: existing.Updated, Metadata: existing.Metadata}
		return nil, prior, nil
	}
	if existing, ok := l.state.Tombstones[actionID]; ok {
		if existing.Binding != binding {
			return nil, nil, ErrBindingMismatch
		}
		prior := &PriorOutcome{ActionID: actionID, Outcome: existing.Outcome, UpdatedAt: existing.Updated, Metadata: existing.Metadata}
		return nil, prior, nil
	}
	if l.state.Dirty {
		return nil, nil, ErrDirty
	}
	if len(l.state.Actions) >= maxActions {
		if err := l.pruneExpired(time.Now()); err != nil {
			return nil, nil, err
		}
		if len(l.state.Actions) >= maxActions {
			return nil, nil, errors.New("replay ledger action capacity exhausted")
		}
	}
	now := time.Now().UTC()
	l.state.Actions[actionID] = actionRecord{Binding: binding, Outcome: "inflight", Started: now, Updated: now}
	if err := l.saveLocked(); err != nil {
		delete(l.state.Actions, actionID)
		return nil, nil, err
	}
	return &Ticket{lease: l, actionID: actionID, binding: binding}, nil, nil
}

// Finish consumes a ticket after recording a terminal outcome durably.
func (l *Lease) Finish(ticket *Ticket, outcome Outcome) error {
	return l.FinishWithMetadata(ticket, outcome, SafeActionMetadata{})
}

// FinishWithMetadata durably records only closed-enum summary fields alongside
// the terminal action outcome. Unknown metadata is rejected before persistence.
func (l *Lease) FinishWithMetadata(ticket *Ticket, outcome Outcome, metadata SafeActionMetadata) error {
	if ticket == nil || ticket.lease != l {
		return errors.New("ticket does not belong to this lease")
	}
	if !validOutcome(outcome) {
		return fmt.Errorf("invalid action outcome %q", outcome)
	}
	if !validSafeActionMetadata(metadata) {
		return errors.New("invalid safe action metadata")
	}
	if !metadataMatchesOutcome(outcome, metadata) {
		return errors.New("action outcome conflicts with safe metadata execution")
	}
	ticket.mu.Lock()
	defer ticket.mu.Unlock()
	if ticket.consumed {
		return ErrTicketUsed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.closing {
		return ErrClosed
	}
	record, ok := l.state.Actions[ticket.actionID]
	if !ok || record.Binding != ticket.binding || record.Outcome != "inflight" {
		return ErrNoTicket
	}
	record.Outcome = outcome
	record.Updated = time.Now().UTC()
	record.Metadata = metadata
	l.state.Actions[ticket.actionID] = record
	if err := l.saveLocked(); err != nil {
		record.Outcome = "inflight"
		record.Metadata = SafeActionMetadata{}
		l.state.Actions[ticket.actionID] = record
		return err
	}
	ticket.consumed = true
	return nil
}

// Quarantine persists a dirty marker while retaining the process-wide writer
// lock. The trusted owner must close/drain the native client before Close.
func (l *Lease) Quarantine(reason string) error {
	if reason != "native_outcome_unknown" && reason != "native_drain_incomplete" && reason != "journal_persistence_failed" {
		return errors.New("unsupported quarantine reason")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.closing {
		return ErrClosed
	}
	l.markDirtyLocked(reason)
	return l.saveLocked()
}

// ReconcileDirty clears the persistent dirty gate only after the trusted host
// verifier confirms that no held input remains. The verifier runs while the
// lease mutex is held and must not call methods on this Lease.
func (l *Lease) ReconcileDirty(ctx context.Context, reason string, verify ReconcileFunc) error {
	if ctx == nil || verify == nil {
		return errors.New("ReconcileDirty requires a context and trusted cleanup verifier")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.closing {
		return ErrClosed
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > maxReasonLength {
		return errors.New("reconciliation reason must be nonempty and bounded")
	}
	if !l.state.Dirty {
		return nil
	}
	if len(l.active) > 0 {
		return ErrDirty
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	heldRows := make([]HeldStatusRecord, 0, len(l.state.Held))
	for id, held := range l.state.Held {
		if held.Status != string(HeldComplete) {
			heldRows = append(heldRows, HeldStatusRecord{ID: id, Status: HeldStatus(held.Status)})
		}
	}
	if err := verify(ctx, heldRows); err != nil {
		return fmt.Errorf("trusted held-input reconciliation failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	previousDirty := l.state.Dirty
	previousDirtyReason := l.state.DirtyReason
	previousReconciliation := l.state.LastReconciliation
	previousHeld := make(map[string]heldRecord, len(l.state.Held))
	for id, held := range l.state.Held {
		previousHeld[id] = held
	}
	for id, held := range l.state.Held {
		held.Status = string(HeldComplete)
		held.UpdatedAt = time.Now().UTC()
		l.state.Held[id] = held
	}
	l.state.Dirty = false
	l.state.DirtyReason = ""
	// Persist only that the trusted verifier ran, never its free-form reason.
	l.state.LastReconciliation = &reconciliationRecord{VerifiedAt: time.Now().UTC()}
	if err := l.saveLocked(); err != nil {
		l.state.Dirty = previousDirty
		l.state.DirtyReason = previousDirtyReason
		l.state.LastReconciliation = previousReconciliation
		for id, held := range previousHeld {
			l.state.Held[id] = held
		}
		return err
	}
	return nil
}

// RegisterHeldInput persists a held marker before a host begins holding an
// input state. The cleanup callback is injected by the trusted host.
func (l *Lease) RegisterHeldInput(id string, cleanup CleanupFunc) (*HeldInput, error) {
	if cleanup == nil {
		return nil, errors.New("held-input cleanup callback is required")
	}
	if err := validateActionID(id); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.closing {
		return nil, ErrClosed
	}
	if l.state.Dirty {
		return nil, ErrDirty
	}
	if _, exists := l.state.Held[id]; exists {
		return nil, fmt.Errorf("held-input ID %q already exists", id)
	}
	if len(l.state.Held) >= maxHeldInputs {
		return nil, errors.New("held-input capacity exhausted")
	}
	l.state.Held[id] = heldRecord{Status: "held", UpdatedAt: time.Now().UTC()}
	if err := l.saveLocked(); err != nil {
		delete(l.state.Held, id)
		return nil, err
	}
	l.active[id] = &cleanupEntry{fn: cleanup}
	return &HeldInput{lease: l, id: id}, nil
}

// Release invokes the injected cleanup and records complete or dirty state.
func (h *HeldInput) Release(ctx context.Context) error {
	if h == nil || h.lease == nil {
		return errors.New("held-input handle is required")
	}
	if ctx == nil {
		return errors.New("Release requires a context")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return errors.New("held-input cleanup already attempted")
	}
	h.done = true
	return h.lease.releaseHeld(ctx, h.id)
}

func (l *Lease) releaseHeld(ctx context.Context, id string) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return ErrClosed
	}
	entry := l.active[id]
	if entry == nil {
		l.mu.Unlock()
		return errors.New("held-input cleanup callback is unavailable")
	}
	if entry.running != nil {
		done := entry.running
		l.mu.Unlock()
		select {
		case <-done:
			return entry.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	entry.running = make(chan struct{})
	done := entry.running
	l.mu.Unlock()

	go func() {
		cleanupErr := invokeCleanup(ctx, entry.fn)
		if cleanupErr == nil && ctx.Err() != nil {
			cleanupErr = ctx.Err()
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		record, ok := l.state.Held[id]
		if !ok {
			entry.err = errors.New("held-input record disappeared")
		} else {
			record.UpdatedAt = time.Now().UTC()
			if cleanupErr == nil {
				record.Status = string(HeldComplete)
			} else {
				record.Status = string(HeldDirty)
				l.markDirtyLocked("held_input_cleanup_failed")
			}
			l.state.Held[id] = record
			saveErr := l.saveLocked()
			if saveErr != nil {
				l.markDirtyLocked("held_input_state_persist_failed")
			}
			entry.err = errors.Join(cleanupErr, saveErr)
		}
		delete(l.active, id)
		close(done)
	}()
	select {
	case <-done:
		return entry.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func invokeCleanup(ctx context.Context, cleanup CleanupFunc) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = ErrCleanupPanic
		}
	}()
	return cleanup(ctx)
}

// Close runs any remaining cleanup callbacks and releases the OS writer lock.
// Failed cleanup persists dirty state and is returned to the host.
func (l *Lease) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("Close requires a context")
	}
	l.closeMu.Lock()
	defer l.closeMu.Unlock()
	l.mu.Lock()
	if l.closed {
		dirty := l.state.Dirty
		l.mu.Unlock()
		if dirty {
			return ErrDirty
		}
		return nil
	}
	l.closing = true
	ids := make([]string, 0, len(l.active))
	for id := range l.active {
		ids = append(ids, id)
	}
	l.mu.Unlock()

	var cleanupErr error
	for _, id := range ids {
		if err := l.releaseHeld(ctx, id); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
		l.mu.Lock()
		stillRunning := l.active[id] != nil
		l.mu.Unlock()
		if stillRunning {
			// Keep the process-wide writer lock until no cleanup callback can
			// mutate the desktop or this lease's ledger.
			return errors.Join(cleanupErr, ErrLeaseRetained)
		}
	}

	l.mu.Lock()
	if len(l.active) != 0 {
		l.markDirtyLocked("held_input_cleanup_incomplete")
		_ = l.saveLocked()
	}
	for id, record := range l.state.Actions {
		if record.Outcome == "inflight" {
			record.Outcome = OutcomeUnknown
			record.Updated = time.Now().UTC()
			l.state.Actions[id] = record
			l.markDirtyLocked("unresolved_action_on_close")
		}
	}
	saveErr := l.saveLocked()
	cleanupErr = errors.Join(cleanupErr, saveErr)
	if saveErr != nil {
		// The durable dirty/terminal state is not confirmed. Keep both the lease
		// object and flock retryable; releasing it could admit another writer.
		l.mu.Unlock()
		return errors.Join(cleanupErr, ErrCloseNotPersisted, ErrLeaseRetained)
	}
	if l.state.Dirty {
		cleanupErr = errors.Join(cleanupErr, ErrDirty)
	}
	l.closed = true
	lockFile := l.lockFile
	l.lockFile = nil
	l.mu.Unlock()
	if lockFile != nil {
		cleanupErr = errors.Join(cleanupErr, releaseFileLock(lockFile), lockFile.Close())
	}
	return cleanupErr
}

func (l *Lease) releaseLock() {
	if l.lockFile != nil {
		_ = releaseFileLock(l.lockFile)
		_ = l.lockFile.Close()
		l.lockFile = nil
	}
}

func (l *Lease) markDirtyLocked(reason string) {
	l.state.Dirty = true
	l.state.DirtyReason = reason
}

func (l *Lease) pruneExpired(now time.Time) error {
	for id, record := range l.state.Actions {
		if record.Outcome != "inflight" && now.Sub(record.Updated) > terminalTTL {
			if len(l.state.Tombstones) >= maxTombstones {
				return errors.New("replay tombstone capacity exhausted; refusing to forget action IDs")
			}
			l.state.Tombstones[id] = actionTombstone{Binding: record.Binding, Outcome: record.Outcome, Updated: record.Updated, Metadata: record.Metadata}
			delete(l.state.Actions, id)
		}
	}
	return nil
}

func (l *Lease) saveLocked() error {
	if len(l.state.Actions) > maxActions || len(l.state.Tombstones) > maxTombstones || len(l.state.Held) > maxHeldInputs {
		return errors.New("replay state exceeds bounded record count")
	}
	data, err := json.Marshal(l.state)
	if err != nil {
		return err
	}
	if len(data) > maxLedgerBytes {
		return errors.New("replay ledger exceeds size limit")
	}
	write := l.write
	if write == nil {
		write = atomicWrite
	}
	return write(filepath.Join(l.root, "replay-v1.json"), data)
}

func loadState(root string) (loaded diskState, resultErr error) {
	path := filepath.Join(root, "replay-v1.json")
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return diskState{}, errors.New("replay ledger must be a regular file")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return diskState{}, fmt.Errorf("inspect replay ledger: %w", statErr)
	}
	file, err := openLedgerFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return diskState{Version: ledgerVersion, Actions: make(map[string]actionRecord), Tombstones: make(map[string]actionTombstone), Held: make(map[string]heldRecord)}, nil
	}
	if err != nil {
		return diskState{}, fmt.Errorf("open replay ledger: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close replay ledger: %w", closeErr))
		}
	}()
	fileInfo, err := file.Stat()
	if err != nil {
		return diskState{}, fmt.Errorf("inspect replay ledger: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return diskState{}, errors.New("replay ledger must be a regular file")
	}
	if err := verifyCurrentOwner(fileInfo); err != nil {
		return diskState{}, fmt.Errorf("replay ledger ownership: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLedgerBytes+1))
	if err != nil {
		return diskState{}, fmt.Errorf("read replay ledger: %w", err)
	}
	if len(data) > maxLedgerBytes {
		return diskState{}, errors.New("replay ledger exceeds size limit")
	}
	var state diskState
	if err := json.Unmarshal(data, &state); err != nil {
		return diskState{}, fmt.Errorf("decode replay ledger: %w", err)
	}
	if state.Version != ledgerVersion {
		return state, nil
	}
	if len(state.Actions) > maxActions || len(state.Tombstones) > maxTombstones || len(state.Held) > maxHeldInputs {
		return diskState{}, errors.New("replay ledger exceeds record limits")
	}
	for id, record := range state.Actions {
		if validateActionID(id) != nil || record.Binding == "" || !validRecordedOutcome(record.Outcome) || !validSafeActionMetadata(record.Metadata) || !metadataMatchesOutcome(record.Outcome, record.Metadata) {
			return diskState{}, fmt.Errorf("invalid replay record %q", id)
		}
	}
	for id, record := range state.Tombstones {
		if validateActionID(id) != nil || record.Binding == "" || !validOutcome(record.Outcome) || !validSafeActionMetadata(record.Metadata) || !metadataMatchesOutcome(record.Outcome, record.Metadata) {
			return diskState{}, fmt.Errorf("invalid replay tombstone %q", id)
		}
	}
	for id, record := range state.Held {
		if validateActionID(id) != nil || (record.Status != "held" && record.Status != string(HeldComplete) && record.Status != string(HeldDirty) && record.Status != string(HeldUnknown)) {
			return diskState{}, fmt.Errorf("invalid held-input record %q", id)
		}
	}
	return state, nil
}

func atomicWrite(path string, data []byte) (resultErr error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".replay-*.tmp")
	if err != nil {
		return fmt.Errorf("create replay temp file: %w", err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if renamed {
			return
		}
		if removeErr := os.Remove(tmpName); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove replay temp file: %w", removeErr))
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("write replay temp file: %w", err), tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync replay temp file: %w", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close replay temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace replay ledger: %w", err)
	}
	renamed = true
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open replay directory: %w", err)
	}
	return errors.Join(wrapSyncError(directory.Sync()), wrapCloseError(directory.Close()))
}

func wrapSyncError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("sync replay directory: %w", err)
}

func wrapCloseError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close replay directory: %w", err)
}

func validateActionID(id string) error {
	if len(id) == 0 || len(id) > maxActionID {
		return errors.New("action ID must be nonempty and at most 128 bytes")
	}
	for _, char := range id {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._:-", char) {
			continue
		}
		return errors.New("action ID contains unsupported characters")
	}
	return nil
}

func validOutcome(outcome Outcome) bool {
	return outcome == OutcomeApplied || outcome == OutcomeNotApplied || outcome == OutcomePartial || outcome == OutcomeUnknown
}

func validRecordedOutcome(outcome Outcome) bool {
	return validOutcome(outcome) || outcome == "inflight"
}

func validSafeActionMetadata(value SafeActionMetadata) bool {
	if value == (SafeActionMetadata{}) {
		return true
	}
	if !validRecordedMethod(value.Method) || !validRecordedSteps(value.CompletedSteps) || !validRecordedVerificationReason(value.VerificationReason) {
		return false
	}
	switch value.Action {
	case "", "read_value", "replace", "insert", "press", "pick", "focus", "scroll":
	default:
		return false
	}
	switch value.Execution {
	case "not_applied", "applied", "partial", "unknown":
	default:
		return false
	}
	switch value.Verification {
	case "verified", "failed", "unavailable":
	default:
		return false
	}
	switch value.StateStatus {
	case "available", "partial", "unavailable":
	default:
		return false
	}
	switch value.Cleanup {
	case "released", "not_required", "failed", "unknown":
	default:
		return false
	}
	if value.ErrorCode == "" {
		return true
	}
	switch value.ErrorCode {
	case "backend_unavailable", "backend_outcome_invalid", "cancelled_before_dispatch",
		"classification_unavailable", "desktop_busy", "dispatch_unknown", "element_stale",
		"native_error", "outcome_unknown", "policy_refused", "postcondition_failed",
		"postcondition_unavailable", "protected_or_unsupported_target", "protected_target",
		"scope_mismatch", "selection_unavailable", "state_expired", "unsupported",
		"validation_error", "value_unavailable", "verification_unavailable",
		"approval_required", "budget_exceeded", "cancelled", "internal_error",
		"invalid_request", "permission_denied", "rate_limited", "session_closed",
		"unknown_outcome":
		return true
	default:
		return false
	}
}

func metadataMatchesOutcome(outcome Outcome, value SafeActionMetadata) bool {
	return value.Execution == "" || value.Execution == string(outcome)
}

// BindingCommitment hashes a caller-keyed opaque commitment into a fixed-size
// ledger value. Callers must use a private keyed commitment and must never pass
// raw action text or a bare secret-value digest.
func BindingCommitment(key, opaqueBinding []byte) ([32]byte, error) {
	if len(key) < 32 || len(opaqueBinding) == 0 || len(opaqueBinding) > 4096 {
		return [32]byte{}, errors.New("commitment requires a 32-byte key and bounded opaque binding")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(opaqueBinding)
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result, nil
}

func validRecordedMethod(v string) bool {
	switch v {
	case "", "ax_press", "ax_pick", "ax_focus", "ax_set_value", "ax_scroll", "ax_focus_window", "cg_click", "cg_unicode", "cg_key", "cg_scroll", "cg_drag":
		return true
	}
	return false
}
func validRecordedSteps(v string) bool {
	if v == "" {
		return true
	}
	parts := strings.Split(v, ",")
	if len(parts) > 128 {
		return false
	}
	for _, p := range parts {
		switch p {
		case "focus", "press", "pick", "set_value", "unicode", "key_down", "key_up", "mouse_down", "mouse_up", "mouse_move", "scroll", "cleanup":
		default:
			return false
		}
	}
	return true
}
func validRecordedVerificationReason(v string) bool {
	switch v {
	case "", "postcondition_met", "postcondition_failed", "state_changed", "target_missing", "state_unavailable", "verification_unavailable":
		return true
	}
	return false
}
