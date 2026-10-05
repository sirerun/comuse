# Comuse implementation plan — parallel GPT-6-Luna delivery

**Status:** E1 seam landed through PR #6 using explicitly authorized local checks; runtime, AX and fixture source is assembled; guarded input, policy, writer/replay and semantic lanes are rolling through three Luna worker slots. GitHub CI remains billing-blocked, and native/runtime acceptance gates remain open.
**Planning contract:** Ordinary, unenrolled repository Git delivery.
**Design baseline:** RFC 0001 v0.5 and vision at `871f3416abf052a73fdaa928ec23e194cd9e634a`.
**Updated:** 2026-10-04 UTC.
**Change summary:** New split plan and use-case catalog; executable feasibility horizon, ten worker lanes, coordinator-owned seams, and gated later Phase 1/Phase 2/qualification outlines.

## 1. Context

Build Comuse as an independently usable open-source Go library, CLI and MCP stdio toolkit with a Swift macOS backend. The LLM chooses an element and read/write/action intent; Comuse owns coordinates, focus, native input and policy. Full semantic JSON comes first, followed by exact-baseline diffs and host reconstruction. Phase 2 adds authorized app opening and explicit screenshots; semantic-only never acquires pixels.

The implementation target is macOS 14+ on Apple Silicon, subject to real qualification. Current compiler availability is not minimum-OS support evidence. Keep Intel, other OS backends, remote HTTP, OCR, scripts, pixel-diff history, multi-display and autonomous agent logic outside this delivery. No new billable resources, provider calls, release tags, package publishing, deployment or App Store submission are authorized by this plan. Preparing/qualifying artifacts is distinct from publishing them.

Success means useful fixture tasks complete through semantic targets, rejected/partial/unknown outcomes remain truthful, reconstruction equals fresh state, privacy/resource bounds hold, and the selected native/CLI/stdio launch paths work on the advertised matrix. Model efficiency is measured with equivalent tasks and actual reported usage, never inferred from JSON/JPEG bytes.

## 2. Discovery summary and bindings

At the design baseline the only tracked files are the license, RFC and vision. There are no Go/Swift implementation files, public API schemas, tests, CI, plan, design/devlog or ADRs. All 20 cataloged use cases are PLANNED: 16 P0 and 4 P1, with no wired paths or acceptance evidence. See [use cases](usecases.md); a task-local `.claude/scratch/usecases-manifest.json` captures the same discovery without becoming a runtime or public dependency.

A code graph CLI is installed but graph inspection found zero nodes/files; no callable graph MCP is available in this session. Bounded tracked-file/RFC discovery is therefore the qualified fallback. No production-controller enrollment or authoritative lifecycle snapshot was found: do not create a second scheduler or assume a service admission path. The host profile selector advertises baseline/delivery capabilities; installed packages are not proof of callable connections.

| Capability | Qualified current binding | Planning boundary / execution refresh |
|---|---|---|
| Planning/coordination | Local file/git tools; native collaboration tools | Three GPT-6-Luna agents supplied planning inputs only. No implementation was dispatched. |
| Worker execution | GPT-6-Luna, isolated task worktrees | Target ten workers plus coordinator; current runtime supports four active slots total, so only three workers here. Qualify a larger harness or explicitly batch the schedule; never claim ten ran here. |
| Go/Swift/macOS tooling | Go 1.27.1, Swift 6.4, Xcode 27.0 on arm64 macOS 26.6.2 observed | Pin chosen build versions at T1.1; minimum macOS 14 runtime, linking/packaging and ABI semantics remain unqualified. |
| MCP | Official Go SDK candidate v1.8.0 observed in upstream releases | Review compatibility and pin at T1.1/T1.18; RFC protocol baseline is 2025-06-18, not an open-ended compatibility claim. No MCP server is implemented. |
| GitHub | `gh` CLI, public repository remote | Existing docs landed. New code candidates use explicit PR/check/review/rebase/landed stages. No implementation CI currently exists. |
| Task predicates | Local acceptance fields; ordinary Git delivery | `acc:` expresses planning acceptance. All current executable rows use `lane: agent` for requested Luna direct work. Planning creates no execution assignments or controller state. |
| Resource/claim tools | Atomic local resource claims and configured shared build lease | Claim the plan resource for edits; execution rechecks load, lease ownership and volume. Current discovery load exceeded the build ceiling; no heavy builds ran. |
| Native GUI/TCC/signing | Availability and authority not proven | T1.0 inventories; T1.6 is operator-owned. Simulator, fake backend, compiler and CI cannot substitute for desktop/signing/minimum-OS evidence. |
| Caches/artifacts | Verified mounted, writable external SSD | Actual local paths live in task-local bindings, not public docs. No silent internal-disk fallback. |

Local execution bindings (worktree/build lease/cache locations and discovery logs) stay in the planning artifact record. Public documentation uses host-configured roots and avoids private home paths, hostnames, private network addresses or another project's internals. The public runtime depends on Apple frameworks and the official SDK, not on an internal orchestrator or the planning tools.

## 3. Scope and deliverables

| ID | Outcome | Owner | Acceptance |
|---|---|---|---|
| D1 | Bounded native/seam feasibility and supported-operation matrix | Coordinator + L01-L10 | E1 exact landed evidence, not mock-only or release evidence |
| D2 | Production semantic library/native backend/CLI/MCP | Coordinator + newly expanded Luna lanes | E2 controlled native/public adapter acceptance with complete safety baseline |
| D3 | Diffs, retention, original state and host recovery | Expanded semantic-state lanes | E3 reconstruction/coverage/privacy/retention and adapter evidence |
| D4 | Authorized app inventory/opening | Expanded phase-2 native/adapters lanes | E4 identity/launch/readiness/replay/scope evidence |
| D5 | Explicit image fallback | Expanded phase-2 capture/adapters lanes | E5 mode/authorization/redaction/budget/runtime evidence |
| D6 | Qualified artifacts and measured comparisons | Coordinator, operator and expanded evidence lanes | E6 signed-artifact/runtime/soak/cost evidence; publishing remains separate |

The first deliverable is a bounded experimental spike, not a fully implemented product. Its result determines later package shapes, reference fingerprints, operation support and thresholds. Later epics are concise outlines with exactly one planning trigger; this prevents speculative precision from becoming mistaken execution authority.

## 4. Contract ownership and planning decisions

The coordinator owns `go.mod`, `go.sum`, the public API/JSON/error contracts, `internal/backend` seam definitions, versioned C header, native Package definitions for the bridge, root build integration, and `docs/plan.md`, `docs/plans/`, design/devlog/ADRs. Proposed initial spike paths are coordinator-owned `spikes/macos/bridge/Package.swift`, `spikes/macos/bridge/include/comuse_spike.h` and `docs/contracts/spike-v0.md`, with consumers assigned below. T1.1 freezes their tested shape; these paths are planned, not created code.

Workers own named distinct files/directories and never modify common seam files concurrently. Changes need an explicit coordinator-owned contract amendment followed by affected checks and review. File ownership and runtime admission are separate from Git/task claims. Read [ADR 0001](adr/0001-parallel-implementation.md) and the [design map](design.md).

## 5. Checkable Work Breakdown

### E1 -- Native feasibility and ten-lane spike -> docs/plans/E1-native-feasibility.md (9/45)
### E2 -- Phase 1 production semantic runtime -> docs/plans/E2-phase1-semantic-runtime.md (0/1)
### E3 -- Phase 1 semantic diffs and recovery -> docs/plans/E3-phase1-diffs-recovery.md (0/1)
### E4 -- Phase 2 authorized application opening -> docs/plans/E4-phase2-app-opening.md (0/1)
### E5 -- Phase 2 explicit image fallback -> docs/plans/E5-phase2-image-fallback.md (0/1)
### E6 -- macOS distribution and measured qualification -> docs/plans/E6-macos-qualification.md (0/1)

## 6. Parallel work and waves

The requested implementation model is **GPT-6-Luna for every coding worker and independent reviewer**, with one coordinator responsible for contracts, integration and final verification. Native-risk tasks do not silently switch models. If a lane cannot resolve an ambiguous seam, it reports evidence to the coordinator and blocks that path; model changes require explicit direction. Independent reviewers did not author their covered candidate or its integration edits.

### Worker lanes

| Lane | Model | Implement / verify | Responsibility | Exclusive files |
|---|---|---|---|---|
| L01 | GPT-6-Luna | T1.10 / T1.20 | Native ABI/runtime | `spikes/macos/bridge/Sources/BridgeProbe/{Exports,Runtime}.swift; spikes/bridgeclient/` |
| L02 | GPT-6-Luna | T1.11 / T1.21 | AX observation/identity | `spikes/macos/bridge/Sources/BridgeProbe/AccessibilityProbe.swift` |
| L03 | GPT-6-Luna | T1.12 / T1.22 | Native element/input operations | `spikes/macos/bridge/Sources/BridgeProbe/InputProbe.swift; spikes/inputprobe/` |
| L04 | GPT-6-Luna | T1.13 / T1.23 | Controlled fixture | `spikes/macos/fixture/` |
| L05 | GPT-6-Luna | T1.14 / T1.24 | Policy/approval probe | `spikes/policyprobe/` |
| L06 | GPT-6-Luna | T1.15 / T1.25 | Writer/replay/cleanup probe | `spikes/desktopprobe/` |
| L07 | GPT-6-Luna | T1.16 / T1.26 | Semantic JSON/coverage contract probe | `spikes/semanticprobe/` |
| L08 | GPT-6-Luna | T1.17 / T1.27 | CLI contract probe | `spikes/adapters/cliprobe/` |
| L09 | GPT-6-Luna | T1.18 / T1.28 | MCP stdio/SDK probe | `spikes/adapters/mcpprobe/` |
| L10 | GPT-6-Luna | T1.19 / T1.29 | Build/CI/evidence tooling probe | `scripts/spike/; .github/workflows/spike-checks.yml` |

### Wave 0: Preflight and coordinator contract gate (one coordinator; one independent Luna reviewer when runnable)

T1.0 -> T1.1 -> T1.2 -> T1.3 -> T1.4 -> T1.5 is deliberately serialized. A tiny frozen and landed seam prevents ten workers from inventing incompatible callback/identity/wire contracts. T1.6 is operator-owned and may proceed once the seam lands; it need not block unrelated source work but blocks native fixture acceptance.

### Wave 1: Independent spike implementation (10 GPT-6-Luna workers)

Dispatch one worker per T1.10-T1.19 after T1.5. These tasks share no write-owned files and all consume the frozen seam. Exactly ten worker slots are intended in a qualified high-capacity harness, plus a reserved coordinator slot. Only one live desktop writer and one shared build-lease holder can run; worker concurrency is not build/GUI concurrency.

For this four-slot runtime, explicitly record the capacity override before execution: batch A L01/L02/L04; batch B L03/L05/L06; batch C L07/L08/L09; batch D L10. At most three workers plus coordinator are active, and blocked native verification does not consume an idle worker indefinitely. This is a transparent fallback schedule, not a claim that the ten-worker target was met. Select a documented larger-capacity harness only at execution preflight; this plan installs/launches no alternate sessions.

### Wave 2: Lane verification (up to 10 GPT-6-Luna workers; limited to 3 here)

Each owner runs T1.20-T1.29 after its implementation dependency; native observation also waits for the fixture and operator context. Source/unit/subprocess work may overlap. Actual native input is reserved for the integrated guarded gate rather than ten competing standalone probes. Shared-lease build work and live desktop scenarios serialize; race-all runs in one lane only.

### Wave 3: Integration and acceptance (one coordinator)

T1.30 integrates all ten candidates; T1.31 verifies the exact assembled head with the complete fixture-only safety baseline. Scoped branch tests are not sufficient after integration. This serial convergence is intentional and prevents incompatible seam changes or mock paths from becoming native claims.

### Wave 4: Independent exact-head review (2 GPT-6-Luna reviewers)

T1.32 and T1.33 run concurrently on separate fresh review worktrees, covering all coding tasks plus coordinator seam/integration changes. Coding workers stop or move to eligible safe work. Neither reviewer authored a covered candidate. Accepted findings create fix/affected-verify/re-review rows and invalidate exact-head approval until resolved.

### Wave 5: Merge, landed verification and next planning (one coordinator)

T1.34 -> T1.35 -> T2.0. The next outline is expanded only from landed findings. E3 requires the E2 landed milestone that does not exist yet; E4/E5 require Phase 1 landed acceptance, and can then be expanded as parallel phase-2 tracks. E6 waits for their actual landed qualification milestones. Explicit milestone blockers are replaced with exact task dependencies when predecessors expand; planning completion alone never substitutes for landing.

## 7. Milestones and effort

| ID | Milestone | Dependency / exit |
|---|---|---|
| M1 | Frozen seam and safe fixture prerequisites | T1.5, T1.6; versioned contracts, identity/permission/resource facts |
| M2 | Landed native feasibility decision | T1.35; tested matrix, operation gaps and measured limits; unlock E2 planning |
| M3 | Landed Phase 1 semantic runtime and recovery | E2/E3 verified-landed milestones created during expansion; all applicable P0 cases |
| M4 | Landed Phase 2 app opening and image fallback | E4/E5 verified-landed milestones created during expansion; separate policy and runtime gates |
| M5 | Qualified distribution/comparison evidence | E6 scoped artifact and acceptance milestones; publishing still separately authorized |

E1 task estimates are ranges in agent-hours, not calendar promises. Its ten independent coding lanes are roughly 28-48 coding hours before verification/integration/review; limited slots, shared build load, operator prerequisites and failed spike assumptions dominate wall-clock duration. Re-estimate later phases only after M2/M3 evidence; do not invent a release date or automatic linear speedup from ten workers.

## 8. Risks and open decisions

| Risk / unresolved point | Effect | Required evidence / mitigation |
|---|---|---|
| Go/Swift C ABI, actor/main-thread and cancel/drain lifetimes | Hangs, leaks or unsafe callbacks | Minimal real seam first; terminal-completion ownership tests and exact native logs |
| Incomplete AX identity/text/write/selection support | Unsupported apps or wrong target/input | Controlled control-family matrix, stable-native-ID reconciliation; explicit unsupported/stale; no model-coordinate workaround |
| Physical user moves/edits/focus changes | Non-atomic race or partial edits | Fresh native checks and guarded postconditions; dirty/partial/unknown results; dedicated desktop for unattended qualification |
| Hidden values leak through state IDs/diffs or historical reads | Privacy breach | Redact before canonicalization/cache/diff; scope-bound baselines and revocation/expiry tests |
| TCC/signing/CLI versus stdio permission ownership | Setup or upgrade fails | Operator-owned permission matrix and actual launch/bundle identities; signed/minimum-OS gates later |
| Proposed macOS 14 runtime lacks a test host | Unsupported support claim | Record untested combinations; provide actual supported-host evidence before advertising; narrow/redesign decision stays visible |
| Ten workers exceed current capacity | Oversubscription or falsely claimed parallelism | Target10 plus coordinator, current3-worker fallback, fresh runtime capacity check and explicit override |
| Shared build/GUI contention | Unsafe input, slow/flaky results | Load ceiling, atomic build lease, at most2 heavy project lanes, one race-all lane, serialized fixture desktop |
| SDK/protocol compatibility and package scope unresolved | Adapter churn | Candidate upstream SDK examined, exact pin/negotiation verified at seam; no hidden production stub |
| Model benchmarking or notarization costs/credentials unavailable | Qualification cannot complete | Keep scoped local checks independent; expose prerequisite; no billable/provider/submission action without authority |

Outstanding spike choices: exact ABI and threading initialization, retained native identity fingerprint/invalidation, supported read/write/insert methods, schema limits and count/byte/TTL defaults, selected Go/Swift/SDK versions and tested deployment matrix. The plan does not silently resolve these from compiler installation alone.

## 9. Operating procedure

Planning has not started implementation. Execution uses ordinary Git task claims and a task-local coordination ledger, never an invented controller. The lead assigns each worker its stable task ID, model, source base, frozen contract version, exclusive paths, acceptance and stop condition. One coordinator edits shared plan/design/ADR records; worker reports carry facts for that owner to integrate.

Each task/reviewer gets a unique external-SSD worktree and task-specific caches/temp/artifact directories. The local binding identifies the required volume root; verify mount, writable space and sufficient capacity before creating workers. If unavailable, stop dependent work rather than use internal disk. Preserve existing worktrees/branches and never perform destructive operations in the shared main checkout.

Before covered multi-package Go/Swift/Xcode builds/tests/lint, inspect one-minute load and hold above 10; claim the configured shared build lease in the same process that runs the command. Verify ownership, release immediately after, and keep at most2 heavy build lanes for this project. Only one `go test -race ./...` lane runs. Go code uses meaningful public behavior tests, typed wrapped errors, context/lifetime cleanup and gofmt/goimports; test doubles are explicit test-only packages and unsupported production paths cannot fabricate success. Native Swift guidance is discovered for the relevant stage rather than importing unrelated UI workflows.

Use the full chain preflight -> implement -> changed-behavior verify -> independent code review -> GitHub rebase merge -> verify-landed. Required checks and exact approved base/head are recorded, never inferred from a worker message. Shared reviews name every included coding task. Accepted findings append stable fix/affected-check/re-review tasks; prior evidence is preserved. Later work waits for verified landing except explicitly speculative same-candidate spike branches. Load the corresponding execution/review/merge guidance only when its stage becomes runnable.

Acceptance evidence records task/use-case IDs, candidate/landed revision, fixture and actual environment/toolchain/launch/permission facts, command/method and artifact locator, pass/fail/blocked/not-run, findings/dispositions and limitations. Keep private logs/paths out of public documentation; publish scrubbed reproducible findings. Report local unit, fake-backend, CI, actual native, signed-artifact, provider usage and operator acceptance separately.

## 10. Progress log

- 2026-10-04 UTC: new plan drafted from RFC v0.5/vision, tracked-file/tool capability discovery and three parallel GPT-6-Luna planning inputs. All implementation/stage tasks remain unchecked. No native build, desktop input, execution admission, independent code review, merge, release or deployment ran in this planning turn. Transient build-load and unverified GUI/signing/minimum-OS prerequisites are explicit.

## 11. Handoff

Use this plan and the E1 epic as the entry point for `/ship`; do not treat plan creation as permission to skip preflight or platform qualification. First runnable agent task is T1.0. T1.1 owns bootstrap contracts; no worker source starts before T1.5. Preserve existing task IDs and completed evidence on later replans. Historical planning-run context: the initial artifact was authored in a dedicated planning worktree, then reviewed and landed. The execution binding below records the current implementation state.

A new executor needs the RFC, vision, use cases, ADR, local execution bindings, actual current runtime capacity and source revision. It must reconstruct ownership/claims rather than assume old worker reports reserve a lane. Operator GUI/TCC/signing/credential tasks need their explicit scope; no secrets are stored in this plan. Deadlines for waiting/blocks and safe next work are recorded in the coordination ledger; do not poll indefinitely or silently replace blocked evidence.

## 12. References

- [RFC 0001 v0.5](rfc/0001-compuse-macos.md), [vision](vision.md), [design map](design.md), [use cases](usecases.md), [decision](adr/0001-parallel-implementation.md), [devlog](devlog.md).
- [Official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk) and [v1.8.0 release candidate reference](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0); final pin/compatibility is a spike task.
- [Swift C interoperability](https://www.swift.org/blog/swift-6.3-released/) and [Apple accessibility model](https://developer.apple.com/library/archive/documentation/Accessibility/Conceptual/AccessibilityMacOSX/OSXAXmodel.html); API/toolchain availability does not prove minimum-OS or app coverage.

## 2026-10-04 execution binding

The user authorized ship implementation, independent review, ordinary GitHub merge and landed verification with GPT-6-Luna workers. This four-slot session uses the documented three-worker batching fallback. Coordinator retains seam integration and plan ownership; T1.1 seam source is delegated to Luna, while two Luna lanes prepare native and safety contracts. These preparation lanes do not start T1.10-T1.19 before T1.5. New worktrees and caches use the verified external volume. Existing checkouts remain preserved.

Preflight: clean main at `590fb6e`, public GitHub repository, no open PRs, no active repository claim refs observed before this run. Go 1.27.1 and Swift 6.4 are available; minimum macOS 14 remains unqualified. Shared host load exceeded 10, so heavy local builds are held. Source and read-only work proceed; T1.2 validation and downstream seam landing cannot claim pass until the applicable checks run. Native input and TCC setup remain gated on T1.6. No production deployment or publication is in scope.

## Rolling source batch update

PR #6 landed at `2973799` after explicit operator authorization to use its recorded local checks despite the GitHub billing lock. The assembled feasibility candidate now contains fixture packaging, native runtime, AX identity/topology, partial terminal handling and state-bound approvals. Those later sources are not yet verified or independently reviewed. The current three Luna workers own L03 input, L06 writer/replay and L07 semantic projection; L05 policy source has completed its coding handoff. This is the documented four-slot fallback with rolling refill, not ten simultaneous workers.

The shared host remains above the build-load ceiling, so new builds/tests are held. No fixture launch, Accessibility grant, native mutation acceptance or later CI exception is inferred from source handoff. The PR #6 local-check authorization applies to that seam only.

## Review capacity refresh

A fresh non-author reviewer spawn failed at the harness total-thread limit. The E1 epic now records three task-partitioned Luna cross-review receipts, with two non-author reviews per implementation task and explicit author exclusions even within shared source files. Final review still follows assembled verification at an exact head. The trusted host input composition is speculative same-candidate source; ordinary CLI/MCP remain read-only and native mutation activation remains gated.

## 2026-10-05 source progress clarification

The fixture-scoped MCP SDK probe now has server and adapter source under `spikes/adapters/mcpprobe`. Earlier statements that no MCP server is implemented describe the planning baseline. T1.18/T1.28 and integrated verification/acceptance remain unchecked; source existence does not establish a verified server or live native acceptance. The resumed local delivery and review gates are recorded in E1.
