# Convergence Loops v0 — Implementation Plan

## Overview

7 epics across 5 implementation waves. The critical path is 5 epics
deep (EPIC-1 → EPIC-2 → EPIC-4 → EPIC-5 → EPIC-7). Two parallelism
opportunities compress the wall-clock schedule: EPIC-2/EPIC-3 in
wave 2 and EPIC-5/EPIC-6 in wave 4.

```
Wave 1:  EPIC-1  Store Foundation
                    │
         ┌──────────┼──────────┐
         ▼                     ▼
Wave 2:  EPIC-2  Formula &     EPIC-3  Gate Engine
         Wisp Execution            │
                │                  │
         └──────────┬──────────┘
                    ▼
Wave 3:  EPIC-4  Controller Handler & Events
                    │
         ┌──────────┼──────────┐
         ▼                     ▼
Wave 4:  EPIC-5  CLI &         EPIC-6  Crash Recovery
         Operator Workflow         │
                │                  │
         └──────────┬──────────┘
                    ▼
Wave 5:  EPIC-7  Stop, Retry & Dependency Filter
```

---

## Wave 1: Foundation

### EPIC-1: Convergence Store Foundation

**Priority:** P0 | **Risk:** high | **Blocked by:** none

Everything depends on this. Establishes the data model, store
invariants, and sling capabilities that all later epics build on.

**Delivers:**
1. `convergence` bead type registration in the bead store
2. All `convergence.*` metadata fields with correct encoding
   (decimal integers, Go durations, lowercase enums)
3. `var.*` template variable namespace on beads
4. **Store invariant: absent-vs-empty-string distinction.** Must
   survive MetaSet/MetaGet, `bd show --json`, metadata filtering,
   and query. Recovery branches on this distinction — it's a hard
   correctness requirement. Add conformance tests if missing.
5. **Idempotency key as immutable store field** (not mutable
   metadata) with exact lookup and prefix queries
   (`converge:<bead-id>:iter:*`). Required for crash recovery,
   dedup, and iteration derivation.
6. **`gc sling --idempotency-key` support.** If a wisp with the
   given key already exists, return the existing wisp ID instead
   of creating a new one. This is load-bearing for the entire
   convergence design.
7. **Protected prefix ACL.** Namespace-generic mechanism: controller
   generates token at startup (`.gc/controller.token`, mode 0600),
   all writes to protected prefixes require this token. Verdict
   allowlist: `convergence.agent_verdict` and
   `convergence.agent_verdict_wisp` (exact key match, not prefix).
8. Agent environment scrubbing: strip `GC_CONTROLLER_TOKEN` from
   agent spawn environment.
9. Config fields: `max_convergence_per_agent` (default: 2),
   `max_convergence_total` (default: 10).

**Spec coverage:** SPEC:metadata-encoding-contract,
SPEC:root-bead-schema, SPEC:threat-model,
SPEC:metadata-write-permissions

**Scope OUT:** Gate execution, controller handler, CLI commands,
event emission. Bead-scoped capabilities (v0 non-boundary).

**Key risks:**
- If `MetaSet`/`MetaGet` currently collapses absent/empty, fixing
  it is a store-level change that may ripple. Investigate early.
- `gc sling` idempotency key support may require changes to
  `MolCookOn`. Size this before committing to timelines.

**Test strategy:** Store-level unit tests. No running controller
needed. Conformance tests for absent-vs-empty on all query paths.

---

## Wave 2: Parallel Tracks (after EPIC-1)

### EPIC-2: Convergence Formula Contract & Wisp Execution

**Priority:** P0 | **Risk:** high | **Blocked by:** EPIC-1

Teaches the runtime how to pour a convergence iteration — from
formula parsing to the injected evaluate step. After this lands,
a convergence wisp can be deterministically created with artifact
paths, template context, and the terminal evaluate step.

**Delivers:**
1. Formula TOML extensions: `convergence = true` flag,
   `required_vars` list, `evaluate_prompt` path
2. Reserved step name validation: reject formulas with a step
   named `evaluate`
3. **Controller-injected evaluate step.** Appended automatically
   to every convergence wisp. Runs with `--evaluate-always` flag
   (executes even when wisp is in failed state).
4. Custom evaluate prompt support: formula's `evaluate_prompt`
   field. Static validation: must contain literal substrings
   `convergence.agent_verdict` and `convergence.agent_verdict_wisp`.
5. Template variable resolution: `{{ .BeadID }}`, `{{ .WispID }}`,
   `{{ .Iteration }}`, `{{ .ArtifactDir }}`, `{{ .Formula }}`,
   `{{ .RetrySource }}`, `{{ .Var.<key> }}`
6. Var key validation: valid Go identifiers only
7. Artifact directory creation: `.gc/artifacts/<bead-id>/iter-<N>/`
   before wisp pour
8. Default evaluate prompt: `prompts/convergence/evaluate.md`
9. Sample prompts: `update-draft.md`, `evaluate-design-review.md`

**Spec coverage:** SPEC:convergence-formula-contract,
SPEC:controller-injected-evaluate-step, SPEC:write-completion-contract,
SPEC:artifact-storage, SPEC:partial-fan-out-failure,
SPEC:sample-formula-mol-design-review-pass, SPEC:prompt-*

**Scope OUT:** Gate evaluation, event emission, crash recovery.

**Test strategy:** Formula parsing tests, template resolution tests,
evaluate step injection ordering tests. Fake bead store, no controller.

---

### EPIC-3: Gate Evaluation Engine

**Priority:** P0 | **Risk:** high | **Blocked by:** EPIC-1
**(parallel with EPIC-2)**

The gate runner that decides whether to iterate, stop, or wait.
A pure function: inputs are bead metadata + gate config, output
is a structured result. The controller calls it but doesn't own it.

**Delivers:**
1. Three gate modes: manual, condition, hybrid
2. **Gate script execution:** argv-style exec (never `/bin/sh -c`),
   environment whitelist (`PATH`, `HOME`, `TMPDIR` + convergence
   vars), cwd = city root
3. All gate environment variables: `$BEAD_ID`, `$ITERATION`,
   `$CITY_PATH`, `$WISP_ID`, `$DOC_PATH`, `$ARTIFACT_DIR`,
   `$ITERATION_DURATION_MS`, `$CUMULATIVE_DURATION_MS`,
   `$MAX_ITERATIONS`, `$AGENT_VERDICT`, `$AGENT_PROVIDER`,
   `$AGENT_MODEL`
4. Timeout handling with configurable `gate_timeout_action`:
   iterate, retry, manual, terminate
5. In-memory retry: max 3, resets on crash (advisory, not durable)
6. Output capture: stdout/stderr truncated to 4 KB each, `truncated` flag
7. Gate path canonicalization: `filepath.Abs` + `filepath.Clean`,
   symlink rejection
8. Artifact directory validation before gate execution
9. **Verdict normalization:** lowercase, trim whitespace, past-tense
   mapping (`approved`→`approve`, `blocked`→`block`), unknown→`block`
10. **Hybrid mode:** always run condition, pass `$AGENT_VERDICT`,
    condition is sole authority. Hybrid fallback to manual when no
    condition specified.
11. Gate output note sanitization (`[gate-output]` prefix)
12. **Dry-run API** for `gc converge test-gate` (read-only, no
    state changes)

**Spec coverage:** SPEC:gate-modes, SPEC:manual, SPEC:condition,
SPEC:hybrid, SPEC:terminal-states

**Scope OUT:** Controller integration, event emission, state
persistence. Gate sandboxing (v0 non-boundary).

**Test strategy:** Fake gate scripts (exit 0/1/timeout/not-found).
Test all timeout actions, hybrid matrix, verdict normalization
edge cases. Pure function — no controller, no bead store mutations.

---

## Wave 3: Core Integration

### EPIC-4: Controller Convergence Handler & Events

**Priority:** P0 | **Risk:** high | **Blocked by:** EPIC-2, EPIC-3

The heart of convergence: wires the gate engine and wisp execution
into the controller event loop. After this lands, convergence loops
execute end-to-end (create → iterate → gate → terminal) on the
normal (non-crash, non-stop) path.

**Delivers:**
1. **All 7 event type definitions** with schemas, stable `event_id`
   formulas, delivery tier classification, and field nullability:
   - Critical: `ConvergenceIteration`, `ConvergenceTerminated`
   - Recoverable: `ConvergenceCreated`, `ConvergenceWaitingManual`,
     `ConvergenceManualIterate`
   - Best-effort: `ConvergenceManualApprove`, `ConvergenceManualStop`
2. Canonical vs delivery-attempt field split
3. **9-step `wisp_closed` handler:**
   1. Guard: skip non-convergence, skip terminated beads
   2. Monotonic dedup: compare iteration numbers, reject stale
   3. Derive iteration: count closed child wisps
   4. Gate evaluation (idempotent via `gate_outcome_wisp` check)
   5. Persist gate outcome (`gate_outcome`, `gate_exit_code`,
      `gate_outcome_wisp`, `gate_retry_count`)
   6. Record iteration note (keyed by iteration number, replace)
   7. Prepare outcome: dispatch on gate result to iterate/
      waiting_manual/terminal/sling-failure path
   8. Emit events
   9. Commit point (`last_processed_wisp` + `status=closed` if terminal)
4. **Write ordering contract:** `terminal_reason` + `terminal_actor`
   before `state=terminated` before `status=closed`
5. Verdict replay safety: check `agent_verdict_wisp` matches
   current wisp before reading verdict
6. Verdict freshness: clear `agent_verdict` + `agent_verdict_wisp`
   before each new wisp pour (wisp-scoped)
7. Verdict non-execution detection: diagnostic warning
8. Wisp failure semantics: failed wisps count against
   `max_iterations`, missing verdict → `block`
9. **Sling failure path:** retry with exponential backoff
   (3 attempts, 1s/2s/4s), idempotency-key lookup before
   parking, `waiting_reason=sling_failure` as durable marker
10. Iteration note + gate output note with stable keys
11. `controller.sock` request types for convergence mutations
12. Nested convergence prevention (query for same-agent active loops)

**Spec coverage:** SPEC:controller-behavior,
SPEC:wisp-failure-semantics, SPEC:event-contracts,
SPEC:convergence* events, SPEC:nested-convergence-prevention,
SPEC:hidden-concurrency

**Scope OUT:** Crash recovery (EPIC-6), stop mechanics (EPIC-7),
CLI parsing (EPIC-5).

**Key risks:**
- This is the largest implementation epic. Consider splitting
  into "normal-path handler" and "event emission/payload" if
  it exceeds 8 stories during planning.
- The commit point (step 9) is the critical correctness boundary.
  Everything before it must be idempotent under replay.

**Test strategy:** Controller integration tests with fake bead
store and fake gate scripts. Cover: duplicate delivery, max-iteration
terminal, manual fallback, hybrid condition, sling failure path,
verdict replay safety.

---

## Wave 4: Parallel Tracks (after EPIC-4)

### EPIC-5: CLI Commands & Operator Workflow

**Priority:** P1 | **Risk:** medium | **Blocked by:** EPIC-4
**(parallel with EPIC-6)**

The operator-facing surface. CLI code is thin — it parses args,
validates preconditions, routes mutations through `controller.sock`,
and formats output.

**Delivers:**
1. `gc converge create` with all flags (`--formula`, `--target`,
   `--max-iterations`, `--gate`, `--gate-condition`,
   `--gate-timeout`, `--gate-timeout-action`, `--title`, `--var`)
2. Create-time validation:
   - `max_convergence_per_agent` and `max_convergence_total` checks
   - Nested convergence prevention (same target agent)
   - Formula `convergence=true` check
   - `required_vars` validation
   - Progressive activation level check (Level 6+)
3. `gc converge approve` handler: idempotency check, state check,
   write ordering
4. `gc converge iterate` handler: budget check, intent persistence
   ordering, verdict clearing, wisp pour with idempotency key
5. `gc converge status`: formatted output with history, gate
   output, duration
6. `gc converge list`: `--all` and `--state` filters, sorted output
7. `gc converge test-gate`: dry-run gate execution via EPIC-3's
   API, no state changes
8. Error messages with current state, rejection reason, suggested
   next action
9. All mutation commands route through `controller.sock`

**Spec coverage:** SPEC:cli, SPEC:preconditions, SPEC:commands,
SPEC:gc-converge-approve-handler, SPEC:gc-converge-iterate-handler,
SPEC:gc-converge-status-output, SPEC:cost-and-resource-controls,
SPEC:progressive-activation

**Scope OUT:** `gc converge stop` and `gc converge retry` (EPIC-7).

**Test strategy:** Cobra command tests with testscripts (txtar).
Golden output tests for status/list. Mutation routing tests via
mock controller socket.

---

### EPIC-6: Crash Recovery & Startup Reconciliation

**Priority:** P1 | **Risk:** high | **Blocked by:** EPIC-4
**(parallel with EPIC-5)**

The hardest testing epic. Implements the startup scan that recovers
convergence beads from any crash point in the controller handler.

**Delivers:**
1. Startup scan: query all `type=convergence` + `status=in_progress`
2. **Empty/missing state recovery:** detect create-crash, adopt or
   pour first wisp
3. **Terminated state recovery:** force-close open wisps, recompute
   iteration, backfill `terminal_actor`, complete terminal
   transition, emit synthetic events
4. **Waiting_manual recovery:**
   - Check `terminal_reason` → partial terminal transition
   - Check `waiting_reason` → intentional hold, repair
     `last_processed_wisp`, reconcile missing events
   - Check for orphaned wisps from crashed iterate
5. **Active state recovery:**
   - Check `terminal_reason` → partial stop
   - Check `waiting_reason` (sling_failure) → idempotency-key
     lookup, adopt or complete waiting_manual
   - Check `active_wisp` (open → noop, closed+unprocessed →
     replay handler, closed+processed → complete commit point,
     empty → derive from children)
6. `terminal_actor` backfill rules per spec
7. Synthetic event re-emission with `recovery: true`
8. Manual-iterate event reconciliation (event log scan)
9. Orphan wisp processing: ascending iteration order, re-read
   state after each
10. **Convergence child wisps must not be GC'd** while root bead
    has `status=in_progress`

**Spec coverage:** SPEC:crash-recovery

**Scope OUT:** CAS/conditional writes. Durable retry budgets.

**Key risks:**
- Every crash window in the controller handler creates a recovery
  case. Test coverage must cover every crash point identified in
  the spec.
- The `waiting_reason` check takes precedence over
  `active_wisp`-based replay to honor persisted decisions.
- When replaying an unprocessed wisp, do NOT clear `agent_verdict`
  beforehand.

**Test strategy:** Simulated crash-window tests. For each of the
9 handler steps: crash before, crash after, verify recovery
produces correct end state. This is where the absent-vs-empty
store invariant from EPIC-1 gets battle-tested.

---

## Wave 5: Final Integration

### EPIC-7: Stop Mechanics, Retry & Dependency Filter

**Priority:** P1 | **Risk:** medium | **Blocked by:** EPIC-5, EPIC-6

The final operator workflow capabilities. Stop is the most complex
handler here; retry is straightforward; `depends_on_filter` enables
downstream workflow integration.

**Delivers:**
1. **Full stop sequence (10 steps):**
   1. Drain completed iteration (if active wisp already closed)
   2. Persist stop intent (`terminal_reason=stopped`,
      `terminal_actor`)
   3. Write `convergence.state=terminated`
   4. Force-close active wisp (if still open)
   5. Recompute iteration count from closed child wisps
   6. Clear verdict metadata (`agent_verdict`,
      `agent_verdict_wisp`)
   7. Emit synthetic `ConvergenceIteration` for force-closed wisp
   8. Write `last_processed_wisp` to highest closed wisp
   9. Emit `ConvergenceTerminated`
   10. Write `status=closed` (commit point)
2. `gc converge stop` CLI command
3. **`gc converge retry` handler:** source validation, same
   concurrency checks, new root bead with `convergence.retry_source`,
   no note copying
4. `gc converge retry` CLI command
5. `ConvergenceManualStop` event (best-effort tier)
6. **`depends_on_filter`**: general-purpose metadata filter
   extension for dependency resolution. Enables downstream beads
   to filter on `convergence.terminal_reason=approved`.
7. Stop recovery: crash during stop sequence handled by EPIC-6's
   reconciliation (verify integration)

**Spec coverage:** SPEC:stop-mechanics,
SPEC:cancellation-propagation, SPEC:gc-converge-retry-handler

**Scope OUT:** Mid-iteration cancellation of nested fan-out.
Nested orchestration teardown (agent-owned). Automatic artifact
cleanup.

**Test strategy:** Stop sequence integration tests covering:
drain-first (wisp closed before stop), force-close (wisp still
open), crash during stop. Retry tests: source validation, context
linkage. `depends_on_filter` tests: filter match/mismatch.

---

## Traceability Matrix

| Spec Section | Epic |
|---|---|
| SPEC:concept | Background (all) |
| SPEC:metadata-encoding-contract | EPIC-1 |
| SPEC:root-bead-schema | EPIC-1 |
| SPEC:threat-model | EPIC-1 |
| SPEC:metadata-write-permissions | EPIC-1 |
| SPEC:gate-modes | EPIC-3 |
| SPEC:manual | EPIC-3 |
| SPEC:condition | EPIC-3 |
| SPEC:hybrid | EPIC-3 |
| SPEC:terminal-states | EPIC-3, EPIC-4 |
| SPEC:controller-behavior | EPIC-4 |
| SPEC:wisp-failure-semantics | EPIC-4 |
| SPEC:write-completion-contract | EPIC-2 |
| SPEC:crash-recovery | EPIC-6 |
| SPEC:controller-injected-evaluate-step | EPIC-2 |
| SPEC:cancellation-propagation | EPIC-7 |
| SPEC:nested-convergence-prevention | EPIC-4 |
| SPEC:hidden-concurrency | EPIC-4 |
| SPEC:stop-mechanics | EPIC-7 |
| SPEC:cost-and-resource-controls | EPIC-5 |
| SPEC:event-contracts | EPIC-4 |
| SPEC:convergencecreated | EPIC-4 |
| SPEC:convergenceiteration | EPIC-4 |
| SPEC:convergenceterminated | EPIC-4 |
| SPEC:convergencewaitingmanual | EPIC-4 |
| SPEC:convergencemanualapprove-* | EPIC-4 |
| SPEC:cli | EPIC-5 |
| SPEC:preconditions | EPIC-5 |
| SPEC:commands | EPIC-5 |
| SPEC:gc-converge-approve-handler | EPIC-5 |
| SPEC:gc-converge-iterate-handler | EPIC-5 |
| SPEC:gc-converge-retry-handler | EPIC-7 |
| SPEC:gc-converge-status-output | EPIC-5 |
| SPEC:artifact-storage | EPIC-2 |
| SPEC:partial-fan-out-failure | EPIC-2 |
| SPEC:convergence-formula-contract | EPIC-2 |
| SPEC:sample-formula-* | EPIC-2 |
| SPEC:prompt-* | EPIC-2 |
| SPEC:composition-* | Reference (EPIC-2) |
| SPEC:what-this-does-not-do | Constraints (all) |
| SPEC:other-convergence-consumers | Documentation |
| SPEC:progressive-activation | EPIC-5 |
| SPEC:open-questions | Resolved in epics |
| SPEC:known-limitations | Scope-out items |

## Critical Path Analysis

```
EPIC-1 (foundation)
  → EPIC-2 (formula/wisp) ──┐
  → EPIC-3 (gate engine) ───┤  ← parallel
                             ▼
                    EPIC-4 (controller)
                      → EPIC-5 (CLI) ────────┐
                      → EPIC-6 (recovery) ───┤  ← parallel
                                              ▼
                                     EPIC-7 (stop/retry)
```

**Critical path depth:** 5 epics
**Parallelism points:** 2 (waves 2 and 4)
**Independent work streams after EPIC-1:** 2 (formula + gate)
**Independent work streams after EPIC-4:** 2 (CLI + recovery)

## Implementation Notes

1. **Start with EPIC-1.** It's the only blocker for everything else.
   Investigate the absent-vs-empty store invariant and `gc sling`
   idempotency key support early — these are the two highest-risk
   items in the foundation.

2. **Waves 2 and 4 are real parallelism opportunities.** Two
   developers can work simultaneously on EPIC-2/3 and EPIC-5/6.

3. **EPIC-4 is the integration point.** Plan a focused integration
   session after waves 2 completes. The gate engine and wisp
   execution contract come together here.

4. **EPIC-6 has the highest test burden.** Every crash window in
   the 9-step handler creates a recovery case. Budget extra time
   for crash-window simulation tests.

5. **EPIC-7 is the least risky.** Stop and retry are well-specified
   and don't introduce new architectural concepts.
