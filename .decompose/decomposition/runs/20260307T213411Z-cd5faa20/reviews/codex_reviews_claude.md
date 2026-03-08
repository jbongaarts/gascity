1. Dimension: Dependency Correctness. Severity: HIGH. Finding: EPIC-5 and EPIC-6 have contradictory ownership over the mutation path. EPIC-5 scopes `approve`/`iterate`/`stop` handlers in, but scopes controller-side socket handling out; EPIC-6 scopes `controller.sock` handlers in. The recommended order also puts EPIC-5 before EPIC-6, which makes the mutation CLI non-deliverable without duplicating controller logic or violating the serialization invariant from the spec. Recommendation: move all mutation semantics and state transitions into EPIC-6 and leave EPIC-5 as CLI parsing/output only, or make EPIC-6 a hard prerequisite and remove duplicate handler ownership from EPIC-5.

2. Dimension: Dependency Correctness. Severity: HIGH. Finding: EPIC-6 is missing an explicit dependency on EPIC-8. The controller handler depends on keyed `gc sling`, `--evaluate-always`, formula `evaluate_prompt` parsing/validation, reserved-step validation, and template resolution to satisfy normal execution and replay semantics. Those are core prerequisites, not optional follow-on work. Recommendation: add EPIC-8 as a prerequisite to EPIC-6 and call out the required EPIC-8 capabilities explicitly.

3. Dimension: Dependency Correctness. Severity: HIGH. Finding: EPIC-5 is also under-declared in the dependency graph. Its `create`/`retry` scope includes formula validation (`convergence=true`, `required_vars`, reserved `evaluate`) and concurrency limit checks, but those capabilities are assigned to EPIC-8 and EPIC-9 while EPIC-5 is only blocked by EPIC-1 and EPIC-3. That creates either duplicate ownership or blocked work inside the epic. Recommendation: add EPIC-8 and EPIC-9 as prerequisites for the relevant EPIC-5 work, or move those validations fully into EPIC-5 and trim the overlapping scope elsewhere.

4. Dimension: Gap Analysis. Severity: MEDIUM. Finding: `SPEC:progressive-activation` is mapped to EPIC-9, but EPIC-9 does not include any implementation work that enforces “Level 6 or above” activation. The decomposition covers limits and observability, not capability gating. Recommendation: add explicit startup/create-time checks and tests that reject convergence below Level 6.

5. Dimension: Traceability Completeness. Severity: MEDIUM. Finding: EPIC-1 under-specifies the absent-vs-empty-string contract. The spec requires that distinction to survive store APIs, `bd show --json`, and metadata filtering/query behavior, but the epic only mentions the store invariant generically. Recommendation: expand EPIC-1 scope and tests to cover JSON serialization and filter/query behavior explicitly, not just storage.

6. Dimension: Epic Boundaries. Severity: MEDIUM. Finding: Gate and artifact ownership is blurred across EPIC-3, EPIC-5, EPIC-6, and EPIC-10. EPIC-3 says the gate engine should be pure, yet it also owns manual-mode state handling and create-time path canonicalization; artifact validation appears in both EPIC-3 and EPIC-10; EPIC-5 also owns gate-path canonicalization. This will produce duplicate implementations and inconsistent behavior. Recommendation: tighten ownership so EPIC-3 owns only gate execution/result normalization, EPIC-5 owns CLI/create validation, EPIC-6 owns controller state transitions, and EPIC-10 owns artifact layout or is split further.

Findings by severity:
- Critical: 0
- High: 3
- Medium: 3
- Low: 0

Top 3 issues:
1. EPIC-5/EPIC-6 ownership and ordering conflict around controller-routed mutation commands.
2. Missing EPIC-8 dependency for EPIC-6, even though controller correctness depends on sling idempotency and evaluate-step capabilities.
3. EPIC-5 missing EPIC-8/EPIC-9 dependencies for `create`/`retry` validation and concurrency enforcement.

Overall assessment:
The decomposition has strong broad coverage and the critical path is close, but it is not implementation-ready yet. The main problem is cross-epic ownership ambiguity around CLI/controller/wisp-pour behavior; fix the dependency graph and tighten the epic boundaries before story breakdown, or integration will stall in the controller path.
tokens used
47,450
[aimux] Using account 'retail-at' with config dir: /home/ubuntu/.aimux/codex/retail-at
