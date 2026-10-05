# Input probe source boundary

This package is an experimental fixture feasibility path. The dedicated
`cmd/fixtureacceptance` command runs one fixed, host-selected scenario and emits
bounded outcome metadata. Ordinary CLI and MCP expose readonly operations. The
Swift native input handler remains compile-closed by
`nativeInputActionsEnabled = false`; live input qualification remains open.

The typed bridge input transport requires an opaque capability type from
`spikes/internal/hostcap`. Go's `internal` import rule keeps that transport out
of external library consumers, while the private input composition keeps the
capability field unexported. This is a trusted in-process code boundary, not an
OS sandbox: other trusted Go code inside this module can import internal
packages. Do not expose a capability factory through CLI, MCP, or model input.

Enabling requires exact-head review of the integrated admission, writer lease,
durable replay journal, protected-target reclassification, native lifecycle,
and independent postcondition and cleanup handling. Until then, keep the native
guard disabled and the ordinary bridge API read-only.

The private durable factory does not issue approval. The acceptance facade may
issue only the predeclared approval for its selected synthetic-fixture scenario
after scope and protected-target checks. It accepts no arbitrary action or text.

The facade currently supports the dedicated command lifetime. On bounded native
close failure, the command reports held and exits. Persisted dirty/inflight
state blocks restart for uncertain or quarantined actions. A clean terminal may
already be durable and its writer released; process exit then ends the native
callback lifetime and restart is permitted. A long-lived embedding caller needs an
explicit retained-owner retry interface before this facade can support it.
