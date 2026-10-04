# RFC 0001: Compuse — A macOS-First Computer Use Toolkit with Go and Swift

- **Status:** Draft — revised design; implementation and platform qualification pending
- **Version:** 0.3
- **Last updated:** 2026-10-03

---

## 1. Abstract

Compuse is a Go toolkit for **computer use** — programmatic observation and control of a desktop environment: screenshots, mouse, keyboard, window state, and accessibility introspection. It ships as:

1. **A core Go library** (`compuse`) — the single source of truth for policy, action scheduling, observations, and accounting.
2. **A CLI** (`compuse`) — scriptable, pipe-friendly, with JSON results for agents without MCP support.
3. **An MCP server** (`compuse mcp`) — the same core capabilities over **stdio** initially; streamable HTTP after the local macOS release is qualified.

The first release targets **macOS 14+ on Apple Silicon**. Intel macOS support requires its own qualification before being advertised. Linux and Windows are future backends, not v1 acceptance requirements. The backend boundary preserves portability without requiring three platform implementations before a useful release.

The design prioritizes **correct input delivery, stable permission handling, bounded observations, and measurable task cost**. Returning fewer images is a hypothesis about efficiency; it must preserve task completion and be demonstrated with real model usage.

---

## 2. Motivation

Existing computer-use tooling can suffer from:

- **Unnecessary observations.** Returning a full screenshot after every action can consume model context and increase latency when compact state would suffice.
- **Poor observability.** Window identity, focus, cursor position, and accessibility state can answer questions without an image.
- **Unreliable control.** Focus changes, stale element paths, coordinate scaling, and interleaved sessions can send input to the wrong destination.
- **Deployment friction.** A collection of shell utilities and language runtimes is harder to install and qualify than one packaged tool using native OS APIs.

Compuse's thesis: a library-first tool with explicit desktop ownership and observation policy can complete desktop tasks reliably while reducing unnecessary observation cost.

Base64 image payloads belong in image content blocks, not ordinary text prompts. Transport bytes and billable image tokens are separate quantities. Neither JPEG byte reduction nor image count alone proves a particular reduction in model cost (§11).

### Goals

- G1: A working, stable macOS release before adding other operating systems.
- G2: Bounded image and text observations, with reported usage and truncation.
- G3: Measured latency and bounded resource use; performance aspirations are not release claims until qualified (§10).
- G4: One distributable macOS package with no required third-party runtime tools. Apple frameworks and OS services remain system dependencies; the executable is **not fully static**.
- G5: MCP stdio using the official Go SDK and an explicitly negotiated protocol version. Qualify the selected version; do not claim open-ended “2025-06-18+” compliance.
- G6: Library-first: CLI and MCP are thin adapters.
- G7: Mandatory policy enforcement, trusted approval authority, desktop ownership, cancellation cleanup, and explicit partial-result semantics from v0.1.

### Non-goals for the first stable macOS release

- N1: Linux, Windows, Wayland, VNC/RDP, mobile, VR, gamepad, or pen backends.
- N2: OCR, WebP encoding, diff-history, image resource references, or parallel JPEG encoding.
- N3: Autonomous agent logic — Compuse is a toolbox, not an agent.
- N4: Multi-display control. The initial session selects the primary display; secondary-display targets and windows crossing its bounds are rejected explicitly.
- N5: Full containment of a malicious desktop application or another physical user. Compuse enforces its own policy; strong isolation requires a dedicated desktop or OS sandbox.

---

## 3. Architecture

```
                       compuse core (Go)
       Session · Desktop ownership · Policy/approval · Results
       Observation budgets · Image pipeline · Cost ledger
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
compuse/
├── compuse.go              // public Session, Do, Observe API
├── action.go               // action types and validation
├── obs.go                  // observation types and budgets
├── image.go                // crop, downscale, JPEG/PNG
├── a11y.go                 // bounded observations and protected-field detection
├── cost.go                 // actual tool usage; model estimates kept separate
├── guard.go                // policy, approval, quotas
├── desktop.go              // exclusive writer ownership and input scheduling
├── cmd/compuse/            // CLI and stdio entrypoint
├── mcp/                    // official Go SDK adapter
├── internal/backend/
│   ├── backend.go          // contract and fake backend
│   └── darwin/
│       ├── backend.go      // Go backend adapter and error translation
│       └── bridge.go       // cgo calls into the native C ABI
├── native/macos/
│   ├── Package.swift       // native backend build definition
│   ├── include/compuse.h   // versioned C ABI: handles, buffers, results
│   └── Sources/CompuseMacOS/
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

`Capabilities` reports availability and reasons, including capture, accessibility, input, and window operations. Permission denial, unsupported operation, locked desktop, and backend failure are distinct errors. Missing capabilities must never silently select a shell utility or less restricted backend. Window movement/resizing is deferred until its macOS behavior can be qualified separately.

`WindowRef` binds the window to process identity (PID plus process-start identity), application identity, and a session-generated reference. Window titles are display metadata, not authorization identity. Recheck identity and eligibility before use; do not treat a recycled numeric window ID as the same target.

`Frame` owns its buffer until explicit `Close()`. Close is idempotent. The bridge normalizes native pixel format/stride and retains native storage until any Go encoding finishes. A canceled request cannot free pixels still used by an encoder. Do not rely on finalizers for timely release or assume arbitrary native pixels are Go `image.RGBA`.

### 3.3 Coordinates and scale factors

Public action coordinates are **logical points**, top-left origin within the selected primary display. Backend conversions must explicitly account for Apple's coordinate conventions, display origin, and scale. Never clamp invalid input into a different valid target.

Every image result includes `display_id`, `display_generation`, `source_bounds` in logical points, encoded `width`/`height` in pixels, capture timestamp, and output-to-logical transforms. For source bounds `(x,y,w,h)` and encoded dimensions `(W,H)`, an output pixel center `(u+0.5,v+0.5)` maps to `(x+(u+0.5)*w/W, y+(v+0.5)*h/H)`. Input targets use that logical position. Native display scale and post-encoding scale are separate metadata.

Display topology, resolution, or scale changes invalidate prior coordinate metadata and element snapshots. Agents must re-observe. Test Retina and non-Retina scaling, crops, downscaling, and display changes; initial support remains primary-display-only even when other displays are connected.

### 3.4 macOS permissions and distribution

- Minimum proposed OS: macOS 14, enabling ScreenCaptureKit's screenshot API. The implementation spike must verify the API availability and build deployment target against the Apple SDK before committing support claims.
- Start with `SCScreenshotManager` for on-demand capture. Consider `SCStream` only after profiling shows a need for persistent capture; bound queue depth and stop streams on close, permission loss, lock, or sleep.
- `compuse doctor` reports Screen Recording, Accessibility, display/session availability, signing identity, and capability failures without injecting input. `compuse setup` is the explicit permission-request path; ordinary tool calls do not repeatedly prompt or attempt to alter TCC settings.
- Distributed releases use a signed, notarized macOS application bundle with stable identity and an embedded executable usable as the CLI/MCP entrypoint. No resident service is required initially. The permission-owning process and launch path must be proven under both direct CLI and stdio-host launch; do not assume the terminal's grants transfer.
- Development builds may require different permission grants. Distinguish development evidence from signed-release evidence, and document permission denial, revocation, update, restart, sleep/wake, and locked-screen behavior.
- No privilege elevation, permission-database edits, or hidden clipboard replacement. The supported execution context is an unlocked, logged-in GUI session.

---

## 4. Token Efficiency: The Observation Budget

Every observation has bounded work/output and reports usage. The first release defaults to compact state after actions; images remain explicit requests. This policy must be evaluated for task completion and total cost, including extra observations and retries.

### 4.1 Observation kinds and defaults

| Observation | Default output | Bound |
|---|---|---|
| `state` | Cursor, selected display, focused window identity, capabilities | bounded JSON; titles truncated |
| `windows` | Authorized visible windows and logical bounds | maximum entries and text bytes |
| `screenshot` | Authorized capture, JPEG/PNG plus transform metadata | pixel, byte, deadline, and memory limits |
| `region` | Authorized crop | same limits as screenshot |
| `a11y` | Roles, names, bounds, state; values omitted by default | depth, node, text-byte, and deadline limits |
| `text` | Optional OCR | deferred; separately authorized and bounded |

The default text budget is depth 6, 256 nodes, 16 KiB serialized text, and a bounded traversal deadline. Results report `truncated` and which bound stopped traversal. `text_tokens_est` is an estimate, never an enforceable provider-token guarantee. Exact model budgets require a host-supplied tokenizer/model adapter.

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

Diff-history is not part of v0.1 or the initial stable macOS acceptance gate. Before adding it, define session-scoped frame references, source display/region/generation, expiry, ownership, byte/count caps, and invalidation on display or policy changes. Never compare captures with incompatible source geometry or authorization.

A future unchanged response may be `{"changed":false}`, but pixel equality does **not** prove an action succeeded. Diff thresholds and ignoring flicker require task-level evaluation. Retained frames must be redacted according to policy and charged against explicit memory limits.

### 4.4 Accessibility observations and future element addressing

Bounded macOS AX observations are part of the initial backend, both for observation and safety checks. They are unavailable or incomplete in some applications; report that explicitly. Do not expose secure-field values, and omit ordinary text-field values unless the observation policy permits them.

Example observation:

```
window "Inbox — Mail" ref=win_7
  toolbar
    button "New Message" [12,52 120x28]
    searchfield "Search" [200,50 300x30] focused
```

Element-targeted mutation is deferred until it can be qualified. When introduced, return opaque `element_ref` values bound to session, observation generation, window/process identity, and a retained native element. Child-index paths such as `0/2/1` may be diagnostic metadata; they are never authoritative click targets.

Revalidate the native identity, role, eligibility, window, and relevant semantic fingerprint before execution. Resolve geometry immediately before injection or use a separately qualified native AX action. If identity is ambiguous, content changed, the reference expired, or the target disappeared, return `element_stale` and require re-observation. Reference expiry and generation checks supplement native identity; they do not replace it.

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
type TypeText struct { Window WindowRef; Text string; Delay time.Duration; Clear bool }
type PressKey struct { Window WindowRef; Keys []Key; Hold time.Duration }
type Scroll struct { Window WindowRef; Delta ScrollDelta; At Point }
type Drag struct { Window WindowRef; From, To Point; Button Button; Steps int; Duration time.Duration }
type Wait struct { Until Condition; Timeout time.Duration }
type FocusWindow struct { Window WindowRef }
```

Every input action specifies an authorized window target. Omitted targets may be resolved to the current focused window only when policy explicitly permits that mode; the result records the resolved identity. Validate mutually exclusive targets, key names, Unicode behavior, limits, durations, and scroll units before posting events. Text length, hold time, drag steps/duration, and waits have hard maximums.

### 5.2 Session, desktop ownership, and cancellation

A session owns backend resources, immutable policy, approved scopes, quotas, action results, and observation references. Input changes global desktop state, so session-local FIFO alone is insufficient.

- **One writer per desktop:** a writer session acquires an exclusive desktop ownership lease. Enforce it across Compuse processes for the same OS user/GUI desktop using a protected local ownership mechanism. A second writer receives `desktop_busy`; read-only sessions may observe within their policy. Do not rely on a Go mutex to exclude another process.
- **Ordering:** all input within the owner session passes through one FIFO scheduler. Composite actions hold ownership across focus validation, moves, key/button presses, duration, and cleanup. State observations report the associated action sequence; do not imply arbitrary concurrent captures are synchronized with input.
- **Focus:** revalidate the authorized process/window and actual destination immediately before each injection step. Stop if the target changes, becomes occluded/ineligible, or focus leaves the allowed target. External user input cannot be fully prevented; record the limitation and qualify unattended tasks on a dedicated desktop.
- **Partial actions:** input is not transactional. Once events are posted, failure can leave a partially applied action; no automatic rollback or retry is implied.
- **Held-input cleanup:** track every key/button pressed by the session. On success, error, cancellation, permission loss, and normal close, attempt matching releases with a fresh bounded cleanup context independent of the canceled request. Release the writer lease only after cleanup; if cleanup fails, mark the desktop state uncertain, refuse further input in the owner process, and require explicit operator recovery. Persist an ownership dirty marker before posting held input and clear it only after successful cleanup. A new owner finding a dirty marker refuses mutation until explicit operator recovery; crash-released OS locks alone do not prove the desktop is clean. Abrupt process termination may prevent cleanup; never claim crash-proof release.
- **Typing:** use CGEvent with documented Unicode/layout behavior. Do not silently paste via the clipboard. Read back only when AX and observation policy permit a meaningful comparison. For `Clear`, compare the expected complete value; for insertion, verify only when the prior value/selection and expected result are known. Unsupported/protected readback yields `unavailable`, not fabricated success.
- **Waiting:** `window_appears`, `window_closed`, and `pixel_changed` use bounded polling with a deadline. Future `element_exists` requires a valid observation reference. Waits do not hold pressed keys/buttons and do not bypass desktop ownership policy.
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
    "cursor": [914, 402],
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

---

## 6. Guardrails

Compuse can perform consequential real-world actions. The safety baseline is mandatory from the first mutating release and shared by library, CLI, and MCP.

```go
type Policy struct {
    AllowedActions       []ActionKind
    AllowedTargets       []WindowRef   // trusted host selects identity-bound targets
    InputRegion          *Rect         // reject targets outside; never clamp
    ObservationRegions   []Rect
    AllowFieldValues     bool          // false by default; secure values always omitted
    AllowUnclassifiedUI  bool          // trusted opt-in, not a tool argument
    MaxActionsPerMinute  int
    MaxImageBytesSession int64
    MaxRetainedBytes     int64
    Approval             ApprovalProvider // trusted host callback / control channel
    DryRun               bool
}
```

### 6.1 Policy scope and enforcement

- Sessions default to observation-only within explicitly granted capture scope. A trusted host/operator must grant action classes and identity-bound target scopes before input. Approved narrow scopes allow routine actions without repeated prompts; the model cannot broaden them.
- Apply target policy to **all** mutation: focus, click, type, shortcuts, scroll, drag, and future window management. Keyboard destination checks are mandatory even when no mouse coordinates are present. Title globs may help discovery, but do not authorize a process.
- Resolve point targets against the live authorized window and reject out-of-region, occluded, cross-display, or ambiguous targets. Validate every step of a drag. Never clamp an invalid action into a different valid click.
- Observation policy covers screenshots, windows, AX values, retained frames, future OCR, and resource fetches independently of input policy. No field text or image payloads in ordinary logs/errors.
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
compuse doctor [--json]
compuse setup
compuse state [--json] [--ledger]
compuse windows [--json]
compuse shot [-r x,y,w,h] [--max-w 1280] [-q 70] [--format jpeg|png] [-o out.jpg]
compuse a11y --window-id <id> [--depth 6]
compuse click x,y --window-id <id> [--button left|right|middle] [--count 2]
compuse type "text" --window-id <id> [--delay 12ms] [--clear]
compuse key cmd+shift+t --window-id <id>
compuse scroll --dy 3 --at 800,450 --window-id <id>
compuse drag 100,200 500,400 --window-id <id> [--steps 20]
compuse wait window-appears --title "Save*" [--timeout 5s]
compuse mcp --transport stdio
```

`--json` is global: output uses the shared envelope, with explicit observation payloads for image/list/tree results. Stdout contains results only; logs go to stderr. Images are written to a requested file or emitted only through an explicit image output option. Do not present raw base64 text as a token-efficiency feature.

Session-local references cannot be reused by unrelated one-shot CLI invocations. `compuse windows` includes a native discovery ID alongside its session reference. One-shot commands use `--window-id` to resolve a fresh process/window identity and `WindowRef`, then enforce a trusted CLI profile's permitted applications/window constraints. The discovery ID selects a candidate; it is not authorization, and a reused ID must not bypass identity/policy checks. Library and MCP calls use `WindowRef` within their persistent session. Future scripts can maintain a session; element clicks require that persistent context.

Exit codes: 0 successful operation, 1 execution/observation failure, 2 policy/approval refusal, 3 backend/capability unavailable, 4 desktop busy. JSON still reports applied/partial/unknown execution and cleanup independently of exit code. Sensitive text passed in command arguments may be visible in shell history/process inspection; provide stdin/file text input and prefer those paths in examples and documentation.

`compuse run` is deferred until the core behavior is qualified. It will initially use sequential, line-based actions and waits; no embedded programming language is required.

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
| `computer_screenshot` | `region?`, image budget | image block and transform/redaction metadata | v0.1 |
| `computer_windows` | bounded list options | authorized window references | v0.1 |
| `computer_a11y` | `window_ref`, text budget | bounded text/structured snapshot | v0.1 |
| `computer_click` | `action_id`, `window_ref`, `x,y`, `button`, `count?` | execution envelope | v0.1 |
| `computer_type` | `action_id`, `window_ref`, `text`, `clear?`, `delay?` | execution and verification status | v0.1 |
| `computer_press_key` | `action_id`, `window_ref`, `keys`, `hold?` | execution envelope | v0.1 |
| `computer_scroll` | `action_id`, `window_ref`, `dx,dy`, `at` | execution envelope | v0.1 |
| `computer_drag` | `action_id`, `window_ref`, `from,to`, `steps`, `duration?` | execution and cleanup status | v0.1 |
| `computer_wait` | bounded `until`, `timeout` | observed condition, not action success | v0.1 |
| `computer_click_element` | `action_id`, `element_ref` | execution envelope | later macOS release |
| `computer_diff`, `computer_text` | separately qualified contracts | optional observations | deferred |

No image is returned by mutating tools by default. A host requests `computer_screenshot` explicitly; automatic post-action images are deferred rather than adding an unbudgeted alternate route. Tool descriptions remain short; detailed documentation belongs in server instructions/resources.

Use MCP `structuredContent` and an output schema for the envelope, with compatible text serialization as required by the selected version. Return tool execution failures with `isError: true`; malformed requests use the appropriate protocol error. Core typed errors include `policy_refused`, `approval_required`, `element_stale`, `permission_denied`, `unsupported`, `backend_unavailable`, `desktop_busy`, `rate_limited`, and `budget_exceeded`.

Use `notifications/cancelled` for cancellation and progress notifications only under the negotiated protocol's progress-token contract. Native operations and cleanup honor bounded deadlines; cancellation does not erase an action that already posted input.

Images use standard base64 MCP image blocks. Future references must use the standard `resource_link` form and define authenticated retrieval, session/owner checks, expiry, and memory caps. References do not by themselves reduce vision-model token cost or guarantee removal of base64 from `resources/read`.

### 8.3 Concurrency model

One exclusive writer session owns a desktop across Compuse processes (§5.2). All input and cleanup are serialized by that owner. Read-only sessions remain separately authorized and bounded.

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
  "encoded_image_bytes": 412000,
  "serialized_text_bytes": 9600,
  "images": 3,
  "retained_bytes": 0,
  "elapsed_ms": 38000,
  "model_usage": null
}
```

Expose the ledger through the library, `compuse state --ledger`, and an authorized MCP resource. Distinguish serialized text bytes, encoded image bytes, estimates, and host-reported actual model usage. Provider estimates require model/version/detail and sizing assumptions; actual usage must identify its source. The toolkit cannot infer model completion tokens or repeated-context cost from tool output alone.

Acceptance compares the **same tasks, model/version/detail, host context policy, and success criteria** using explicit-observation defaults versus a screenshot-after-action baseline. Include all model requests, failed attempts, retries, extra observations, latency, and completed/failed tasks. Report actual input/output/image usage where the provider makes it available, task completion, and cost per completed task. No “80%” or “5–10×” saving is accepted without evidence; smaller JPEG bytes alone do not demonstrate savings.

---

## 12. Security & Privacy

- **Threat model:** the model/tool client may be prompt-injected or malicious; screen/AX content is untrusted. Other applications or a user may change focus. Compuse policy/approval authority lives in trusted host code/configuration outside tool arguments.
- **Mandatory safeguards:** enforced action and observation scopes; identity-bound windows; exclusive writer ownership; bounded operations/resources; trusted approvals; held-input cleanup; explicit partial/unknown results; output redaction before serialization/caching.
- **Protected contexts:** input into terminals, secure fields, or unclassified contexts requires a trusted scope/approval decision. App titles and missing AX roles cannot prove a context is safe. Native permissions grant capability, not application-level authorization.
- **Privacy:** no telemetry or content logging. AX values are omitted by default; secure values are always omitted. Capture classification limitations are explicit, with policy-approved broader capture required when restricted redaction cannot be established. Source pixels are transient, and future retained data must be redacted and bounded.
- **Transport:** initial MCP is local stdio. Future HTTP enforces Origin validation, authentication, loopback defaults, TLS for remote binding, and identity-bound resources (§8).
- **Privilege posture:** no elevation, TCC edits, permission bypasses, or automatic third-party utility fallbacks. Compuse itself initiates no telemetry/network egress in the default local mode. Authorized desktop input may cause controlled applications to send data; policy must account for that consequence.
- **Audit:** record action IDs, policy/approval decisions, target identity references, execution/verification/cleanup outcomes, and timings without text payloads, screen contents, tokens, or secret values.

---

## 13. Testing & CI

### 13.1 Core and bridge verification

- Fake-backend tests cover policy on **every** mutation/read path, default-deny scopes, trusted approval binding/expiry/replay, quotas, result semantics, stale window/element identity, and cancellation at every input step.
- Concurrency tests prove a second process cannot obtain desktop writer ownership and that composites cannot interleave. Include cleanup failure, rejected replay, and loss of post-action state.
- Golden/synthetic image tests cover transforms, Retina/downscale/crop mapping, redaction ordering, JPEG/PNG attempt/byte/pixel caps, and incompatible capture requests. Bound text-tree traversal and explicitly report truncation.
- Native tests cover Swift/Apple-object ownership, ABI version/argument/status validation, Go callback-handle lifetimes, pixel formats/stride, callback cancellation/completion races, main-thread dispatch, and repeated open/capture/close. Core unit tests cannot prove these properties.
- CLI/SDK integration tests cover stdin/stdout isolation, schemas, output/execution status, capability negotiation, and cancellation. HTTP security/protocol tests are prerequisites for adding HTTP, not v0.1 requirements.

### 13.2 macOS release acceptance

Release qualification uses controlled real Macs with an unlocked GUI session and controlled fixture applications. CI build/unit success alone does not establish desktop-control readiness.

1. Build and test the selected Apple Silicon deployment target. Record the actual macOS versions/machines exercised; Intel support requires separate native evidence.
2. Prove Screen Recording/Accessibility grant, denial, revocation, restart, locked desktop, sleep/wake, and signed update behavior for both CLI and a real stdio host. Verify the actual permission-owning executable/bundle identity.
3. Complete fixture scenarios: focus a permitted window, click, type Unicode and shortcuts, scroll, drag, wait, and observe the expected outcome. Record applied/partial/unknown and verification status accurately.
4. Prove denied-window/region actions, focus switches, a competing writer process, canceled held input, revoked permissions, and budget violations cannot silently perform an unauthorized or replayed action.
5. Exercise repeated sessions/actions and capture stress; record memory/descriptor/native-object growth, cleanup, CPU, and failure rates. Establish explicit soak duration/count and resource thresholds from the baseline before declaring stable.
6. Measure §10 latency and §11 task-cost comparisons with completion criteria. Publish the workload, sample sizes, observed limitations, and reproduction commands.
7. Pass independent review of the exact implementation revision and signed-artifact acceptance. No unresolved high-severity correctness/security findings; local tests, CI, signed distribution, and real-desktop evidence are reported separately.

### 13.3 Implementation sequence

The next task, after accepting this revised design, is a bounded macOS feasibility spike: build/link the Go and Swift targets; validate the C ABI and callback/cancellation lifetimes; prove native capture, Unicode/key input, AX identity/focus, permission checks, main-thread integration, and signed identity under CLI/stdio launch. Use those results to finalize deployment target, one-shot CLI identity/policy handling, resource limits, and measurable release thresholds. Then implement the core/policy lifecycle, adapters, and acceptance harness. Do not start deferred platforms/features to compensate for an unqualified native foundation.

---

## 14. Roadmap

| Phase | Scope and exit gate |
|---|---|
| **v0.1 macOS foundation** | Apple Silicon/macOS 14+ feasibility evidence; Go core + Swift backend; CLI/MCP stdio; bounded JPEG/PNG/state/AX observations; complete safety baseline; benchmark and fixture harness. Exit: core/native correctness and explicit permission/ownership/cleanup evidence. |
| **v0.2 stable macOS** | Signed/notarized distribution and upgrade/permission qualification; measured soak/resource/latency thresholds; real-host E2E; opaque element-targeted actions only after identity/staleness qualification; optional Intel qualification. Exit: §13 release acceptance and independent review. |
| **v0.3 optional adapters/features** | Authenticated streamable HTTP, bounded scripts, diff-history, OCR, or resource links only as separate evidence-backed slices. None is required to call the local macOS release stable. |
| **v1.0** | Stable public Go API and documented compatibility after proven macOS use. Additional platforms remain separately scoped and qualified. |
| **Future** | Linux X11/Wayland, Windows, multi-display, remote backends, embedded OCR, or alternate capture/encode optimizations based on demonstrated demand. |

No mutating release may defer the mandatory safety baseline under “guardrails complete later.”

---

## 15. Open Questions

The former review blockers have design decisions above: trusted host approval, one desktop writer, cancellation cleanup, native element identity, actual task-cost measurement, and a macOS-first backend. Remaining questions require a feasibility spike or benchmark:

1. **Native integration:** can the Go/cgo-to-Swift C ABI meet capture, main-thread, callback, cancellation, and lifetime needs cleanly for embedded Go and CLI/MCP launches? Demonstrate this before adding bridge abstractions or a helper process.
2. **Distribution/permissions:** confirm stable permission ownership for the signed bundle and real host launch path. Decide the development versus release setup instructions from that evidence.
3. **Release coverage:** proposed baseline is macOS 14+ Apple Silicon. Which OS versions and machines can be qualified now? Advertise only tested coverage and explicitly label unqualified combinations.
4. **Resource/performance thresholds:** what limits preserve small-text legibility, usable AX observations, completion rate, and bounded memory? Measure before making absolute latency or savings promises.
5. **Element references:** determine the native semantic fingerprint and invalidation rules during macOS AX qualification. Positional paths are excluded as authoritative identity.
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
type ElementRef string // opaque observation reference; future mutation target

type ScrollDelta struct { DX, DY int } // explicit logical wheel steps; positive DY = down
type TextBudget struct { MaxDepth, MaxNodes, MaxBytes int; Timeout time.Duration }
type CaptureRequest struct { DisplayID string; Region Rect; Budget ImageBudget }
type Condition struct { Kind string; Window WindowRef; Region *Rect; Timeout time.Duration }
```

Native smooth-scroll conversion and keyboard layout behavior are backend responsibilities with qualification tests. Session references are neither global identifiers nor approval capabilities. Every retained reference has count/byte bounds and expiry; session close invalidates it.

---

## 17. Appendix B: MCP Tool Schema Example

Initial coordinate-only tool; policy and approval are host-controlled:

```json
{
  "name": "computer_click",
  "description": "Click in an authorized window. Returns execution status and compact state.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "action_id": {"type": "string", "minLength": 1, "maxLength": 128},
      "window_ref": {"type": "string", "minLength": 1},
      "x": {"type": "number"},
      "y": {"type": "number"},
      "button": {"type": "string", "enum": ["left", "right", "middle"], "default": "left"},
      "count": {"type": "integer", "enum": [1, 2], "default": 1}
    },
    "required": ["action_id", "window_ref", "x", "y"],
    "additionalProperties": false
  }
}
```

The core rejects non-finite/out-of-bounds coordinates and validates the window/session/policy before injection. No `confirm`, `preapprove`, `include_image`, or unbounded alternative target is accepted through this tool. Future element clicks use a separate qualified tool schema rather than ambiguous mixed coordinate/path targets.

---

## 18. Appendix C: Example Agent Loop and Acceptance Comparison

A representative future task is “open Settings, change theme to dark”:

1. Observe authorized windows/state or bounded AX content.
2. Request an authorized click using the exact current window/target context.
3. Wait for the required window/condition; do not equate a posted click with success.
4. Re-observe before navigating changed UI; use coordinate transforms or, when qualified, opaque element identity.
5. Request a final observation and evaluate the actual task postcondition.

This is a proposed scenario, not an executed benchmark. The acceptance harness compares Compuse's explicit-observation policy with a screenshot-after-action baseline under the same model/task/host conditions (§11), reporting successful completion, retries, latency, and actual model usage. No fixed image/token total or savings multiplier is claimed in advance.

---

## 19. References

Primary references for the design (API availability, toolchain/SDK versions, and support claims must be verified during the spike):

- [Apple ScreenCaptureKit](https://developer.apple.com/documentation/screencapturekit) and [SCScreenshotManager](https://developer.apple.com/documentation/screencapturekit/scscreenshotmanager).
- [Apple CGDisplayStream](https://developer.apple.com/documentation/coregraphics/cgdisplaystream) — deprecated; not selected for the new backend.
- [Apple Accessibility AXUIElement](https://developer.apple.com/documentation/applicationservices/axuielement) and [AXIsProcessTrusted](https://developer.apple.com/documentation/applicationservices/1460720-axisprocesstrusted).
- [Apple notarization guidance](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution).
- [Swift 6.3 C interoperability](https://www.swift.org/blog/swift-6.3-released/) — supported C exports and header-validated implementations.
- [Swift ABI stability on Apple platforms](https://www.swift.org/blog/abi-stability-and-more/) — runtime/distribution background, not proof of deployment compatibility for newly used features.
- [Official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk).
- [MCP 2025-06-18 transports](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports), [cancellation](https://modelcontextprotocol.io/specification/2025-06-18/basic/utilities/cancellation), and [tools/results](https://modelcontextprotocol.io/specification/2025-06-18/server/tools). These are the review baseline; implementation pins and qualifies its actual supported protocol version.
- [Go image/jpeg](https://pkg.go.dev/image/jpeg), [image/png](https://pkg.go.dev/image/png), and [cgo](https://pkg.go.dev/cmd/cgo).
- [OpenAI image token/cost accounting](https://developers.openai.com/api/docs/guides/images-vision#calculating-costs) — an example provider model; other providers require their own adapters/evidence.
- [XDG Desktop Portal RemoteDesktop](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.RemoteDesktop.html) — future platform research only.

---

*End of RFC. This document records the Go core and Swift macOS backend design. Implementation, performance, cost savings, and signed-release acceptance require the evidence defined above.*
