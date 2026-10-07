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

Native source has since been integrated; the historical check failures below remain evidence, not current passing receipts. Full Go, Swift, race, lint and independent integrated review remain pending. The first targeted check passed internal/writer, internal/backend and internal/jsonwire, but failed core and MCP; it is not an integrated passing receipt. Shared lease was released after the check. Hosted CI remains unavailable due to account billing.

A second targeted adapter check at 4825336 passed backend/jsonwire but failed MCP no-argument decoding and its expected sanitized error code. Fix e2c4ed7 accepts omitted/null arguments only for no-argument tools and aligns the test with the shared backend_unavailable error. Both remain pending rerun. No failing check has been reported as passing.

## Native review after first source integration

Independent immutable-source review of 09191d2 identified permission-loss cache invalidation, UTF-8 byte bounds, cancellation marker retention, and whole-envelope frame bounds as blockers. The native author is fixing these with source regressions. Process-main validation before library activation and resolved-image identity were also raised by the coordinator. Source image pinning was restored in 5564a04; later identity/activation fixes remain pending.

Local checks: Go at 5564a04 passed native lifecycle/wire, writer, backend, JSON and spike packages, but failed core dirty-close retry, MCP read/cancellation fixtures and CLI test compilation. Core dirty-close fix 874a2a1 and CLI test fix 8f3bcf8 are integrated. MCP typed result schemas, framing cap and fixture/cancellation fixes are integrated in 971bd5f. All need rerun. Swift compile diagnostics have been returned to the native author; no Swift test pass or native acceptance is claimed.

## Current source review and pending checks

At immutable `a1a7b5283465bd53326928acaf58c0fba9d7eb9e`, independent nonauthor reviews found no new foundation blocker in core permission epochs, durable replay, explicit reads of normal disabled controls, CLI fixture identity, strict decoding, native bounds/lifecycle, or MCP framing/schema paths. Review partitions excluded each reviewer's own changes. These are source reviews, not execution receipts.

Permission/cache fixes `2e6cbaf`, `67531e3` and `a1a7b52` prevent stale snapshots and action dispatch after an observed permission revocation, including revocation followed by restoration during blocked observation or approval. Explicit text reads remain separate from write eligibility. Native traversal and window enumeration are bounded before materialization (`5ac6b4f`, `97a3c5d`, `27e2a53`).

The last full Go check at `2e6cbaf` passed native/backend/JSON/writer/spike packages but failed a journal fixture after Close scrubbing, a CLI read fixture, and an MCP refresh expectation. Corresponding source fixes are integrated; no passing rerun is claimed. Production Swift compilation has no passing receipt yet. Current-head Go/Swift attempts have been held by the shared one-minute load threshold of 10, before any build begins.

Controlled native smoke source (`5cd9d14`, `fb3db7f`) has independent lifecycle review. It is opt-in, pins the initial thread, validates the controlled fixture identity, tests Doctor callbacks and cancellation/drain/close, rejects a copied second image, and reopens the original image. Uncertain cleanup retains the library. No smoke execution or fixture launch is claimed here.

Phase 1 source parity remains incomplete: full response metadata/accounting, action inventory/adapters, semantic scroll, disabled native actions, canonical desktop exclusion and durable cross-session minute quotas remain planned. Current per-journal locking and per-session action caps do not qualify desktop-wide mutation authority. Default input routes remain closed; runtime fixture acceptance remains a separate gate.

## 2026-10-07 source convergence update

The historical pending checks and planned source gaps above describe their recorded revisions. Foundation PR #9 landed98b5b81 after independent exact-head review and passing local Go/Swift/race/lint and landed checks. Frozen continuation contract PR #10 landed2a481a8 after independent schema review and exact-tree verification.

Current unmerged parity source includes shared canonical envelopes/accounting, strict17-operation requests, official SDK and CLI routes, semantic scroll and complete wait/raw inventory, protected canonical desktop exclusion/durable minute quota/replay/recovery, pure diff/history and common inspected-context DTO. These remain source candidates, not full Phase1 delivery or GUI qualification.

Portable independent review of153ec59 requested changes for PP-R1-F01: revoked Accessibility could appear as an empty successful window enumeration and falsely satisfy window_closed. Regression failed atc25ba07 and correction6e473e4 requires fresh permission/context evidence. Exact6e473e4 passes full Linux Go race, vet, lint, cgo-disabled checks and35 actual-envelope vectors against the normative schema. Semantic reconstruction fuzz passes325674 executions in16seconds. Actual Apple default synthetic fixtures20 and qualification fixtures21 pass; macOS14 arm64 release target compilation passes. Darwin CLI/backend/seam checks also pass under the shared lease. These are local source receipts, not hosted CI or native GUI/input acceptance.

Native display/focus producer and private generation guards are in a separate author lane. Affected common context/permission independent review is queued; full integrated final review, guarded merge and landed checks remain required. Public stored/auto/since integration is intentionally gated until E2 lands. Literal default production input stays disabled, and no signing or release is claimed.

## Native remediation convergence and current independent review

Draft PR #11 publishes source candidate36ba79e from landed contract2a481a8. Common Go context review CP-R3 independently approves frozen63e38bb and resolves all four CP-R1 findings, independently reproducing six prior red regressions and preserving PP-R1-F01. Common source remains unchanged in the native remediation wave.

Native review NP-R1 requested five source fixes: actual focused control identity, fresh coordinate hit proof, AX process/launch identity before disclosure, unknown title handling and strict closed ABI JSON. Author796e464 and coordinator corrections address these plus stable inspected US keyboard layout/modifier order, explicit per-kind zero/empty fields and CF runtime type guards before AXValue geometry/selection access. Production native input remains literalfalse. The actual Apple compiler/test failures and fixes are recorded in the E2 plan; no failing receipt is discarded or described as passing.

Exact tested source d9d7e1a passes Apple default33 and qualification34 synthetic fixtures, arm64 macOS14 target release compilation, Darwin CLI/backend/seam tests and full Mac Go race. Host is macOS26.6.2, so the target compile does not prove minimumOS runtime behavior. Original Go1.27.1 full Linux race/vet/lint0/cgo0 and39 actual-envelope schema vectors pass. Plan-only commits preserve identical tested native/Go/contracts source.

Native rereview T2.79 is queued at frozen36ba79e. It is not yet approved. Full candidate T2.26/27, guarded merge and landed checks remain open. Public E3 observation modes remain gated on E2 landing; Phase2 and qualification source planning follow their explicit predecessor gates. No native events, GUI/input acceptance, hosted CI pass, signing, release publication or deployment is claimed.
