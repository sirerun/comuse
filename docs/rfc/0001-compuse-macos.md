# RFC 0001: Comuse — A macOS-First Computer Use Toolkit with Go and Swift

- **Status:** Draft — revised design; implementation and platform qualification pending
- **Version:** 0.5
- **Last updated:** 2026-10-03

---

## 1. Abstract

Comuse is an open-source Go toolkit for **computer use** through compact semantic observations and validated desktop actions. The LLM chooses what to read, write, or activate through named controls and opaque references; Comuse owns coordinates, focus, and native input delivery. It exposes accessible text, controls, state, and hierarchy as JSON, without requiring geometry in model context. A session begins with a full snapshot and subsequently returns semantic diffs against a client-supplied baseline. Screenshots are an explicit fallback when accessibility information cannot answer the task. Phase 2 adds opening authorized installed applications. It ships as:

1. **A core Go library** (`comuse`) — the single source of truth for policy, action scheduling, observations, and accounting.
2. **A CLI** (`comuse`) — scriptable, pipe-friendly, with JSON results for agents without MCP support.
3. **An MCP server** (`comuse mcp`) — the same core capabilities over **stdio** initially; streamable HTTP after the local macOS release is qualified.

The first release targets **macOS 14+ on Apple Silicon**. Intel macOS support requires its own qualification before being advertised. Linux and Windows are future backends, not v1 acceptance requirements. The backend boundary preserves portability without requiring three platform implementations before a useful release.

The design prioritizes **correct input delivery, stable permission handling, bounded observations, and measurable task cost**. The normal semantic path captures no pixels and requires no vision model. Applications with incomplete accessibility support may need explicit visual fallback, which requires a vision-capable host/model to interpret images. Reduced observations and diffs are efficiency hypotheses; they must preserve task completion and be demonstrated with real model usage. See [the project vision](../vision.md).

---

## 2. Motivation

Existing computer-use tooling can suffer from:

- **Unnecessary observations.** Returning a full screenshot after every action can consume model context and increase latency when compact state would suffice.
- **Poor observability.** Window identity, focus, cursor position, and accessibility state can answer questions without an image.
- **Unreliable control.** Focus changes, stale element paths, coordinate scaling, and interleaved sessions can send input to the wrong destination.
- **Deployment friction.** A collection of shell utilities and language runtimes is harder to install and qualify than one packaged tool using native OS APIs.

Comuse's thesis: a library-first tool with full semantic JSON snapshots, baseline-bound diffs, validated element actions, and explicit desktop ownership can complete accessible desktop tasks reliably while reducing unnecessary observation cost. The trusted host reconstructs state deterministically; the LLM receives the initial state and relevant changes rather than maintaining an unlimited patch history from memory.

Base64 image payloads belong in image content blocks, not ordinary text prompts. Transport bytes and billable image tokens are separate quantities. Neither JPEG byte reduction nor image count alone proves a particular reduction in model cost (§11).

### Goals

- G1: A working, stable macOS release before adding other operating systems.
- G2: Bounded full JSON snapshots and semantic diffs, with explicit coverage, baseline recovery, and reported usage; bounded images only on explicit fallback.
- G3: Measured latency and bounded resource use; performance aspirations are not release claims until qualified (§10).
- G4: One distributable macOS package with no required third-party runtime tools. Apple frameworks and OS services remain system dependencies; the executable is **not fully static**.
- G5: MCP stdio using the official Go SDK and an explicitly negotiated protocol version. Qualify the selected version; do not claim open-ended “2025-06-18+” compliance.
- G6: Library-first: CLI and MCP are thin adapters.
- G7: Mandatory policy enforcement, trusted approval authority, desktop ownership, cancellation cleanup, and explicit partial-result semantics from v0.1.
- G8: Coordinate-free model tools for reading/writing elements and qualified semantic actions, with live target validation and explicit staleness errors. Geometry and input mechanics belong to Comuse.
- G9: A host-controlled semantic-only mode that prohibits pixel capture, and a semantic-first mode that permits separately authorized, explicit screenshot requests.
- G10: Phase 2 application opening through authorized installed-app identities, with bounded launch/readiness handling and no arbitrary command execution.

### Non-goals for the first stable macOS release

- N1: Linux, Windows, Wayland, VNC/RDP, mobile, VR, gamepad, or pen backends.
- N2: OCR, WebP encoding, pixel-diff history, image resource references, or parallel JPEG encoding. Bounded semantic snapshot retention and diffs are core capabilities, separate from pixel diffs.
- N3: Autonomous agent logic — Comuse is a toolbox, not an agent.
- N4: Multi-display control. The initial session selects the primary display; secondary-display targets and windows crossing its bounds are rejected explicitly.
- N5: Full containment of a malicious desktop application or another physical user. Comuse enforces its own policy; strong isolation requires a dedicated desktop or OS sandbox.

---

## 3. Architecture

```
                       comuse core (Go)
       Session · Desktop ownership · Policy/approval · Results
       Semantic snapshots/diffs · Observation budgets · Cost ledger
                      Explicit image fallback
                        /                 \
                  CLI adapter        MCP stdio adapter
                              |
                      Backend interface
                              |
                 darwin: Go + small C ABI bridge
                 Swift / Apple frameworks
          ScreenCaptureKit · CGEvent · AXUIElement · AppKit

        Future: HTTP adapter; separately qualified OS backends
```

### 3.1 Package layout and stack decision

```
comuse/
├── comuse.go               // public Session, Do, Observe API
├── action.go               // action types and validation
├── obs.go                  // JSON snapshot/delta types, coverage, and budgets
├── diff.go                 // canonical state comparison and bounded baselines
├── image.go                // crop, downscale, JPEG/PNG
├── a11y.go                 // bounded observations and protected-field detection
├── cost.go                 // actual tool usage; model estimates kept separate
├── guard.go                // policy, approval, quotas
├── desktop.go              // exclusive writer ownership and input scheduling
├── cmd/comuse/             // CLI and stdio entrypoint
├── mcp/                    // official Go SDK adapter
├── internal/backend/
│   ├── backend.go          // contract and fake backend
│   └── darwin/
│       ├── backend.go      // Go backend adapter and error translation
│       └── bridge.go       // cgo calls into the native C ABI
├── native/macos/
│   ├── Package.swift       // native backend build definition
│   ├── include/comuse.h    // versioned C ABI: handles, buffers, results
│   └── Sources/ComuseMacOS/
│       ├── Exports.swift   // C-callable entrypoints
│       ├── Capture.swift   // ScreenCaptureKit
│       ├── Input.swift     // CGEvent and held-input ownership
│       ├── Accessibility.swift // AX identity, observations, focus
│       └── Runtime.swift   // native queues, cancellation, lifetimes
└── examples/
```

**Stack decision:** Go for the public library, CLI, policy, desktop ownership, scheduling, observation budgets, image encoding, accounting, and MCP; Swift for the macOS backend. Swift owns ScreenCaptureKit capture, CGEvent input, AXUIElement accessibility, permission checks, native execution queues, and Apple-object lifetimes. Go's `image/jpeg` and `image/png` provide initial encoding; add `golang.org/x/image/draw` only if measured scaling quality/performance warrants the dependency. Use the official `github.com/modelcontextprotocol/go-sdk`, pinned to a reviewed release during implementation.

Go fits an embeddable library and a small server/CLI. Swift fits the native Apple work and lets that backend grow without making the public API platform-specific. The cost is a mixed-toolchain build and an explicit foreign-function boundary. Keep that boundary small and test cancellation, threading, and ownership across it before expanding the feature set. The initial design links the native backend into the same permission-owning executable; it does not require a resident helper process or IPC.

Use a **narrow, versioned C ABI through cgo**. Only primitive fields, opaque handles, byte buffers with lengths, request IDs, status codes, and C-compatible completion callbacks cross it. Do not expose Swift objects, generics, `async` functions, or thrown errors directly to Go. Swift translates its operations into this contract; Go translates native statuses into typed core errors. Validate ABI compatibility when opening the backend and reject mismatches explicitly.

The proposed native build baseline is **Swift 6.3+**, whose supported `@c` exports can expose functions to C; `@c` with `@implementation` can validate an implementation against an imported C declaration. Pin the selected Go, Swift, Apple SDK, and deployment-target versions after the feasibility spike. Swift compiler version is a build requirement, not a promise that newly used runtime features work on every supported OS. Swift's stable Apple-platform ABI and OS-provided runtime make distribution practical, but the spike must prove linking, any compatibility-library packaging, signing, and launch behavior on the actual minimum deployment target. See the primary Swift references in §19.

The native layer owns retained Apple objects, its execution queues, Swift tasks, and native pixel storage. Main-thread/AppKit operations are isolated and explicitly dispatched; the CLI initializes the required run-loop integration, and library embedding exposes a documented initialization contract. Do not assume any Go goroutine is the OS main thread, or synchronously block the main thread awaiting work that must return to it. The C interface uses completion callbacks for asynchronous native work; Swift actor/task semantics remain inside the backend.

Each asynchronous request has a retained native request handle and a defined cancellation/completion handshake. Cancellation signals the Swift task/native operation and produces exactly one terminal completion. Keep callback state alive until that completion is drained, including when the requesting Go context has expired. Use Go-managed handle IDs for callback state; native code must not retain an ordinary Go pointer or a Go-owned buffer beyond the permitted cgo lifetime. Native buffers have explicit retain/release ownership and remain valid until all consumers finish. Close cancels and drains outstanding work before destroying handles; it cannot discard callback state still reachable by native code.

### 3.2 Backend interface

The following is an illustrative contract, not implemented or compile-validated code. All native code stays behind this boundary; core tests use a fake backend.

```go
type Backend interface {
    Open(ctx context.Context) (Capabilities, error)
    Close() error
    Permissions(ctx context.Context) (PermissionState, error)
    Displays(ctx context.Context) ([]DisplayInfo, error)
    State(ctx context.Context) (DesktopState, error)
    Capture(ctx context.Context, req CaptureRequest) (*Frame, error)
    Windows(ctx context.Context) ([]WindowInfo, error)
    A11yTree(ctx context.Context, target WindowRef, budget TextBudget) (*A11ySnapshot, error)
    ResolveElement(ctx context.Context, ref ElementRef) (ResolvedElement, error)
    ReadElement(ctx context.Context, target ResolvedElement, budget TextBudget) (*ElementContent, error)
    SetElementValue(ctx context.Context, target ResolvedElement, value string) error
    PerformElementAction(ctx context.Context, target ResolvedElement, action ElementActionKind) error
    ValidateTarget(ctx context.Context, target WindowRef) error
    MouseMove(ctx context.Context, p Point) error
    MouseDown(ctx context.Context, b Button) error
    MouseUp(ctx context.Context, b Button) error
    Scroll(ctx context.Context, d ScrollDelta) error
    KeyDown(ctx context.Context, k Key) error
    KeyUp(ctx context.Context, k Key) error
    TypeText(ctx context.Context, s string, delay time.Duration) error
    FocusWindow(ctx context.Context, target WindowRef) error
    ReleaseOwnedInput(ctx context.Context) error
}
```

`Capabilities` reports availability and reasons, including capture, accessibility, input, window operations, and supported element actions. Native AX actions are capability-dependent; semantic diffs and baseline storage belong in the Go core, not the native backend. Permission denial, unsupported operation, locked desktop, and backend failure are distinct errors. Missing capabilities must never silently select a shell utility or less restricted backend. Window movement/resizing is deferred until its macOS behavior can be qualified separately.

`WindowRef` binds the window to process identity (PID plus process-start identity), application identity, and a session-generated reference. Window titles are display metadata, not authorization identity. Recheck identity and eligibility before use; do not treat a recycled numeric window ID as the same target.

`Frame` owns its buffer until explicit `Close()`. Close is idempotent. The bridge normalizes native pixel format/stride and retains native storage until any Go encoding finishes. A canceled request cannot free pixels still used by an encoder. Do not rely on finalizers for timely release or assume arbitrary native pixels are Go `image.RGBA`.

### 3.3 Coordinates and scale factors

Low-level library/CLI action coordinates are **logical points**, top-left origin within the selected primary display. They are implementation/developer facilities, not the default LLM tool contract. Comuse resolves native target geometry, scrolling/focus, and input coordinates internally; model calls accept element references and semantic intent, never `x,y`, pixel rectangles, or drag endpoints. Default model observations omit cursor positions and bounds. A trusted developer diagnostic projection may include geometry, has its own scope identity, and must not be silently mixed into model state. Backend conversions explicitly account for Apple's coordinate conventions, display origin, and scale. Never clamp invalid input into a different valid target.

Every core image result includes `display_id`, `display_generation`, `source_bounds` in logical points, encoded `width`/`height` in pixels, capture timestamp, and output-to-logical transforms. Host/backend code owns these transforms; the default model image tool chooses a window or element reference and Comuse computes any crop. For source bounds `(x,y,w,h)` and encoded dimensions `(W,H)`, an output pixel center `(u+0.5,v+0.5)` maps to `(x+(u+0.5)*w/W, y+(v+0.5)*h/H)`. Low-level input targets use that logical position. Native display scale and post-encoding scale are separate metadata.

Display topology, resolution, or scale changes invalidate prior coordinate metadata and element snapshots. Agents must re-observe. Test Retina and non-Retina scaling, crops, downscaling, and display changes; initial support remains primary-display-only even when other displays are connected.

### 3.4 macOS permissions and distribution

- Minimum proposed OS: macOS 14, enabling ScreenCaptureKit's screenshot API. The implementation spike must verify the API availability and build deployment target against the Apple SDK before committing support claims.
- Start with `SCScreenshotManager` for on-demand capture. Consider `SCStream` only after profiling shows a need for persistent capture; bound queue depth and stop streams on close, permission loss, lock, or sleep.
- `comuse doctor` reports Screen Recording, Accessibility, display/session availability, signing identity, and capability failures without injecting input. `comuse setup` is the explicit permission-request path; ordinary tool calls do not repeatedly prompt or attempt to alter TCC settings.
- Distributed releases use a signed, notarized macOS application bundle with stable identity and an embedded executable usable as the CLI/MCP entrypoint. No resident service is required initially. The permission-owning process and launch path must be proven under both direct CLI and stdio-host launch; do not assume the terminal's grants transfer.
- Development builds may require different permission grants. Distinguish development evidence from signed-release evidence, and document permission denial, revocation, update, restart, sleep/wake, and locked-screen behavior.
- Accessibility permission supports the semantic path independently of Screen Recording. Semantic-only sessions must work without Screen Recording where the qualified metadata/input APIs permit it; unavailable window metadata is reported rather than recovered by capture. Screen Recording is requested only through explicit setup for image capabilities. Prove these boundaries under both CLI and stdio launch.
- No privilege elevation, permission-database edits, or hidden clipboard replacement. The supported execution context is an unlocked, logged-in GUI session.

---

## 4. Token Efficiency: The Observation Budget

Every observation has bounded work/output and reports usage. The normal agent flow begins with a full semantic JSON snapshot, then requests changes with `since: <state_id>`. Compact execution metadata follows actions; a fresh semantic observation is explicit and can be requested after an action or bounded wait. Images are never added implicitly. This policy must be evaluated for task completion and total cost, including extra observations, retries, and recovery snapshots.

### 4.1 Observation kinds and defaults

| Observation | Default output | Bound |
|---|---|---|
| `state` | Selected display, focused window identity, capabilities; no cursor geometry in model output | bounded JSON; titles truncated |
| `windows` | Authorized visible window identities; bounds available only in trusted diagnostics | maximum entries and text bytes |
| `screenshot` | Authorized capture, JPEG/PNG plus transform metadata | pixel, byte, deadline, and memory limits |
| `region` | Authorized crop | same limits as screenshot |
| `a11y` | Full semantic JSON snapshot, or baseline-bound delta/unchanged response with `since` | depth, node, text-byte, deadline, and retention limits |
| `text` | Optional OCR | deferred; separately authorized and bounded |

The default text budget is depth 6, 256 nodes, 16 KiB serialized text, and a bounded traversal deadline. Results report `truncated` and which bound stopped traversal. `text_tokens_est` is an estimate, never an enforceable provider-token guarantee. Exact model budgets require a host-supplied tokenizer/model adapter.

Observation mode is trusted session policy: `semantic_only` prohibits all pixel acquisition, including screenshots, crops, pixel waits, and hidden image-based verification. `semantic_first` follows the same semantic default but permits explicit, separately authorized image requests. Model tool arguments cannot change the mode. Accessible image labels/descriptions may appear as text; image contents, screen colors, and visual styling are not inferred. Field values remain opt-in; secure values are always omitted. JSON is the canonical public representation, not a claim that JSON is always the cheapest token encoding.

### 4.2 Image budgeting

```go
type ImageBudget struct {
    MaxWidth         int    // default 1280
    MaxHeight        int    // default 800
    Format           Format // jpeg | png; default jpeg
    Quality          int    // default 70; jpeg only
    MaxBytes         int    // default 200 KiB encoded
    MaxCapturePixels int64  // default 20 million; reject larger sources
    MaxAttempts      int    // default 8 total encoding attempts
}
```

Encoding pipeline:

1. Validate the source display, authorized capture region, pixel count, and memory reservation before capture/allocation. Crop only within the authorized region.
2. Apply permitted redaction to the source pixels **before** downscaling, encoding, caching, or returning data. Raw pixels remain transient and are never logged.
3. Scale by `min(1, MaxWidth/sourceWidth, MaxHeight/sourceHeight)` while preserving aspect ratio. Choose a measured scaling filter; do not assume box filtering is artifact-free for small text.
4. For JPEG, try quality 70 → 60 → 50 → 40, then reduce dimensions by 25% and retry within the total attempt/deadline limit. PNG must reduce dimensions rather than pretend it has a lossy quality setting.
5. Enforce minimum usable dimensions and the total memory/session budget. Return `budget_exceeded` when the request cannot fit; never return an oversized image or retry indefinitely.
6. Report encoded dimensions/bytes, format, source bounds, transform, timestamp, and applied redactions/truncation.

Encoded bytes measure transport and storage cost. Image-token estimates depend on the selected provider/model/detail setting and are separate. Defaults are starting points to qualify for legibility, completion rate, and latency.

### 4.3 Diff screenshots (deferred)

Pixel-diff history is not part of v0.1 or the initial stable macOS acceptance gate. Semantic diffs (§4.5) do not require screenshots. Before adding pixel diffs, define session-scoped frame references, source display/region/generation, expiry, ownership, byte/count caps, and invalidation on display or policy changes. Never compare captures with incompatible source geometry or authorization.

A future unchanged response may be `{"changed":false}`, but pixel equality does **not** prove an action succeeded. Diff thresholds and ignoring flicker require task-level evaluation. Retained frames must be redacted according to policy and charged against explicit memory limits.

### 4.4 Accessibility observations and element addressing

Bounded macOS AX observations are the primary backend observation path and support safety checks. Normalize them into relevant text and controls with roles, labels, supported read/write/action capabilities, state, and parent/order relationships. Keep logical geometry inside Comuse for target resolution; include it only in a trusted diagnostic projection. Exclude implementation-only containers when relationships can be preserved unambiguously. Preserve meaningful text and structural context, not just clickable controls. AX information is unavailable or incomplete in some applications; report that explicitly. Do not expose secure-field values, and omit ordinary text-field values unless the observation policy permits them. Missing information is unknown, not a fabricated default such as `enabled: false`.

Example observation:

```
window "Inbox — Mail" ref=win_7
  toolbar
    e12 button "New Message" actions=[press]
    e13 searchfield "Search" focused capabilities=[read,write]
```

Element-targeted actions are part of the initial semantic foundation, gated on native identity/staleness qualification before exposure. Return opaque `element_ref` values bound to session, window/process identity, and a retained native element. Preserve an ID across observations only while that same native element can be identified confidently; never recycle it for another control within the session. State IDs version observations separately from element identity. Child-index paths such as `0/2/1` may be diagnostic metadata; they are never authoritative action targets.

An element action supplies the window, element reference, and expected `state_id`. Revalidate native identity, role, eligibility, window, expected target semantics, and the requested operation before execution. A newer unrelated observation does not itself invalidate the element, but changed target semantics require re-observation. Prefer a qualified native AX action where advertised. A qualified click may resolve live geometry and use CGEvent when no AX press action is available; apply the same region/occlusion/focus checks, report the execution method, and never retry through another method after uncertain dispatch. Unsupported semantic operations return `unsupported`. If identity is ambiguous, relevant content changed, the reference expired, or the target disappeared, return `element_stale`. Baselines and references are not authorization, and neither replaces live checks.

Reading and writing are element operations too (§5.1). The model requests `read(e13)` or `write(e13, text, mode)` and does not decide where to click, how to focus, or which keyboard shortcut clears a field. Unsupported or ambiguous targets return an explicit limitation rather than asking the model to supply coordinates. Moving a window does not by itself change a control's semantic identity: refresh internal geometry and eligibility before execution. Revalidate the editable context for the selected write mode. User text/selection changes can be compared only where qualified reads and policy permit; unavailable readback must not imply protection against concurrent edits. Concurrent input remains non-atomic (§5.2).

### 4.5 Full JSON snapshots and semantic diffs

The following payloads sit in the shared result envelope's `observation` field. `schema_version` versions the JSON contract; `state_id` identifies one immutable, redacted, normalized state within an authorized session. Observation timestamps and action sequence are metadata outside that semantic identity; historical retrieval preserves the metadata from the original retained snapshot. `scope_id` binds the window/process identity, selected display generation, observation selector/budget, and observation policy version. A full snapshot means the complete returned semantic projection of that scope, not proof that the application exposes every visible control.

Illustrative initial snapshot:

```json
{
  "schema_version": 1,
  "kind": "snapshot",
  "state_id": "s42",
  "scope_id": "scope_1",
  "window_ref": "win_7",
  "observed_at": "2026-10-03T12:00:00Z",
  "action_sequence": 14,
  "coverage": {"status": "complete", "truncated": false, "limitations": []},
  "context": {"root_refs": ["e14", "e15"], "focused_element_ref": "e14"},
  "nodes": {
    "e14": {
      "role": "checkbox",
      "label": "Unread only",
      "parent_ref": null,
      "child_refs": [],
      "enabled": true,
      "checked": false,
      "actions": ["press"]
    },
    "e15": {
      "role": "button",
      "label": "Mark all as read",
      "parent_ref": null,
      "child_refs": [],
      "enabled": true,
      "actions": ["press"]
    }
  }
}
```

`coverage.status` is `complete`, `partial`, or `unavailable`: complete means the selected AX scope was traversed within its limits, not that AX covers every screen pixel. Partial results report the limiting bounds and known unsupported content; unavailable results do not claim an empty application. Native reads are not an atomic application transaction. Report detected concurrent mutation and avoid publishing a coherent-looking delta from an inconsistent traversal. `observed_at` and `action_sequence` bound the observation's context; they do not establish a task postcondition.

`nodes` is a map keyed by stable opaque element references. A record contains all currently permitted, available properties for that element. Omitted properties mean unavailable/not disclosed, not false. `parent_ref`, ordered `child_refs`, and `root_refs` preserve topology/order in the returned projection; excluded containers cannot leave dangling references. Geometry is omitted from the default model projection. A trusted diagnostic projection may carry logical bounds under §3.3, with a distinct scope/baseline. Accessible labels and text remain untrusted application data.

A persistent library/MCP client requests `since: "s42"` against the same window and scope. Compare newly observed canonical state with that exact retained baseline, even if intermediate states were emitted but never received. If eligible and smaller than a snapshot, return:

```json
{
  "schema_version": 1,
  "kind": "delta",
  "base_state_id": "s42",
  "state_id": "s43",
  "scope_id": "scope_1",
  "window_ref": "win_7",
  "observed_at": "2026-10-03T12:00:01Z",
  "action_sequence": 15,
  "coverage": {"status": "complete", "truncated": false, "limitations": []},
  "context": {"root_refs": ["e14", "e15"], "focused_element_ref": "e14"},
  "upsert": {
    "e14": {
      "role": "checkbox",
      "label": "Unread only",
      "parent_ref": null,
      "child_refs": [],
      "enabled": true,
      "checked": true,
      "actions": ["press"]
    }
  },
  "removed": []
}
```

Application rules are deterministic: validate schema, session/scope, and `base_state_id`; remove listed IDs; replace/add each complete `upsert` record; replace `context` and coverage metadata; validate all relationships; publish the new `state_id` atomically. Reject duplicate/out-of-order application rather than applying it to a different baseline; recover with a full snapshot on a baseline mismatch. No ID may appear in both `upsert` and `removed`. Replacing whole changed records also clears previously present properties that are now omitted. Reparenting/reordering includes every affected record. This small semantic contract is chosen for readable agent output and straightforward reconstruction rather than array-index patches or ambiguous partial-field merges.

If normalized semantic state is unchanged, return `kind: "unchanged"` with `base_state_id` and `state_id` both equal to the baseline, plus fresh observation time, action sequence, scope, window, and coverage metadata. Collection time and action sequence do not themselves count as semantic changes. Window movement alone need not produce a model diff when semantics/eligibility are unchanged; internal geometry must still be refreshed before action. An unchanged response is not proof of action success. Failed or unavailable reads return an explicit observation status/error, never `unchanged`.

Recovery and retention rules:

- No `since`, or `mode: "full"`, requests a fresh full snapshot. Default `mode: "auto"` treats `since` as a request for an eligible delta/unchanged result, not a demand to omit recovery state.
- Return a fresh snapshot with `reset_reason` when the same-session baseline expired/is unknown, schema/scope/policy/display changed, native identity reconciliation became uncertain, either observation is partial, or the delta would be at least as large as the full snapshot. Baselines from another session/owner are rejected without exposing their content. Known concurrent inconsistency produces an explicit incomplete observation or bounded retry, never a reliable-looking delta.
- A node absent because traversal stopped, AX failed, or coverage changed is not evidence of deletion. The initial implementation emits diffs only between complete traversals of the same scope. A partial replacement snapshot replaces the client's projection and explicitly marks reduced coverage; it does not imply previously seen controls disappeared from the application.
- Store only redacted canonical snapshots, with hard session caps for bytes, count, native references, and TTL, all charged before admission. Finalize and document default limits during the spike. Baseline eviction only reduces diff eligibility; it never permits unbounded storage or reference reuse. Session close, permission loss, and policy revocation invalidate/purge affected state and references. Historical reads recheck current policy and cannot bypass revocation.
- `mode: "stored", state_id: <id>` retrieves the original immutable snapshot while retained and authorized, with its original observation time and an explicit historical marker. It performs no fresh read and is mutually exclusive with `since`. Expired historical state returns `state_expired`; it must not silently substitute current state. This is observation retrieval, not a persistent desktop recording.

The trusted host maintains the reconstructed JSON state and supplies a fresh full snapshot after model-context compaction, session changes, or an absent baseline. It may send relevant deltas to the model while retaining the full state locally, or provide the current full projection when the model needs orientation. The model is not responsible for applying an unbounded patch chain from memory. Keep full snapshots available even when diffs are enabled. JSON verbosity and repeated model-context costs must be measured, not inferred from response byte counts.

Start by freshly reading bounded AX state and comparing it deterministically. AX notifications may later trigger refreshes, but are not a complete change log; never build the authoritative delta solely from notifications. Screenshots remain independent observations and do not establish a semantic baseline or conceal missing AX coverage.

---

## 5. Action Model

### 5.1 Actions

```go
type Action interface{ isAction() }

type Click struct {
    Target Point
    Window WindowRef
    Button Button // left, right, middle
    Count  int    // 1 or 2; double-click is not a button
    Hold   time.Duration
}
type ClickElement struct { Window WindowRef; Element ElementRef; ExpectedState StateID }
type PerformElementAction struct { Window WindowRef; Element ElementRef; ExpectedState StateID; Kind ElementActionKind }
type ReadElement struct { Window WindowRef; Element ElementRef; ExpectedState StateID; Budget TextBudget }
type WriteElement struct { Window WindowRef; Element ElementRef; ExpectedState StateID; Text string; Mode WriteMode }
type ScrollElement struct { Window WindowRef; Element ElementRef; ExpectedState StateID; Direction string; Amount string }
type TypeText struct { Window WindowRef; Text string; Delay time.Duration; Clear bool }
type PressKey struct { Window WindowRef; Keys []Key; Hold time.Duration }
type Scroll struct { Window WindowRef; Delta ScrollDelta; At Point }
type Drag struct { Window WindowRef; From, To Point; Button Button; Steps int; Duration time.Duration }
type Wait struct { Until Condition; Timeout time.Duration }
type FocusWindow struct { Window WindowRef }
```

Every model read/write/input operation specifies an authorized window and, where relevant, element target and expected state. The model never relies on whichever text field happens to be focused. Developer low-level input may resolve an omitted window to the current focused window only when policy explicitly permits that mode; the result records the resolved identity. Validate mutually exclusive targets, key names, Unicode behavior, limits, durations, and scroll units before posting events. Text length, hold time, drag steps/duration, and waits have hard maximums.

Element reads/writes and semantic actions are the model control contract. Initial native operations are advertised `press`, `pick`, and focus operations only where platform behavior is proven; capability absence is explicit. `ClickElement` uses the AX-press/live-coordinate contract (§4.4). `ReadElement` retrieves fresh permitted text/value within its budget after validating the target; it is observation, not input, and denied/protected readback is explicit. Default snapshots can omit field values even when a separately authorized explicit read is available.

`WriteElement` initially supports `replace` (replace the entire editable value) and `insert` (insert at a freshly validated current selection/caret), only on qualified writable controls. Use a separately qualified writable AX value operation for replacement where supported, or internally focus and inject input with the same target/focus checks. Insertion requires a reliably resolved selection/caret and returns `unsupported` when it cannot be established. Do not silently change modes, append instead of replace, use the clipboard, or switch execution methods after uncertain dispatch. The model supplies content and mode, never click positions or clear-field shortcuts. Protected writes require explicit host scope and never return secure values. Readback/verification may be unavailable; distinguish dispatch from verified final contents. Advanced editor/range manipulation remains separately qualified.

`ScrollElement` names a scrollable container plus bounded semantic direction/amount (for example `down`, `page`); Comuse owns pointer placement and native scroll units. Coordinate click/drag/scroll and window-bound typing remain developer-only library/CLI operations and are not advertised to the default model MCP client. Semantic drag-by-reference and other operations must have separately qualified contracts before exposure. Visual fallback may help interpret a screen, but it does not bypass this boundary: if a visual target cannot be bound to a validated element, report `unsupported` rather than asking the model for pixel coordinates.

### 5.2 Session, desktop ownership, and cancellation

A session owns backend resources, immutable policy, approved scopes, quotas, action results, and observation references. Input changes global desktop state, so session-local FIFO alone is insufficient.

- **One writer per desktop:** a writer session acquires an exclusive desktop ownership lease. Enforce it across Comuse processes for the same OS user/GUI desktop using a protected local ownership mechanism. A second writer receives `desktop_busy`; read-only sessions may observe within their policy. Do not rely on a Go mutex to exclude another process.
- **Ordering:** all input within the owner session passes through one FIFO scheduler. Composite actions hold ownership across focus validation, moves, key/button presses, duration, and cleanup. State observations report the associated action sequence; do not imply arbitrary concurrent captures are synchronized with input.
- **Focus:** revalidate the authorized process/window and actual destination immediately before each injection step. Stop if the target changes, becomes occluded/ineligible, or focus leaves the allowed target. External user input cannot be fully prevented; record the limitation and qualify unattended tasks on a dedicated desktop.
- **Partial actions:** input is not transactional. Once events are posted, failure can leave a partially applied action; no automatic rollback or retry is implied.
- **Held-input cleanup:** track every key/button pressed by the session. On success, error, cancellation, permission loss, and normal close, attempt matching releases with a fresh bounded cleanup context independent of the canceled request. Release the writer lease only after cleanup; if cleanup fails, mark the desktop state uncertain, refuse further input in the owner process, and require explicit operator recovery. Persist an ownership dirty marker before posting held input and clear it only after successful cleanup. A new owner finding a dirty marker refuses mutation until explicit operator recovery; crash-released OS locks alone do not prove the desktop is clean. Abrupt process termination may prevent cleanup; never claim crash-proof release.
- **Typing:** use CGEvent with documented Unicode/layout behavior. Do not silently paste via the clipboard. Read back only when AX and observation policy permit a meaningful comparison. For `Clear`, compare the expected complete value; for insertion, verify only when the prior value/selection and expected result are known. Unsupported/protected readback yields `unavailable`, not fabricated success.
- **Waiting:** `window_appears`, `window_closed`, `element_exists`, and qualified element-state conditions use bounded fresh semantic reads with a deadline and valid session/window context. A semantic wait failure cannot silently request pixels. `pixel_changed` requires explicit image authorization in `semantic_first` and is rejected in `semantic_only`. Waits do not hold pressed keys/buttons and do not bypass desktop ownership policy; condition satisfaction is separate from an action's semantic outcome.
- **Replay:** adapters carry an `action_id`. Atomically register IDs before execution, retain the admission registry until session close, and reject conflicting reuse. Bound registry memory with a hard per-session action-count cap; reaching it rejects new actions rather than forgetting old IDs. Completed/partial result bodies may expire or be evicted, but reuse of their admitted ID returns `replay_result_expired`, never another injection. Never replay input merely because an HTTP connection or post-action observation failed. Report uncertainty after loss of durable knowledge; do not promise exactly-once execution across process crashes.

### 5.3 Result envelope

The library, CLI JSON mode, and MCP structured results share one envelope:

```json
{
  "ok": true,
  "action_id": "a_14",
  "action": "click",
  "execution": "applied",
  "duration_ms": 24,
  "state_status": "available",
  "state": {
    "focused_window": {"ref": "win_7", "title": "Inbox — Mail"},
    "display_id": "display_1",
    "display_generation": 3
  },
  "verification": {"status": "unavailable", "reason": "no_postcondition_requested"},
  "usage": {"images": 0, "encoded_bytes": 0, "text_tokens_est": 80}
}
```

`execution` is `not_applied`, `applied`, `partially_applied`, or `unknown`; `verification.status` is `verified`, `failed`, or `unavailable`. `ok` denotes successful execution of the requested operation, not proof of an application's semantic outcome. Verification failure is recorded separately and must not cause blind input replay.

Compact state is included when available, subject to observation policy and budget. If state collection fails after input, preserve execution status, return `state_status: unavailable`, and report the observation error. An unavailable state never turns already applied input into a retry-safe failure. Partial results include completed steps, cleanup status, and typed errors without secret input text.

Read tools add an `observation` payload with the §4.5 discriminator (`snapshot`, `delta`, or `unchanged`); image requests use a separate image payload/content block. Execution metadata, verification, and observations remain distinct. Hosts explicitly observe after mutations with their retained `state_id`; an observation failure must not replay already applied input.

### 5.4 Phase 2: Opening applications

Phase 2 (`v0.2`) adds bounded installed-application discovery and opening through native AppKit APIs. `computer_apps` returns a bounded inventory of only host-authorized installed launch candidates, with names and opaque `app_ref` values. The host binds each reference to a resolved installed bundle URL, bundle identifier, and the verified identity requirements of its policy (including publisher/signature where required). Display names and bundle identifiers alone cannot authorize an arbitrary installation. Recheck the installed identity before launch; removed, replaced, or ambiguous candidates require fresh discovery and return `app_stale`/`unsupported`.

`computer_open_app` accepts `action_id`, `app_ref`, an optional `activate` preference, and a bounded readiness timeout. The normalized request binds activation and target identity to host approval. The native backend uses `NSWorkspace.openApplication(at:configuration:completionHandler:)` with explicitly selected activation behavior; it does not invoke a shell command. Opening an already running approved app may activate it when permitted, and reports `already_running` rather than starting an unnecessary duplicate instance. Launch/activation is scheduled under desktop writer ownership, cancellation, quotas, approval, and replay handling; it may change focus or cause the application to perform startup/network work.

No arbitrary executable paths, shell strings, process arguments, URLs, or documents are accepted by this operation. Launch authority does not grant permission to read/control every window of that app: discovered process/window identities must also satisfy the host's observation/input scopes. Once an eligible window is available, return its `window_ref` so the model can request a fresh semantic snapshot. Phase 1 begins with already running authorized applications; it does not advertise app launch capability.

Distinguish native launch outcome (`started`, `already_running`, `failed`, or `unknown`) from readiness (`window_ready`, `no_eligible_window`, or `unavailable`). A returned running application is not proof that a window, document, or AX tree is ready. Bounded readiness observation can time out after a successful launch without making launch retry-safe. Preserve applied/partial/unknown execution, do not automatically relaunch or terminate the app on cancellation, and use the existing `action_id` replay contract. Phase 2 must qualify missing/ambiguous apps, already-running apps, launch failures, focus/activation, delayed or absent windows, permission denial, cancellation after dispatch, and changed installed identities before advertising this capability.

The native bridge gains a separately qualified application inventory/identity/open contract in phase 2; raw bundle paths and OS object lifetimes remain inside the backend. This is application opening, not installation, arbitrary process execution, or a task-completion guarantee.

---

## 6. Guardrails

Comuse can perform consequential real-world actions. The safety baseline is mandatory from the first mutating release and shared by library, CLI, and MCP.

```go
type Policy struct {
    AllowedActions       []ActionKind
    AllowedTargets       []WindowRef   // trusted host selects identity-bound targets
    InputRegion          *Rect         // reject targets outside; never clamp
    ObservationRegions   []Rect
    AllowFieldValues     bool          // false by default; secure values always omitted
    AllowUnclassifiedUI  bool          // trusted opt-in, not a tool argument
    ObservationMode      ObservationMode // semantic_first by default; semantic_only forbids pixels
    AllowGeometryDiagnostics bool      // trusted developer projection; false for model output
    AllowedApplications  []ApplicationIdentity // trusted phase 2 installed identities; model sees AppRef
    MaxActionsPerMinute  int
    MaxImageBytesSession int64
    MaxRetainedBytes     int64
    Approval             ApprovalProvider // trusted host callback / control channel
    DryRun               bool
}
```

### 6.1 Policy scope and enforcement

- Sessions default to observation-only within explicitly granted semantic target scopes. Image scope is separately granted and unused on the semantic path. A trusted host/operator must grant action classes and identity-bound target scopes before input. Approved narrow scopes allow routine actions without repeated prompts; the model cannot broaden them or change observation mode.
- Apply target policy to **all** mutation: focus, click, type, shortcuts, scroll, drag, and future window management. Keyboard destination checks are mandatory even when no mouse coordinates are present. Title globs may help discovery, but do not authorize a process.
- Resolve point targets against the live authorized window and reject out-of-region, occluded, cross-display, or ambiguous targets. Validate every step of a drag. Never clamp an invalid action into a different valid click.
- Observation policy covers screenshots, windows, AX values/text, full/delta/unchanged state, retained snapshots/frames, historical retrieval, future OCR, and resource fetches independently of input policy. Redact before canonicalization, comparison, retention, and serialization; hidden-value changes must not leak through a changed flag, delta, or state-ID rotation. No field text or image payloads in ordinary logs/errors.
- Secure fields are masked in authorized captures where reliably detected and omitted from AX output. If classification is unavailable/incomplete, report it and deny restricted capture/input unless a trusted host has explicitly granted the broader scope. AX masking is not a guarantee that all secrets can be discovered.
- Apply quotas both per session and across the writer/host policy lifetime. Opening new sessions or using a stateless transport must not reset global limits. Validate bounds before allocating memory or entering the native layer.

### 6.2 Trusted approval authority

An approval challenge is a request to a **trusted host/operator**, not a bearer token returned to the model for self-approval.

1. Normalize and validate an immutable action, target identity, observation generation, and policy version. Compute the exact approval payload.
2. Invoke the trusted `ApprovalProvider`, or return a non-authorizing `pending_approval` challenge ID for a separately authenticated host control channel. Tool arguments cannot grant approval.
3. Only the trusted authority can approve or refuse. Approval is single-use and expires; bind it to principal, session, action ID, normalized parameters, target identity/context, and policy version. Pending challenges also have TTL/count limits.
4. Revalidate context and policy before consuming approval and injecting input. Changed context, modified parameters, conflicting reuse, or replay requires rejection/re-observation. A retry of an already completed action returns its cached outcome, not another injection.

The CLI may ask the operator on a TTY; unattended JSON mode returns `approval_required` unless a trusted preconfigured policy/provider authorizes the exact scope. The library supplies the approval callback. MCP uses host authorization outside model-controlled tools; it exposes no model-callable approval tool or `confirm` bearer parameter. HTTP, when added, needs a separate authenticated control scope and principal-bound approvals.

### 6.3 Operational controls

Rate limits return typed errors, never indefinite waits. Dry-run validates policy and resolves actual current targets but returns a proposed action; it cannot predict future application state or mark an action as verified. Normal close releases input and native resources. Permission loss, desktop lock, and unexpected ownership/focus changes stop injection and return an explicit failure/partial outcome.

---

## 7. CLI

Initial CLI surface:

```
comuse doctor [--json]
comuse setup
comuse state [--json] [--ledger]
comuse windows [--json]
comuse shot [-r x,y,w,h] [--max-w 1280] [-q 70] [--format jpeg|png] [-o out.jpg]
comuse a11y --window-id <id> [--depth 6] --json
comuse click x,y --window-id <id> [--button left|right|middle] [--count 2]
comuse type "text" --window-id <id> [--delay 12ms] [--clear]
comuse key cmd+shift+t --window-id <id>
comuse scroll --dy 3 --at 800,450 --window-id <id>
comuse drag 100,200 500,400 --window-id <id> [--steps 20]
comuse wait window-appears --title "Save*" [--timeout 5s]
comuse mcp --transport stdio
```

The coordinate commands above are developer low-level facilities. The default model MCP surface uses the semantic read/write/action tools in §8.2. Phase 2 adds `comuse apps --json` and `comuse open-app --app-id <installed-id> [--activate] [--timeout 5s]`; the one-shot discovery ID selects a fresh identity-checked candidate under a trusted launch profile, not an authorization token or a cross-session `AppRef`.

`--json` is global: output uses the shared envelope, with explicit observation payloads for image/list/tree results. Stdout contains results only; logs go to stderr. Images are written to a requested file or emitted only through an explicit image output option. Do not present raw base64 text as a token-efficiency feature.

Session-local references cannot be reused by unrelated one-shot CLI invocations. `comuse windows` includes a native discovery ID alongside its session reference. One-shot commands use `--window-id` to resolve a fresh process/window identity and `WindowRef`, then enforce a trusted CLI profile's permitted applications/window constraints. The discovery ID selects a candidate; it is not authorization, and a reused ID must not bypass identity/policy checks. A one-shot `a11y` emits a fresh full JSON snapshot; it cannot accept a baseline or element ID from an earlier invocation. Diffs, historical reads, and element actions initially use persistent library or MCP sessions, including CLI-launched `comuse mcp`. Future scripts can maintain a session; they must preserve the same core lifetimes rather than serialize native handles to disk.

Exit codes: 0 successful operation, 1 execution/observation failure, 2 policy/approval refusal, 3 backend/capability unavailable, 4 desktop busy. JSON still reports applied/partial/unknown execution and cleanup independently of exit code. Sensitive text passed in command arguments may be visible in shell history/process inspection; provide stdin/file text input and prefer those paths in examples and documentation.

`comuse run` is deferred until the core behavior is qualified. It will initially use sequential, line-based actions and waits; no embedded programming language is required.

---

## 8. MCP Server

### 8.1 Transports

- **stdio, v0.1:** newline-delimited JSON-RPC through the official Go SDK. Initialize/negotiation and capability declarations follow the selected SDK/spec version. Logs go to stderr, never stdout.
- **Streamable HTTP, deferred:** one MCP endpoint accepts POST; GET provides SSE or returns the specified 405 when not offered. SSE is a response content type, not an HTTP “upgrade.” Follow request Accept/content types, notification/response 202 handling, initialization, negotiated `MCP-Protocol-Version`, and session lifecycle rules for the pinned protocol version.
- HTTP defaults to `127.0.0.1:9911`, with an explicit IPv6 loopback option. Reject wildcard/non-loopback binds without `--allow-remote`. Validate Origin according to the MCP transport specification and a configured allowlist; absence from a non-browser client does not bypass authentication. Require authenticated callers for HTTP, TLS for remote access, request-size limits, and principal-bound sessions, approvals, quotas, and resources. Never treat `Mcp-Session-Id` as authentication.
- A future stateless HTTP mode may omit transport session IDs, but must still preserve principal/desktop policy and quotas. It cannot reset ownership, approve actions implicitly, or advertise diff/element references requiring retained state. Implement stateful mode first.

### 8.2 Tool surface

| Tool | Initial params | Returns | Phase |
|---|---|---|---|
| `computer_state` | — | state and capability status | v0.1 |
| `computer_screenshot` | `window_ref`, optional `element_ref` with `state_id`, image budget | explicit image block; Comuse computes authorized crop, host keeps transforms | phase 2 qualified fallback |
| `computer_windows` | bounded list options | authorized window references | v0.1 |
| `computer_a11y` | `window_ref`, text budget, `mode: auto/full/stored`, `since?`, `state_id?` | §4.5 JSON snapshot/delta/unchanged; explicit historical marker for stored state | v0.1 semantic foundation |
| `computer_read_element` | `window_ref`, `element_ref`, `state_id`, text budget | fresh permitted text/value or explicit unavailable/refused status | phase 1, qualified readable targets |
| `computer_write_element` | `action_id`, `window_ref`, `element_ref`, `state_id`, `text`, `mode: replace/insert` | execution and readback verification status | phase 1, qualified writable targets |
| `computer_scroll_element` | `action_id`, `window_ref`, `element_ref`, `state_id`, bounded direction/amount | execution envelope; internal placement and scroll conversion | phase 1, qualified scrollable targets |
| `computer_wait` | bounded `until`, `timeout` | observed condition, not action success | v0.1 |
| `computer_click_element` | `action_id`, `window_ref`, `element_ref`, `state_id` | validated execution envelope and execution method | v0.1 semantic foundation, after identity qualification |
| `computer_element_action` | `action_id`, `window_ref`, `element_ref`, `state_id`, advertised action kind | validated execution envelope | v0.1, qualified native actions only |
| `computer_apps` | bounded inventory options | authorized installed application names/references | phase 2 / v0.2 |
| `computer_open_app` | `action_id`, `app_ref`, `activate?`, readiness timeout | launch outcome, readiness status, eligible window reference when available | phase 2 / v0.2 |
| Pixel diff / `computer_text` | separately qualified contracts | optional image/OCR observations | deferred |

No image is returned by mutating tools. A host requests `computer_screenshot` explicitly; `semantic_only` sessions reject it and do not advertise image capability. The host must have a vision-capable consumer to interpret fallback images; Comuse does not choose a model or invoke hidden vision/OCR work. Semantic diffs use `computer_a11y` with `since`, not a separate pixel-diff tool. Tool descriptions remain short; detailed documentation belongs in server instructions/resources.

No default model tool takes screen coordinates, pixel crop rectangles, free-form selectors, arbitrary app paths, or raw window-focused typing. Read/write tools bind the field explicitly. Low-level library/CLI coordinate facilities remain available for developers, but are not automatically advertised as a recovery route when semantic targeting fails. Visual-only tasks lacking a validated target have an explicit support limit. This keeps the LLM's responsibility at choosing the content and operation; Comuse owns input mechanics.

Use MCP `structuredContent` and an output schema for the envelope, with discriminated snapshot/delta/unchanged schemas and compatible JSON text serialization as required by the selected version. A host should avoid adding redundant copies of structured and text output to model context. Return tool execution failures with `isError: true`; malformed requests use the appropriate protocol error. Core typed errors include `policy_refused`, `approval_required`, `element_stale`, `state_expired`, `permission_denied`, `unsupported`, `backend_unavailable`, `desktop_busy`, `rate_limited`, and `budget_exceeded`, plus phase 2 `app_stale`/`launch_failed` outcomes. Reject contradictory mode/baseline/historical parameters during input validation.

Use `notifications/cancelled` for cancellation and progress notifications only under the negotiated protocol's progress-token contract. Native operations and cleanup honor bounded deadlines; cancellation does not erase an action that already posted input.

Images use standard base64 MCP image blocks. Future references must use the standard `resource_link` form and define authenticated retrieval, session/owner checks, expiry, and memory caps. References do not by themselves reduce vision-model token cost or guarantee removal of base64 from `resources/read`.

### 8.3 Concurrency model

One exclusive writer session owns a desktop across Comuse processes (§5.2). All input and cleanup are serialized by that owner. Read-only sessions remain separately authorized and bounded.

Capture coalescing is allowed only for compatible display, region, generation, policy/redaction, and freshness requirements. Cancellation of one subscriber must not cancel others or return a different requested region. Start with serial capture and explicit buffer ownership; add coalescing or pooling only after profiling and correctness tests. “Last-wins” must never silently change another request's result.

---

## 9. OCR (optional integration)

OCR is deferred. A future integration may explicitly opt into an external executable, with separate process timeout, input/output caps, temp-file permissions/cleanup, and observation policy. Do not automatically enable OCR merely because a binary appears on PATH, use OCR as a hidden verification fallback, or imply the optional mode meets the no-third-party-runtime-tools default.

Expose an optional tool only when configured and available. Embedded OCR/model runtimes require their own accuracy, distribution, resource, and dependency review.

---

## 10. Performance Engineering

### 10.1 Initial targets and measurement

| Operation | Initial aspiration | Measurement boundary |
|---|---|---|
| On-demand capture + authorized redaction + downscale + JPEG at 1280×800 max | warm p99 ≤150 ms | complete observation pipeline; cold capture separate |
| Simple click / key event posting | native posting overhead <10 ms | excludes intentional hold/delay, policy, focus lookup, state/readback |
| Bounded AX observation | measure baseline before setting a p99 gate | include timeout/truncation and app identity |
| Semantic diff / host reconstruction | measure baseline before setting a p99 gate | fresh AX read, canonicalization, comparison, retention, serialization, and deterministic host apply |
| Full CLI/MCP action round-trip | measure baseline before setting a gate | policy + native action + optional verification + state + serialization |
| Startup / permission checks | report cold and warm separately | include native/framework initialization |

These are proposed targets, not established performance or guarantees. Record machine/architecture, OS version, selected display scale, workload, image policy, sample count, cold/warm distinction, failures, and p50/p95/p99. Long typing, drags, waits, and application postconditions cannot satisfy a universal sub-10 ms action promise.

### 10.2 Techniques

- Start with ScreenCaptureKit's on-demand screenshot path and standard Go JPEG/PNG encoders. `CGDisplayStream` is deprecated and is not the selected backend.
- Normalize native pixel format and own native buffers explicitly. Add copies when required for correctness; profile before pursuing zero-copy.
- Bound concurrent work and retained memory. Pools are optional optimizations, not ownership/lifetime mechanisms; `sync.Pool` is not a retention or security boundary.
- Add an explicit benchmark harness during v0.1 implementation. Only adopt persistent capture, pooling, capture coalescing, or alternate encoders when measured improvements justify complexity.
- Do not assume JPEG is safely accelerated by independent row-strip encoding. A valid replacement encoder needs correctness/quality tests and measured benefit before adoption.
- Use controlled Macs for performance qualification. Shared or virtual CI can verify correctness/builds; unstable runner p99s are not absolute release gates.

---

## 11. Cost Ledger

Each session records actual toolkit usage:

```json
{
  "session": "s_1",
  "actions": 14,
  "observations": {"screenshot": 3, "a11y": 5, "state": 14},
  "semantic_results": {"snapshot": 2, "delta": 2, "unchanged": 1},
  "baseline_resets": 1,
  "encoded_image_bytes": 412000,
  "serialized_text_bytes": 9600,
  "images": 3,
  "retained_bytes": 8192,
  "elapsed_ms": 38000,
  "model_usage": null
}
```

Expose the ledger through the library, `comuse state --ledger`, and an authorized MCP resource. Distinguish serialized text bytes, encoded image bytes, estimates, and host-reported actual model usage. Provider estimates require model/version/detail and sizing assumptions; actual usage must identify its source. The toolkit cannot infer model completion tokens or repeated-context cost from tool output alone.

Acceptance compares the **same tasks, model/version/detail, host context policy, and success criteria** across full semantic snapshots on each observation, initial snapshot plus semantic diffs/recovery, and a screenshot-after-action baseline. Qualify semantic-only completion separately from semantic-first tasks requiring explicit images. Include all model requests, failed attempts, retries, full-state checkpoints after compaction, latency, AX/diff/reconstruction overhead, and completed/failed tasks. Record reset/fallback rates, retention peaks, actual input/output/image usage where available, and cost per completed task. No “80%” or “5–10×” saving is accepted without evidence; smaller JSON or JPEG bytes alone do not demonstrate savings.

---

## 12. Security & Privacy

- **Threat model:** the model/tool client may be prompt-injected or malicious; screen/AX content is untrusted. Other applications or a user may change focus. Comuse policy/approval authority lives in trusted host code/configuration outside tool arguments.
- **Mandatory safeguards:** enforced action and observation scopes; identity-bound windows; exclusive writer ownership; bounded operations/resources; trusted approvals; held-input cleanup; explicit partial/unknown results; output redaction before serialization/caching.
- **Protected contexts:** input into terminals, secure fields, or unclassified contexts requires a trusted scope/approval decision. App titles and missing AX roles cannot prove a context is safe. Native permissions grant capability, not application-level authorization.
- **Privacy:** no telemetry or content logging. AX field values are omitted by default; secure values are always omitted. Semantic snapshots/deltas can contain sensitive text and receive the same policy checks, redaction, retention bounds, and revocation handling as other observations. Semantic-only sessions acquire no pixels. Capture classification limitations are explicit, with policy-approved broader capture required when restricted redaction cannot be established. Source pixels are transient; retained semantic state is redacted, session-scoped, and bounded.
- **Transport:** initial MCP is local stdio. Future HTTP enforces Origin validation, authentication, loopback defaults, TLS for remote binding, and identity-bound resources (§8).
- **Privilege posture:** no elevation, TCC edits, permission bypasses, or automatic third-party utility fallbacks. Comuse itself initiates no telemetry/network egress in the default local mode. Authorized desktop input may cause controlled applications to send data; policy must account for that consequence.
- **Audit:** record action IDs, policy/approval decisions, target identity references, execution/verification/cleanup outcomes, and timings without text payloads, screen contents, tokens, or secret values.

---

## 13. Testing & CI

### 13.1 Core and bridge verification

- Fake-backend tests cover policy on **every** mutation/read path, default-deny scopes, trusted approval binding/expiry/replay, quotas, result semantics, stale window/element identity, and cancellation at every input step.
- Concurrency tests prove a second process cannot obtain desktop writer ownership and that composites cannot interleave. Include cleanup failure, rejected replay, and loss of post-action state.
- Golden/synthetic image tests cover transforms, Retina/downscale/crop mapping, redaction ordering, JPEG/PNG attempt/byte/pixel caps, and incompatible capture requests. Bound text-tree traversal and explicitly report truncation.
- Semantic contract tests prove that applying a delta to its exact baseline reconstructs the fresh normalized snapshot, including property omission, additions/removals, focus, text, geometry, reparenting, and child ordering. Cover unchanged state, skipped responses, stale/foreign baselines, duplicate/out-of-order application, schema mismatch, partial/concurrently changing trees, size-based resets, and TTL/count/byte eviction. Historical retrieval must return the original state or `state_expired`, never a substituted live state.
- Model-contract tests reject coordinate and implicit-focus write parameters, prove default state omits geometry, and exercise explicit element read/replace/insert, target movement, focus changes, caret ambiguity, stale edits, protected values, unsupported targets, and unavailable verification. Geometry changes appear only in the separately scoped diagnostic projection. Phase 2 tests cover authorized inventory, installed identity drift, launch/activation admission, already-running apps, readiness timeouts, replay/cancellation, and independent window scopes.
- Privacy/mode tests prove redaction precedes diffing and retention, hidden-value changes do not leak through deltas/IDs, revocation prevents historical reads, and semantic-only observation/input/waits never invoke capture or require Screen Recording. Host reconstruction, compaction recovery, and duplicate MCP content handling are integration checks, not LLM-memory assumptions.
- Native tests cover Swift/Apple-object ownership, ABI version/argument/status validation, Go callback-handle lifetimes, pixel formats/stride, callback cancellation/completion races, main-thread dispatch, and repeated open/capture/close. Core unit tests cannot prove these properties.
- CLI/SDK integration tests cover stdin/stdout isolation, schemas, output/execution status, capability negotiation, and cancellation. HTTP security/protocol tests are prerequisites for adding HTTP, not v0.1 requirements.

### 13.2 macOS release acceptance

Release qualification uses controlled real Macs with an unlocked GUI session and controlled fixture applications. CI build/unit success alone does not establish desktop-control readiness.

1. Build and test the selected Apple Silicon deployment target. Record the actual macOS versions/machines exercised; Intel support requires separate native evidence.
2. Prove Accessibility grant, denial, revocation, restart, locked desktop, sleep/wake, and signed update behavior for both CLI and a real stdio host. Demonstrate semantic-only tasks without Screen Recording. Separately qualify Screen Recording and explicit screenshot fallback under semantic-first policy. Verify the actual permission-owning executable/bundle identity.
3. Complete fixture scenarios: observe a full JSON state; act through a live-validated element reference; reconstruct subsequent state from diffs; focus a permitted window; type Unicode and shortcuts; scroll, drag, wait, and verify actual postconditions. Exercise a lost response, baseline eviction, model-context reset, and original-state retrieval. Demonstrate explicit visual fallback on selected AX-insufficient tasks without adding images to ordinary semantic tasks. Record applied/partial/unknown and verification status accurately.
4. Prove denied-window/region actions, focus switches, a competing writer process, canceled held input, revoked permissions, and budget violations cannot silently perform an unauthorized or replayed action.
5. Exercise repeated sessions/actions and capture stress; record memory/descriptor/native-object growth, cleanup, CPU, and failure rates. Establish explicit soak duration/count and resource thresholds from the baseline before declaring stable.
6. Measure §10 latency and §11 task-cost comparisons with completion criteria. Publish the workload, sample sizes, observed limitations, and reproduction commands.
7. Pass independent review of the exact implementation revision and signed-artifact acceptance. No unresolved high-severity correctness/security findings; local tests, CI, signed distribution, and real-desktop evidence are reported separately.

### 13.3 Implementation sequence

The next task is a bounded macOS feasibility spike: build/link the Go and Swift targets; validate C ABI and callback/cancellation lifetimes; prove AX text/control/geometry reads, identity/focus, supported element actions, Unicode/key input, permissions, main-thread integration, and signed identity under CLI/stdio launch. Prove semantic-only operation independently of Screen Recording; test capture separately for the optional fallback. Use those results to finalize deployment target, element reconciliation, one-shot CLI identity/policy handling, snapshot/reference retention limits, and measurable release thresholds.

Implement in dependency order, preserving the mandatory safety baseline at every mutating stage:

1. Phase 1: full semantic JSON snapshots without model geometry, bounded state/reference retention, and qualified element reads/writes/actions through the core, library, CLI full-state path, and persistent MCP adapter. Comuse owns focus, geometry, and input delivery; no model-coordinate recovery path.
2. Deterministic semantic diffs, unchanged responses, original-state retrieval, host reconstruction guidance, and full-state recovery. Establish equivalence with fresh snapshots before measuring efficiency.
3. Phase 2: opening authorized installed applications with bounded launch/readiness results, plus explicit authorized screenshot fallback with image budgets, internal coordinate metadata, mode enforcement, and separate qualification. Both depend on the phase 1 semantic foundation; app opening also works in semantic-only mode.
4. Signed release, real-host fixtures/soak, comparative cost/latency evidence, documented application limitations, and independent review.

Do not start deferred platforms/features to compensate for an unqualified native foundation. Do not advertise element/diff/image capability before its stage is qualified.

---

## 14. Roadmap

| Phase | Scope and exit gate |
|---|---|
| **Phase 1 / v0.1 macOS semantic foundation** | Apple Silicon/macOS 14+ feasibility evidence; Go core + Swift backend; CLI full-state and persistent library/MCP stdio; coordinate-free model snapshots and qualified element reads/writes/actions first, then semantic diffs/unchanged/history recovery; work with already running authorized apps; complete safety baseline; benchmark and fixture harness. Exit: reconstruction equivalence, identity/staleness/read/write correctness, semantic-only permission evidence, and explicit ownership/cleanup evidence. |
| **Phase 2 / v0.2 app opening, stable macOS, and explicit fallback** | Authorized installed-app discovery/opening, live application identity, activation and bounded launch/readiness handling; separately qualified bounded JPEG/PNG fallback in semantic-first mode; signed/notarized distribution and upgrade/permission qualification; measured soak/resource/latency thresholds; real-host E2E and model-cost comparisons; optional Intel qualification. Exit: §13 release acceptance, phase 2 app-opening qualification, and independent review. |
| **v0.3 optional adapters/features** | Authenticated streamable HTTP, bounded scripts, pixel-diff history, OCR, or resource links only as separate evidence-backed slices. None is required to call the local macOS release stable. |
| **v1.0** | Stable public Go API and documented compatibility after proven macOS use. Additional platforms remain separately scoped and qualified. |
| **Future** | Linux X11/Wayland, Windows, multi-display, remote backends, embedded OCR, or alternate capture/encode optimizations based on demonstrated demand. |

No mutating release may defer the mandatory safety baseline under “guardrails complete later.”

---

## 15. Open Questions

The former review blockers have design decisions above: trusted host approval, one desktop writer, cancellation cleanup, native element identity, actual task-cost measurement, and a macOS-first backend. Remaining questions require a feasibility spike or benchmark:

1. **Native integration:** can the Go/cgo-to-Swift C ABI meet capture, main-thread, callback, cancellation, and lifetime needs cleanly for embedded Go and CLI/MCP launches? Demonstrate this before adding bridge abstractions or a helper process.
2. **Distribution/permissions:** confirm stable permission ownership for the signed bundle and real host launch path. Decide the development versus release setup instructions from that evidence.
3. **Release coverage:** proposed baseline is macOS 14+ Apple Silicon. Which OS versions and machines can be qualified now? Advertise only tested coverage and explicitly label unqualified combinations.
4. **Resource/performance thresholds:** what traversal, retained-state/reference count/byte/TTL, and observation limits preserve usable semantic snapshots, reconstruction, completion rate, and bounded memory? Measure image legibility separately; make no absolute latency or savings promises before qualification.
5. **Element references and coverage:** determine the native semantic fingerprint, stable-ID reconciliation, supported actions, and concurrent-mutation detection during macOS AX qualification. Positional paths are excluded as authoritative identity. Which application/UI families expose enough state for semantic-only completion? Publish tested limitations.
6. **CLI persistence:** qualify the selected `--window-id` discovery selector and trusted policy profile under one-shot launches. Future persistent scripts must preserve reference lifetimes rather than pretend a prior session's reference remains valid.
7. **Future Wayland:** if pursued, scope supported compositors and portal RemoteDesktop/ScreenCast capabilities explicitly. Screenshot access alone does not establish authorized input; unavailable capabilities return typed errors. No automatic `ydotool`/`grim` fallback.

---

## 16. Appendix A: Key Type Reference

Illustrative types; finalize during implementation without claiming this RFC is compiling source:

```go
// Logical coordinates within the selected display, top-left origin.
type Point struct{ X, Y float64 }
type Rect struct{ X, Y, W, H float64 }
type Button string // left | right | middle
type Key string    // validated keys mapped using the active keyboard layout
type Format string // jpeg | png

type WindowRef string  // opaque session reference bound to process/window identity
type ElementRef string // opaque native identity; stable only while confidently reconciled
type StateID string    // immutable redacted semantic state in one session/scope
type ScopeID string    // window/display/selector/budget/policy identity
type ObservationMode string // semantic_first | semantic_only; trusted policy
type ElementActionKind string // qualified advertised operations only
type WriteMode string // replace | insert; explicit element target, never implicit focus
type AppRef string // phase 2 opaque reference bound to authorized installed identity
type OpenApplication struct { Application AppRef; Activate bool; ReadinessTimeout time.Duration }

type ScrollDelta struct { DX, DY int } // explicit logical wheel steps; positive DY = down
type TextBudget struct { MaxDepth, MaxNodes, MaxBytes int; Timeout time.Duration }
type SnapshotBudget struct { MaxCount, MaxReferences int; MaxBytes int64; TTL time.Duration }
type ObserveRequest struct {
    Window WindowRef
    Budget TextBudget
    Mode string // auto | full | stored; request mode differs from trusted ObservationMode
    Since StateID // auto only; empty means a fresh full snapshot
    State StateID // stored only; historical lookup, never a fresh read
}
type CaptureRequest struct { DisplayID string; Region Rect; Budget ImageBudget }
type Condition struct { Kind string; Window WindowRef; Region *Rect; Timeout time.Duration }
```

Native smooth-scroll conversion and keyboard layout behavior are backend responsibilities with qualification tests. Session references are neither global identifiers nor approval capabilities. Every retained reference has count/byte bounds and expiry; session close invalidates it.

---

## 17. Appendix B: MCP Tool Schema Example

Primary element-click tool; policy and approval are host-controlled:

```json
{
  "name": "computer_click_element",
  "description": "Click a live-validated element in an authorized window. Returns execution status and compact state.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "action_id": {"type": "string", "minLength": 1, "maxLength": 128},
      "window_ref": {"type": "string", "minLength": 1},
      "element_ref": {"type": "string", "minLength": 1},
      "state_id": {"type": "string", "minLength": 1}
    },
    "required": ["action_id", "window_ref", "element_ref", "state_id"],
    "additionalProperties": false
  }
}
```

The core checks that the expected state belongs to the session/window/scope and included the element, then validates the live target and policy before execution (§4.4). If the expected state expired, require a fresh observation rather than trusting the ID. No `confirm`, `preapprove`, `include_image`, coordinates, or unbounded alternative target is accepted through this tool. Read/write/scroll schemas use the same explicit element/state binding with their bounded semantic parameters. Low-level coordinate commands remain library/CLI developer facilities; do not mix them into the model tool schema.

For a separate snapshot containing a qualified writable field `e13`, the model can request replacement directly:

```json
{
  "tool": "computer_write_element",
  "arguments": {
    "action_id": "a_write_1",
    "window_ref": "win_7",
    "element_ref": "e13",
    "state_id": "s_field_1",
    "text": "quarterly report",
    "mode": "replace"
  }
}
```

Comuse owns focus, replacement mechanics, and any readback. The corresponding `computer_read_element` uses the same window/element/state binding plus a text budget and does not inject input. Neither operation accepts coordinates.

---

## 18. Appendix C: Example Agent Loop and Acceptance Comparison

A representative future task is “open Settings, change theme to dark”:

1. Discover an authorized window and request a full semantic JSON snapshot without coordinates. In phase 2, first open an authorized installed app if needed, then use the eligible returned window reference. The host retains its state ID and projection.
2. Request a qualified element action using the window, element reference, and expected state ID; Comuse validates the live target and authority.
3. Wait for a bounded semantic condition; do not equate a posted action with success.
4. Observe with `since` set to the host's retained state ID. Apply eligible diffs atomically; replace state on an explicit recovery snapshot. Supply a full projection when model context has been reset.
5. If AX evidence is insufficient, report that limitation. A semantic-first host may explicitly request an authorized crop/screenshot and use a vision-capable model; semantic-only stops with an explicit limitation. No automatic image/OCR path is implied.
6. Request a final fresh observation and evaluate the actual task postcondition. A diff or unchanged response alone does not establish success.

This is a proposed scenario, not an executed benchmark. The acceptance harness compares full semantic snapshots, semantic diffs with recovery, and a screenshot-after-action baseline under the same model/task/host conditions (§11), reporting successful completion, retries, reset/fallback rates, latency, and actual model usage. No fixed image/token total or savings multiplier is claimed in advance.

---

## 19. References

Primary references for the design (API availability, toolchain/SDK versions, and support claims must be verified during the spike):

- [Apple ScreenCaptureKit](https://developer.apple.com/documentation/screencapturekit) and [SCScreenshotManager](https://developer.apple.com/documentation/screencapturekit/scscreenshotmanager).
- [Apple CGDisplayStream](https://developer.apple.com/documentation/coregraphics/cgdisplaystream) — deprecated; not selected for the new backend.
- [Apple Accessibility AXUIElement](https://developer.apple.com/documentation/applicationservices/axuielement) and [AXIsProcessTrusted](https://developer.apple.com/documentation/applicationservices/1460720-axisprocesstrusted).
- [Apple accessibility model](https://developer.apple.com/library/archive/documentation/Accessibility/Conceptual/AccessibilityMacOSX/OSXAXmodel.html) — semantic hierarchy, properties, actions, and application-provided accessibility support.
- [Apple NSWorkspace application opening](https://developer.apple.com/documentation/appkit/nsworkspace/openapplication(at:configuration:completionhandler:)) — phase 2 native installed-app launch; running status is separate from accessible-window readiness.
- [JSON Patch (RFC 6902)](https://datatracker.ietf.org/doc/html/rfc6902) — an alternative generic patch format; §4.5 selects complete semantic record replacement instead.
- [Apple notarization guidance](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution).
- [Swift 6.3 C interoperability](https://www.swift.org/blog/swift-6.3-released/) — supported C exports and header-validated implementations.
- [Swift ABI stability on Apple platforms](https://www.swift.org/blog/abi-stability-and-more/) — runtime/distribution background, not proof of deployment compatibility for newly used features.
- [Official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk).
- [MCP 2025-06-18 transports](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports), [cancellation](https://modelcontextprotocol.io/specification/2025-06-18/basic/utilities/cancellation), and [tools/results](https://modelcontextprotocol.io/specification/2025-06-18/server/tools). These are the review baseline; implementation pins and qualifies its actual supported protocol version.
- [Go image/jpeg](https://pkg.go.dev/image/jpeg), [image/png](https://pkg.go.dev/image/png), and [cgo](https://pkg.go.dev/cmd/cgo).
- [OpenAI image token/cost accounting](https://developers.openai.com/api/docs/guides/images-vision#calculating-costs) — an example provider model; other providers require their own adapters/evidence.
- [XDG Desktop Portal RemoteDesktop](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.RemoteDesktop.html) — future platform research only.

---

*End of RFC. This document records the open-source semantic-first toolkit, JSON snapshot/diff contract, Go core, and Swift macOS backend design. Implementation, performance, cost savings, and signed-release acceptance require the evidence defined above.*
