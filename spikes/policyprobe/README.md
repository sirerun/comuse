# Fixture policy approval probe

This standard-library-only package is an experimental in-process authorization gate. It does not perform native AX reads or input. Its zero-value approval denies. A caller that receives `HostAuthority` from `New` is trusted host wiring and must keep it out of model-facing tools, prompts, JSON, and untrusted plugins. Only a distinct user-confirmation callback may call `ApproveHost`; the package cannot itself prove that a person approved.

The host must freshly validate the fixture process, launch generation, window reference, element reference, principal/session, and protected native control class before both challenge creation and admission. The caller-supplied `TargetKind` is only a typed hint and cannot replace native reclassification. The title is a display field; the opaque window reference is the identity key. Secure targets are denied before action payload hashing. Unknown actions, scopes, policy versions, and zero-value approvals fail closed.

Challenges expire after at most two minutes and no later than the scope expiry. An approval binds principal/session, process PID/bundle/launch generation, fixture nonce, observed `state_id`, opaque window and element references, policy version, scope expiry, action, and the normalized typed payload commitment. The action payload is transient input; the gate stores only an internal keyed commitment. Capability consumption is atomic and single-use. The host can revoke one approval, an exact element scope, or a principal/session. Bounded active and replay records reject new work at capacity; they are not silently evicted while valid.

Decision/audit records contain outcome, action kind, policy version, and time only. They do not contain action text, payload digests, challenge IDs, approval tokens, target references, or native addresses. The in-memory audit snapshot is diagnostic and bounded; it is not a durable audit system.

The package deliberately has no HTTP, MCP, CLI, persistence, keychain, or native input integration. `HostAuthority` is an in-process Go capability and does not independently establish a secure process boundary.
