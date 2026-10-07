# ADR 0003: Phase 1 parity integration

Status: accepted source decision; independent review and integrated merge pending.

## Trusted selectors

Condition waits keep the frozen five-condition enum. Selected attributes can be
retained as nullable canonical node data without introducing a selected wait.
Condition results keep the frozen WaitResult shape and null observation.
ProcessRef accepts only a configured process identity, returns a session-bound
opaque SHA256 reference, and refuses foreign, expired, or closed scopes.
JSON selectors resolve against those authorized identities; they do not grant
process authority.

## Immutable history

Byte accounting must not mutate retained topology. Charge exact canonical
public snapshot bytes plus fixed private binding charges, including window
and scope bindings. Native identifiers and hidden values are never measured.
Retired state identities have bounded hash commitments. Once an expired or
evicted identical hash would reappear, or the commitment budget is exhausted,
the owner must rotate the trusted semantic scope generation before purging and
publishing anew. This keeps stored timestamps and action sequences original
without unbounded tombstones. Public E3 integration waits for T2.29.

## Desktop persistence

Journal and desktop exclusion use that lock order everywhere. Canonical intent
persists before input and binds the protected journal and key. Trusted recovery
requires a present exact intent, closed/drained prior backends, and a host cleanup
verifier. Successful durable journal reconciliation and journal close precede
canonical intent removal. No model, CLI, or MCP recovery route is exposed.
Replaceable journal ancestors are refused; root-owned sticky temporary parents
are allowed. Fixture-owned directories are explicitly protected.

A canonical completion persistence fault after durable native settlement
preserves the native execution and cleanup result. The call returns an
operational failure and retains canonical exclusion until backend drain.
Storage failure cannot fabricate uncertain native cleanup.

## Accounting and qualification

New well-formed mutation admission attempts count once, including refusal,
failure, and cancellation. Exact durable replay counts no new action or native
read. Actual native execution advances action_sequence independently.
Default native input stays disabled. Passing Go, Swift, and synthetic protocol
fixtures proves source behavior, not real GUI acceptance or release readiness.
Native display/focus producer and the action inventory are implemented and
pass controlled source fixtures. Live display/focus and action behavior still
require separate controlled runtime qualification.

## Inspected desktop context

Trusted optional backend context carries only an opaque runtime display identity,
a positive observed generation and scoped focused window or inspected no-focus
null. Missing or unreadable facts remain unavailable. Display topology, primary
display, bounds and scale changes rotate generation and purge private native
references; common semantic scope also binds this authority. No request field
grants it, no unlocked state is inferred, and default production input stays false.
