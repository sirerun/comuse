# ADR 0002: Greeter consumes Comuse through a trusted Go host

- Status: Accepted dependency design; implementation and runtime qualification pending.
- Date: 2026-10-04

## Decision

Comuse remains an independently versioned macOS semantic computer-use library and toolkit. Greeter plans to embed the public Go library in its trusted Go host. A UI-to-host boundary and the exact permission-owning binary and launch mode require separate qualification. The model-facing stdio MCP surface cannot issue trusted approvals.

Comuse owns native AX identity, focus and target checks, bounded scoped observations, redaction before state IDs/cache/diff/output, native input, independent policy enforcement, host-bound one-use exact-action approvals, one desktop writer, and bounded cleanup. Greeter owns stream/session presence and chat interpretation, model orchestration, knowledge, speech/audio output, the operator console, product autonomy policy, output allowlists, audit correlation and emergency stop. Greeter approval supplements Comuse enforcement and cannot bypass it. MISTT is a separate owner-qualified audio/inference dependency.

Initial Greeter adoption is scoped semantic read-only observation. Opaque element/window references are session-bound identities, not durable participant IDs. Complete AX traversal does not prove complete visual content or reliable join/departure events; incomplete or ambiguous coverage must remain explicit in Greeter event interpretation. Public chat delivery needs an actual postcondition; a cleared composer alone does not prove send.

A pending challenge cannot authorize an action. The trusted host binds approval to principal/session/action/normalized parameters/exact target/policy version/expiry, with single-use consumption. Greeter owns event deduplication and task idempotency. Cancellation stops new work and revokes queued authority, without promising reversal of dispatched input. Applied, partial and unknown outcomes are separate from verification and cleanup evidence. Ordinary audit excludes field/chat values, tokens and images; secure values are always omitted. Retention and revocation are explicitly bounded host policy.

## Gates and scope

E1 must qualify the native bridge and controlled fixture; E2 freezes and verifies the semantic runtime/library/CLI/MCP; E3 adds diffs/reconstruction. App opening and screenshots remain E4/E5. E6 qualifies signed launch, permission ownership, minimum OS and distribution. Live-chat AX coverage, action postconditions and end-to-end Greeter behavior need separate acceptance. Neither a design agreement nor a source build proves those properties.

Greeter adoption is not a prerequisite or an additional authorized implementation task for Comuse delivery. Greeter packaging, first-stream behavior, public speech/chat authority, models/TTS, knowledge and retention choices remain with its founder/project. The cross-project discussion concluded by both owners at design level; no release, deployment or live-stream action was authorized by that discussion.

References: [Comuse RFC](../rfc/0001-compuse-macos.md), [implementation plan](../plan.md).
