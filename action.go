package comuse

import (
	"context"
	"github.com/sirerun/comuse/internal/backend"
	"time"
)

type Action = backend.Action
type ActionResult = backend.ActionResult

// ApprovalProvider belongs to trusted host code and is never exposed as a tool.
type ApprovalProvider interface {
	Approve(context.Context, ApprovalRequest) (Approval, error)
}
type ApprovalRequest struct {
	SessionID     string
	Action        Action
	Process       ProcessIdentity
	ObservedAt    time.Time
	PolicyVersion uint64
	ExpiresAt     time.Time
}
type Approval struct {
	SessionID     string
	Action        Action
	Process       ProcessIdentity
	ObservedAt    time.Time
	PolicyVersion uint64
	ExpiresAt     time.Time
}

type ExecutionStatus = backend.ExecutionStatus
type VerificationStatus = backend.VerificationStatus
type Verification = backend.Verification
type StateStatus = backend.StateStatus
type CleanupStatus = backend.CleanupStatus

const (
	ExecutionNotApplied       = backend.ExecutionNotApplied
	ExecutionApplied          = backend.ExecutionApplied
	ExecutionPartiallyApplied = backend.ExecutionPartiallyApplied
	ExecutionUnknown          = backend.ExecutionUnknown
	VerificationVerified      = backend.VerificationVerified
	VerificationFailed        = backend.VerificationFailed
	VerificationUnavailable   = backend.VerificationUnavailable
	StateAvailable            = backend.StateAvailable
	StateUnavailable          = backend.StateUnavailable
	CleanupComplete           = backend.CleanupComplete
	CleanupDirty              = backend.CleanupDirty
	CleanupUnknown            = backend.CleanupUnknown
	ActionPress               = backend.ActionPress
	ActionReplace             = backend.ActionReplace
	ActionInsert              = backend.ActionInsert
)
