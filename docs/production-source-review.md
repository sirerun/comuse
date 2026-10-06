# Production semantic source review

This record covers the E2 source integration after main `776e953eed2293db337b69457f71e127052af9a4`. It does not qualify native input, GUI acceptance, or minimum macOS runtime behavior.

## Findings and verification loop

| Finding | Evidence | Owner | Required fix and verification | Status |
| --- | --- | --- | --- | --- |
| Approval cancellation replay changes error code | Independent review of `c862dc7a63cbe70fe01f228352a4b6f3bc560724`, desktop.go approval path | Core | Persist the same cancellation/deadline code returned; retry the same action ID in regression tests; independent re-review | Open |
| Unknown outcome terminal record precedes durable dirty gate | Independent review of same head, desktop.go uncertain-result paths | Core | Persist dirty before terminal outcome; retain inflight/lock on save failure; inject failure then reopen/new action regression; independent re-review | Open |
| Core action fixture returns backend_unavailable | Targeted local check of same head, core_test.go:215 | Core | Diagnose private state-directory fixture permissions; rerun core tests without weakening production checks | Open |
| MCP test unused variable | Same targeted check, mcp/server_test.go:97 | MCP | Test-only fix integrated as d8103ea; rerun adapter checks | Fixed, verification pending |
| CLI refusal exit codes conflict with RFC | Coordinator inspection against RFC section 7 | CLI | Mapping corrected in 1df3b15; refusal/busy tests; independent review | Fixed, verification pending |
| MCP structured output lacks output schema | Coordinator independent source review of d8103ea | MCP | Publish schema matching current bounded envelope; tools/list and schema regressions; independent review | Open |

Native source preflight findings are being resolved before its first integrated commit. Full Go, Swift, race, lint and independent integrated review remain pending. The first targeted check passed internal/writer, internal/backend and internal/jsonwire, but failed core and MCP; it is not an integrated passing receipt. Shared lease was released after the check. Hosted CI remains unavailable due to account billing.
