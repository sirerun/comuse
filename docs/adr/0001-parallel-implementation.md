# ADR 0001: Parallel Luna workers behind one contract owner

- Status: Proposed execution protocol for the implementation plan; no execution admission or runtime compatibility approval.
- Date: 2026-10-04 UTC
- Basis: user-requested high-parallelism GPT-6-Luna implementation planning; accepted RFC 0001 v0.5.

## Context

The repository has design documents and a license but no implementation. Native ABI/permission/identity behavior needs a bounded real-Mac spike before production contracts and thresholds can be finalized. High worker count helps independent source/test work, while shared API edits, desktop input and constrained builds require serial ownership.

## Decision for the draft plan

Target ten GPT-6-Luna coding workers plus one coordinator in a qualified harness. Use three workers plus coordinator in the current four-slot runtime unless execution explicitly qualifies larger capacity. Give every worker/reviewer an isolated external-SSD worktree, named file ownership and bounded task handoff. Reserve API/schema/ABI/header/module/shared-doc integration to one coordinator, freeze a provisional spike contract first, and require controlled native evidence before expanding production epics.

The ten spike branches form one explicitly speculative integrated candidate. Independent non-author Luna reviewers cover every code task and coordinator integration changes; exact-head verification/review gate a GitHub rebase merge and landed acceptance. Reviewers are separate from the authors of covered work. Worker width never overrides one-desktop writer ownership, the shared build lease/load gate, at-most-two heavy project lanes or single race-all lane.

Later phase outlines retain exactly one dependency-triggered planning task each. Missing future landed milestone IDs are explicit expansion blockers, not invented dependencies or permission to start after planning alone. Phase 2 opening apps remains separate from Phase 1 work on already running apps; screenshot fallback retains its own permission/mode qualification. No release publishing or provider/submission action is authorized by planning.

## Consequences

More independent source work can proceed with fewer shared-file conflicts. Contract freeze and integration gates deliberately serialize the critical path. This harness cannot provide ten workers concurrently; transparent batching is the qualified fallback, and the executor records any capacity override. Native/signing/minimum-OS evidence may block acceptance while safe independent contract work continues. The plan must never promote mock/probe success into product readiness.

References: [plan](../plan.md), [RFC](../rfc/0001-compuse-macos.md), [vision](../vision.md).
