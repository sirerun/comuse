package comuse

import (
	"context"
	"testing"
)

type closedPermissionBackend struct {
	*fakeBackend
	revoked bool
}

func (b *closedPermissionBackend) Windows(ctx context.Context, budget Budget) ([]Window, error) {
	if b.revoked {
		return []Window{}, nil
	}
	return b.fakeBackend.Windows(ctx, budget)
}
func (b *closedPermissionBackend) Doctor(ctx context.Context) (DoctorReport, error) {
	if b.revoked {
		return DoctorReport{Capabilities: Capabilities{Accessibility: false}, Permissions: map[string]string{"accessibility": "denied"}}, nil
	}
	return b.fakeBackend.Doctor(ctx)
}
func TestWindowClosedRefusesEmptyEnumerationAfterPermissionRevocation(t *testing.T) {
	b := &closedPermissionBackend{fakeBackend: &fakeBackend{process: testProcess(), nativeState: "permission-state", elements: testElements()}}
	s := newTestSession(t, b.fakeBackend, false)
	s.backend = b
	if _, err := s.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.revoked = true
	result, err := s.WaitCondition(context.Background(), WaitParams{Condition: "window_closed", WindowRef: "window-1", TimeoutMS: 500})
	if result.Satisfied || ErrorCode(err) != "permission_denied" || result.Reason != "unavailable" {
		t.Fatalf("permission revocation became closure: result=%+v code=%s", result, ErrorCode(err))
	}
	if s.currentPermissionEpoch() == 0 {
		t.Fatal("revocation did not purge semantic authority")
	}
}
