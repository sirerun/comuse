package comuse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/sirerun/comuse/internal/writer"
)

const (
	maxSnapshotGenerations = 8
	maxSnapshotStorage     = 4 * 1024 * 1024
	snapshotTTL            = 2 * time.Minute
)

// Config defines a scoped session. Writer and approval fields are required
// together only when host-authorized mutation is enabled.
type Config struct {
	Backend          Backend
	Scope            Scope
	Budget           Budget
	AllowValues      bool
	ApprovalProvider ApprovalProvider
	WriterDirectory  string
	WriterKey        []byte
	MaxActions       int
}

// Session owns a backend and its bounded semantic snapshot and action state.
type Session struct {
	mu                sync.Mutex
	actionMu          sync.Mutex
	contextMu         sync.Mutex
	closeMu           sync.Mutex
	changed           chan struct{}
	active            int
	closing           bool
	closed            bool
	backendClosed     bool
	terminalCloseCode string
	permissionEpoch   uint64
	contextEpoch      uint64
	desktopContext    *DesktopContext
	hasDesktopContext bool

	ledger              *Ledger
	acquireDesktop      func(context.Context) (desktopAuthority, error)
	reserveQuota        func(context.Context, [32]byte) error
	journalBinding      func(string, []byte) ([32]byte, error)
	quarantinedDesktops []desktopAuthority
	backend             Backend
	scope               Scope
	budget              Budget
	allowValues         bool
	approvalProvider    ApprovalProvider
	writerDirectory     string
	writerKey           []byte
	maxActions          int
	mutationEnabled     bool
	sessionID           string
	now                 func() time.Time

	windows        map[string]Window
	snapshots      map[string][]snapshotBinding
	snapshotBytes  int
	actionsUsed    int
	actionSequence uint64
	usedApprovals  map[string]struct{}
	quarantined    []*writer.Lease
}

func (s *Session) stableBackendError(ctx context.Context, err error) error {
	s.invalidateOnBackendError(err)
	return stableContextError(ctx, err)
}

func (s *Session) invalidateOnBackendError(err error) {
	var backendErr *Error
	if errors.As(err, &backendErr) && backendErr.Code == "permission_denied" {
		s.invalidateSemanticState()
	}
}

func (s *Session) invalidateIfPermissionDenied(report DoctorReport) {
	if !report.Capabilities.Accessibility || report.Permissions["accessibility"] == "denied" {
		s.invalidateSemanticState()
	}
}

func (s *Session) invalidateSemanticState() {
	s.mu.Lock()
	s.purgeSemanticStateLocked()
	s.mu.Unlock()
}

func (s *Session) purgeSemanticStateLocked() {
	s.permissionEpoch++
	s.clearSemanticStateLocked()
}

func (s *Session) clearSemanticStateLocked() {
	s.windows = make(map[string]Window)
	s.snapshots = make(map[string][]snapshotBinding)
	s.snapshotBytes = 0
	if s.ledger != nil {
		_ = s.ledger.SetRetainedBytes(0)
	}
}

func (s *Session) currentPermissionEpoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.permissionEpoch
}

// NewSession validates and copies configuration without making backend calls.
func NewSession(config Config) (*Session, error) {
	if config.Backend == nil {
		return nil, coreError("invalid_request")
	}
	mutationEnabled := config.WriterDirectory != "" || len(config.WriterKey) != 0 || config.MaxActions != 0 || config.ApprovalProvider != nil
	if mutationEnabled && (config.WriterDirectory == "" || len(config.WriterKey) != 32 || config.MaxActions < 1 || config.MaxActions > 4096 || config.ApprovalProvider == nil) {
		return nil, coreError("invalid_request")
	}
	now := time.Now()
	if err := validateScope(config.Scope, now); err != nil {
		return nil, err
	}
	if err := validateBudget(config.Budget); err != nil {
		return nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, coreError("internal_error")
	}
	config.Scope.Processes = append([]ProcessIdentity(nil), config.Scope.Processes...)
	config.WriterKey = append([]byte(nil), config.WriterKey...)
	return &Session{
		changed:          make(chan struct{}),
		ledger:           NewLedger(),
		acquireDesktop:   acquireDesktopAuthority,
		reserveQuota:     reserveDesktopQuota,
		journalBinding:   writer.DesktopJournalBinding,
		backend:          config.Backend,
		scope:            config.Scope,
		budget:           config.Budget,
		allowValues:      config.AllowValues,
		approvalProvider: config.ApprovalProvider,
		writerDirectory:  config.WriterDirectory,
		writerKey:        config.WriterKey,
		maxActions:       config.MaxActions,
		mutationEnabled:  mutationEnabled,
		sessionID:        hex.EncodeToString(id[:]),
		now:              time.Now,
		windows:          make(map[string]Window),
		snapshots:        make(map[string][]snapshotBinding),
		usedApprovals:    make(map[string]struct{}),
	}, nil
}

func (s *Session) enter(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if s == nil || ctx == nil {
		return nil, nil, coreError("invalid_request")
	}
	s.mu.Lock()
	if s.closed || s.closing {
		s.mu.Unlock()
		return nil, nil, coreError("session_closed")
	}
	if !s.now().Before(s.scope.ExpiresAt) {
		s.purgeSemanticStateLocked()
		s.mu.Unlock()
		return nil, nil, coreError("state_expired")
	}
	s.active++
	s.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, s.budget.Timeout)
	return callCtx, func() {
		cancel()
		s.mu.Lock()
		s.active--
		close(s.changed)
		s.changed = make(chan struct{})
		s.mu.Unlock()
	}, nil
}

// Close blocks new calls, waits for active calls, closes the backend, then
// settles quarantined writer leases. Failed closes retain ownership for retry.
func (s *Session) Close(ctx context.Context) error {
	if s == nil || ctx == nil {
		return coreError("invalid_request")
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.mu.Lock()
	if s.closed {
		terminalCloseCode := s.terminalCloseCode
		s.mu.Unlock()
		if terminalCloseCode != "" {
			return coreError(terminalCloseCode)
		}
		return nil
	}
	s.closing = true
	for s.active > 0 {
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return coreError("cancelled")
		}
		s.mu.Lock()
	}
	// Once active calls have drained, invalidate all public and private native
	// references even if backend close must be retried.
	s.purgeSemanticStateLocked()
	backend := s.backend
	backendClosed := s.backendClosed
	s.mu.Unlock()
	if !backendClosed {
		if err := backend.Close(ctx); err != nil {
			return stableContextError(ctx, err)
		}
		s.mu.Lock()
		s.backendClosed = true
		s.mu.Unlock()
	}
	s.mu.Lock()
	leases := append([]*writer.Lease(nil), s.quarantined...)
	s.mu.Unlock()
	var closeErr error
	terminalCloseCode := ""
	remaining := make([]*writer.Lease, 0, len(leases))
	for _, lease := range leases {
		if err := lease.Close(ctx); err != nil {
			if errors.Is(err, writer.ErrLeaseRetained) {
				remaining = append(remaining, lease)
				closeErr = errors.Join(closeErr, err)
				continue
			}
			if errors.Is(err, writer.ErrDirty) {
				terminalCloseCode = "unknown_outcome"
				continue
			}
			closeErr = errors.Join(closeErr, err)
		}
	}
	// The backend has drained before canonical desktop locks can be released.
	s.mu.Lock()
	desktops := append([]desktopAuthority(nil), s.quarantinedDesktops...)
	s.mu.Unlock()
	remainingDesktops := make([]desktopAuthority, 0, len(desktops))
	for _, desktop := range desktops {
		if len(remaining) > 0 {
			remainingDesktops = append(remainingDesktops, desktop)
			continue
		}
		if err := desktop.Close(); err != nil {
			remainingDesktops = append(remainingDesktops, desktop)
			closeErr = errors.Join(closeErr, err)
		}
	}
	s.mu.Lock()
	s.quarantinedDesktops = remainingDesktops
	s.quarantined = remaining
	if closeErr == nil && len(remaining) == 0 && len(remainingDesktops) == 0 {
		s.closed = true
		s.backend = nil
		s.terminalCloseCode = terminalCloseCode
		for i := range s.writerKey {
			s.writerKey[i] = 0
		}
		s.writerKey = nil
		s.writerDirectory = ""
		s.approvalProvider = nil
		s.sessionID = ""
	}
	s.mu.Unlock()
	if closeErr != nil {
		return stableContextError(ctx, closeErr)
	}
	if terminalCloseCode != "" {
		return coreError(terminalCloseCode)
	}
	return nil
}
