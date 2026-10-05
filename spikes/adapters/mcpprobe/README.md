# Read-only MCP adapter spike

This spike wires the pinned official Go MCP SDK v1.8.0 to the existing fixture-only BridgeProbe through `bridgeclient`. It serves exactly `hello`, non-prompting `doctor`, `windows`, and `a11y` over stdio and permits only MCP protocol `2025-06-18`. There is no write tool, approval issuer, app opener, screenshot path, arbitrary native operation, or model-supplied PID, bundle, nonce, library path, process reference, or launch generation.

## Host startup

Build the executable on the macOS host using the repository's external artifact/cache root. The host process must launch it with trusted `--native-library`, `--fixture-pid`, `--fixture-bundle-id=com.sirerun.comuse.fixture`, `--fixture-nonce`, and `--process-launch-generation` arguments. The library path must be absolute; the nonce is 1–64 ASCII letters, digits, `.`, `_`, or `-`. Missing or invalid configuration fails before native library loading. The fixture PID and nonce must identify the already launched synthetic fixture.

`cmd/comuse-mcp` opens the bridge from the process main thread before starting MCP. The SDK stdio loop runs on a worker goroutine; the main goroutine pumps native callbacks every 5–15 ms until server shutdown. `bridgeclient.Close` drains on that same thread. A pump, cancellation, or drain failure is returned on stderr and never rewritten as successful completion. MCP protocol bytes alone go to stdout. This is an experimental host entry point and is not release packaging or TCC/GUI acceptance evidence.

## Tools and reference lifetime

`hello`, `doctor`, and `windows` take empty objects. `a11y` takes only `window_ref` from the one current complete, unambiguous `windows` response, plus optional `include_values`. The single current selection is bound to its MCP session; a new windows call in any session invalidates it. The adapter passes the trusted process scope and the observation's process-start reference to its backend; the native AX handler rechecks the process/bundle/fixture window before traversal. Opaque refs are runtime-local, expire natively, and are not promised to work after another process invocation. A partial or multiple-window result exposes no selectable refs.

`a11y` passes the native envelope through `semanticprobe`. Secure and unapproved values are stripped before canonical hashing; only the explicit synthetic text-field and counter allowlist can include values. Native precondition state and redacted semantic comparison state remain separate; partial snapshots carry neither state ID. Results are returned as one compact JSON text block. Inbound stdio JSON-RPC frames are capped at 64 KiB. The complete serialized MCP tool result is capped below that with 1 KiB framing reserve; oversized results become the explicit `result_limit` error.

## Evidence boundary

Tests use injected backends and SDK in-memory transports. They can verify schemas, negotiation, scope binding, cancellation forwarding, semantic projection, and response limits; they do not establish dynamic library loading, actual main-thread identity, native AX/TCC behavior, installed fixture state, or MCP host acceptance.
