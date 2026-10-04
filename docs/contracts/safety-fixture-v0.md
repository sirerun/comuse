# Fixture-only safety contract proposal (v0)

**Status:** Proposed source contract for the E1 feasibility probes. Not a public
runtime guarantee. Adopt or amend only after the T1.5 seam is landed and frozen.

This contract is grounded in RFC 0001 v0.5, E1 T1.5–T1.19, and UC-001–UC-016.
It constrains the synthetic SwiftUI fixture with deterministic AppKit-backed AX controls and its test doubles. It grants no
authority over the user's desktop or real applications. A fixture pass proves
only the named fixture case.

## Fixture boundary and identity

- The only mutable target is the synthetic fixture process launched for the
  probe. The policy profile allowlists its signed/bundle identity and a
  per-launch fixture nonce. A title, PID alone, bundle name, or AX label is
  never authorization.
- A window reference binds session, fixture nonce, PID plus process-start
  identity, native window identity, and window generation. An element reference
  additionally binds its parent path/role, stable fixture key, and observed
  state ID. Native AX element handles are short-lived and never become model
  references.
- Resolve and validate identity immediately before each read or input step. A
  process restart, window recreation/movement that changes identity, element
  removal/reparenting, changed expected state, focus loss, or fixture nonce
  mismatch returns `element_stale`/`window_stale` or `policy_refused`; it never
  retargets by title or coordinates.
- Diagnostic geometry may exist in fixture-only diagnostics. It is absent from
  the model projection and is never accepted as an MCP or semantic CLI target.

## Redaction, observation, and retention

Redaction occurs before canonicalization, state-ID derivation, diff comparison,
cache admission, or serialization. Redaction rules are versioned and included
in `scope_id`.

- Secure/password values are never read into Go, compared, retained, logged, or
  returned. Emit `value_status: "protected"`; do not emit a placeholder that
  could be mistaken for a real value.
- Ordinary field values are omitted by default. Fixture policy may expose only
  explicitly allowlisted synthetic values, such as `fixture-note-1`; never use
  real user text. If a field cannot be classified, omit it and mark coverage
  partial with reason `classification_unknown`.
- A change only to a redacted/omitted value cannot change public `state_id`,
  `changed`, a delta, an element result, or an error string. Output errors and
  audit records contain references, typed reason codes, action IDs, timings,
  and cleanup state only, never text payloads or screen contents.
- Semantic-only fixture sessions must not call capture. Screenshots, crops,
  pixel waits, and image-based verification return `unsupported` under this
  profile.
- Per observation: depth 6, 256 nodes, 16 KiB serialized text, 500 ms AX
  traversal deadline. Report every limiting bound; timeout or AX uncertainty
  yields `partial`/`unavailable`, never an empty complete tree.
- Per session proposal: at most 8 retained canonical snapshots, 512 KiB total
  canonical bytes, 256 references, 60 second TTL, and 256 admitted action IDs.
  Charge limits before retention/admission. On cap, refuse the new admission or
  evict only expired/eligible observation bodies; never evict an action ID to
  make it replayable. Session close, permission loss, or policy revocation
  invalidates references and purges affected snapshots. These fixture values
  are proposed test bounds; runtime defaults remain open for T1.5/T1.16.

## Requests, approval, writer, and replay

The host owns policy, approval, budgets, and fixture identity. Tool arguments
cannot grant scope or approval. For any probe that exercises input, approval is
single-use and host-issued after normalization. It binds:

`principal + session + action_id + operation + fixture/window/element identity
+ expected_state_id + normalized-parameter digest + policy version + expiry`.

The model/client never receives an approval bearer value. Reject modified or
expired approval, a stale target/state, scope mismatch, and duplicate action ID
before native dispatch. A separate read-only path still passes the same scope,
identity, redaction, and quota checks. T1.18 MCP contract exchange remains
read-only; no fixture mutation tool is exposed to a normal model client.

One writer lease covers the OS-user/GUI desktop across Comuse processes. A
second writer returns `desktop_busy`; a Go mutex is insufficient. Composite
input is serialized and checks focus/target before every event. Record a dirty
ownership marker before posting held input. On success, error, cancellation,
permission loss, or close, issue matching key/button releases under a fresh,
bounded cleanup context. Clear the dirty marker only after cleanup succeeds.
Failure to release makes desktop state uncertain, retains/refuses writer
ownership, and requires explicit operator recovery. A new process finding a
dirty marker refuses mutation. A process crash is not proof that cleanup ran.

Action IDs are atomically admitted before native work and held through session
close. Reusing an ID with different normalized parameters is rejected. Reusing
an admitted ID never dispatches again: return the retained terminal result if
present; otherwise return `replay_result_expired`/`unknown`. Loss of the
post-action read or transport response does not authorize a retry. Do not claim
exactly-once behavior across crashes.

## Results and partial/unknown semantics

Use one shared envelope across library, CLI JSON, and MCP structured output.
Keep action dispatch, verification, observation, and cleanup separate:

```json
{
  "schema_version": "fixture.v0",
  "ok": false,
  "session_ref": "sess_…",
  "action_id": "act_…",
  "action": "replace",
  "execution": "partially_applied",
  "verification": {"status": "unavailable", "reason": "readback_denied"},
  "state_status": "unavailable",
  "cleanup": {"status": "released"},
  "error": {"code": "permission_denied"}
}
```

- `execution`: `not_applied` only when refusal is known to precede dispatch;
  `applied` when the requested dispatch completed; `partially_applied` when a
  known subset of steps was posted; `unknown` when dispatch may have occurred
  but completion cannot be established.
- `verification.status`: `verified`, `failed`, or `unavailable`. Dispatch
  success is not application success. Failed/unavailable verification never
  triggers automatic input retry or rollback.
- `state_status`: `available`, `partial`, or `unavailable`; a read failure
  after input preserves the execution value.
- Cleanup status: `released`, `not_required`, `failed`, or `unknown`; cleanup
  failure marks ownership dirty. Cancellation is a request to stop and clean up,
  not evidence that no input occurred.
- Coverage is `complete`, `partial`, or `unavailable`, with typed reasons and
  explicit truncation bounds. Partial traversal never implies deletion. Missing
  AX properties are unknown, not false/empty defaults.

Core error codes include `policy_refused`, `approval_required`, `window_stale`,
`element_stale`, `state_expired`, `permission_denied`, `unsupported`,
`backend_unavailable`, `desktop_busy`, `replay_result_expired`,
`rate_limited`, and `budget_exceeded`. Malformed inputs are validation errors;
they do not reach policy approval or native dispatch.

## CLI and MCP wire choices

- Library core remains the source of policy and result semantics. `comuse`
  JSON mode reads one request from stdin or arguments, writes exactly one JSON
  envelope to stdout, writes diagnostics only to stderr, and returns typed
  nonzero exit codes for request/policy/execution failure. One-shot references
  are session-bound and expire at process close. `doctor` and observation paths
  never inject input or request TCC prompts. No low-level coordinate command is
  exposed through the fixture's model-facing profile.
- MCP is stdio JSON-RPC only for this probe. Use the official Go SDK, with
  `github.com/modelcontextprotocol/go-sdk v1.8.0` as the research candidate
  observed 2026-10-04; do not treat that as a go.mod pin or compatibility
  result. Its official README describes `mcp.Server`, typed `AddTool` handlers,
  and stdin/stdout transport. It lists protocol `2026-07-28` as newest for
  v1.7.0+ and support for `2025-11-25`, `2025-06-18`, `2025-03-26`, and
  `2024-11-05`; T1.18 must verify exact negotiation for the selected release.
  [SDK release](https://github.com/modelcontextprotocol/go-sdk/releases),
  [SDK README](https://github.com/modelcontextprotocol/go-sdk/blob/main/README.md).
- Initial fixture tools are read-only `computer_state`, `computer_a11y`, and
  `computer_read_element`; the latter returns fresh element content and does
  not advance a snapshot baseline. No write/click/input tool is registered in
  the fixture model surface. Tool schemas accept opaque refs and budgets, not
  coordinates, arbitrary selectors, paths, or approval tokens. Prefer
  `structuredContent` with an output schema and do not duplicate identical JSON
  in text content. Invalid arguments use protocol invalid-params behavior;
  well-formed tool failures use `isError: true` plus the typed envelope.
- Context cancellation maps to MCP `notifications/cancelled`, but SDK v1.8.0
  sends this notification asynchronously after retiring the call. The server
  must keep native request/callback state alive until its own completion and
  cleanup handshake drains; a canceled client call does not prove the fixture
  stopped or that the writer lease is safe to release. [SDK cancellation
  contract](https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/protocol.md#cancellation).
- Capabilities are explicit. Do not register a tool whose backend/policy
  capability is unavailable. A stale cached call or direct call to a known but
  unavailable operation returns a typed `unsupported` result/error; never fake
  success. stdout is protocol-only; process logs go to stderr.

## Planned-use-case coverage

| Use case | Fixture contract coverage |
|---|---|
| UC-001 readiness | Permission/capability status only; no prompt or incidental input. |
| UC-002 window discovery | Authorized fixture identities and opaque session refs. |
| UC-003 semantic state | Bounded, geometry-free, redacted JSON plus coverage. |
| UC-004 element read | Fresh target validation; protected/unavailable content explicit; no baseline advance. |
| UC-005 replace | Dispatch distinct from readback verification; fixture policy/approval only. |
| UC-006 insert | Refuse unknown selection/caret; no clipboard fallback. |
| UC-007 press/scroll | Allow only fixture capabilities; target/focus revalidation and bounded operation. |
| UC-008 authority | Default-deny read/action scopes, approval binding, quotas. |
| UC-009 cancellation/recovery | Cross-process writer, cleanup, dirty marker, replay and partial/unknown. |
| UC-010 privacy | Redact before comparison/IDs/cache/output; revocation blocks history. |
| UC-011 CLI | stdout/stderr, JSON envelope, exit status, one-shot refs. |
| UC-012 MCP | stdio SDK negotiation/schema/cancellation; read-only model surface. |
| UC-013 diffs | Only compare complete same-scope redacted states; partial is not deletion. |
| UC-014 history | Return the original retained state or `state_expired`. |
| UC-015 baseline recovery | Bounded reset/checkpoint with reason, no unbounded state. |
| UC-016 interference | User/focus/tree changes invalidate target or report partial/unknown; no atomicity claim. |

UC-017–020 (app opening, image fallback, signed install, and cost/resource
measurement) are outside this fixture-only contract except that image fallback
is explicitly refused here. They remain planned and unqualified.

## T1.5 freeze and evidence boundary

After T1.5 lands, compare this proposal with the frozen Go/C ABI, schema, error,
fixture, CLI, and MCP seam. Record each adopted field and any divergence in the
probe contract before workers implement against it. Worker code must stop if
the seam's versions/ownership or the fixture's process/window identity differ;
coordinator owns amendments. T1.18 qualifies SDK/protocol behavior; T1.14–T1.16
qualify policy, writer/replay/cleanup, and JSON/coverage respectively.

No code, go.mod pin, native action, provider request, build, or CI run was part
of preparing this proposal. It must not be cited as runtime, SDK integration,
host, TCC, or product acceptance evidence.

## Reviewed clarifications

The exact permission-owning executable must separately preflight AX trust and Core Graphics event-posting access. Neither proves the other. Native posting acceptance records grant, deny, and revoke outcomes for that executable before T1.31. This does not authorize a prompt from ordinary read commands.

The approval-bound `state_id` is a pre-dispatch precondition. A composite operation may produce its own expected intermediate state transitions while preserving the bound process, window, element, and focus identities. Those transitions are recorded and checked against the operation sequence; they do not authorize unrelated changes. Unexpected external edits, focus changes, or identity drift stop dispatch and produce a truthful partial or unknown result with cleanup evidence.
