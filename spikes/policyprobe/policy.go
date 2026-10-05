// Package policyprobe provides an experimental, in-process approval gate for
// fixture-scoped semantic actions. It is not a production security boundary.
// The host must keep HostAuthority out of model-facing APIs and must freshly
// classify each native target; TargetKind supplied here is not authoritative.
package policyprobe

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"sync"
	"time"
)

const PolicyVersion uint64 = 1

const (
	maxChallengeTTL = 2 * time.Minute
	maxTextBytes    = 4096
	maxScroll       = 1000
	maxChallenges   = 128
	maxApprovals    = 128
	maxConsumed     = 256
	maxAuditEvents  = 512
)

var (
	ErrNilClock          = errors.New("policy clock is required")
	ErrInvalidScope      = errors.New("invalid fixture scope")
	ErrExpiredScope      = errors.New("fixture scope expired")
	ErrPolicyVersion     = errors.New("unsupported policy version")
	ErrInvalidTTL        = errors.New("approval challenge lifetime is invalid")
	ErrInvalidAction     = errors.New("action is not supported by the fixture policy")
	ErrProtectedTarget   = errors.New("protected target is denied")
	ErrCapacity          = errors.New("policy capacity reached")
	ErrChallengeNotFound = errors.New("approval challenge is unavailable")
	ErrApprovalNotFound  = errors.New("approval capability is unavailable")
)

type Clock func() time.Time

type ProcessIdentity struct {
	PID              int32
	BundleID         string
	LaunchGeneration string
}

// Scope is supplied by trusted host code, never decoded directly from a model
// request. WindowTitle is display-only and is deliberately excluded from the
// capability identity; the opaque WindowRef is the bound window identity.
type Scope struct {
	PrincipalID   string
	SessionID     string
	Process       ProcessIdentity
	FixtureNonce  string
	StateID       string
	WindowRef     string
	WindowTitle   string
	ElementRef    string
	PolicyVersion uint64
	ExpiresAt     time.Time
}

type ActionKind uint8

const (
	ActionReadSemantics ActionKind = iota + 1
	ActionReadValue
	ActionPress
	ActionReplaceText
	ActionScroll
)

func (kind ActionKind) String() string {
	switch kind {
	case ActionReadSemantics:
		return "read_semantics"
	case ActionReadValue:
		return "read_value"
	case ActionPress:
		return "press"
	case ActionReplaceText:
		return "replace_text"
	case ActionScroll:
		return "scroll"
	default:
		return "unknown"
	}
}

type TargetKind uint8

const (
	TargetSemantic TargetKind = iota + 1
	TargetButton
	TargetTextField
	TargetSecureField
	TargetScrollRegion
)

// Action is a typed request. Text is transient input only: the gate never
// stores, returns, logs, or audits it. The host must freshly derive TargetKind
// and ElementRef from a native identity check before calling Issue or Admit.
type Action struct {
	Kind       ActionKind
	Target     TargetKind
	ElementRef string
	Text       string
	ScrollX    int32
	ScrollY    int32
}

type Challenge struct {
	ID          string
	Action      ActionKind
	TargetRef   string
	WindowTitle string
	ExpiresAt   time.Time
}

// Approval is an opaque capability that cannot be reconstructed from JSON.
// Its zero value denies.
type Approval struct{ token [32]byte }

type DecisionCode uint8

const (
	DecisionAllowed DecisionCode = iota + 1
	DecisionDeniedMissingApproval
	DecisionDeniedInvalidScope
	DecisionDeniedExpired
	DecisionDeniedPolicyVersion
	DecisionDeniedProtectedTarget
	DecisionDeniedBindingMismatch
	DecisionDeniedRevoked
	DecisionDeniedReplay
	DecisionDeniedCapacity
	DecisionDeniedInvalidAction
)

func (code DecisionCode) String() string {
	switch code {
	case DecisionAllowed:
		return "allowed"
	case DecisionDeniedMissingApproval:
		return "missing_approval"
	case DecisionDeniedInvalidScope:
		return "invalid_scope"
	case DecisionDeniedExpired:
		return "expired"
	case DecisionDeniedPolicyVersion:
		return "policy_version_mismatch"
	case DecisionDeniedProtectedTarget:
		return "protected_target"
	case DecisionDeniedBindingMismatch:
		return "binding_mismatch"
	case DecisionDeniedRevoked:
		return "revoked"
	case DecisionDeniedReplay:
		return "replay"
	case DecisionDeniedCapacity:
		return "capacity"
	case DecisionDeniedInvalidAction:
		return "invalid_action"
	default:
		return "unknown"
	}
}

type Decision struct {
	Code          DecisionCode
	PolicyVersion uint64
	At            time.Time
}

// AuditEvent contains only low-risk policy metadata. It excludes payload text,
// action digests, challenge IDs, approval tokens, and native addresses.
type AuditEvent struct {
	Code          DecisionCode
	Action        ActionKind
	PolicyVersion uint64
	At            time.Time
}

type scopeIdentity struct {
	PrincipalID   string
	SessionID     string
	Process       ProcessIdentity
	FixtureNonce  string
	StateID       string
	WindowRef     string
	ElementRef    string
	PolicyVersion uint64
	ScopeExpiryNS int64
}

type challengeRecord struct {
	scope      scopeIdentity
	bindingMAC [32]byte
	action     ActionKind
	targetRef  string
	expiresAt  time.Time
}

type approvalRecord struct {
	scope      scopeIdentity
	bindingMAC [32]byte
	action     ActionKind
	expiresAt  time.Time
}

type Gate struct {
	mu         sync.Mutex
	clock      Clock
	secret     [32]byte
	challenges map[string]challengeRecord
	approvals  map[[32]byte]approvalRecord
	consumed   map[[32]byte]time.Time
	expired    map[[32]byte]time.Time
	audit      []AuditEvent
}

// HostAuthority is the separate capability issuer returned only to trusted
// host wiring. Do not expose it to model prompts, tool schemas, or JSON input.
type HostAuthority struct{ gate *Gate }

func New(clock Clock) (*Gate, *HostAuthority, error) {
	if clock == nil {
		return nil, nil, ErrNilClock
	}
	gate := &Gate{
		clock:      clock,
		challenges: make(map[string]challengeRecord),
		approvals:  make(map[[32]byte]approvalRecord),
		consumed:   make(map[[32]byte]time.Time),
		expired:    make(map[[32]byte]time.Time),
	}
	if _, err := rand.Read(gate.secret[:]); err != nil {
		return nil, nil, errors.New("creating policy capability key")
	}
	return gate, &HostAuthority{gate: gate}, nil
}

func (host *HostAuthority) Issue(scope Scope, action Action, ttl time.Duration) (Challenge, error) {
	if host == nil || host.gate == nil {
		return Challenge{}, ErrChallengeNotFound
	}
	gate := host.gate
	now := gate.clock().UTC()
	identity, err := makeScopeIdentity(scope)
	if err != nil {
		return Challenge{}, err
	}
	if identity.PolicyVersion != PolicyVersion {
		return Challenge{}, ErrPolicyVersion
	}
	if !now.Before(scope.ExpiresAt) {
		return Challenge{}, ErrExpiredScope
	}
	if ttl <= 0 || ttl > maxChallengeTTL {
		return Challenge{}, ErrInvalidTTL
	}
	kind, targetRef, actionDigest, err := normalizeAction(action)
	if err != nil {
		return Challenge{}, err
	}
	if targetRef != scope.ElementRef {
		return Challenge{}, ErrInvalidAction
	}
	expiresAt := now.Add(ttl)
	if scope.ExpiresAt.Before(expiresAt) {
		expiresAt = scope.ExpiresAt
	}
	binding := gate.bindingMAC(identity, actionDigest)
	idBytes, err := randomToken()
	if err != nil {
		return Challenge{}, errors.New("creating approval challenge")
	}
	id := hex.EncodeToString(idBytes[:16])
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.cleanupLocked(now)
	if len(gate.challenges) >= maxChallenges {
		return Challenge{}, ErrCapacity
	}
	if _, exists := gate.challenges[id]; exists {
		return Challenge{}, errors.New("approval challenge collision")
	}
	gate.challenges[id] = challengeRecord{
		scope: identity, bindingMAC: binding, action: kind, targetRef: targetRef, expiresAt: expiresAt,
	}
	return Challenge{ID: id, Action: kind, TargetRef: targetRef, WindowTitle: scope.WindowTitle, ExpiresAt: expiresAt}, nil
}

// ApproveHost must be invoked only by the separately trusted host confirmation
// path after a person approves the displayed challenge. It is not a model API.
func (host *HostAuthority) ApproveHost(challengeID string) (Approval, error) {
	if host == nil || host.gate == nil || challengeID == "" {
		return Approval{}, ErrChallengeNotFound
	}
	gate := host.gate
	now := gate.clock().UTC()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.cleanupLocked(now)
	challenge, exists := gate.challenges[challengeID]
	if !exists {
		return Approval{}, ErrChallengeNotFound
	}
	if !now.Before(challenge.expiresAt) {
		delete(gate.challenges, challengeID)
		gate.recordLocked(DecisionDeniedExpired, challenge.action, challenge.scope.PolicyVersion, now)
		return Approval{}, ErrChallengeNotFound
	}
	if len(gate.approvals) >= maxApprovals {
		return Approval{}, ErrCapacity
	}
	token, err := randomToken()
	if err != nil {
		return Approval{}, errors.New("creating approval capability")
	}
	delete(gate.challenges, challengeID)
	gate.approvals[token] = approvalRecord{
		scope: challenge.scope, bindingMAC: challenge.bindingMAC,
		action: challenge.action, expiresAt: challenge.expiresAt,
	}
	return Approval{token: token}, nil
}

func (host *HostAuthority) Revoke(approval Approval) bool {
	if host == nil || host.gate == nil || approval.token == ([32]byte{}) {
		return false
	}
	gate := host.gate
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if _, exists := gate.approvals[approval.token]; !exists {
		return false
	}
	delete(gate.approvals, approval.token)
	return true
}

// RevokeScope invalidates outstanding challenges and approvals for one exact
// bound element scope. It does not reverse an action already admitted.
func (host *HostAuthority) RevokeScope(scope Scope) int {
	if host == nil || host.gate == nil {
		return 0
	}
	identity, err := makeScopeIdentity(scope)
	if err != nil {
		return 0
	}
	return host.gate.revokeMatching(func(candidate scopeIdentity) bool { return candidate == identity })
}

// RevokeSession invalidates every outstanding grant for this principal/session.
func (host *HostAuthority) RevokeSession(principalID, sessionID string) int {
	if host == nil || host.gate == nil || principalID == "" || sessionID == "" {
		return 0
	}
	return host.gate.revokeMatching(func(candidate scopeIdentity) bool {
		return candidate.PrincipalID == principalID && candidate.SessionID == sessionID
	})
}

func (gate *Gate) Admit(scope Scope, action Action, approval Approval) Decision {
	if gate == nil || gate.clock == nil {
		return Decision{Code: DecisionDeniedInvalidScope, PolicyVersion: scope.PolicyVersion}
	}
	now := gate.clock().UTC()
	identity, err := makeScopeIdentity(scope)
	if err != nil {
		if errors.Is(err, ErrPolicyVersion) {
			return gate.decision(DecisionDeniedPolicyVersion, action.Kind, scope.PolicyVersion, now)
		}
		return gate.decision(DecisionDeniedInvalidScope, ActionKind(0), scope.PolicyVersion, now)
	}
	if identity.PolicyVersion != PolicyVersion {
		return gate.decision(DecisionDeniedPolicyVersion, action.Kind, identity.PolicyVersion, now)
	}
	if !now.Before(scope.ExpiresAt) {
		return gate.decision(DecisionDeniedExpired, action.Kind, identity.PolicyVersion, now)
	}
	if approval.token == ([32]byte{}) {
		return gate.decision(DecisionDeniedMissingApproval, action.Kind, identity.PolicyVersion, now)
	}

	gate.mu.Lock()
	gate.cleanupLocked(now)
	record, exists := gate.approvals[approval.token]
	if !exists {
		_, replayed := gate.consumed[approval.token]
		_, expired := gate.expired[approval.token]
		gate.mu.Unlock()
		if replayed {
			return gate.decision(DecisionDeniedReplay, action.Kind, identity.PolicyVersion, now)
		}
		if expired {
			return gate.decision(DecisionDeniedExpired, action.Kind, identity.PolicyVersion, now)
		}
		return gate.decision(DecisionDeniedRevoked, action.Kind, identity.PolicyVersion, now)
	}
	if !now.Before(record.expiresAt) {
		delete(gate.approvals, approval.token)
		gate.mu.Unlock()
		return gate.decision(DecisionDeniedExpired, action.Kind, identity.PolicyVersion, now)
	}
	if record.scope != identity {
		gate.mu.Unlock()
		return gate.decision(DecisionDeniedBindingMismatch, action.Kind, identity.PolicyVersion, now)
	}
	gate.mu.Unlock()

	kind, _, actionDigest, err := normalizeAction(action)
	if err != nil {
		if errors.Is(err, ErrProtectedTarget) {
			return gate.decision(DecisionDeniedProtectedTarget, action.Kind, identity.PolicyVersion, now)
		}
		return gate.decision(DecisionDeniedInvalidAction, action.Kind, identity.PolicyVersion, now)
	}
	if action.ElementRef != scope.ElementRef {
		return gate.decision(DecisionDeniedBindingMismatch, action.Kind, identity.PolicyVersion, now)
	}
	binding := gate.bindingMAC(identity, actionDigest)
	if kind != record.action || !hmac.Equal(binding[:], record.bindingMAC[:]) {
		return gate.decision(DecisionDeniedBindingMismatch, action.Kind, identity.PolicyVersion, now)
	}

	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.cleanupLocked(now)
	record, exists = gate.approvals[approval.token]
	if !exists {
		if _, replayed := gate.consumed[approval.token]; replayed {
			return gate.recordDecisionLocked(DecisionDeniedReplay, action.Kind, identity.PolicyVersion, now)
		}
		if _, expired := gate.expired[approval.token]; expired {
			return gate.recordDecisionLocked(DecisionDeniedExpired, action.Kind, identity.PolicyVersion, now)
		}
		return gate.recordDecisionLocked(DecisionDeniedRevoked, action.Kind, identity.PolicyVersion, now)
	}
	if !now.Before(record.expiresAt) {
		delete(gate.approvals, approval.token)
		return gate.recordDecisionLocked(DecisionDeniedExpired, action.Kind, identity.PolicyVersion, now)
	}
	binding = gate.bindingMAC(identity, actionDigest)
	if record.scope != identity || kind != record.action || !hmac.Equal(binding[:], record.bindingMAC[:]) {
		return gate.recordDecisionLocked(DecisionDeniedBindingMismatch, action.Kind, identity.PolicyVersion, now)
	}
	if len(gate.consumed) >= maxConsumed {
		return gate.recordDecisionLocked(DecisionDeniedCapacity, action.Kind, identity.PolicyVersion, now)
	}
	delete(gate.approvals, approval.token)
	gate.consumed[approval.token] = record.expiresAt
	return gate.recordDecisionLocked(DecisionAllowed, action.Kind, identity.PolicyVersion, now)
}

func (gate *Gate) AuditSnapshot() []AuditEvent {
	if gate == nil {
		return nil
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return append([]AuditEvent(nil), gate.audit...)
}

func (gate *Gate) decision(code DecisionCode, action ActionKind, version uint64, at time.Time) Decision {
	if gate == nil {
		return Decision{Code: code, PolicyVersion: version, At: at}
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.recordDecisionLocked(code, action, version, at)
}

func (gate *Gate) recordDecisionLocked(code DecisionCode, action ActionKind, version uint64, at time.Time) Decision {
	decision := Decision{Code: code, PolicyVersion: version, At: at}
	if len(gate.audit) == maxAuditEvents {
		copy(gate.audit, gate.audit[1:])
		gate.audit = gate.audit[:len(gate.audit)-1]
	}
	gate.audit = append(gate.audit, AuditEvent{Code: code, Action: action, PolicyVersion: version, At: at})
	return decision
}

func (gate *Gate) recordLocked(code DecisionCode, action ActionKind, version uint64, at time.Time) {
	gate.recordDecisionLocked(code, action, version, at)
}

func (gate *Gate) bindingMAC(scope scopeIdentity, actionDigest [32]byte) [32]byte {
	mac := hmac.New(sha256.New, gate.secret[:])
	writeScope(mac, scope)
	_, _ = mac.Write(actionDigest[:])
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}

func (gate *Gate) revokeMatching(matches func(scopeIdentity) bool) int {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	removed := 0
	for id, challenge := range gate.challenges {
		if matches(challenge.scope) {
			delete(gate.challenges, id)
			removed++
		}
	}
	for token, approval := range gate.approvals {
		if matches(approval.scope) {
			delete(gate.approvals, token)
			removed++
		}
	}
	return removed
}

func (gate *Gate) cleanupLocked(now time.Time) {
	for id, challenge := range gate.challenges {
		if !now.Before(challenge.expiresAt) {
			delete(gate.challenges, id)
		}
	}
	for token, approval := range gate.approvals {
		if !now.Before(approval.expiresAt) {
			if len(gate.expired) < maxConsumed {
				gate.expired[token] = now.Add(maxChallengeTTL)
			}
			delete(gate.approvals, token)
		}
	}
	for token, expiry := range gate.consumed {
		if !now.Before(expiry) {
			delete(gate.consumed, token)
		}
	}
	for token, expiry := range gate.expired {
		if !now.Before(expiry) {
			delete(gate.expired, token)
		}
	}
}

func makeScopeIdentity(scope Scope) (scopeIdentity, error) {
	if scope.PolicyVersion != PolicyVersion {
		return scopeIdentity{}, ErrPolicyVersion
	}
	if scope.PrincipalID == "" || len(scope.PrincipalID) > 128 ||
		scope.SessionID == "" || len(scope.SessionID) > 128 ||
		scope.Process.PID <= 0 || scope.Process.BundleID == "" || len(scope.Process.BundleID) > 256 ||
		scope.Process.LaunchGeneration == "" || len(scope.Process.LaunchGeneration) > 128 ||
		scope.FixtureNonce == "" || len(scope.FixtureNonce) > 64 ||
		scope.StateID == "" || len(scope.StateID) > 128 ||
		scope.WindowRef == "" || len(scope.WindowRef) > 256 ||
		scope.WindowTitle == "" || len(scope.WindowTitle) > 256 ||
		scope.ElementRef == "" || len(scope.ElementRef) > 256 || scope.ExpiresAt.IsZero() {
		return scopeIdentity{}, ErrInvalidScope
	}
	return scopeIdentity{
		PrincipalID: scope.PrincipalID, SessionID: scope.SessionID, Process: scope.Process,
		FixtureNonce: scope.FixtureNonce, StateID: scope.StateID, WindowRef: scope.WindowRef, ElementRef: scope.ElementRef,
		PolicyVersion: scope.PolicyVersion, ScopeExpiryNS: scope.ExpiresAt.UTC().UnixNano(),
	}, nil
}

func normalizeAction(action Action) (ActionKind, string, [32]byte, error) {
	if action.Target == TargetSecureField {
		return 0, "", [32]byte{}, ErrProtectedTarget
	}
	if action.ElementRef == "" || len(action.ElementRef) > 256 {
		return 0, "", [32]byte{}, ErrInvalidAction
	}
	valid := false
	switch action.Kind {
	case ActionReadSemantics:
		valid = knownTarget(action.Target) && action.Text == "" && action.ScrollX == 0 && action.ScrollY == 0
	case ActionReadValue:
		valid = action.Target == TargetTextField && action.Text == "" && action.ScrollX == 0 && action.ScrollY == 0
	case ActionPress:
		valid = action.Target == TargetButton && action.Text == "" && action.ScrollX == 0 && action.ScrollY == 0
	case ActionReplaceText:
		valid = action.Target == TargetTextField && len(action.Text) <= maxTextBytes && action.ScrollX == 0 && action.ScrollY == 0
	case ActionScroll:
		valid = action.Target == TargetScrollRegion && action.Text == "" &&
			action.ScrollX >= -maxScroll && action.ScrollX <= maxScroll &&
			action.ScrollY >= -maxScroll && action.ScrollY <= maxScroll
	}
	if !valid {
		return 0, "", [32]byte{}, ErrInvalidAction
	}
	return action.Kind, action.ElementRef, digestAction(action), nil
}

func knownTarget(target TargetKind) bool {
	switch target {
	case TargetSemantic, TargetButton, TargetTextField, TargetScrollRegion:
		return true
	default:
		return false
	}
}

func digestAction(action Action) [32]byte {
	digest := sha256.New()
	writeString(digest, "comuse-fixture-action-v1")
	writeUint64(digest, uint64(action.Kind))
	writeUint64(digest, uint64(action.Target))
	writeString(digest, action.ElementRef)
	writeString(digest, action.Text)
	writeUint64(digest, uint64(int64(action.ScrollX)))
	writeUint64(digest, uint64(int64(action.ScrollY)))
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func writeScope(writer hash.Hash, scope scopeIdentity) {
	writeString(writer, "comuse-fixture-scope-v1")
	writeString(writer, scope.PrincipalID)
	writeString(writer, scope.SessionID)
	writeUint64(writer, uint64(scope.Process.PID))
	writeString(writer, scope.Process.BundleID)
	writeString(writer, scope.Process.LaunchGeneration)
	writeString(writer, scope.FixtureNonce)
	writeString(writer, scope.StateID)
	writeString(writer, scope.WindowRef)
	writeString(writer, scope.ElementRef)
	writeUint64(writer, scope.PolicyVersion)
	writeUint64(writer, uint64(scope.ScopeExpiryNS))
}

func writeString(writer hash.Hash, value string) {
	writeUint64(writer, uint64(len(value)))
	_, _ = writer.Write([]byte(value))
}

func writeUint64(writer hash.Hash, value uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], value)
	_, _ = writer.Write(buffer[:])
}

func randomToken() ([32]byte, error) {
	var token [32]byte
	_, err := rand.Read(token[:])
	return token, err
}
