# Comuse vision

**Status:** Agreed product direction; implementation and platform qualification pending.
**Updated:** 2026-10-03

Comuse lets agents choose what to read, write, or activate in desktop applications without working out screen coordinates. It is an open-source computer-use toolkit: begin with a compact JSON description of the interface, address a named control through its reference, and receive the changes. Comuse handles geometry, focus, and native input. Request a screenshot explicitly when the application cannot expose enough semantic information. Phase 2 also lets agents open authorized installed applications.

Developers should be able to build useful desktop agents with text-only models for accessible workflows. Vision becomes an optional capability for tasks that need visual information, rather than a requirement for every click or observation.

## The experience

An agent opens an authorized session and receives a full semantic snapshot of a selected window. It sees relevant text, named controls, their state, relationships, and supported read/write/actions. It asks to read a control, write content into a specific field, or activate a button through an opaque element reference. Comuse checks the live target and the host's authority, focuses it when needed, and resolves any input geometry internally. Moving a window does not make the model responsible for new positions.

Subsequent observations return JSON changes against the state the client already holds: added or changed controls, removed elements, and changed context. The host applies those changes in software and retains the current full state. The model can receive the initial state and relevant changes, with a fresh full snapshot when its context is reset or it needs orientation.

The developer can always request a fresh full snapshot. Original immutable state remains retrievable while retained and authorized. Missing or expired baselines recover through a full snapshot; the system does not depend on the model remembering an endless sequence of patches.

For example, a form task can start with its labels and controls, request replacement of a specific field's text, and observe that a validation message appeared or a button became enabled. The model supplies the target, content, and intent; Comuse handles clicking/focus/input mechanics. The agent then checks the actual task postcondition. An unchanged interface or successfully dispatched action alone is not proof of completion.

Phase 1 operates on already running authorized apps. In phase 2, an agent can select an authorized installed app and ask Comuse to open it. Comuse validates the installed identity and reports whether it was started or already running, then separately reports whether an eligible accessible window is ready. Opening an app does not automatically authorize every window or imply its startup workflow completed.

## What Comuse provides

- **Semantic observations first.** Structured accessible text, controls, state, and hierarchy in JSON, with geometry omitted from model output. Full snapshots, baseline-bound diffs, and unchanged responses share a versioned contract.
- **Element reads, writes, and actions.** The model addresses a specific target and supplies content or intent. Comuse owns native accessibility operations, focus, and qualified input delivery. Stale, unsupported, or ambiguous targets return explicit limitations.
- **Application opening in phase 2.** Authorized installed-app identities, bounded launch/activation, and separate window-readiness results. No arbitrary process commands, documents, or URLs are implied.
- **Explicit visual fallback.** Bounded screenshots/crops when semantic evidence is insufficient and host policy permits them. Image interpretation requires a vision-capable consumer.
- **A strict semantic-only mode.** No pixel capture or hidden image-based verification. Accessible image labels may be returned as text; Comuse does not infer image contents, colors, or visual styling on this path.
- **A reusable local toolkit.** A Go library, scriptable CLI, and persistent MCP interface, with shared policy, execution results, budgets, and accounting. The host chooses the model and owns the agent loop.

## Who it serves

Comuse serves developers building desktop agents, workflow automation, and supervised assistants. The first useful workflows are those whose applications expose meaningful text and controls: navigating settings, completing forms, finding information, and working with accessible application interfaces.

The first backend targets macOS on Apple Silicon, using native Apple APIs behind a small Swift bridge. Other operating systems remain future, separately qualified backends. Public support claims must match tested OS, application, and launch configurations.

## Principles

**Give the agent sufficient evidence with bounded overhead.** Send the smallest useful semantic state and changes. The model chooses the target and operation; screen positions and input mechanics belong to Comuse. A full snapshot is the recovery mechanism whenever a diff cannot be applied confidently or would be larger. Smaller payloads are a hypothesis about efficiency; measure model usage and task outcomes before claiming savings.

**Keep state reconstruction in software.** Baselines are explicit, snapshots are immutable, and diffs apply deterministically. Missing responses, retention expiry, scope changes, and model-context compaction must have defined recovery paths.

**Treat application support honestly.** Accessibility information is supplied by applications. Custom canvases, charts, image editors, and unlabeled controls may expose insufficient information. Report unavailable or partial coverage. Never interpret a traversal limit as evidence that a control was deleted. A screenshot can help interpretation, but if the desired target cannot be bound to a validated element, report the limitation instead of requiring the model to supply pixel coordinates.

**Make authority and outcomes explicit.** The trusted host controls observation and action scopes, permissions, and approvals. Model arguments cannot broaden them. Validate live targets, serialize desktop input, clean up held input, and distinguish dispatched actions, partial execution, uncertainty, and verified application outcomes.

**Protect observed content.** Redact before comparison, retention, and output. Bound session state by bytes, count, and expiry; invalidate affected state on permission or policy revocation. The default local toolkit initiates no telemetry or model requests. The host decides whether observations are sent to a model provider; controlled applications may themselves perform network actions.

**Keep the open-source project independently usable.** Publish self-contained contracts, examples, application limitations, and reproducible qualification evidence. The library, CLI, and MCP interface should work without proprietary orchestration services or knowledge of another project.

## Delivery and evidence

Build in this order:

1. Phase 1: full JSON snapshots and qualified element reads/writes/actions, with Comuse handling focus and geometry and the complete policy/input safety baseline. Work with already running authorized apps.
2. Semantic diffs, bounded original-state retrieval, deterministic reconstruction, and full-snapshot recovery.
3. Phase 2: opening authorized installed apps with launch/readiness qualification, plus explicit screenshot fallback with separate permissions, budgets, and visual qualification. App opening remains usable in semantic-only mode.
4. Signed macOS distribution, real-host task acceptance, resource/latency measurements, and independent review.

Compare full semantic observations, semantic diffs with recovery, and screenshot-based observations under the same task and model conditions. Report completion rate, retries, reset/fallback frequency, latency, retained memory, actual model usage, and cost per completed task. Also demonstrate semantic-only workflows without Screen Recording permission.

Comuse is a toolkit, not an autonomous agent or a guarantee of universal desktop compatibility. These are design commitments and acceptance criteria, not claims that the repository already implements them.

The detailed API, JSON examples, retention/recovery rules, platform design, and release gates live in [RFC 0001](rfc/0001-compuse-macos.md).
