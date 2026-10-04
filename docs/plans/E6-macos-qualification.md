# E6 -- macOS distribution and measured qualification

fidelity: outline
Acceptance: UC-019 and UC-020 have exact artifact/revision/runtime acceptance, measured retention/resources/latency and actual model-usage provenance; limitations and failed/skipped cases are published as evidence without invented efficiency or minimum-OS claims.

Intent: Prepare reproducible signed/notarized artifacts and the OS/launch/update/permission/soak matrix. Measure same-task full-state, diff/recovery and explicit-image comparisons with a host-supplied model adapter only when usage/budget authority is provided. Release publishing requires separate authorization.

- [ ] T6.0 PLAN: expand E6 from preceding landed evidence  Owner: coordinator  Est: 1-2h  kind: plan  verifies: [UC-019, UC-020]  blocked-by: [T4.0, T5.0]  blocked: Await verified-landed E4 and E5 milestones created by their planning tasks and operator-owned signing/host prerequisites; bind exact dependencies before dispatch  acc: [E6 expanded to executable fidelity with concrete owned files, supported contracts, exact trigger milestones and complete preflight/implement/verify/independent-review/merge/landed chains; parser IDs/dependencies/criteria pass]

This is exactly one dependency-triggered planning task, not dispatchable future implementation. Where the predecessor is also an outline, the explicit milestone blocker must be replaced by the actual verified-landed task ID when that predecessor expands; completion of its planning task alone never opens this epic. Shared contracts stay coordinator-owned, independent author/reviewer roles stay separate, and later decomposition preserves these IDs.
