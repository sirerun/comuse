# Phase 1 parity v1 — candidate contract

Status: REVIEW CANDIDATE until independent review and verified contract landing T2.54; thereafter frozen phase1-parity-v1 source authority only. Task T2.17; source basis1830cf54a88f9842a3bac505979db70aeccc53e6 and RFC0001v0.5. Preserves existing plan IDs. Coordinator reconciled a Luna proposal; independent review must check every limit, enum, authority decision and RFC gap before this becomes the frozen leaf contract.

## Contract decision

Publish a versioned `phase1-parity-v1` contract before implementation. It must freeze the complete RFC action and observation matrix, schemas, closed status vocabularies, trust boundaries, limits, canonical desktop identity and quota rules, and disjoint source ownership. Every native mutation remains disabled by default and absent from CLI/MCP advertisement unless a later, separately reviewed capability gate admits it.

Phase 1 parity means **full source parity for the RFC’s Phase 1 surface**, across core, native source, CLI, and MCP. A source stub or compiling-only route is not parity. Source parity, synthetic verification, and controlled runtime qualification are separate gates. This proposal establishes none of those gates as complete.

## Action and operation matrix

| RFC operation | Phase 1 contract | Default model-facing MCP |
|---|---|---|
| `ClickElement` / `computer_click_element` | Fresh, identity-bound target and expected `state_id`; use only a qualified AX press or live-geometry click method. Report method and execution separately. Never retry through another method after uncertain dispatch. | Unadvertised until exact capability admission; direct invocation returns typed `unsupported`. |
| `PerformElementAction` / `computer_element_action` | Only explicitly advertised kinds. Distinguish `press`, `pick`, and target-bound `focus`; do not alias them to one operation. | Unadvertised until each kind is admitted and qualified. |
| `ReadElement` / `computer_read_element` | Fresh, separate observation bound to window, element, and expected `state_id`. `AllowValues:false` suppresses inline snapshot values but does not itself prohibit a separately authorized read. Only positively classified `normal` targets within trusted host observation scope may return text. Disabled state alone does not prohibit reading. Secure, protected, unknown, ambiguous, stale, or out-of-scope targets return a typed limitation and no value. | Read-only route remains available; host policy and target classification govern returned text. |
| `WriteElement` / `computer_write_element` | `replace` replaces the entire value; empty text clears. `insert` requires a fresh, reliable caret/selection and non-empty text. No clipboard, keyboard, coordinate, or mode fallback after unsupported or uncertain dispatch. Verification may be unavailable. | Unadvertised until capability admission; direct invocation returns typed `unsupported`. |
| `ScrollElement` / `computer_scroll_element` | Target a fresh, explicitly scrollable element. Direction is `up|down|left|right`; amount is `line|page`. At most one semantic unit per call, with native units and pointer placement owned by Comuse. Revalidate focus/target immediately before dispatch. | Unadvertised until capability admission; direct invocation returns typed `unsupported`. |
| `Wait` / `computer_wait` | Bounded fresh semantic reads for `window_appears`, `window_closed`, `element_exists`, and qualified element-state conditions. Deadline is at most 10 seconds. No held input and no implicit image fallback. Pixel conditions require separately authorized image capability and are rejected in `semantic_only`. Condition satisfaction is not action success. | Available for supported semantic conditions; unsupported conditions return typed `unsupported`. |
| `Click`, `TypeText`, `PressKey`, coordinate `Scroll`, `Drag`, `FocusWindow` | Developer-only library/CLI facilities, each with trusted scope, approval, target/focus revalidation, quotas, replay protection, cancellation and cleanup, and typed unsupported outcomes. Coordinate inputs are logical points; validate hard limits before native work. Never use implicit focused-window targeting unless trusted policy explicitly permits it. | Not exposed as model-facing tools or MCP arguments. |
| App opening, semantic drag, screenshot/image fallback, OCR, pixel diff | Deferred to later separately contracted and qualified work. | Not advertised. |

Existing backend vocabulary is `press`, `replace`, and `insert`; `pick`, semantic focus, semantic scroll, and click need explicit capability and typed unsupported behavior until implemented and qualified. RFC operations with no admitted Phase 1 route must fail closed; they must not silently map to a different operation.

All mutation requests bind opaque `action_id`, `window_ref`, `element_ref`, and expected `state_id`. Approval authority, policy, target scope, writer paths/keys, and permission state come only from trusted host configuration. Model input cannot supply coordinates, raw native references, writer secrets, approval tokens, or confirmation fields. Validate target identity, process generation, window, role/classification, scope, current state, focus, permission, and operation support immediately before each dispatch step.

## Target eligibility and text policy

Freeze explicit-read permission as a trusted host policy scoped to approved process/window identities. A model argument cannot broaden it. Before returning text, the implementation must freshly validate the requested window, element, expected public state, target classification, current permission epoch, and scope.

Only a positively classified normal target can return text. Unknown and protected classifications fail closed. Do not first read a value to decide whether the target is protected. Redact before canonicalization, hashing, retention, comparison, or serialization. Hidden values do not affect public state IDs, deltas, accounting, or errors. Preserve missing information as unknown; do not manufacture defaults.

## Shared response and ledger contract

Keep one shared typed response model across library, CLI JSON, and MCP. Preserve the existing `Envelope{schema_version,status,result,error}` during additive migration:

- Add RFC execution metadata to that envelope: `ok`, `action_id`, `action`, `execution`, `duration_ms`, `state_status`, `state`, `verification`, `cleanup`, `usage`, and `observation`.
- Keep `status` as the legacy `ok|error` projection **derived from `ok`**; it is not an independent outcome.
- Keep `result` for the operation-specific payload, such as window lists, state, element content, or the existing action-result compatibility DTO. Do not duplicate a second independently authored result payload.
- Keep `error` as the existing typed safe error. `ok` reports operation execution outcome, not application postcondition or verification. Error, verification, state availability, and cleanup remain distinct.
- During migration, CLI and MCP serialize the same shared envelope. MCP’s `structuredContent` and JSON text encode semantically identical data; `isError` reflects tool execution failure separately from the typed error.

Freeze these closed enums:

- `execution`: `not_applied|applied|partially_applied|unknown`
- `verification.status`: `verified|failed|unavailable`
- `state_status`: `available|partial|unavailable`
- `cleanup`: `released|not_required|failed|unknown`
- observation kind: `snapshot|delta|unchanged|element_content`
- coverage: `complete|partial|unavailable`

Keep the existing `ActionResult` as the compatibility representation during migration. The shared envelope is authoritative; compatibility fields are projected from the same typed outcome, not independently computed. Unknown execution or cleanup uncertainty requires durable dirty state before reporting terminal status. If dirty persistence fails, retain the inflight record and desktop writer lock. Cancellation after dispatch reports partial or unknown as supported by evidence; never convert uncertainty into retry permission.

Use the RFC §11 ledger names and define counters precisely:

```json
{
  "session": "opaque-session-id",
  "actions": 0,
  "observations": {"screenshot": 0, "a11y": 0, "state": 0},
  "semantic_results": {"snapshot": 0, "delta": 0, "unchanged": 0},
  "baseline_resets": 0,
  "encoded_image_bytes": 0,
  "serialized_text_bytes": 0,
  "images": 0,
  "retained_bytes": 0,
  "elapsed_ms": 0,
  "model_usage": null
}
```

Count domain requests admitted to core once, including refused, failed, and canceled attempts; exclude malformed transport/protocol requests rejected before core. Retries contribute to duration/cost but do not create another caller action count. Semantic-result counters count returned result kinds; baseline resets count only when a reset result is returned. `serialized_text_bytes` is the canonical bounded redacted semantic JSON payload once, independent of MCP framing/duplication. `retained_bytes` is a current bounded gauge covering retained redacted public state and required private binding metadata. `elapsed_ms` is monotonic elapsed session time; per-call `duration_ms` covers the complete bounded call.

`encoded_image_bytes` measures only encoded image bytes actually returned; Phase 1 semantic-only results have zero image count/bytes. `model_usage` is `null` unless a trusted host supplies provider usage with source and model identity; estimates must be explicitly marked estimates with model/version/detail and method. Never count hidden values, secure values, raw AX errors, action text, credentials, secrets, prompts, or completions. Do not infer model usage from serialized bytes. No telemetry or provider calls are added.

## Observation and history parity

Phase 1 observations use the RFC snapshot/delta/history contract: `schema_version`, `kind`, `state_id`, `scope_id`, `window_ref`, `observed_at`, `action_sequence`, `coverage`, `context`, and `nodes`. These names and structures require an explicit additive DTO migration from the current `Snapshot{window_ref,state_id,observed_at,elements,coverage{complete,reason}}`; the contract must not claim the current DTO is already RFC-equivalent.

Semantic identity covers only deterministic, normalized, redacted content. Timestamps, action sequence, private native IDs, and hidden values are metadata or excluded. Deltas require the exact base state in the same session/scope/schema/policy/display generation. Emit them only between complete observations. Upserts are complete replacement records; removals are unique and disjoint. Partial traversal never proves deletion. Validate topology and bounds before atomically publishing the next baseline.

`unchanged` has equal base/current state IDs and fresh metadata; it does not prove action success. `auto` may return a bounded reset snapshot when the baseline is missing, expired, partial, incompatible, or produces a delta no smaller than full state. `stored` returns the original immutable retained snapshot and original timestamp/action sequence with a historical marker; it performs no live substitution and is incompatible with `since`. Foreign-session/scope references reveal no contents. Expiry returns `state_expired`. Retention limits: 8 generations, 4 MiB canonical serialized data per session, 2-minute TTL, and native-reference lifetime no longer than the owning retained snapshot. Close, permission loss, scope expiry, and policy revocation purge public snapshots and native bindings together.

## Desktop identity, exclusion, dirty state, and quotas

`WriterDirectory` remains journal/application data configuration and cannot choose the desktop lock, dirty marker, or quota store. On macOS, resolve GUI identity with public Security `SessionGetInfo(callerSecuritySession,...)`, require graphic-session access, and verify through public Quartz `CGSessionCopyCurrentDictionary()` that login is complete, the session is on-console for active-desktop operations, and its user ID equals `getuid()`. Any unavailable, inconsistent, unsupported, or unsafe identity/root result denies mutation. Do not derive identity from environment variables, configurable journal paths, private audit-session APIs, or raw identifiers in public errors.

Use a protected app-owned canonical root with non-symlink path validation and private ownership/permissions. Lock and dirty state are keyed by `(uid, Security login-session ID)` so separate verified GUI sessions do not contend. Quota state is separately keyed by UID so logout/login, new Comuse sessions, process restarts, and stateless transports cannot reset it. Serialize quota updates with a per-UID lock. Persist only bounded metadata and action commitments, not text or secret values.

Freeze `MaxActionsPerMinute = 60` as a rolling 60-second per-UID window, plus the existing per-session `MaxActions` hard cap of 4096 admitted IDs. Atomically reserve one quota slot for each unique, policy-admitted action attempt before approval; refused approval and canceled pre-dispatch attempts consume quota. Malformed, out-of-scope, unsupported-before-admission requests, reads, and exact idempotent replays do not consume a second slot. Quota exhaustion returns `rate_limited` before native dispatch. Use UTC wall time persisted with monotonic nondecreasing clamp: rollback cannot restore quota; if durable clock/quota state is unreadable or corrupt, fail closed. Retain rolling-window metadata for at least 61 seconds and any needed anti-rollback watermark durably. Keep quota admission distinct from replay tombstones and desktop ownership. This policy applies to all developer-only mutation paths too.

Before any held input or dispatch that could leave desktop state uncertain, persist dirty/inflight ownership. Cancellation or cleanup uncertainty uses a fresh bounded cleanup context; retain writer ownership when cleanup or callback drain is uncertain. Only trusted operator reconciliation clears dirty state.

## Limits and typed failures

Freeze conservative parity defaults (these are not claims about existing source maxima): opaque IDs/references at most 128 ASCII bytes; action text at most 8192 UTF-8 bytes; semantic observation depth at most 6, 256 nodes, 16 KiB serialized text, and 10-second traversal deadline; per-session action IDs at most 4096; retained snapshots at most 8 generations, 4 MiB, and 2 minutes. Reject out-of-range requests before allocation/native entry. Freeze waits at 10 seconds and semantic scroll to one `line|page` unit per call.

Preserve the existing safe typed error codes where applicable: `invalid_request`, `policy_refused`, `approval_required`, `element_stale`, `state_expired`, `permission_denied`, `unsupported`, `backend_unavailable`, `desktop_busy`, `rate_limited`, `budget_exceeded`, `cancelled`, `session_closed`, and `unknown_outcome`. Add `replay_result_expired` for admitted IDs whose bodies have expired; never redispatch. Refuse unknown action kinds with `unsupported`; use `element_stale` for stale/disappeared identity and `policy_refused` for a current but ineligible/protected target. Preserve safe public errors without native identifiers or content.

## Source ownership and sequence

Existing task IDs are immutable. T2.17 owns preparing this freeze (completed by T2.54 receipt) and coordinator shared DTO/API seams. T2.18/T2.19 metadata/accounting implementation/verification own cost.go, obs.go, and dedicated ledger tests after coordinator interface patches. T2.20/T2.21 own target-bound semantic scroll and tests in action.go/desktop.go/guard.go; these must be serialized with T2.30/T2.31 core-writer integration if touching the same files. T2.22/T2.23 own cmd/comuse and mcp action routes/protocol tests, consuming shared types. T2.24/T2.25 own native/macos and internal/backend/darwin disabled native action source/tests. T2.30/T2.31 own internal/writer canonical lock/quota storage and new trusted desktop identity modules; coordinator integrates core admission hooks to avoid collision. New capability DTOs/backend interfaces are coordinator-owned before leaves start. Nonauthor reviewer covers final source, separate from implementation tests.

T2.26 integrates only after verified foundation T2.15 and leaf verification; T2.27 independent review, T2.28 guarded rebase merge, T2.29 landed verification. T2.32-42 foundation remediation IDs and historical receipts are preserved. Default input remains disabled through all source stages; T2.16 runtime gate is separate.

Remaining wait-condition/developer-action/read-policy/ledger-resource source paths are explicitly decomposed as T2.43-50; they cannot be claimed complete through only the semantic-scroll task. Source leaves depend on verified contract landing T2.54. E3 snapshot/delta/history migration has its own planning trigger and delivery gates after T2.15.

## RFC traceability and source gap

- **RFC §§3, 3.2–3.4:** Current backend boundary exposes semantic observations, reads, and a typed `Execute`; default capability remains closed. Contract must preserve the backend/native ownership split, async cancellation/drain, public permission APIs, and semantic-only operation without image fallback.
- **RFC §§4.1–4.4:** Current source has bounded `Budget`, redacted semantic bindings, and explicit reads; freeze full snapshot metadata, classification and eligibility, history/scope semantics, and the separation of semantic references from diagnostic geometry.
- **RFC §4.5:** Current backend snapshot lacks the full RFC context/topology/delta/history union. This is Phase 1 source work, not evidence of existing parity.
- **RFC §§5.1–5.3:** Current shared action result has execution, verification, state, and cleanup fields, while `Envelope` still has legacy `status/result/error`. Add the authoritative shared RFC envelope and a derived compatibility projection; freeze all action routes and per-operation result rules.
- **RFC §§6.1–6.3:** Current policy/action path is trusted-host gated and has session cap/replay. It lacks the required canonical per-desktop identity and durable cross-session/per-UID minute quota contract. Preserve default deny, exact approval binding, revalidation, cleanup, and fail-closed errors.
- **RFC §§7–8:** Current CLI/MCP adapters must expose only the frozen common contract, reject unknown/conflicting arguments, enforce closed schemas and cancellation, and keep developer raw-input routes outside model MCP.
- **RFC §§11–13:** Current source ledger/schema parity and full semantic reconstruction are not established by the current DTOs. Count only redacted public outputs; test source properties without claiming GUI/runtime qualification.

