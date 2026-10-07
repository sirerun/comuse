package comuse

import (
	"context"
	"testing"
	"time"
)

func TestProcessRefRequiresCurrentScopedHostIdentity(t *testing.T) {
	b := &fakeBackend{process: testProcess()}
	config := Config{Backend: b, Scope: Scope{Processes: []ProcessIdentity{b.process}, ExpiresAt: time.Now().Add(time.Hour)}, Budget: testBudget()}
	s, err := NewSession(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	identity := config.Scope.Processes[0]
	first, err := s.ProcessRef(identity)
	if err != nil || !stateIdentity(first) {
		t.Fatalf("reference=%q err=%v", first, err)
	}
	same, err := s.ProcessRef(identity)
	if err != nil || same != first {
		t.Fatal("unstable process reference")
	}
	identity.LaunchID = "foreign"
	if _, err := s.ProcessRef(identity); ErrorCode(err) != "policy_refused" {
		t.Fatalf("foreign identity err=%v", err)
	}
	s.now = func() time.Time { return config.Scope.ExpiresAt.Add(time.Second) }
	if _, err := s.ProcessRef(config.Scope.Processes[0]); ErrorCode(err) != "state_expired" {
		t.Fatalf("expired scope err=%v", err)
	}
}
