# Decomposition Review: Convergence Loops v0

## Findings

### 1. Traceability Completeness

| Dimension | Severity | Finding | Recommendation |
|-----------|----------|---------|----------------|
| Traceability | **MEDIUM** | `SPEC:event-delivery-model` (the at-least-once/best-effort/recoverable tier classification) is claimed by EPIC-4 via `SPEC:event-contracts`, but the reconciliation logic that *implements* recoverable-tier re-emission lives in EPIC-6. The traceability matrix doesn't reflect this split. | Add `SPEC:event-contracts` (delivery tiers, reconciliation) as explicit coverage in EPIC-6 alongside EPIC-4. |
| Traceability | **LOW** | `SPEC:verdict-normalization-temporary` is not explicitly traced. It's presumably in EPIC-3 (verdict normalization), but the traceability matrix doesn't call it out. | Minor — normalization is clearly in EPIC-3's scope text. No action needed unless you're building automated traceability checks. |
| Traceability | **LOW** | `SPEC:verdict-non-execution-detection` (the diagnostic warning when the agent narrates `bd meta set` instead of executing it) isn't explicitly assigned. It's a controller behavior triggered during `wisp_closed` handling, so it belongs in EPIC-4, but the scope text doesn't mention it. | Add to EPIC-4 scope-in: "Detect verdict non-execution (narrated command in output without metadata write) and log diagnostic warning." |

### 2. Dependency Correctness

| Dimension | Severity | Finding | Recommendation |
|-----------|----------|---------|----------------|
| Dependencies | **HIGH** | EPIC-3 declares dependency on EPIC-2, but the gate evaluation engine doesn't actually *require* the wisp execution contract. Gate evaluation needs: (a) metadata reads from the store (EPIC-1), (b) environment variable population from bead metadata (EPIC-1), (c) script execution with timeout. The verdict normalization reads `convergence.agent_verdict` which is a store field (EPIC-1). Nothing in the gate runner requires injected evaluate steps, artifact directories, or formula schemas. The dependency is phantom. | Remove EPIC-2 from EPIC-3's dependencies. This unlocks EPIC-2 and EPIC-3 for **parallel development** after EPIC-1, which is a significant schedule improvement. |
| Dependencies | **MEDIUM** | EPIC-5 depends only on EPIC-4, but `gc converge create` (in EPIC-5) needs formula validation (`convergence=true`, `required_vars`, reserved step name) which is EPIC-2 scope. The dependency on EPIC-2 is real but unlisted. | Add EPIC-2 as an explicit dependency of EPIC-5. The current graph accidentally works because EPIC-4→EPIC-2 is transitive, but explicit is better — if someone restructured the graph, this would break. |
| Dependencies | **LOW** | No circular dependencies. The DAG is clean. | None needed. |

### 3. Epic Boundaries

| Dimension | Severity | Finding | Recommendation |
|-----------|----------|---------|----------------|
| Cohesion | **HIGH** | EPIC-6 is a "kitchen sink" resilience epic combining three distinct concerns: (1) startup reconciliation/crash recovery, (2) stop mechanics, and (3) retry-loop creation. These have very different risk profiles and test strategies. Crash recovery alone is the highest-complexity work in the entire system (the spec dedicates ~40% of its word count to recovery paths). Bundling stop and retry into the same epic obscures the critical path. | Split EPIC-6 into: **EPIC-6a** (Crash Recovery & Reconciliation — high risk, mandatory for production), **EPIC-6b** (Stop Mechanics — medium risk, operator workflow), **EPIC-6c** (Retry — low risk, convenience feature). EPIC-6b and 6c depend on 6a. This lets you ship stop and retry incrementally. |
| Cohesion | **MEDIUM** | EPIC-1 mixes two distinct subsystems: (a) convergence-specific bead store extensions (type, metadata constants, child-wisp queries) and (b) the general-purpose protected-prefix ACL mechanism (token generation, environment scrubbing, namespace registration). The ACL mechanism is explicitly designed to be reusable ("namespace-generic so future SDK mechanisms can register their own protected prefixes"). Bundling it into a convergence epic hides its reuse potential. | Consider splitting the ACL/token mechanism into a separate story or sub-epic within EPIC-1 with its own acceptance criteria. Not a hard split — just explicit boundaries within the epic so the ACL can be validated independently. |
| Size | **MEDIUM** | EPIC-4 covers the entire `wisp_closed` handler (9 steps), all three event types (`ConvergenceIteration`, `ConvergenceTerminated`, `ConvergenceWaitingManual`), wisp failure semantics, terminal state logic, event payload construction, and dedup. This is likely 10-15 stories. | Acceptable if stories are well-scoped, but monitor during planning. The wisp_closed handler has enough internal complexity that a sub-epic split (normal-path transitions vs. event emission/payload construction) would reduce review cognitive load. |

### 4. Risk Ordering

| Dimension | Severity | Finding | Recommendation |
|-----------|----------|---------|----------------|
| Risk ordering | **MEDIUM** | The decomposition is strictly sequential: EPIC-1→2→3→4→5→6. With the phantom EPIC-2→EPIC-3 dependency removed, the critical path becomes EPIC-1→{EPIC-2 ∥ EPIC-3}→EPIC-4→EPIC-5→EPIC-6. This parallelism is not identified. | Explicitly call out the EPIC-2/EPIC-3 parallelism opportunity in the implementation plan. Two developers can work simultaneously after EPIC-1 lands. |
| Risk ordering | **LOW** | Crash recovery (EPIC-6) is deferred to last, which is correct — you can't test recovery without the normal path. The risk is acknowledged in the epic description. | No change needed. The ordering is sound. |

### 5. Implementation Feasibility

| Dimension | Severity | Finding | Recommendation |
|-----------|----------|---------|----------------|
| Feasibility | **HIGH** | EPIC-2 scope includes "Finish keyed idempotency plumbing in `gc sling`" — but the spec's Known Limitations section explicitly calls out that `gc sling` doesn't yet support idempotency keys. This is a dependency on an **external capability that doesn't exist yet**. The epic doesn't acknowledge this as a prerequisite or size the work. If `gc sling` changes are large, they could dominate EPIC-2's timeline. | Either (a) add a dedicated story/spike for `gc sling` idempotency key support with its own acceptance criteria, or (b) explicitly list it as a prerequisite in EPIC-2's "blocked by" with a size estimate. This is load-bearing for the entire convergence design — recovery, dedup, and iteration derivation all depend on it. |
| Feasibility | **MEDIUM** | EPIC-5 includes `depends_on_filter` — a general-purpose extension to the dependency mechanism. The spec calls it "a small, general-purpose extension" but it touches dependency resolution, which is a core scheduling path. It's scoped into a CLI epic where it doesn't naturally belong. | Move `depends_on_filter` to EPIC-1 (store primitives) or create a standalone story. It's a store/scheduler concern, not a CLI concern. |
| Feasibility | **LOW** | Each epic's "independent testability" claim is credible. Store tests for EPIC-1, runtime tests for EPIC-2, fake-script tests for EPIC-3, controller integration tests for EPIC-4 — all feasible without running the full system. | No change needed. |

### 6. Gap Analysis

| Dimension | Severity | Finding | Recommendation |
|-----------|----------|---------|----------------|
| Gap | **MEDIUM** | The spec's `gc converge test-gate` command (dry-run gate evaluation) requires the gate runner from EPIC-3 and the CLI surface from EPIC-5, but neither epic explicitly mentions it. It's implied by EPIC-5's scope ("test-gate" appears in the cobra tree list) but the gate runner API contract in EPIC-3 doesn't mention dry-run mode. | Add "dry-run/read-only mode" to EPIC-3's gate runner API design, and ensure EPIC-5 has an explicit story for `test-gate` CLI wiring. |
| Gap | **MEDIUM** | The spec's `ConvergenceManualStop` event is best-effort tier and explicitly listed in event contracts, but EPIC-6 (which owns stop) doesn't mention event tier classification. The stop handler emits `ConvergenceManualStop` and `ConvergenceTerminated` — the former is best-effort, the latter is critical. This distinction matters for recovery. | Add event-tier awareness to EPIC-6's scope-in text so implementers know which events need recovery re-emission and which don't. |
| Scope creep | **LOW** | No scope creep detected. Every epic item traces to spec content. The decomposition is conservative — it doesn't introduce new concepts. | None needed. |

---

## Summary

### Findings by Severity

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 3 |
| MEDIUM | 7 |
| LOW | 4 |

### Top 3 Issues

1. **Phantom dependency EPIC-3→EPIC-2 blocks parallelism.** The gate engine doesn't need the wisp execution contract. Removing this dependency unlocks parallel development of EPIC-2 and EPIC-3, compressing the critical path by one full epic duration.

2. **EPIC-6 is too large and mixes three distinct concerns.** Crash recovery is the hardest engineering in this system. Bundling it with stop mechanics and retry obscures the critical path and makes the epic difficult to estimate, review, or ship incrementally.

3. **`gc sling` idempotency keys are an unacknowledged external dependency.** The entire convergence design — recovery, dedup, iteration derivation — rests on a capability that doesn't exist yet. This needs explicit sizing and sequencing, not a bullet in EPIC-2's scope.

### Overall Assessment

The decomposition is **solid and conservative**. The epic boundaries mostly follow natural architectural seams (store → runtime → gate → controller → CLI → resilience). Traceability is good — no major spec sections are orphaned. The dependency graph is acyclic and the ordering is defensible.

The main weaknesses are structural, not conceptual: one phantom dependency that prevents parallelism, one oversized epic that should split, and one hidden external dependency that could blow up timelines. None of these are design errors — they're decomposition granularity issues that are straightforward to fix.

### How I Would Split the Epics

```
EPIC-1a: Convergence Bead Store Extensions
  (type, metadata constants, absent-vs-empty semantics, child-wisp queries)

EPIC-1b: Protected-Prefix ACL and Controller Token
  (namespace-generic ACL, token lifecycle, agent env scrubbing, verdict allowlist)

EPIC-1c: Idempotency Key Store Primitives + gc sling Support
  (immutable key storage, exact/prefix lookup, gc sling --idempotency-key)

EPIC-2: Convergence Wisp Execution Contract  [depends: 1a, 1b, 1c]
  (formula schema, evaluate injection, artifact dirs, template context)

EPIC-3: Gate Evaluation Engine  [depends: 1a, 1b]  ← parallel with EPIC-2
  (script execution, timeout, hybrid/manual, verdict normalization, dry-run API)

EPIC-4: Controller Iteration State Machine  [depends: 2, 3]
  (wisp_closed handler, transitions, event emission, dedup, terminal states)

EPIC-5: Operator CLI  [depends: 2, 4]
  (create, list, status, test-gate, approve, iterate, creation-time validation)

EPIC-6a: depends_on_filter  [depends: 1a]  ← parallel with everything after 1a
  (general-purpose metadata-filtered dependencies)

EPIC-6b: Crash Recovery & Reconciliation  [depends: 4, 5]
  (startup scan, partial-transition repair, event re-emission)

EPIC-6c: Stop Mechanics  [depends: 4, 5]
  (force-close, drain, synthetic events, terminal ownership)

EPIC-6d: Retry  [depends: 5, 6b]
  (new loop from terminated, context linkage, note propagation safety)
```

Key differences: (1) EPIC-1 splits into three independently testable pieces, with `gc sling` idempotency keys getting explicit attention; (2) EPIC-2 and EPIC-3 run in parallel; (3) `depends_on_filter` is extracted as a standalone parallel track; (4) EPIC-6 splits into three pieces ordered by risk. This gives a critical path of `1a/1b/1c → {2 ∥ 3} → 4 → 5 → 6b → 6c/6d` with meaningful parallelism at two points.
