# E5 -- Phase 2 explicit image fallback

fidelity: outline
Acceptance: UC-018 passes permission/mode/capture/redaction/resource/transform/stdio cases and chosen visual fixtures; no pixels or hidden vision/OCR calls on semantic-only or ordinary semantic paths.

Intent: Add separately authorized ScreenCaptureKit capture, reference-selected crops, redaction-before-encoding, budgets and image blocks. Comuse keeps transforms internal; model tool contracts still require no coordinates. AX-insufficient visual targets without a validated element remain unsupported.

- [ ] T5.0 PLAN: expand E5 from preceding landed evidence  Owner: coordinator  Est: 1-2h  kind: plan  verifies: [UC-018]  blocked-by: [T3.11]  acc: [E5 expanded to executable fidelity with concrete owned files, supported contracts, exact trigger milestones and complete preflight/implement/verify/independent-review/merge/landed chains; parser IDs/dependencies/criteria pass]

This is exactly one dependency-triggered planning task, not dispatchable future implementation. Where the predecessor is also an outline, the explicit milestone blocker must be replaced by the actual verified-landed task ID when that predecessor expands; completion of its planning task alone never opens this epic. Shared contracts stay coordinator-owned, independent author/reviewer roles stay separate, and later decomposition preserves these IDs.

Source planning begins from full Phase1 source landing T3.11; actual native capability acceptance remains separately gated on T3.12 and the relevant controlled Phase2 fixture checks. Source planning/coding cannot advertise or enable unqualified capabilities.
