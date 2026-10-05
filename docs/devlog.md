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
