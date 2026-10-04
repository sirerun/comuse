# Comuse spike seam v0

This contract is experimental and exists only for bounded feasibility probes. It does not implement or claim a usable computer-use backend. ABI version 1 exports the functions in `spikes/macos/bridge/include/comuse_spike.h`; the dynamic library product is `BridgeProbe`.

## Request and response

The only implemented operation is `hello`. Requests are UTF-8 JSON objects with `schema_version: 1`, a nonempty `request_id`, and `op: "hello"`. The request limit is 32 KiB. A response is a UTF-8 JSON envelope containing `schema_version`, the same `request_id`, `status`, and either `result` or `error`. The response limit is 64 KiB. Unknown operations, malformed JSON, and unsupported schema versions fail before a handle is allocated. This operation demonstrates callback delivery only; it does no AppKit, accessibility, screen capture, or input work.

## Buffer and callback ownership

`request_start` borrows request bytes only for that call and copies/decode them before returning. The native registry allocates a nonzero opaque handle and retains the callback and opaque 64-bit callback token until drain succeeds. The token is an integer identity such as a Go `runtime/cgo.Handle`; it must never be a Go pointer. Completion fires exactly once. Response bytes are borrowed from Swift and valid only until the callback returns; a consumer must copy them during the callback. Swift releases the buffer after callback return.

`request_cancel` races completion under the registry lock. If cancellation wins before completion is committed, the one terminal envelope has status `cancelled`; if completion already committed, cancellation leaves that result intact. `request_drain` succeeds only after terminal callback return and then releases retained native callback state. Unknown handles, early drain, and duplicate drain return explicit statuses. The Go client keeps its `cgo.Handle` alive through callback and successful drain, then deletes it. Dynamic library close follows drain.

## Bounds and support

The initial client has a five-second smoke deadline. Native input and output are bounded as above, with at most 64 outstanding request handles. There is no GUI prompt or permission request. The dylib loader is available only on macOS with cgo enabled; other targets report unsupported. The package declares macOS 14 as its compile deployment baseline. Runtime behavior on macOS 14, signing, alternate launch paths, callback cancellation stress, and all AX/TCC behavior remain unqualified until the controlled-host verification lane records them.

## Future operation boundary

The general session protocol remains versioned `open`, `request`, `cancel`, `drain`, and `close`. Each request has a retained native handle, one terminal completion, cancellation signaling, and drain before callback state or native handles are released. This hello implementation provides a minimal request/cancel/drain instance. Session opening/closing, AX object ownership, main-thread dispatch policy, and application-facing fixture JSON are not implemented here. Swift/native objects never cross the C ABI; only integers, byte spans, status values, and the C callback cross it.

## Pins observed for this candidate

The host reports Go 1.27.1 and Swift 6.4 on arm64 macOS 26.6.2 with Xcode 27.0 (build 27A266a). `go.mod` requires Go 1.27.1 and SwiftPM declares tools 6.0 plus a macOS 14 deployment target. No third-party Go or Swift package dependencies are required. Foundation JSON and Dispatch are the only native APIs in this probe. The official Go MCP SDK v1.8.0 remains a candidate for the later MCP adapter check; this seam does not depend on it. These are build declarations, not evidence for a tested macOS 14 runtime or broad deployment support.
