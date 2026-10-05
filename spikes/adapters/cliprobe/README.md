# cliprobe read-only CLI

This source candidate targets macOS with cgo. It exposes `hello`, non-prompting `doctor`, fixture-scoped `windows`, and fixture-scoped `a11y`. It emits one bounded JSON envelope on stdout and diagnostics on stderr. `a11y` resolves the configured fixture window within the same runtime session; opaque references are process/runtime-local and are not promised to survive another invocation. More than one matching fixture window is an ambiguity error.

The only source of native library path and fixture identity is an owner-only trusted config file, for example:

```json
{"library_path":"/absolute/path/to/libComuseBridge.dylib","fixture":{"pid":1234,"bundle_id":"com.sirerun.comuse.fixture","nonce":"fixture-nonce"}}
```

Pass it with `--config /absolute/path/to/config.json`. CLI arguments cannot change PID, bundle ID, nonce, or library path. `--window-index 0` is required for `a11y`; `--include-values` is opt-in and passes only the native/semantic fixture allowlist. Secure values remain omitted. The CLI does not expose native JSON, approvals, input, or app launch.

This candidate does not establish Accessibility trust, native fixture launch, permission-grant, live AX, or GUI acceptance. `doctor` reports current AX and event-posting permission without prompting. Input remains compile-closed and is not routed by this CLI.
