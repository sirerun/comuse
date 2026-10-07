package comuse

import "context"

type accountingContextKey struct{}

// account records actual attempts in the current call when one exists; direct
// typed API calls still advance the owning session's cumulative ledger.
func (s *Session) account(ctx context.Context, counter Counter, amount uint64) {
	if s == nil || s.ledger == nil {
		return
	}
	if call, ok := ctx.Value(accountingContextKey{}).(*CallSnapshot); ok {
		_, _ = call.Add(counter, amount)
	} else {
		_, _ = s.ledger.Add(counter, amount)
	}
}
