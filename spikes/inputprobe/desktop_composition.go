package inputprobe

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/sirerun/comuse/spikes/desktopprobe"
	"github.com/sirerun/comuse/spikes/policyprobe"
)

var (
	ErrNativeDrainRequired = errors.New("native client drain confirmation is required before releasing quarantined desktop ownership")
	ErrHostClosed          = errors.New("desktop input host is closed")
)

type writerLeaseContextKey struct{}

type desktopInputHost struct {
	executor *Executor
	writer   *desktopWriterAdapter
	backend  nativeBackend
	mu       sync.Mutex
	closeMu  sync.Mutex
	active   sync.WaitGroup
	closing  bool
	closed   bool
}

type desktopWriterAdapter struct {
	root        string
	journalKey  []byte
	mu          sync.Mutex
	quarantined map[*desktopLeaseAdapter]struct{}
}

type desktopLeaseAdapter struct {
	owner   *desktopWriterAdapter
	lease   *desktopprobe.Lease
	mu      sync.Mutex
	tickets map[string]*desktopprobe.Ticket
	priors  map[string]JournalRecord
	closed  bool
}

type desktopJournalAdapter struct {
	journalKey []byte
}

type nativeBackendDrainer interface {
	CloseAndDrain(context.Context) error
}

// newDesktopInputHost requires both caller-owned durable keys. It does not
// mint approvals and remains private to trusted in-process integration.
func newDesktopInputHost(gate *policyprobe.Gate, root string, journalKey, actionCommitmentKey []byte, backend nativeBackend, clock func() time.Time) (*desktopInputHost, error) {
	if gate == nil || backend == nil || len(journalKey) < 32 || len(actionCommitmentKey) < 32 || clock == nil {
		return nil, errors.New("trusted desktop input host requires gate, backend, clock, and two caller-owned 32-byte keys")
	}
	writer := &desktopWriterAdapter{root: root, journalKey: append([]byte(nil), journalKey...), quarantined: make(map[*desktopLeaseAdapter]struct{})}
	journal := &desktopJournalAdapter{journalKey: append([]byte(nil), journalKey...)}
	executor, err := newExecutor(gate, writer, journal, backend, actionCommitmentKey, clock)
	if err != nil {
		return nil, err
	}
	return &desktopInputHost{executor: executor, writer: writer, backend: backend}, nil
}

func (host *desktopInputHost) execute(ctx context.Context, request Request) ([]byte, error) {
	if host == nil {
		return nil, ErrHostClosed
	}
	host.mu.Lock()
	closed := host.closed || host.closing
	if !closed {
		host.active.Add(1)
	}
	host.mu.Unlock()
	if closed {
		return nil, ErrHostClosed
	}
	defer host.active.Done()
	return host.executor.execute(ctx, request)
}

// closeAfterNativeDrain must be called by the native owner after it no longer
// accepts requests. The backend's CloseAndDrain is the authoritative callback
// drain fence; dirty leases are released only after that returns successfully.
func (host *desktopInputHost) closeAfterNativeDrain(ctx context.Context) error {
	if host == nil || ctx == nil {
		return ErrHostClosed
	}
	host.closeMu.Lock()
	defer host.closeMu.Unlock()
	drainer, ok := host.backend.(nativeBackendDrainer)
	if !ok {
		return ErrNativeDrainRequired
	}
	host.mu.Lock()
	if host.closed {
		host.mu.Unlock()
		return nil
	}
	host.closing = true
	host.mu.Unlock()
	if err := drainer.CloseAndDrain(ctx); err != nil {
		return err
	}
	drained := make(chan struct{})
	go func() {
		host.active.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-ctx.Done():
		return ctx.Err()
	}
	host.mu.Lock()
	host.closed = true
	host.mu.Unlock()
	return host.writer.closeQuarantined(ctx)
}

func (writer *desktopWriterAdapter) Acquire(ctx context.Context) (writerLease, error) {
	lease, err := desktopprobe.Acquire(ctx, writer.root)
	if err != nil {
		return nil, err
	}
	return &desktopLeaseAdapter{owner: writer, lease: lease, tickets: make(map[string]*desktopprobe.Ticket), priors: make(map[string]JournalRecord)}, nil
}

func (lease *desktopLeaseAdapter) Release(ctx context.Context) error {
	if ctx == nil {
		return errors.New("lease release requires a context")
	}
	err := lease.lease.Close(ctx)
	lease.mu.Lock()
	lease.closed = err == nil || errors.Is(err, desktopprobe.ErrDirty)
	lease.mu.Unlock()
	if err != nil && !errors.Is(err, desktopprobe.ErrDirty) {
		lease.owner.retain(lease)
	}
	return err
}

func (lease *desktopLeaseAdapter) Quarantine(_ context.Context, _ error) error {
	if err := lease.lease.Quarantine("native_outcome_unknown"); err != nil {
		lease.owner.retain(lease)
		return err
	}
	lease.owner.retain(lease)
	return nil
}

func (writer *desktopWriterAdapter) retain(lease *desktopLeaseAdapter) {
	writer.mu.Lock()
	writer.quarantined[lease] = struct{}{}
	writer.mu.Unlock()
}

func (writer *desktopWriterAdapter) closeQuarantined(ctx context.Context) error {
	writer.mu.Lock()
	leases := make([]*desktopLeaseAdapter, 0, len(writer.quarantined))
	for lease := range writer.quarantined {
		leases = append(leases, lease)
	}
	writer.mu.Unlock()
	var result error
	for _, lease := range leases {
		err := lease.lease.Close(ctx)
		if err == nil || errors.Is(err, desktopprobe.ErrDirty) {
			writer.mu.Lock()
			delete(writer.quarantined, lease)
			writer.mu.Unlock()
			lease.mu.Lock()
			lease.closed = true
			lease.mu.Unlock()
		}
		result = errors.Join(result, err)
	}
	return result
}

func (journal *desktopJournalAdapter) Lookup(ctx context.Context, actionID string) (JournalRecord, bool, error) {
	lease, err := leaseFromContext(ctx)
	if err != nil {
		return JournalRecord{}, false, err
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	record, ok := lease.priors[actionID]
	return record, ok, nil
}

func (journal *desktopJournalAdapter) Begin(ctx context.Context, record JournalRecord) (bool, error) {
	lease, err := leaseFromContext(ctx)
	if err != nil {
		return false, err
	}
	commitment, err := desktopprobe.BindingCommitment(journal.journalKey, record.Commitment[:])
	if err != nil {
		return false, err
	}
	ticket, prior, err := lease.lease.Begin(record.ActionID, commitment)
	if err != nil {
		return false, err
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if prior != nil {
		lease.priors[record.ActionID] = journalRecordFromPrior(record, *prior)
		return false, nil
	}
	if ticket == nil {
		return false, errors.New("desktop lease returned neither a ticket nor prior outcome")
	}
	lease.tickets[record.ActionID] = ticket
	return true, nil
}

func (journal *desktopJournalAdapter) Finish(ctx context.Context, record JournalRecord) error {
	lease, err := leaseFromContext(ctx)
	if err != nil {
		return err
	}
	outcome, err := desktopOutcome(record.Execution)
	if err != nil {
		return err
	}
	metadata := desktopprobe.SafeActionMetadata{Action: record.Action, Execution: record.Execution, Verification: record.Verification, StateStatus: record.StateStatus, Cleanup: record.Cleanup, ErrorCode: record.ErrorCode}
	lease.mu.Lock()
	ticket := lease.tickets[record.ActionID]
	lease.mu.Unlock()
	if ticket == nil {
		return errors.New("durable input finish has no create-only ticket")
	}
	if err := lease.lease.FinishWithMetadata(ticket, outcome, metadata); err != nil {
		return err
	}
	lease.mu.Lock()
	delete(lease.tickets, record.ActionID)
	lease.mu.Unlock()
	return nil
}

func leaseFromContext(ctx context.Context) (*desktopLeaseAdapter, error) {
	if ctx == nil {
		return nil, errors.New("desktop journal context is required")
	}
	lease, ok := ctx.Value(writerLeaseContextKey{}).(*desktopLeaseAdapter)
	if !ok || lease == nil {
		return nil, errors.New("desktop journal requires the active writer lease")
	}
	return lease, nil
}

func journalRecordFromPrior(current JournalRecord, prior desktopprobe.PriorOutcome) JournalRecord {
	metadata := prior.Metadata
	current.Execution = string(prior.Outcome)
	if metadata.Execution != "" {
		current.Execution = metadata.Execution
	}
	current.Action = metadata.Action
	current.Verification = metadata.Verification
	if current.Verification == "" {
		current.Verification = "unavailable"
	}
	current.StateStatus = metadata.StateStatus
	if current.StateStatus == "" {
		current.StateStatus = "unavailable"
	}
	current.Cleanup = metadata.Cleanup
	if current.Cleanup == "" {
		current.Cleanup = "unknown"
	}
	current.ErrorCode = metadata.ErrorCode
	if current.Execution == "unknown" && current.ErrorCode == "" {
		current.ErrorCode = "outcome_unknown"
	}
	return current
}

func desktopOutcome(execution string) (desktopprobe.Outcome, error) {
	switch execution {
	case "applied":
		return desktopprobe.OutcomeApplied, nil
	case "not_applied":
		return desktopprobe.OutcomeNotApplied, nil
	case "partial":
		return desktopprobe.OutcomePartial, nil
	case "unknown":
		return desktopprobe.OutcomeUnknown, nil
	default:
		return "", errors.New("input execution has no durable desktop outcome")
	}
}
