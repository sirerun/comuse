# Input probe source boundary

This package is a source-only fixture feasibility path. It is not wired to CLI
or MCP. The Swift native input handler remains compile-closed by
`nativeInputActionsEnabled = false`; no live input qualification is claimed.

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
