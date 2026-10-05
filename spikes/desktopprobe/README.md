# Desktop writer lease spike

This package is a standard-library-only admission and replay boundary for a
trusted desktop host. It does not emit events, call Accessibility, or implement
desktop actions.

## Writer exclusion and replay

`Acquire(ctx, root)` opens a private absolute state directory and holds a
nonblocking OS `flock` on `.writer.lock` for the lease lifetime. Only Darwin and
Linux have a lock implementation; other platforms return
`ErrUnsupportedLock`. Keep the root on a local filesystem whose `flock` and
atomic rename semantics are reliable. The caller owns and supplies this trusted
host path.
All GUI writer entry points for one host must receive this same state root;
separate roots intentionally create separate lock domains and do not exclude
one another.

`Begin(actionID, commitment)` writes `inflight` to a size bounded JSON ledger and
syncs it before returning a one-use ticket. Callers invoke their own writer only
after receiving that ticket, then record the result with `Finish`. A repeated
ID and matching commitment returns the recorded outcome without a ticket; a
changed commitment returns `ErrBindingMismatch`. An in-flight action on restart
becomes `unknown`, and unknown never gets a new ticket.

The caller should pass a keyed HMAC commitment from `BindingCommitment`, using
a private key and opaque binding bytes. The ledger stores the action ID, keyed
commitment, terminal outcome, and timestamps. It never stores raw action text
or a digest of a bare secret value. Terminal records older than seven days are
compacted to bounded tombstones. Tombstones are retained until capacity is
reached; new actions fail closed when the bounded ledger cannot preserve their
replay identity.

## Held input and dirty recovery

Before a host begins holding an input, it calls `RegisterHeldInput` with a
stable ID and an injected cleanup callback. `Release` or `Close(ctx)` runs that
callback and records `complete` or `dirty`. Cleanup callbacks must honor the
provided context. If the process exits with a persisted `held` record, the next
`Acquire` marks it `unknown` and makes the lease dirty. Writes stay blocked
until `ReconcileDirty(ctx, reason, verify)` calls the trusted host verifier and
that verifier confirms there is no outstanding held input. A reason string by
itself cannot clear dirty state.

This is state-machine evidence only. A successful mock cleanup callback proves
that the callback path ran; it does not prove a native key or button was
released.
