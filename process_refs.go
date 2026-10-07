package comuse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ProcessRef returns a session-bound reference for an already authorized
// process identity. It grants no authority to extend the session's scope.
func (s *Session) ProcessRef(identity ProcessIdentity) (string, error) {
	_, done, err := s.enter(context.Background())
	if err != nil {
		return "", err
	}
	defer done()
	if !s.now().Before(s.scope.ExpiresAt) || !scopeContains(s.scope, identity) {
		return "", coreError("policy_refused")
	}
	s.mu.Lock()
	sessionID := s.sessionID
	s.mu.Unlock()
	data, err := json.Marshal(identity)
	if err != nil {
		return "", coreError("internal_error")
	}
	sum := sha256.Sum256(append([]byte("comuse-process-ref-v1\x00"+sessionID+"\x00"), data...))
	return hex.EncodeToString(sum[:]), nil
}
