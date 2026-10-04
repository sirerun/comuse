# Comuse design map

The accepted product architecture is [RFC 0001 v0.5](rfc/0001-compuse-macos.md), with the [vision](vision.md) defining the developer benefit and scope. This map routes readers to the source rather than duplicating that contract. Implementation is not present at the planning baseline.

| Concern | Authoritative design | Planned delivery |
|---|---|---|
| Go library, thin CLI/MCP and Swift/C ABI | RFC §3 | E1 proves native seam, E2 expands production runtime |
| Coordinate-free model target/read/write/actions | RFC §3.3, §4.4, §5.1, §8.2 | E1 prototypes methods/identity, E2 production tools |
| JSON projection, coverage, baseline/history/recovery | RFC §4.5, §5.3 | E1 schema feasibility, E2 full state, E3 diffs/history |
| Approval, desktop writer, focus, replay and cleanup | RFC §5.2, §6, §12 | Mandatory before any normal mutating runtime |
| Authorized native application opening | RFC §5.4 | E4, Phase 2 only |
| Explicit image fallback and internal transforms | RFC §3.3, §4.2, §8 | E5, separately authorized and qualified |
| Distribution, measured resource/task cost and support | RFC §10-14 | E6 evidence; publishing is separate authority |

[ADR 0001](adr/0001-parallel-implementation.md) records the proposed parallel delivery/seam protocol. [Plan](plan.md) and [epics](plans/E1-native-feasibility.md) own assignments; [devlog](devlog.md) owns planning/discovery events. Future ABI/API amendments are versioned coordinator-owned contracts with exact revision evidence, not competing worker definitions.
