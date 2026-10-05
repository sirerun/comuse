# Read-only adapter spike contract

E1 adapter source consumes the assembled runtime and semantic probes. These adapters expose `hello`, non-prompting `doctor`, fixture-scoped `windows`, and fixture-scoped `a11y` only. They do not expose approval issuance, arbitrary raw native calls, input, app opening, screenshots, or model-controlled native library paths.

Trusted launch configuration supplies the absolute library path and exact fixture PID, bundle identifier and launch nonce. Tool requests can select the exposed read operation and whether permitted synthetic values are included; they cannot widen the configured scope. Invalid or missing configuration fails closed. References are observation/runtime-local; a one-shot CLI does not promise references usable by a future invocation.

The native runtime opens and pumps on the actual process main thread. A worker runs adapter handling while the main thread services the native run loop. Cancellation and shutdown must preserve callback/library ownership until drain; timeout is a truthful failure, never fabricated completion.

CLI emits one bounded JSON response on stdout, diagnostics on stderr, with distinct usage/unsupported/native error exits. MCP uses the official Go SDK v1.8.0 pinned by the coordinator, stdio only, `SupportedProtocolVersions: []string{"2025-06-18"}`. Protocol tests explicitly negotiate this baseline; latest SDK protocol support is not automatically a product claim. No unframed stdout diagnostics, remote HTTP, resources/prompts masquerading as tools, or model approval route.

Native envelopes retain complete/partial/error/cancelled distinctions. Semantic projection redacts before canonicalization, keeps native precondition and semantic comparison IDs distinct, and reports partial coverage without inferring absent elements. Tests use explicit injected read-only backends; they cannot establish native/TCC/CLI launch acceptance.
