# Comuse vision

**Status:** Agreed product direction; implementation and platform qualification pending.
**Updated:** 2026-10-03

Comuse gives agents the text, controls, state, and positions they need to work with desktop applications. It is an open-source computer-use toolkit: begin with a compact JSON description of the interface, act on validated controls, and receive the changes. Request a screenshot explicitly when the application cannot expose enough semantic information.

Developers should be able to build useful desktop agents with text-only models for accessible workflows. Vision becomes an optional capability for tasks that need visual information, rather than a requirement for every click or observation.

## The experience

An agent opens an authorized session and receives a full semantic snapshot of a selected window. It sees relevant text, named controls, their state, relationships, supported actions, and logical bounds. It can ask Comuse to act on an opaque element reference. Comuse checks the live target and the host's authority before acting.

Subsequent observations return JSON changes against the state the client already holds: added or changed controls, removed elements, and changed context. The host applies those changes in software and retains the current full state. The model can receive the initial state and relevant changes, with a fresh full snapshot when its context is reset or it needs orientation.

The developer can always request a fresh full snapshot. Original immutable state remains retrievable while retained and authorized. Missing or expired baselines recover through a full snapshot; the system does not depend on the model remembering an endless sequence of patches.

For example, a form task can start with its labels and controls, enter authorized text, and observe that a validation message appeared or a button became enabled. The agent then checks the actual task postcondition. An unchanged interface or successfully dispatched click alone is not proof of completion.

## What Comuse provides

- **Semantic observations first.** Structured accessible text, controls, state, hierarchy, and positions in JSON. Full snapshots, baseline-bound diffs, and unchanged responses share a versioned contract.
- **Validated element actions.** Stable references while native identity can be reconciled, supported native accessibility actions, and qualified live-coordinate input where appropriate. Stale or ambiguous targets require a fresh observation.
- **Explicit visual fallback.** Bounded screenshots/crops when semantic evidence is insufficient and host policy permits them. Image interpretation requires a vision-capable consumer.
- **A strict semantic-only mode.** No pixel capture or hidden image-based verification. Accessible image labels may be returned as text; Comuse does not infer image contents, colors, or visual styling on this path.
- **A reusable local toolkit.** A Go library, scriptable CLI, and persistent MCP interface, with shared policy, execution results, budgets, and accounting. The host chooses the model and owns the agent loop.

## Who it serves

Comuse serves developers building desktop agents, workflow automation, and supervised assistants. The first useful workflows are those whose applications expose meaningful text and controls: navigating settings, completing forms, finding information, and working with accessible application interfaces.

The first backend targets macOS on Apple Silicon, using native Apple APIs behind a small Swift bridge. Other operating systems remain future, separately qualified backends. Public support claims must match tested OS, application, and launch configurations.

## Principles

**Give the agent sufficient evidence with bounded overhead.** Send the smallest useful semantic state and changes. A full snapshot is the recovery mechanism whenever a diff cannot be applied confidently or would be larger. Smaller payloads are a hypothesis about efficiency; measure model usage and task outcomes before claiming savings.

**Keep state reconstruction in software.** Baselines are explicit, snapshots are immutable, and diffs apply deterministically. Missing responses, retention expiry, scope changes, and model-context compaction must have defined recovery paths.

**Treat application support honestly.** Accessibility information is supplied by applications. Custom canvases, charts, image editors, and unlabeled controls may expose insufficient information. Report unavailable or partial coverage. Never interpret a traversal limit as evidence that a control was deleted.

**Make authority and outcomes explicit.** The trusted host controls observation and action scopes, permissions, and approvals. Model arguments cannot broaden them. Validate live targets, serialize desktop input, clean up held input, and distinguish dispatched actions, partial execution, uncertainty, and verified application outcomes.

**Protect observed content.** Redact before comparison, retention, and output. Bound session state by bytes, count, and expiry; invalidate affected state on permission or policy revocation. The default local toolkit initiates no telemetry or model requests. The host decides whether observations are sent to a model provider; controlled applications may themselves perform network actions.

**Keep the open-source project independently usable.** Publish self-contained contracts, examples, application limitations, and reproducible qualification evidence. The library, CLI, and MCP interface should work without proprietary orchestration services or knowledge of another project.

## Delivery and evidence

Build in this order:

1. Full JSON snapshots and qualified element actions, with the complete policy and desktop-input safety baseline.
2. Semantic diffs, bounded original-state retrieval, deterministic reconstruction, and full-snapshot recovery.
3. Explicit screenshot fallback with separate permissions, budgets, and visual qualification.
4. Signed macOS distribution, real-host task acceptance, resource/latency measurements, and independent review.

Compare full semantic observations, semantic diffs with recovery, and screenshot-based observations under the same task and model conditions. Report completion rate, retries, reset/fallback frequency, latency, retained memory, actual model usage, and cost per completed task. Also demonstrate semantic-only workflows without Screen Recording permission.

Comuse is a toolkit, not an autonomous agent or a guarantee of universal desktop compatibility. These are design commitments and acceptance criteria, not claims that the repository already implements them.

The detailed API, JSON examples, retention/recovery rules, platform design, and release gates live in [RFC 0001](rfc/0001-compuse-macos.md).
