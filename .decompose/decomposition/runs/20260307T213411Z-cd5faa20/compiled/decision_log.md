# Compilation Decision Log

## D1: Epic granularity — 10 (Claude) vs 6 (Codex)
- Claude: 10 epics, more parallelism, finer boundaries
- Codex: 6 epics, tighter linear chain, fewer integration points
- Both reviews: neither has critical structure issues
- **Resolution:** 7 epics. Claude's separation of concerns is valuable (both reviews flag Codex's EPIC-6 as too large). But Claude's EPIC-2 (metadata ACL) and EPIC-9 (cost controls) are too small standalone. Merge ACL into foundation, merge cost controls into CLI create validation.

## D2: Foundation epic — split ACL or merge?
- Claude: separate EPIC-1 (store) and EPIC-2 (ACL)
- Codex: merged into single EPIC-1
- Claude's review: ACL could split "but not a hard split"
- **Resolution:** Merge. ACL is ~2 stories max. The token mechanism, verdict allowlist, and agent env scrubbing are part of the store foundation.

## D3: Gate engine dependencies
- Claude: EPIC-3 depends only on EPIC-1
- Codex: EPIC-3 depends on EPIC-1 AND EPIC-2 (wisp execution)
- Claude's review: dependency is phantom — gate doesn't need evaluate injection or formula schema
- **Resolution:** Claude is correct. Gate engine depends only on store primitives. This unlocks EPIC-2/EPIC-3 parallelism.

## D4: gc sling idempotency placement
- Claude: in EPIC-8 (formula contract), separate from foundation
- Codex: in EPIC-2 (wisp execution)
- Claude's review: flags this as unacknowledged external dependency
- **Resolution:** Place in EPIC-1 (foundation). It's load-bearing for the entire design. Explicit sizing.

## D5: CLI/Controller ownership split
- Claude: EPIC-5 (CLI) includes handler semantics, EPIC-6 (controller) includes socket handler
- Codex: EPIC-5 (CLI) depends on EPIC-4 (controller)
- Codex's review: ownership conflict — CLI can't deliver mutations without controller support
- **Resolution:** Controller handler owns all state transitions and event emission. CLI is thin: parse args, route through controller.sock, format output.

## D6: Recovery/Stop/Retry split
- Claude: crash recovery separate (EPIC-7), stop in EPIC-5/6
- Codex: all three merged in EPIC-6
- Claude's review: EPIC-6 too large, split into 3
- **Resolution:** EPIC-6 = crash recovery (high risk, critical). EPIC-7 = stop + retry (medium risk, depends on recovery).

## D7: depends_on_filter placement
- Claude: EPIC-10 (artifact storage)
- Codex: EPIC-5 (CLI)
- Claude's review: should be store-level, not CLI-level
- **Resolution:** Place in EPIC-7 (stop/retry). It's needed for downstream beads to depend on convergence outcome. Not urgent until convergence can terminate.
