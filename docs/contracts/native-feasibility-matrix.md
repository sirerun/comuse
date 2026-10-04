# Native feasibility matrix for E1

**Status:** Source-grounded design candidate. No fixture was built or launched, and no native accessibility or input call was made. This matrix informs T1.0 and the T1.6 operator gate; it does not claim implementation or platform qualification.

**Plan basis:** [E1 native feasibility](../plans/E1-native-feasibility.md), especially T1.0, T1.5, T1.6, T1.10–T1.13, T1.21–T1.23, and the acceptance gates. The architecture follows [RFC 0001](../rfc/0001-compuse-macos.md): Go owns policy/scheduling, Swift owns Apple API calls and Apple-object lifetime, with the versioned narrow C ABI as the only bridge.

## Current preflight snapshot

Read-only observations made on 2026-10-04:

| Item | Evidence | Meaning |
|---|---|---|
| Host | macOS 26.6.2, arm64 | One current Apple Silicon host only; it says nothing about macOS 14 runtime behavior. |
| Toolchain | Go 1.27.1; Swift 6.4; Xcode 27.0; SDK 27.0 | Inventory only. No build, deployment target, signing, or launch qualification was performed. |
| Build load | 1-minute load 14.16 at inspection | Above the repository's `>10` hold threshold. Do not run a multi-package Go/Swift build until load is rechecked and the shared build lease is held. |
| External work volume | Mounted, writable, ample free capacity | Native-prep worktree is on the external volume. Build/cache roots still need to be assigned by the execution lane. |
| Accessibility TCC | Read-only query of the TCC database returned `authorization denied`; no prompt was issued | Permission-owner state is unknown. A denial from this query is not evidence that the native process itself is denied or allowed. Resolve through the operator-owned T1.6 path. |
| Event-post TCC | Not inspected for the eventual native permission-owning process | Unknown. `AXIsProcessTrusted()` is not a substitute for `CGPreflightPostEventAccess()` when the probe posts Quartz events. |
| Signing identities | `security find-identity -v -p codesigning` reported two valid identities | Presence does not establish which identity owns the fixture/host, entitlements, Developer ID, notarization, or launch-path stability. T1.0/T1.6 must name the actual executable and permission owner. |
| Existing Comuse native runtime | None found in current source inventory | T1.10–T1.13 remain planned work, not an existing implementation. |

No GUI process was launched, no System Settings UI was opened, no TCC state was modified, and no input was injected. The root plan notes a four-slot harness, coordinator plus at most three workers per batch; the assigned slot count must be rechecked by the coordinator at T1.0.

## macOS operation matrix

Apple documents AXUIElement as a remote reference to an accessibility object, with calls that enumerate attributes/actions, read attributes, ask if an attribute is settable, set it, and perform an advertised action. Those APIs return `AXError`; they do not guarantee support or success in any particular app. `AXUIElementCopyAttributeValues` accepts an explicit index and maximum count, which supports bounded child enumeration. See [AXUIElement](https://developer.apple.com/documentation/applicationservices/axuielement), [AXUIElement.h](https://developer.apple.com/documentation/applicationservices/axuielement_h), [copy attribute values](https://developer.apple.com/documentation/applicationservices/1462060-axuielementcopyattributevalues), [set attribute value](https://developer.apple.com/documentation/applicationservices/1460434-axuielementsetattributevalue), [perform action](https://developer.apple.com/documentation/applicationservices/1462091-axuielementperformaction), and [AXError](https://developer.apple.com/documentation/applicationservices/axerror).

| Requested behavior | Apple surface | Bounded probe rule | Required outcome/evidence |
|---|---|---|---|
| Locate the controlled app and top-level window | `AXUIElementCreateApplication(pid)` then documented app/window attributes | Bind the PID to the launched fixture process and its stable bundle identity; never infer target from the frontmost app alone. Cap windows per process. | Return app/window identity, query errors, and count truncation. No global/system-wide tree walk. |
| Read semantic tree and text | Attribute names, values, children, role/subrole, title/description/value, enabled/focused state | Explicit maximum depth, nodes, children per node, string bytes, and deadline. Do not include geometry in the semantic projection. Do not query secure value. | Return `complete`, `partial`, `truncated`, or `unknown` coverage plus exact unsupported/missing/timeout errors. Never treat truncation as deletion. |
| Read larger child arrays | `AXUIElementCopyAttributeValues(element, attribute, index, maxValues, ...)` | Request a bounded page; advance only while budget remains. Avoid eager unbounded copies of large trees/tables. | Preserve stable ordering only where the source provides it; expose omitted range/count as incomplete coverage. |
| Identify a referenced element again | Re-query within the bound window/process and compare role, identifier/label, parent/window identity and available app-specific identity attributes | References are hints, not durable native handles. Revalidate just before every mutation. Window/element movement, removal or reparenting can invalidate assumptions. | Stale/ambiguous references fail closed with `stale`, `ambiguous`, or `unknown`; never fall back to another same-label element. |
| Focus a control | Check `AXFocused`/`AXFocusedWindow`; query settable state; set `AXFocused` only if supported | After request, re-read both target identity and focused element. Do not send text based only on a successful setter return. | Return requested/applied/verified separately; focus races are partial/unknown and stop further input. |
| Press a control | Query `AXUIElementCopyActionNames`; call `AXUIElementPerformAction` only for an advertised action such as `AXPress` | Revalidate app/window/element identity immediately before call. Limit probe controls to synthetic fixture buttons. | Record advertised action set, `AXError`, and fixture postcondition. Unsupported is explicit; never claim success from dispatch alone. |
| Replace or insert text | Query `AXValue` support/settable state and, if the fixture advertises them, selected range/text attributes; alternatively fixture-specific keyboard events | Treat every text method as app-dependent. `AXUIElementSetAttributeValue` can fail with unsupported, invalid-element, cannot-complete, or other errors. Do not silently switch from AX value mutation to keyboard events. Secure values are never read back or emitted. | Distinguish replace from insertion at a known selection. Unknown caret/selection, changed focus, or unavailable value is unsupported/ambiguous, not best effort. Verify only a non-sensitive fixture postcondition. |
| Secure text field | AppKit secure-text-field accessibility subrole | Include a synthetic secure field solely to prove that output and caches omit its value. If the subrole or sensitivity is uncertain, suppress value conservatively. Do not inject test secrets. | Only role/subrole and safe metadata; no `AXValue`, selected text, typed test content, snapshots, logs, or diff payload. |
| Scroll | AX scroll action/attributes when advertised; otherwise `CGEvent` scroll-wheel event | Probe AX support first. Coordinate/event fallback requires current fixture-window bounds and a fresh focus/target check; keep this fallback behind the integrated gate. | Record method, event/AX status, and visible synthetic sentinel transition. Never infer scroll completion from posting alone. |
| Synthetic key/text event | `CGEvent` creation, keyboard Unicode string, `post(tap:)` / targeted posting | Build events in isolated sink/unit tests first. Real posting is allowed only through integrated policy with exact fixture process/window/element, current focus, exclusive desktop writer, operator authorization, bounded sequence, cancellation and release/drain. | Posting means queued, not applied. Verify fixture state after event; if target/focus changes, stop and return partial/unknown. Maintain an owned-input ledger so cancellation/close releases held keys/buttons and marks dirty state when release cannot be proved. |
| Permission status | `AXIsProcessTrusted()` or `AXIsProcessTrustedWithOptions` for AX; `CGPreflightPostEventAccess()` for Quartz posting | Use prompt disabled for AX; use only `CGPreflightPostEventAccess()` and never `CGRequestPostEventAccess()` during preflight/doctor/probe. Both checks describe the calling process, so query in the exact permission-owning executable for each launch path. | Report AX trust and event-post access as separate trusted/untrusted/unknown fields with permission-owner identity. One does not imply the other. Preflight never grants/revokes permission or qualifies another launch path. |

`CGEvent` is a low-level Quartz event posted into the event stream; Apple documents the posting APIs and event locations. This does not bind a posted event to a semantic element, prove which app receives it, or prove the app applied it. See [CGEvent](https://developer.apple.com/documentation/coregraphics/cgevent), [CGEventTapLocation](https://developer.apple.com/documentation/coregraphics/cgeventtaplocation), and [Quartz Event Services](https://developer.apple.com/documentation/coregraphics/quartz-event-services). Avoid event taps in the spike unless a narrowly justified requirement appears; no listener is needed for the planned fixture probes, and the current plan does not authorize global input monitoring.

## T1.10–T1.13 bounded implementation handoff

These are candidate implementation boundaries only. Start only after the coordinator lands and freezes T1.5's seam. Respect each E1 owned-path list.

### T1.10 ABI/runtime — `L01`

- Implement the frozen request envelope and status mapping; do not add native APIs to the shared seam without coordinator review.
- Each request has a retained native request handle and exactly one terminal completion (`success`, `partial`, `unsupported`, `cancelled`, `failed`, or `unknown` as allowed by the frozen vocabulary). Define ownership for input/output buffers and callback context explicitly.
- Cancellation signals the native task; `drain` waits for terminal callbacks; `close` rejects new requests, cancels outstanding work, drains callbacks and only then releases state. A timeout is reported as unknown/dirty rather than freeing state still reachable by Swift.
- Build a non-GUI round-trip probe and lifetime/error table. Keep TCC/native GUI evidence out of this lane until T1.6/T1.31.

### T1.11 AX observation and identity — `L02`

- Keep a serial native AX executor and explicit timeout/deadline. Bound windows, nodes, depth, children, strings and total encoded bytes.
- Represent each external call as a typed result preserving `AXError`. Missing/unsupported attributes are not fatal to the whole observation; they lower per-node coverage. Messaging timeout/cannot-complete, invalid element, permission disabled and resource cap remain distinguishable.
- Produce stable references from the frozen ABI's process/window/element identity fields, but label their confidence and revalidate them on use. Never retain `AXUIElement` across requests as if it were durable identity.
- Do not query secure-field values. Do not capture pixels on the semantic path.

### T1.12 element and input operations — `L03`

- Start with a method capability query: advertised actions and settable attributes for the exact element. Press via advertised AX action; focus via checked setter plus independent re-read.
- Treat `AXValue` replacement and selected-text insertion as separate optional methods. A supported attribute is still not evidence of correct text semantics; require a synthetic fixture postcondition. No fallback between methods without policy and explicit result method.
- Keep keyboard/scroll event creation deterministic and test it against an in-process sink. The fixture-only actual-posting route must check process, window, element, focus and single-writer identity immediately before posting and after completion.
- If key-down/button-down is ever supported, ledger only inputs Comuse itself created. On every terminal path send releases, then re-query the fixture; inability to prove release marks the desktop dirty and refuses subsequent actions.
- No standalone worker executable may inject native input. T1.22's event-sink evidence is not actual injection evidence.

### T1.13 controlled fixture — `L04`

- Build a small **SwiftUI-first** macOS fixture using standard labelled controls, with a narrowly scoped AppKit representable only where the ordinary SwiftUI control cannot expose a deterministic AX role/subrole/action or scroll behavior required by the matrix. Keep the fixture visually plain; this is a test instrument, not product UI.
- Give the app a stable bundle identifier, deterministic window title, named accessibility identifiers/labels, and launch profiles for reproducible initial states. Record app and fixture revision in artifacts.
- Include only synthetic controls: static label; counter button with an inspectable count; editable field with known harmless seed text; empty secure field; scroll view with numbered sentinel rows; delayed label; removable/reinsertable child; and fixture-owned controls for deliberate focus/edit interference. Ensure interference is synthetic and reversible.
- Prefer deterministic internal state transitions (launch profile or fixture-owned debug controls) over timing races. Expose a machine-readable local fixture state only if T1.5 freezes that into the contract; otherwise keep postcondition checks to AX-observable synthetic state.
- Never store real user data, request a password, or persist entered text. Secure field stays empty. Do not take screenshots for semantic acceptance. Test launch/close and each transition through the visible synthetic fixture only after T1.6.

## T1.0 and T1.6 operator requirements

T1.0 is a read-only coordinator preflight: record clean-base/source inventory, actual worker slots, external capacity/writability and task-local cache roots, current host/toolchain/SDK, current load and lease availability, signing identity details relevant to this task, native executable paths and current permission state where readable. State `unknown` when macOS denies inspection. Do not change Settings, TCC databases, prompts, signing configuration, login/session lock state, or permissions.

T1.6 is a real operator gate before any fixture GUI launch used as evidence, AX observation, event posting, or grant/deny/revoke experiment. The operator must:

1. Provide an unlocked, logged-in GUI session and reserve it for serialized fixture verification, confirming no competing Comuse writer or automation owns the desktop.
2. Identify the exact executable/bundle that calls AX and posts events for each CLI/native/stdio launch path. Record signature/team/bundle identity and whether the launch path preserves that permission owner. Do not assume Terminal's grant authorizes a different executable, or that an app-bundle grant authorizes an unbundled test binary.
3. Explicitly authorize the AX grant, deny, and revoke scenarios named in E1, scoped to the synthetic fixture and the permission-owning executable. Before any Quartz event posting, separately record `CGPreflightPostEventAccess()` for that executable; treat its state independently from AX trust. The agent must not prompt, call a request API, edit TCC, toggle Settings, or infer permission from another process.
4. Identify fixture-only target window/control scope and approve the input methods/sequences, focus-interference scenario, cancellation/held-input cleanup, and stop conditions. No other app or user content is in scope.
5. Record start/end permission state and cleanup outcome, then release the GUI reservation. If a permission change, target identity, input release, or cleanup state cannot be verified, stop native verification and mark the result unknown.

**Gate decision:** Yes—fixture-only GUI use still needs the T1.6 operator gate for native evidence. A source-only build/design does not require the GUI gate, but an agent must not launch the fixture “just to inspect it” before the gate because app launch establishes the GUI target and can trigger permission behavior. The current discovery did not read TCC successfully, so permission status remains unknown.

## Open questions for T1.5 seam freeze

1. Does request input carry a single operation per request, or a bounded batch? Prefer one operation per request until the Go writer/policy contract proves ordered batches and per-step partial outcomes.
2. Which exact identity fields are frozen for process, window and element references? At minimum preserve process/bundle identity, window discriminator, role and semantic identifier/label where available, plus an explicit confidence/ambiguity outcome.
3. Are AX capability discovery and execution distinct request kinds? They should be: callers need to know whether the exact element currently advertises an action or settable property, then revalidate immediately before acting.
4. How does the ABI carry deadline/budget and coverage/errors without unbounded native-to-Go allocations? Require caller-supplied or capped output capacity with explicit required/truncated lengths.
5. Is a trusted permission check included as a non-prompting doctor operation? If so, it must report the permission-owning current process, never ask for access and never imply a GUI gate was completed.

## Sources

- Apple, [AXUIElement](https://developer.apple.com/documentation/applicationservices/axuielement)
- Apple, [AXUIElement.h](https://developer.apple.com/documentation/applicationservices/axuielement_h)
- Apple, [AXUIElementCopyAttributeValues](https://developer.apple.com/documentation/applicationservices/1462060-axuielementcopyattributevalues)
- Apple, [AXUIElementIsAttributeSettable](https://developer.apple.com/documentation/applicationservices/axuielement_h)
- Apple, [AXUIElementSetAttributeValue](https://developer.apple.com/documentation/applicationservices/1460434-axuielementsetattributevalue)
- Apple, [AXUIElementPerformAction](https://developer.apple.com/documentation/applicationservices/1462091-axuielementperformaction)
- Apple, [AXError](https://developer.apple.com/documentation/applicationservices/axerror)
- Apple, [AXIsProcessTrustedWithOptions](https://developer.apple.com/documentation/applicationservices/1459186-axisprocesstrustedwithoptions)
- Apple, [CGPreflightPostEventAccess](https://developer.apple.com/documentation/coregraphics/cgpreflightposteventaccess%28%29)
- Apple, [NSAccessibility secure text field subrole](https://developer.apple.com/documentation/appkit/nsaccessibility-swift.struct/subrole/securetextfield)
- Apple, [CGEvent](https://developer.apple.com/documentation/coregraphics/cgevent)
- Apple, [CGEventTapLocation](https://developer.apple.com/documentation/coregraphics/cgeventtaplocation)
- Apple, [Quartz Event Services](https://developer.apple.com/documentation/coregraphics/quartz-event-services)
