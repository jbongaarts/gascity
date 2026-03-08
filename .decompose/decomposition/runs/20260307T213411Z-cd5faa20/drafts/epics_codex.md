# Convergence Implementation Epics

### EPIC-1: Convergence State and Store Primitives

**Priority:** P0  
**Risk:** high  
**Blocked by:** none  
**Spec sections:** SPEC:convergence-loops-v0, SPEC:concept, SPEC:metadata-encoding-contract, SPEC:root-bead-schema, SPEC:threat-model, SPEC:metadata-write-permissions

**Summary:** This epic makes convergence a durable first-class concept in the bead store and controller-facing APIs. It establishes the metadata, ACL, and identity guarantees that every later lifecycle, recovery, and CLI path depends on.

**Scope IN:**
- Add `type=convergence` support and root-bead metadata constants/helpers
- Preserve absent-vs-empty-string metadata semantics across store APIs, JSON output, and filtering
- Implement protected-prefix metadata writes with controller token enforcement and verdict-key allowlist
- Add controller token generation, storage, and agent environment scrubbing hooks
- Add immutable idempotency-key storage/indexes plus exact and prefix lookup for convergence wisps
- Add store/query helpers needed to enumerate convergence child wisps safely

**Scope OUT:**
- Gate execution and timeout logic
- Controller `wisp_closed` state transitions
- User-facing `gc converge` commands

**Key implementation notes:**
- Enforce ACLs in the store API, not only in CLI code paths
- Treat idempotency keys as store fields, not mutable metadata
- Independent testability: store/API regression tests can fully validate this epic without a running controller

---

### EPIC-2: Convergence Wisp Execution Contract

**Priority:** P0  
**Risk:** high  
**Blocked by:** EPIC-1  
**Spec sections:** SPEC:write-completion-contract, SPEC:controller-injected-evaluate-step, SPEC:artifact-storage, SPEC:convergence-formula-contract, SPEC:sample-formula-mol-design-review-pass, SPEC:prompt-update-draft-md, SPEC:prompt-evaluate-md-default-generic, SPEC:prompt-evaluate-design-review-md-domain-specific-o

**Summary:** This epic teaches the runtime how a convergence iteration is poured and executed while keeping formulas single-pass. After it lands, a convergence wisp can be created deterministically with artifact paths, template context, and the injected terminal evaluate step.

**Scope IN:**
- Extend formula schema with `convergence`, `required_vars`, and `evaluate_prompt`
- Validate reserved `evaluate` step name and required prompt literals for custom evaluate prompts
- Inject the final evaluate step automatically and ensure it always runs after formula-declared steps
- Add `evaluate-always` runtime behavior for failed wisps
- Create iteration artifact directories and populate template/env context (`BeadID`, `WispID`, `Iteration`, `ArtifactDir`, `RetrySource`, `var.*`)
- Finish keyed idempotency plumbing in `gc sling` for convergence pours

**Scope OUT:**
- Gate pass/fail decision logic
- Event emission and lifecycle transitions
- Crash recovery

**Key implementation notes:**
- Keep evaluate-step injection centralized; formula authors should not encode controller behavior themselves
- Validate prompt/config statically and conservatively; do not parse prompt intent heuristically
- Independent testability: runtime tests can verify injected-step ordering, artifact layout, and idempotent wisp adoption

---

### EPIC-3: Gate Evaluation Engine

**Priority:** P0  
**Risk:** high  
**Blocked by:** EPIC-1, EPIC-2  
**Spec sections:** SPEC:gate-modes, SPEC:manual, SPEC:condition, SPEC:hybrid, SPEC:cost-and-resource-controls, SPEC:partial-fan-out-failure

**Summary:** This epic builds the structured gate runner that the controller will call after each iteration. It covers safe script execution, hybrid/manual semantics, timeout handling, verdict normalization, and the captured result payload used by both lifecycle handlers and `gc converge test-gate`.

**Scope IN:**
- Implement manual, condition, and hybrid gate evaluators with one shared result contract
- Canonicalize gate paths, reject symlinks, scrub environment, set working directory, and validate artifact trees
- Implement timeout behavior for `iterate`, `retry`, `manual`, and `terminate`
- Normalize and scope agent verdict reads to the current wisp
- Capture stdout/stderr, truncation flags, exit code, retry count, and duration/cumulative timing
- Expose a reusable gate-runner API for controller handlers and dry-run CLI usage

**Scope OUT:**
- Persisting loop state or pouring next wisps
- Startup reconciliation
- Manual command routing

**Key implementation notes:**
- Use exec-style argv invocation only; never shell-interpolate bead data
- Return enough structured data that controller code can persist and replay gate outcomes deterministically
- Independent testability: fake scripts can cover pass/fail/timeout/pre-exec-error/hybrid-no-condition cases

---

### EPIC-4: Controller Iteration State Machine and Events

**Priority:** P0  
**Risk:** high  
**Blocked by:** EPIC-2, EPIC-3  
**Spec sections:** SPEC:terminal-states, SPEC:controller-behavior, SPEC:wisp-failure-semantics, SPEC:event-contracts, SPEC:convergenceiteration, SPEC:convergenceterminated, SPEC:convergencewaitingmanual, SPEC:composition-design-review-inside-convergence

**Summary:** This epic implements the normal-path convergence lifecycle inside the controller. It turns a closed convergence wisp into persisted loop state, a next action, and the required critical/recoverable event stream.

**Scope IN:**
- Handle `wisp_closed` for convergence roots with monotonic dedup and iteration derivation
- Persist `gate_outcome*`, iteration notes, and commit-point metadata in the documented write order
- Decide iterate vs waiting-manual vs terminal transitions for normal non-crash execution
- Emit stable `ConvergenceIteration`, `ConvergenceWaitingManual`, and `ConvergenceTerminated` events with deterministic `event_id`s
- Apply terminal-state semantics (`approved`, `no_convergence`) and close the root bead correctly
- Treat failed wisps and missing verdicts according to convergence failure semantics

**Scope OUT:**
- Startup recovery and repair after crashes
- `gc converge stop` and retry-loop creation
- User-facing CLI output formatting

**Key implementation notes:**
- `gate_outcome_wisp` and `last_processed_wisp` are the replay boundary; bugs here will cascade into recovery defects
- Build event payloads from persisted state wherever possible so duplicates stay canonical
- Independent testability: controller integration tests should cover duplicate delivery, max-iteration terminal, manual fallback, and hybrid condition paths

---

### EPIC-5: Operator CLI and Workflow Integration

**Priority:** P1  
**Risk:** medium  
**Blocked by:** EPIC-4  
**Spec sections:** SPEC:convergencecreated, SPEC:convergencemanualapprove-convergencemanualiterate-, SPEC:cli, SPEC:preconditions, SPEC:commands, SPEC:gc-converge-approve-handler, SPEC:gc-converge-iterate-handler, SPEC:gc-converge-status-output, SPEC:nested-convergence-prevention, SPEC:hidden-concurrency, SPEC:progressive-activation

**Summary:** This epic exposes convergence to operators and adjacent workflow machinery once the controller lifecycle exists. It delivers the usable `gc converge` command surface, creation-time validation, stateful manual actions, and the scheduler/dependency integration needed for real workflows.

**Scope IN:**
- Add the `gc converge` cobra tree for `create`, `list`, `status`, `test-gate`, `approve`, and `iterate`
- Add controller RPC/request types so CLI mutations stay serialized through `controller.sock`
- Implement create-time checks for activation level, per-agent limit, city-wide limit, nested same-agent convergence, and formula vars
- Emit `ConvergenceCreated`, `ConvergenceManualApprove`, and `ConvergenceManualIterate` from command flows
- Implement status/list/test-gate output and operator-facing error contracts
- Extend dependency resolution with generic `depends_on_filter` support so downstream work can require `convergence.terminal_reason=approved`

**Scope OUT:**
- Startup reconciliation
- `gc converge stop` and `gc converge retry`
- Notifications/automation driven from convergence events

**Key implementation notes:**
- Keep CLI code thin; controller logic remains the single writer for convergence state
- Make `depends_on_filter` generic rather than convergence-specific
- Independent testability: golden CLI tests plus scheduler tests can validate this epic without fault-injection machinery

---

### EPIC-6: Recovery, Stop, and Retry Resilience

**Priority:** P1  
**Risk:** high  
**Blocked by:** EPIC-4, EPIC-5  
**Spec sections:** SPEC:crash-recovery, SPEC:cancellation-propagation, SPEC:stop-mechanics, SPEC:gc-converge-retry-handler, SPEC:convergencemanualapprove-convergencemanualiterate-

**Summary:** This epic hardens convergence for production failures and operator interruption. It adds startup reconciliation, partial-transition repair, stop semantics, retry-loop creation, and recovery-time event re-emission so convergence remains correct under crash-and-restart conditions.

**Scope IN:**
- Implement startup reconciliation for `active`, `waiting_manual`, and partially terminated convergence beads
- Repair partial commit points using `active_wisp`, `waiting_reason`, `terminal_reason`, `last_processed_wisp`, and `gate_outcome_wisp`
- Implement full `gc converge stop` behavior, including force-close, synthetic iteration events, and terminal finalization ownership
- Implement `gc converge retry` to seed a new loop from a terminated non-approved loop
- Re-emit recoverable and critical events on recovery with `recovery=true`
- Wire remaining CLI/controller handling for `stop` and `retry`

**Scope OUT:**
- Mid-iteration cancellation of nested fan-out
- Durable gate retry budgets across crashes
- Artifact cleanup/archival automation

**Key implementation notes:**
- Model recovery as a finite state repair table, not as scattered special cases
- Simulated crash-window tests are mandatory for every documented write-order boundary
- Preserve the spec’s non-goals: stop ends at the wisp boundary, and nested orchestration teardown remains agent-owned

---

## Traceability Matrix

| Epic | Primary spec coverage |
| --- | --- |
| EPIC-1 | SPEC:convergence-loops-v0, SPEC:concept, SPEC:metadata-encoding-contract, SPEC:root-bead-schema, SPEC:threat-model, SPEC:metadata-write-permissions |
| EPIC-2 | SPEC:write-completion-contract, SPEC:controller-injected-evaluate-step, SPEC:artifact-storage, SPEC:convergence-formula-contract, SPEC:sample-formula-mol-design-review-pass, SPEC:prompt-update-draft-md, SPEC:prompt-evaluate-md-default-generic, SPEC:prompt-evaluate-design-review-md-domain-specific-o |
| EPIC-3 | SPEC:gate-modes, SPEC:manual, SPEC:condition, SPEC:hybrid, SPEC:cost-and-resource-controls, SPEC:partial-fan-out-failure |
| EPIC-4 | SPEC:terminal-states, SPEC:controller-behavior, SPEC:wisp-failure-semantics, SPEC:event-contracts, SPEC:convergenceiteration, SPEC:convergenceterminated, SPEC:convergencewaitingmanual, SPEC:composition-design-review-inside-convergence |
| EPIC-5 | SPEC:convergencecreated, SPEC:convergencemanualapprove-convergencemanualiterate-, SPEC:cli, SPEC:preconditions, SPEC:commands, SPEC:gc-converge-approve-handler, SPEC:gc-converge-iterate-handler, SPEC:gc-converge-status-output, SPEC:nested-convergence-prevention, SPEC:hidden-concurrency, SPEC:progressive-activation |
| EPIC-6 | SPEC:crash-recovery, SPEC:cancellation-propagation, SPEC:stop-mechanics, SPEC:gc-converge-retry-handler |
| Cross-cutting constraints | SPEC:what-this-does-not-do, SPEC:other-convergence-consumers, SPEC:open-questions, SPEC:known-limitations -> enforced across EPIC-2, EPIC-3, EPIC-4, EPIC-5, and EPIC-6 |

## Dependency Graph

```text
EPIC-1
EPIC-2 -> EPIC-1
EPIC-3 -> EPIC-1, EPIC-2
EPIC-4 -> EPIC-2, EPIC-3
EPIC-5 -> EPIC-4
EPIC-6 -> EPIC-4, EPIC-5
```

Recommended implementation order: `EPIC-1 -> EPIC-2 -> EPIC-3 -> EPIC-4 -> EPIC-5 -> EPIC-6`.
tokens used
51,363
[aimux] Using account 'retail-at' with config dir: /home/ubuntu/.aimux/codex/retail-at
