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
