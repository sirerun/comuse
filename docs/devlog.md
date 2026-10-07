# Comuse devlog

## 2026-10-04 UTC — Parallel implementation planning

The user requested an implementation plan using many parallel GPT-6-Luna agents. Discovery at `871f3416abf052a73fdaa928ec23e194cd9e634a` found only the license, RFC v0.5 and vision; no implementation/CI or existing plan/ADR records were present. Three Luna agents supplied native, core and delivery planning inputs in isolated worktrees. The coordinator drafted the split plan, planned use-case catalog and delivery decision.

The current runtime has four active slots total, so execution has a three-worker fallback despite the target of ten coding workers plus coordinator. Local Go/Swift/Xcode tools and an external SSD were available; current load exceeded the build ceiling. Minimum macOS runtime, controlled GUI/TCC/signing authority and native support were not qualified. The code graph contained no source nodes, so discovery used bounded source-document inspection.

All implementation tasks remain pending. This run authored planning artifacts only, with no native builds, desktop control, execution admission, independent code review, merge, release, provider calls or deployment. Planning structure/dependency checks are recorded in the task-local validation evidence. Stable design stays in the RFC/design map, process proposal in ADR 0001 and assignments in the plan.

## 2026-10-04 — Ship execution begins

- Founder authorized implementation and ordinary review/merge through the ship workflow, using parallel GPT-6-Luna workers.
- Four runtime slots permit three worker lanes plus coordinator; the plan already specifies the batch schedule. Seam coding and two independent contract preparation lanes start first.
- External worktree storage was mounted, writable, with sufficient free capacity. The repository was clean and main matched the landed planning baseline.
- The shared one-minute load was above 10. Heavy build commands are held; source preparation continues. Native fixture operation and permissions remain unqualified until the operator gate.

- The operator authorized controlled fixture checks and offered to grant Accessibility to the exact Comuse test host. This provides GUI scope; actual permission and deny/revoke evidence are still pending.
- Independent native review found separate event-posting permission must be checked in addition to AX trust, and clarified approval state binding during expected composite input transitions. Both findings are accepted into the fixture safety contract.

- Accepted COMUSE-DOC-001 by aligning T1.13 with the SwiftUI shell and deterministic AppKit-backed AX controls; the coordinator recorded the fixture clarification in ADR 0001. Accepted COMUSE-DOC-002 by dating the stale planning handoff as historical context.

- Accepted residual review findings by aligning the safety fixture wording and making separate posting owner/status evidence explicit in T1.6/T1.31. False or unknown posting permission blocks affected native event scenarios; preflight never requests permission.

## 2026-10-04 — Greeter dependency discussion concluded

The Comuse and Greeter owners agreed at design level on the independently versioned Go library consumed by a trusted Greeter host, read-only adoption before qualified mutations, and the native/product ownership split. ADR 0002 records the contract and E1/E2/E6/live-platform gates. The discussion did not authorize Greeter implementation, deployment or public output. Comuse ship continues independently.

CI attempted PR #6 but the runner did not start: GitHub reported an account billing lock. No CI compile/test result exists. A local safe-load window permitted Swift compilation and exposed a test source type-inference failure. The test was repaired, and the author subsequently reported all three Swift lifecycle tests passing. This is local compile/link and current-host evidence, not minimum-OS runtime or CI evidence.

- Independent Luna native and contracts reviewers accepted SEAM-001 (terminal completion lost on a cancellation race) and SEAM-002 (assumed Swift dylib product directory). Stable remediation/verification/re-review tasks T1.36–T1.38 track the fixes. The CI path now uses Swift build-system resolution; the cancellation outcome fix is with the original author.

## 2026-10-04 — Corrected seam verification

At source candidate `4beb040ceeb04a7e4b88889744817ee17684b3c5`, the coordinator ran `go test ./spikes/seamprobe` (pass), `CGO_ENABLED=0 go test ./spikes/seamprobe` (pass), and the real Go CLI loading the preserved native dylib (completed hello JSON). Build caches/artifacts stayed on external storage; the shared lease was released immediately afterward. Swift source/tests are unchanged from the author's three-test passing candidate, built on the current macOS 26.6 host with a macOS 14 compilation target. The compiler target does not prove macOS 14 runtime support.

SEAM-001 now preserves a validated completed terminal response after a cancellation race; native cancelled, malformed, wrong-identity and oversized callbacks remain explicit outcomes. SEAM-002 resolves the Swift product directory through `--show-bin-path`. Independent contracts review at base `590fb6e` / head `4beb040` found no remaining blocking source/workflow issue, with its own authored safety proposal excluded from scope and covered by the independent native reviewer. Exact final documentation refresh review remains pending.

PR #6 is unmerged. GitHub's check annotation reports that the account billing lock prevented the job from starting. The merge skill treats failed checks as a blocker; no merge/landed, full native fixture acceptance, release or deployment evidence exists. T1.5-dependent coding lanes remain undispatched.

- Coordinator final Swift validation at source `5021d94` passed all four lifecycle/byte-boundary tests. The nonnil 32 KiB + 1 buffer was rejected before handle allocation, and valid JSON padded to exactly 32 KiB completed and drained. Native source is unchanged from the already successful mixed-runtime smoke. Final exact-head review receipts are recorded with PR #6; shipping remains blocked on failed CI/account access.

## Seam landing and maximum Luna source batch

The operator authorized the exact reviewed PR #6 merge using local evidence. GitHub rebase landed at `2973799`; target reachability and a zero-diff comparison with the reviewed candidate were observed. CI remains billing-blocked; this is an explicit seam-only exception. Three Luna lanes now implement native runtime, scoped AX observation and the controlled fixture. The source batch contract records additive runtime/main-thread integration and exclusive file ownership. Heavy builds remain subject to fresh host load and shared lease; actual native permission and fixture evidence are still pending.

## 2026-10-04 rolling source assembly

Three GPT-6-Luna slots progressed through runtime, AX, fixture, policy, compile-closed input, desktop replay and semantic projection; CLI/MCP follow while runtime findings are remediated. Secure projection and canonical/native state identities remain distinct. Persistent replay never silently recycles a retired action identifier, and timed-out cleanup retains writer ownership. Sources remain experimental and unqualified; actual native mutation routing is disabled. The partial assembled Go run failed a bridgeclient test compile and ran after the host load rebounded above its ceiling, so it does not satisfy acceptance. Numeric pre/post-lease guards are required before further checks. PR #6 remains the only landed implementation; its explicit local-check merge waiver does not apply to this candidate.

## 2026-10-05 — Resumed local verification and locked-session boundary

The owner authorizes local checks, independent exact-head review and normal protected merge while hosted Actions cannot start because of billing. This applies beyond the historical seam-only waiver. Hosted CI remains unavailable; it was not retried. The landed seam remains `2973799ab1c8acc02826eed95a7f55341b49f7d1`.

Three parallel Luna lanes prepare concrete durable host composition, a fixed-scenario fixture command, and fresh independent source review. Native input remains compile-closed. No release, deployment, provider worker or real-user-app operation occurred.

| Exact source | Observed check | Result and limit |
|---|---|---|
| `168d668668c2ce824ad9f888199ea3f066d3a44f` | `CGO_ENABLED=0 go test -p 2 ./...` | Passed available packages under the shared lease. |
| `25634f6a96c189d3d02405d46e277a3a585067a2` | Built real MCP host; initialize, tools/list, hello, doctor, windows, EOF | Negotiated 2025-06-18, listed exactly four readonly tools, hello/doctor completed and native close exited 0. Windows returned truthful reference_unavailable. |
| `e2229f54a5cc6668f61ab4bde3eeab5af65251e0` | Built seamprobe; real native normal and cancel-before-pump processes | Callback, cancellation, drain and second-image rejection passed. Swift bridge artifact was built from `7665aaf`. |
| `e2229f54a5cc6668f61ab4bde3eeab5af65251e0` | Swift bridge build with `--triple arm64-apple-macosx14.0 --jobs 2` | Passed compile/link under the lease; no macOS 14 runtime qualification. |
| `ee044d48cb976d04e9fba2bc906e7644b1e40463` | Swift fixture build with the same macOS 14 target | Passed compile/link under the lease; no GUI qualification. |
| `d7ec58b8e5cfc80d3d93264a41c0f15b8f991272` | `go test -count=1 ./spikes/desktopprobe` | Passed, including failed-persistence writer exclusion regression. |
| `361a7537be70561c29218675a0c07443d81fde3e` | Focused input test and full configured lint | Input compiled but a new test journal map panicked. Lint reported nine findings. Both remain author-owned remediation, not accepted checks. |

The synthetic fixture process is running and matches its configured bundle/nonce. Actual doctor observed Accessibility and event posting available without prompts. OS session metadata reports the desktop locked; fixture window discovery is unavailable. No unlock, permission prompt, real-user-app reads/pixels, or input mutation was attempted. T1.48 and GUI acceptance remain held; compiler, fake backend, source review and readonly startup evidence do not satisfy them.

Independent review found and assigned durable close, retry ownership and report truthfulness failures in the new composition. Final assembled Go/race/vet/lint/Swift checks and two nonauthor exact-head reviews remain required after all fixes. Detailed private command receipts and paths stay in the task artifact ledger rather than public documentation.

### Final local source checks and fixture command

The complete Go, disabled-cgo, race and vet passes, Swift bridge (14) and fixture core (3) test passes, and four native command builds are recorded at `66f1765c6cd92799de1f6a3e3054a9aaf4aeaeca`. Configured lint with both finding caps disabled passes with zero issues at `b610d747e974861a62a437a96b822e196e64d3c3` after an equivalent test predicate change. Independent source reviews are clear; authored hunks remain excluded from each cross-review.

The actual dedicated fixture command opened the native bridge on its process-main owner and emitted only bounded outcome fields: held, exit 75, fixture_window_unavailable. It did not satisfy GUI acceptance. Its current supported lifetime is the dedicated process; a long-lived embedding needs a retained-owner retry API. The fixed fixture facade issues only a predeclared trusted-host scenario approval, while the generic factory/executor has no automatic approval path. Native input remains disabled and ordinary CLI/MCP remain readonly.

## 2026-10-06 — Bounded feasibility source landed

PR #7 rebase merged at da0071a8929500d159a5f356fc7140d8020963dc; exact tree matches independently reviewed e97. Full recorded local checks provide source evidence while hosted Actions cannot start due to billing. Owner requests coding-first delivery; added source-specific E1 stages and expanded E2 source tasks. Existing GUI, mutation and minimum-OS gates stay open.

## 2026-10-07 — Production foundation landed and source continuation

PR #9 source1830cf5 independently approved and rebase merged98b5b81 over776e953. Reviewed and landed trees equal, revision reachable from current main, landed Linux Go/race checks passed. Mac exact-source Darwin tests/lint/full race passed; unchanged Swift native source had eight tests and macOS14-target build passing8797. Every hosted failing check individually reported billing-only job-not-started. No protections changed. Actual read-only CLI/MCP Doctor/protocol/EOF/idleSIGINT checks passed from protected external-backed artifact volume; fixture window list empty and complete opt-in native probe failed path admission. No GUI/action/minimumOS/signing/release acceptance claimed. Portable coding/reviews moved to isolated DGX worktrees with checked transfer hashes and preserved originals. Shared dispatcher owns fair Luna admission; remaining source parity, E3 and later phases are unfinished.

### Continuation review PC01 remediation

Independent review44597a6 over98b5b81 requested five changes: exact response/observation schemas, full operation limits, deterministic ledger/resources, serialized ownership and correct full Phase1 milestone. Accepted PC01-F01 through F05 tracked as T2.55-57. Added closed2020-12 schema definitions and eleven illustrative success/error/full/reset/stored/delta/unchanged/read/action examples; schema checks, negative authority/error branches, canonical131task IDs/meta/resolved acyclic dependencies, local links and whitespace passed. T2.29 explicitly excludes E3; T3.11 is full Phase1 source parity, T3.12 live recovery acceptance. Serial leaf handoffs preserve existing IDs and independent review/merge/landing gate. No source leaves or native input enabled by these document corrections.

### Continuation review PC02 remediation

Independent6b54c52 review accepted PC01 ownership/full-phase milestone correction and requested PC02-F01 wait elapsed/window identity, F02 modifier/key cardinality and scroll location, F03 byte-accounting completeness. T2.58-60 track accepted fixes/check/rereview. Actual elapsed is not falsely truncated at request timeout; unique appearance returns its window reference and ambiguity refuses. Key contains cardinality and explicit scroll point schemas have positive/negative checks. Canonical response bytes cover all domain routes/errors/replay and exclude usage to prevent recursion; ledger snapshots precede their own response charge. Separate observation_error and actual completed_steps preserve execution truth after applied input. Fifteen illustrative envelopes/schema checks, three key successes/five rejections, one scroll success/four rejections and two wait identity rejections pass;134task parser IDs/meta/resolved DAG/links/whitespace pass. No source leaves/input/release enabled; exact-head rereview pending.
