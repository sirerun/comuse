# First native source batch contract

Status: Provisional fixture-only experiment based on the landed ABI v1 seam. This is not the public production API.

## Ownership and shared calls

The coordinator delegates `Seam.swift`, `Exports.swift`, `Runtime.swift`, the versioned C header and native package integration solely to the runtime lane, plus `spikes/bridgeclient/`. The AX lane owns `AccessibilityProbe.swift` and its named tests; the fixture lane owns `spikes/macos/fixture/`. Amendments require coordinator agreement before another owner changes a shared file.

The AX component provides `@MainActor func handleAccessibilityProbe(_ requestData: Data) -> Data`, returning a complete versioned JSON terminal envelope. The runtime calls this for `doctor`, `windows` and `a11y`; `hello` remains compatible. Later mutation routing is absent until the safety/admission integration gate. Additive C exports are `runtime_open(out_runtime)`, `runtime_pump(runtime, timeout_ms)`, and `runtime_close(runtime)`. Opening/pumping requires the actual native main thread, not merely any pinned Go goroutine. Queued cancelled work must be suppressed before invoking the AX handler. Closing stops admission and cancels/drains without destroying callback state that remains reachable.

The Go consumer is `spikes/bridgeclient`: `Open(path)`, `Call(ctx, requestJSON)`, `Pump(duration)` and `Close(ctx)`. A worker goroutine calls while the native owning main thread pumps. Synchronous waiting from the owning main thread is rejected. Request IDs and callback handles are validated; known completion wins late cancellation. Other platforms report unsupported. No pointer or Swift object crosses the C ABI.

## Fixture identity and observation

Fixture bundle ID is `com.sirerun.comuse.fixture`; launch requires `--fixture-nonce`. Its window title is `Comuse Fixture <nonce>`. Before any AX content read, requests must match the exact fixture process and scope `{pid, bundle_id, fixture_nonce}`. Other app content is denied. The AX owner additionally retains and freshly rechecks process/window/element identities; opaque references expose no native address. `resolveAccessibilityElement(ref, pid)` returns an AX element only after fresh identity checks, for later guarded input integration.

Requests carry `schema_version: 1`, `request_id`, `op` and explicit scope for content reads. Responses preserve the request ID and return status plus structured result or typed error. Default semantic output omits geometry and field values; optional synthetic normal-field reads require an explicit include-values request, and secure values are always omitted before retention/output. Bounds are fixed or tightened by the fixture probe: at most 256 nodes, depth 16, a 250 ms traversal budget, and the ABI's 64 KiB response ceiling. Coverage reports complete/truncated/unavailable, reasons and counts. Complete AX traversal does not prove complete visual or participant events.

The fixture has deterministic labels/identifiers and synthetic postconditions for button count, normal/secure text, scroll sentinels, delayed state, removable child and focus/edit interference. It uses a SwiftUI app shell and AppKit-backed controlled AX elements. Source/build evidence is separate from launch, AX grant/deny/revoke, event-post access, actual input, signing and minimum-OS runtime acceptance. No screenshot or live-stream content is involved.
