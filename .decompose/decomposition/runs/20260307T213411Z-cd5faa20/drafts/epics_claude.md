# Convergence Loops v0 — Implementation Epics

---

### EPIC-1: Convergence Bead Type and Metadata Schema

**Priority:** P0
**Risk:** high
**Blocked by:** none
**Spec sections:** SPEC:metadata-encoding-contract, SPEC:root-bead-schema

**Summary:** Establish the `convergence` bead type with its full metadata namespace (`convergence.*`, `var.*`). Implement the encoding contract (absent vs empty-string distinction, integer/duration/enum serialization) and the per-field lifecycle table. This is the data foundation everything else builds on.

**Scope IN:**
- Register `convergence` as a valid bead type in the bead store
- Implement all `convergence.*` metadata fields with correct encoding (decimal integers, Go duration strings, lowercase enums)
- Implement `var.*` template variable namespace on beads
- Verify store preserves absent-vs-empty-string distinction (add tests if missing)
- Bead status constraint: convergence beads use only `in_progress` and `closed`, never `open` or `failed`
- Idempotency key as an immutable store-level field (not mutable metadata) with index for prefix queries (`converge:<bead-id>:iter:*` → matching wisps with IDs and closure status)

**Scope OUT:**
- Protected-prefix ACL enforcement (EPIC-2)
- CLI commands (EPIC-5)
- Event emission (EPIC-4)

**Key implementation notes:**
- The store invariant on absent-vs-empty is a hard correctness requirement — if the current `MetaSet`/`MetaGet` collapses these, fix it here
- Idempotency key prefix queries are a required capability for crash recovery; design the index now
- `convergence.iteration` is a count of closed child wisps, not a simple counter — the derivation logic lives in the controller but the field lives here

---

### EPIC-2: Metadata Write Permissions (Protected Prefix ACL)

**Priority:** P0
**Risk:** high
**Blocked by:** EPIC-1
**Spec sections:** SPEC:metadata-write-permissions, SPEC:threat-model

**Summary:** Implement the namespace-generic protected prefix mechanism in the bead store. Controller generates a token at startup, stores it in `.gc/controller.token` (mode 0600), and all writes to protected prefixes (`convergence.*`, `var.*`) require this token — except the explicit allowlist (`convergence.agent_verdict`, `convergence.agent_verdict_wisp`).

**Scope IN:**
- Protected prefix registration API in the bead store (generic, not convergence-specific)
- Token generation at controller startup (`openssl rand -hex 32` or Go equivalent)
- Token file write (`.gc/controller.token`, mode 0600)
- `MetaSet` token check for protected-prefix writes at the API level
- `bd meta set` CLI reads `GC_CONTROLLER_TOKEN` env var and passes to `MetaSet`
- Agent-writable allowlist: exact key match for `convergence.agent_verdict` and `convergence.agent_verdict_wisp`
- Agent session token scrubbing: strip `GC_CONTROLLER_TOKEN` from agent spawn environment

**Scope OUT:**
- Cryptographic security boundary (explicitly not v0)
- Bead-scoped capabilities
- Content hashing of gate scripts

**Key implementation notes:**
- The mechanism must be namespace-generic so future SDK mechanisms can register their own prefixes
- Token is read once at startup, held in process memory — not re-read per write
- This is accidental-write prevention, not a security boundary per the threat model

---

### EPIC-3: Gate Evaluation Engine

**Priority:** P0
**Risk:** high
**Blocked by:** EPIC-1
**Spec sections:** SPEC:gate-modes, SPEC:manual, SPEC:condition, SPEC:hybrid, SPEC:terminal-states

**Summary:** Implement the three gate modes (manual, condition, hybrid) including gate script execution with environment scrubbing, timeout handling, configurable timeout actions (iterate/retry/manual/terminate), output capture with truncation, verdict normalization, and the hybrid decision matrix.

**Scope IN:**
- Gate script execution: argv-style exec (not `/bin/sh -c`), environment whitelist (`PATH`, `HOME`, `TMPDIR`, plus convergence-specific vars), cwd = city root
- All gate environment variables: `$BEAD_ID`, `$ITERATION`, `$CITY_PATH`, `$WISP_ID`, `$DOC_PATH`, `$ARTIFACT_DIR`, `$ITERATION_DURATION_MS`, `$CUMULATIVE_DURATION_MS`, `$MAX_ITERATIONS`, `$AGENT_VERDICT`, `$AGENT_PROVIDER`, `$AGENT_MODEL`
- Configurable timeout with `gate_timeout_action` dispatch (iterate/retry/manual/terminate)
- Retry logic: in-memory counter, max 3, resets on crash
- Output capture: stdout/stderr truncated to 4 KB each, `truncated` flag
- Path canonicalization at create time: `filepath.Abs` + `filepath.Clean`, symlink rejection
- Artifact directory validation before gate execution (reject symlinks outside root, reject non-regular files)
- Verdict normalization: lowercase, trim whitespace, past-tense mapping (`approved`→`approve`, `blocked`→`block`), unknown→`block`
- Hybrid mode: always run condition, pass `$AGENT_VERDICT`, condition is sole authority
- Hybrid fallback to manual when no condition specified
- Gate output note sanitization (`[gate-output]` prefix)
- Manual mode: set `waiting_manual` state, no gate evaluation

**Scope OUT:**
- Controller event loop integration (EPIC-6)
- Gate script content hashing
- Full sandboxing (chroot, seccomp)

**Key implementation notes:**
- Gate evaluation must be a pure function (inputs: bead metadata, gate config; outputs: outcome, exit code, captured output) for testability — the controller orchestrates it but the engine is independent
- Verdict normalization scope is frozen: case folding, whitespace trimming, past-tense mapping only
- The `gate_outcome` enum is `{pass, fail, timeout, error}` — `error` for pre-exec failures (not found, permission denied), no retry regardless of `gate_timeout_action`

---

### EPIC-4: Convergence Event Types

**Priority:** P1
**Risk:** medium
**Blocked by:** EPIC-1
**Spec sections:** SPEC:event-contracts, SPEC:convergencecreated, SPEC:convergenceiteration, SPEC:convergenceterminated, SPEC:convergencewaitingmanual, SPEC:convergencemanualapprove-convergencemanualiterate-

**Summary:** Define and register all convergence event types with their schemas, stable `event_id` formulas, delivery tier classification, field nullability rules, and the canonical-vs-delivery-attempt field split.

**Scope IN:**
- Event type definitions: `ConvergenceCreated`, `ConvergenceIteration`, `ConvergenceTerminated`, `ConvergenceWaitingManual`, `ConvergenceManualApprove`, `ConvergenceManualIterate`, `ConvergenceManualStop`
- Stable `event_id` formulas per event type (deterministic, iteration-scoped where applicable)
- Three delivery tiers: critical (at-least-once), recoverable (best-effort + reconciliation), best-effort
- All event fields per the schema tables, including nullable fields (always present as `null`, never omitted)
- Canonical vs delivery-attempt field classification
- Common fields: `event_id`, `event_type`, `bead_id`, `timestamp`, `recovery`
- Event emission helper functions usable by controller handlers

**Scope OUT:**
- Event bus infrastructure changes (assumes existing event bus works)
- Consumer-side deduplication logic
- Subscriber recovery protocol

**Key implementation notes:**
- `event_id` determinism is critical — same handler re-execution must produce same `event_id`
- `ConvergenceIteration` carries `waiting_reason` to let consumers act on `waiting_manual` from the critical tier alone
- `ConvergenceTerminated.final_status` is always `closed` and reflects handler intent, not committed state
- Normalized verdict (not raw) goes into events

---

### EPIC-5: CLI Command Tree (`gc converge`)

**Priority:** P1
**Risk:** medium
**Blocked by:** EPIC-1, EPIC-3
**Spec sections:** SPEC:cli, SPEC:preconditions, SPEC:commands, SPEC:gc-converge-approve-handler, SPEC:gc-converge-iterate-handler, SPEC:gc-converge-retry-handler, SPEC:gc-converge-status-output

**Summary:** Implement the `gc converge` command tree: `create`, `status`, `approve`, `iterate`, `stop`, `list`, `test-gate`, and `retry`. All mutation commands route through `controller.sock`. Includes concurrency limit checks, nested convergence prevention, and formula validation.

**Scope IN:**
- `gc converge create` with all flags (`--formula`, `--target`, `--max-iterations`, `--gate`, `--gate-condition`, `--gate-timeout`, `--gate-timeout-action`, `--title`, `--var`), prints bead ID to stdout
- Create validation: `max_convergence_per_agent`, `max_convergence_total`, nested convergence check, formula `convergence=true` check, `required_vars` validation, reserved `evaluate` step name rejection, gate path canonicalization
- `gc converge approve` handler: idempotency check, state check, write ordering (reason+actor before state before status)
- `gc converge iterate` handler: state check, budget check, intent persistence ordering (clear `waiting_reason` before `state=active`), verdict clearing, wisp pour with idempotency key
- `gc converge stop` handler: full stop sequence (drain completed iteration, persist intent, force-close wisp, recompute iteration, synthetic iteration event, terminal events)
- `gc converge retry` handler: source validation, same concurrency checks, new root bead with `retry_source`, no note copying
- `gc converge status`: formatted output with history, gate output, duration
- `gc converge list`: filtered/sorted output, `--all` and `--state` flags
- `gc converge test-gate`: dry-run gate execution, no state changes
- Error messages with current state, rejection reason, suggested next action
- All mutation commands route through `controller.sock`

**Scope OUT:**
- Controller-side socket handler implementation (EPIC-6)
- Root bead idempotency (acknowledged v0 limitation)

**Key implementation notes:**
- Stop mechanics are the most complex handler — the drain-completed-iteration step (checking if active wisp is already closed) prevents discarding legitimate work
- `iterate` write ordering is subtle: clearing `waiting_reason` before `state=active` prevents recovery from reverting a durable iterate intent
- `retry` does NOT copy notes — only stores `retry_source` metadata and a reference link

---

### EPIC-6: Controller Convergence Handler

**Priority:** P0
**Risk:** high
**Blocked by:** EPIC-1, EPIC-2, EPIC-3, EPIC-4
**Spec sections:** SPEC:controller-behavior, SPEC:wisp-failure-semantics, SPEC:write-completion-contract, SPEC:controller-injected-evaluate-step, SPEC:cancellation-propagation, SPEC:nested-convergence-prevention, SPEC:hidden-concurrency, SPEC:stop-mechanics

**Summary:** Implement the controller's `wisp_closed` handler for convergence beads — the core 9-step processing pipeline. Includes the injected evaluate step, wisp failure semantics, sling failure recovery path, serialization with CLI commands via `controller.sock`, and the write ordering contract.

**Scope IN:**
- 9-step `wisp_closed` handler: guard check → dedup (monotonic) → derive iteration → gate evaluation (idempotent via `gate_outcome_wisp`) → persist gate outcome → record iteration note → prepare outcome → emit events → commit point
- Serialization invariant: all convergence mutations (wisp_closed + CLI commands) serialized through controller event loop
- Controller-injected evaluate step: append to formula at wisp pour, `--evaluate-always` flag for failure-immune execution
- Custom evaluate prompt support: formula `evaluate_prompt` field, static validation (must contain `convergence.agent_verdict` and `convergence.agent_verdict_wisp` strings)
- Verdict freshness: clear `agent_verdict` and `agent_verdict_wisp` before each new wisp pour
- Verdict replay safety: check `agent_verdict_wisp` matches current wisp before reading verdict
- Verdict non-execution detection: log diagnostic warning if evaluate output contains `convergence.agent_verdict` but metadata is empty
- Wisp failure semantics: failed wisps count against `max_iterations`, missing verdict → `block`
- Sling failure path: retry with exponential backoff (3 attempts, 1s/2s/4s), idempotency key lookup, `waiting_reason=sling_failure` as durable decision marker
- Write ordering contract: `terminal_reason`+`terminal_actor` before `state=terminated` before `status=closed`
- Iteration note keyed by iteration number for idempotency (replace, not append)
- Gate output note keyed by `[gate-output:iter-<N>]`
- `convergence.active_wisp` retained through terminal transitions (not cleared)
- Artifact directory creation before wisp pour
- `controller.sock` handler for approve/iterate/stop commands

**Scope OUT:**
- Crash recovery reconciliation (EPIC-7)
- CLI argument parsing (EPIC-5 — this epic handles the controller-side logic)

**Key implementation notes:**
- The commit point (step 9) is the critical correctness boundary — everything before it must be idempotent under replay
- `active_wisp` is NOT cleared on terminal transition to avoid a crash window where recovery sees empty `active_wisp` and incorrectly pours a new wisp
- The sling-failure path writes `waiting_reason` FIRST as a durable decision marker before any state changes
- Evaluate step uses `--evaluate-always` flag — runs even when wisp is in failed state (hybrid failure mode specific to convergence)

---

### EPIC-7: Crash Recovery and Startup Reconciliation

**Priority:** P1
**Risk:** high
**Blocked by:** EPIC-6
**Spec sections:** SPEC:crash-recovery

**Summary:** Implement the startup reconciliation scan that recovers convergence beads from any crash point. Handles all state combinations: empty/missing state, terminated with incomplete writes, waiting_manual with orphaned wisps or partial terminal transitions, active with sling-failure markers or unprocessed closed wisps.

**Scope IN:**
- Startup scan: query all `type=convergence` + `status=in_progress` beads
- Empty/missing state recovery: detect create-crash, adopt or pour first wisp
- Terminated state recovery: force-close open wisps, recompute iteration, backfill `terminal_actor`, complete terminal transition, emit synthetic events
- Waiting_manual recovery: check `terminal_reason` for partial terminal transitions; check `waiting_reason` for intentional manual holds (repair `last_processed_wisp`, reconcile missing events); check for orphaned wisps (open or closed) from crashed `gc converge iterate`
- Active state recovery: check `terminal_reason` for partial stops; check `waiting_reason` for sling-failure markers (idempotency key lookup for next wisp, adopt or complete waiting_manual); check `active_wisp` (open → do nothing, closed+unprocessed → replay handler, closed+processed → complete partial commit point, empty → derive from closed children and replay/pour)
- `terminal_actor` backfill rules: `stopped` → `operator:unknown`; `approved`/`no_convergence` from `waiting_manual` → `operator:unknown`; from `active` → `controller`
- Synthetic event emission with `recovery: true` for missing critical/recoverable events
- Manual-iterate event reconciliation: detect by checking event log for `converge:<bead_id>:iter:<N>:manual_iterate`
- Orphan wisp processing: ascending iteration order, re-read state after each, stop if handler transitions state
- Convergence child wisps must not be GC'd while root bead has `status=in_progress`

**Scope OUT:**
- CAS/conditional writes (acknowledged limitation)
- Durable retry budgets

**Key implementation notes:**
- This is the most complex epic — every crash window in the controller handler creates a recovery case
- The `waiting_reason` check takes precedence over `active_wisp`-based replay to honor persisted decisions
- When replaying an unprocessed wisp, do NOT clear `agent_verdict` beforehand — the wisp's evaluate step may have written a fresh verdict
- After replay, re-read `convergence.state` before taking further action — the replayed handler owns the state transition
- Test coverage must cover every crash point identified in the spec

---

### EPIC-8: Convergence Formula Contract and Keyed Sling Idempotency

**Priority:** P1
**Risk:** medium
**Blocked by:** EPIC-1
**Spec sections:** SPEC:convergence-formula-contract, SPEC:sample-formula-mol-design-review-pass, SPEC:prompt-update-draft-md, SPEC:prompt-evaluate-md-default-generic, SPEC:prompt-evaluate-design-review-md-domain-specific-o

**Summary:** Implement the convergence formula TOML contract (`convergence=true`, `required_vars`, `evaluate_prompt`), template variable resolution (`{{ .BeadID }}`, `{{ .Var.* }}`, etc.), and add idempotency key support to `gc sling`. Includes the default and sample evaluate prompts.

**Scope IN:**
- Formula TOML parsing: `convergence` boolean flag, `required_vars` list, `evaluate_prompt` path
- `gc sling --idempotency-key` support: if wisp with given key exists, return existing wisp ID
- `gc sling --on` integration: pass idempotency key `converge:<bead-id>:iter:<N>`
- `gc sling --evaluate-always` flag for the injected evaluate step
- Template variable resolution: `{{ .BeadID }}`, `{{ .WispID }}`, `{{ .Iteration }}`, `{{ .ArtifactDir }}`, `{{ .Formula }}`, `{{ .RetrySource }}`, `{{ .Var.<key> }}`
- Var key validation: must be valid Go identifiers (letters, digits, underscores)
- `var.*` metadata read from root bead at wisp-pour time
- Default evaluate prompt: `prompts/convergence/evaluate.md`
- Sample prompts: `update-draft.md`, `evaluate-design-review.md`
- Reserved step name validation: reject formulas with a step named `evaluate`

**Scope OUT:**
- Formula resolution system changes beyond convergence flags
- The sample `mol-design-review-pass` formula as a deployable artifact (it's a reference)

**Key implementation notes:**
- Idempotency key support in `gc sling` is a prerequisite for convergence — this is flagged as a known limitation in the spec
- Template variables are read from the root bead, not copied to the wisp
- `evaluate_prompt` validation: must contain both `bd meta set` and `convergence.agent_verdict` as literal substrings

---

### EPIC-9: Cost Controls and Concurrency Limits

**Priority:** P1
**Risk:** low
**Blocked by:** EPIC-1
**Spec sections:** SPEC:cost-and-resource-controls, SPEC:hidden-concurrency

**Summary:** Implement `max_convergence_per_agent` and `max_convergence_total` config fields, concurrency limit enforcement at `gc converge create`/`retry`, and cost proxy infrastructure (duration tracking, token count passthrough).

**Scope IN:**
- Config fields: `max_convergence_per_agent` (default: 2), `max_convergence_total` (default: 10) in `[city]` section
- Concurrency enforcement at create/retry time: query active convergence beads by target, count total active
- Duration tracking: `iteration_duration_ms` (wall-clock of wisp), `cumulative_duration_ms` (sum across iterations)
- Token count passthrough: `iteration_tokens`, `cumulative_tokens` (nullable, provider-dependent)
- Cost proxy environment variables for gate conditions: `$ITERATION_DURATION_MS`, `$CUMULATIVE_DURATION_MS`

**Scope OUT:**
- Priority ordering among active loops
- Mid-iteration circuit breakers
- Backpressure mechanisms

**Key implementation notes:**
- Concurrency checks must also run during `gc converge retry` (it creates a new active loop)
- Duration is wall-clock, computed from wisp open/close timestamps
- Token counts are advisory delivery-attempt fields — not persisted durably before event emission

---

### EPIC-10: Artifact Storage and Depends-On Filter

**Priority:** P2
**Risk:** low
**Blocked by:** EPIC-1
**Spec sections:** SPEC:artifact-storage, SPEC:partial-fan-out-failure, SPEC:terminal-states (depends_on_filter), SPEC:composition-design-review-inside-convergence

**Summary:** Implement the artifact directory convention (`.gc/artifacts/<bead-id>/iter-<N>/`), artifact directory creation/validation, and the `depends_on_filter` extension for metadata-filtered dependency resolution.

**Scope IN:**
- Artifact directory layout: `.gc/artifacts/<bead-id>/iter-<N>/`
- Directory creation by controller before wisp pour
- `$ARTIFACT_DIR` environment variable and `{{ .ArtifactDir }}` template variable
- Artifact directory validation for gate execution: reject symlinks outside artifact root, reject non-regular files
- `depends_on_filter` extension to dependency mechanism: optional metadata filter map on `depends_on`
- No automatic cleanup (explicit v0 policy)

**Scope OUT:**
- Automatic artifact cleanup/archival
- Remote artifact storage
- Size limits
- Manifest-based completeness checking (formula-level concern, not primitive)

**Key implementation notes:**
- `depends_on_filter` is a general-purpose bead store extension, not convergence-specific — design accordingly
- Artifact directory validation must happen before every gate execution (an agent could place special files between iterations)
- The composition example (design review inside convergence) is a reference for documentation, not implementation scope

---

## Traceability Matrix

| Spec Section | Epic |
|---|---|
| SPEC:concept | Background context (all epics) |
| SPEC:metadata-encoding-contract | EPIC-1 |
| SPEC:root-bead-schema | EPIC-1 |
| SPEC:threat-model | EPIC-2 |
| SPEC:metadata-write-permissions | EPIC-2 |
| SPEC:gate-modes | EPIC-3 |
| SPEC:manual | EPIC-3 |
| SPEC:condition | EPIC-3 |
| SPEC:hybrid | EPIC-3 |
| SPEC:terminal-states | EPIC-3 (states), EPIC-10 (depends_on_filter) |
| SPEC:controller-behavior | EPIC-6 |
| SPEC:wisp-failure-semantics | EPIC-6 |
| SPEC:write-completion-contract | EPIC-6 |
| SPEC:crash-recovery | EPIC-7 |
| SPEC:controller-injected-evaluate-step | EPIC-6, EPIC-8 |
| SPEC:cancellation-propagation | EPIC-6 |
| SPEC:nested-convergence-prevention | EPIC-5, EPIC-6 |
| SPEC:hidden-concurrency | EPIC-9 |
| SPEC:stop-mechanics | EPIC-5, EPIC-6 |
| SPEC:cost-and-resource-controls | EPIC-9 |
| SPEC:event-contracts | EPIC-4 |
| SPEC:convergencecreated | EPIC-4 |
| SPEC:convergenceiteration | EPIC-4 |
| SPEC:convergenceterminated | EPIC-4 |
| SPEC:convergencewaitingmanual | EPIC-4 |
| SPEC:convergencemanualapprove-convergencemanualiterate- | EPIC-4 |
| SPEC:cli | EPIC-5 |
| SPEC:preconditions | EPIC-5 |
| SPEC:commands | EPIC-5 |
| SPEC:gc-converge-approve-handler | EPIC-5 |
| SPEC:gc-converge-iterate-handler | EPIC-5 |
| SPEC:gc-converge-retry-handler | EPIC-5 |
| SPEC:gc-converge-status-output | EPIC-5 |
| SPEC:artifact-storage | EPIC-10 |
| SPEC:partial-fan-out-failure | EPIC-10 |
| SPEC:convergence-formula-contract | EPIC-8 |
| SPEC:sample-formula-mol-design-review-pass | EPIC-8 |
| SPEC:prompt-update-draft-md | EPIC-8 |
| SPEC:prompt-evaluate-md-default-generic | EPIC-8 |
| SPEC:prompt-evaluate-design-review-md-domain-specific-o | EPIC-8 |
| SPEC:composition-design-review-inside-convergence | EPIC-10 |
| SPEC:what-this-does-not-do | Design constraints (all epics) |
| SPEC:other-convergence-consumers | Documentation only |
| SPEC:progressive-activation | EPIC-9 |
| SPEC:open-questions | Resolved in respective epics |
| SPEC:known-limitations | Distributed across epics as scope-out items |

## Dependency Graph

```
EPIC-1  Bead Type & Metadata Schema
  ├──► EPIC-2  Metadata Write Permissions (Protected Prefix ACL)
  ├──► EPIC-3  Gate Evaluation Engine
  ├──► EPIC-4  Convergence Event Types
  ├──► EPIC-8  Formula Contract & Keyed Sling Idempotency
  ├──► EPIC-9  Cost Controls & Concurrency Limits
  └──► EPIC-10 Artifact Storage & Depends-On Filter

EPIC-1 + EPIC-2 + EPIC-3 + EPIC-4
  └──► EPIC-6  Controller Convergence Handler

EPIC-1 + EPIC-3
  └──► EPIC-5  CLI Command Tree

EPIC-6
  └──► EPIC-7  Crash Recovery & Startup Reconciliation
```

**Critical path:** EPIC-1 → EPIC-2 + EPIC-3 (parallel) → EPIC-6 → EPIC-7

**Recommended implementation order:**
1. EPIC-1 (foundation — everything depends on it)
2. EPIC-2, EPIC-3, EPIC-4, EPIC-8 (parallel — independent of each other, all depend only on EPIC-1)
3. EPIC-9, EPIC-10 (parallel — low risk, minimal dependencies)
4. EPIC-5 (needs EPIC-3 for test-gate; can start CLI scaffolding earlier)
5. EPIC-6 (needs 1-4; the core integration epic)
6. EPIC-7 (needs EPIC-6; the hardest testing epic)
