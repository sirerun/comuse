# Comuse planned use cases

All 20 use cases are **PLANNED**, with no wired implementation or acceptance evidence at base `871f3416abf052a73fdaa928ec23e194cd9e634a`. Priorities: 16 P0, 4 P1. The spike demonstrates bounded feasibility for named cases; it does not mark production use cases implemented.

| ID | Priority | Phase | User outcome | Acceptance boundary |
|---|---|---|---|---|
| UC-001 | P0 | phase1 | Inspect capability and permission readiness | Typed available/denied/unsupported status; no incidental input or permission prompts. |
| UC-002 | P0 | phase1 | Discover authorized running windows | Identity-bound opaque window references; denied windows omitted; names are not authorization. |
| UC-003 | P0 | phase1 | Observe full semantic JSON state | Bounded coverage-aware state; no geometry in model projection; no pixels in semantic-only mode. |
| UC-004 | P0 | phase1 | Read a specific element | Fresh target-validated element_content; unavailable/protected content explicit; no snapshot-baseline advancement. |
| UC-005 | P0 | phase1 | Replace field contents | Comuse owns focus/input mechanics; dispatch and readback verification remain distinct. |
| UC-006 | P0 | phase1 | Insert at a validated selection | Unsupported/ambiguous selection refused; no hidden mode change or clipboard replacement. |
| UC-007 | P0 | phase1 | Activate and scroll semantic controls | Live target validation and bounded operation; no model coordinates. |
| UC-008 | P0 | phase1 | Constrain observation and action authority | All read/mutation paths enforce scope, approval binding, quotas and default-deny policy. |
| UC-009 | P0 | phase1 | Cancel or recover desktop input safely | Cross-process single writer; owned input released; partial/unknown outcomes and dirty ownership reported; no blind replay. |
| UC-010 | P0 | phase1 | Protect observed and retained content | Redaction before comparison/cache/output; hidden changes do not leak; revocation blocks affected historical state. |
| UC-011 | P0 | phase1 | Use a scriptable JSON CLI | JSON results only on stdout, typed exit codes and stdin content; one-shot references cannot cross sessions. |
| UC-012 | P0 | phase1 | Use persistent MCP stdio | Pinned qualified negotiation/cancellation, tool-specific schemas, shared core semantics and unavailable capabilities omitted. |
| UC-013 | P0 | phase1 | Receive semantic diffs or unchanged state | Deterministic application reconstructs fresh canonical state; no false deletion from partial traversal. |
| UC-014 | P0 | phase1 | Retrieve original retained state | Original contents/time or state_expired; never silent substitution with live state. |
| UC-015 | P0 | phase1 | Recover a missing or stale baseline | Full reset/checkpoint restores current state with reset reason; bounded retention and no unbounded model patch memory. |
| UC-016 | P0 | phase1 | Handle user edits and target movement | Geometry stays internal; relevant semantic changes/staleness revalidated; no atomicity or rollback guarantee. |
| UC-017 | P1 | phase2 | Open an authorized installed application | Installed identity rechecked; started/already_running distinct from window readiness; no arbitrary commands, paths, URLs or documents. |
| UC-018 | P1 | phase2 | Request explicit screenshot fallback | Separate image scope and bounds; semantic-only rejects pixels; model uses target references, not pixel input. |
| UC-019 | P1 | qualification | Install a qualified signed macOS package | Actual OS/architecture/launch/TCC matrix, signed-artifact and upgrade evidence; no public publishing implied. |
| UC-020 | P1 | qualification | Measure task cost and resource use | Reported actual model usage/source, completion/retries/fallback/reset/latency/retention; no inferred savings multiplier. |

Sources: [vision](vision.md), [RFC v0.5](rfc/0001-compuse-macos.md), [delivery plan](plan.md). The task-local discovery manifest is generated from this catalog and remains outside public commits.
