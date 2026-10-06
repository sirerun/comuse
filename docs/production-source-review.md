# Production semantic source review

This record covers the E2 source integration after main `776e953eed2293db337b69457f71e127052af9a4`. It does not qualify native input, GUI acceptance, or minimum macOS runtime behavior.

## Findings and verification loop

| Finding | Evidence | Owner | Required fix and verification | Status |
| --- | --- | --- | --- | --- |
| Approval cancellation replay changes error code | Independent review of `c862dc7a63cbe70fe01f228352a4b6f3bc560724`, desktop.go approval path | Core | Persist the same cancellation/deadline code returned; same-ID replay regressions in 3353e3e; independent re-review | Fixed in 0ba9b1e, verification pending |
| Unknown outcome terminal record precedes durable dirty gate | Independent review of same head, desktop.go uncertain-result paths | Core | Persist dirty before terminal outcome; retain inflight/lock on save failure; chmod regression checks general write failure, not selective dirty-save ordering; independent ordering review required | Fixed in 0ba9b1e, verification pending |
| Core action fixture returns backend_unavailable | Targeted local check of same head, core_test.go:215 | Core | Use a private child state directory; rerun core tests without weakening production checks | Fixed in 0ba9b1e, verification pending |
| MCP test unused variable | Same targeted check, mcp/server_test.go:97 | MCP | Test-only fix integrated as d8103ea; rerun adapter checks | Fixed, verification pending |
| CLI refusal exit codes conflict with RFC | Coordinator inspection against RFC CLI exit contract | CLI | Mapping corrected in 1df3b15; refusal/busy tests; independent review | Fixed, verification pending |
| MCP structured output lacks output schema | Coordinator independent source review of d8103ea | MCP | Publish schema matching current bounded envelope; tools/list and schema regressions; independent review | Fixed in 4825336, verification pending |

Native source preflight findings are being resolved before its first integrated commit. Full Go, Swift, race, lint and independent integrated review remain pending. The first targeted check passed internal/writer, internal/backend and internal/jsonwire, but failed core and MCP; it is not an integrated passing receipt. Shared lease was released after the check. Hosted CI remains unavailable due to account billing.

A second targeted adapter check at 4825336 passed backend/jsonwire but failed MCP no-argument decoding and its expected sanitized error code. Fix e2c4ed7 accepts omitted/null arguments only for no-argument tools and aligns the test with the shared backend_unavailable error. Both remain pending rerun. No failing check has been reported as passing.

## Native review after first source integration

Independent immutable-source review of 09191d2 identified permission-loss cache invalidation, UTF-8 byte bounds, cancellation marker retention, and whole-envelope frame bounds as blockers. The native author is fixing these with source regressions. Process-main validation before library activation and resolved-image identity were also raised by the coordinator. Source image pinning was restored in 5564a04; later identity/activation fixes remain pending.

Local checks: Go at 5564a04 passed native lifecycle/wire, writer, backend, JSON and spike packages, but failed core dirty-close retry, MCP read/cancellation fixtures and CLI test compilation. Core dirty-close fix 874a2a1 and CLI test fix 8f3bcf8 are integrated. MCP typed result schemas, framing cap and fixture/cancellation fixes are integrated in 971bd5f. All need rerun. Swift compile diagnostics have been returned to the native author; no Swift test pass or native acceptance is claimed.
