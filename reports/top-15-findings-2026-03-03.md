# Top 15 Findings (2026-03-03)

1. **File:** `cmd/gc/main.go:214`
   **Severity:** critical
   **Problem:** Exec bead store formula resolver uses `filepath.Join(cityPath, "formulas")`.
   **Why wrong:** Runtime formulas are materialized under `.gc/formulas` (and layered symlinks), so this points at the wrong default directory and can cause formula resolution failures.
   **Correct behavior:** Resolve formulas from the configured/layered formulas location (at minimum `.gc/formulas`).

2. **File:** `cmd/gc/formula_resolve.go:45`
   **Severity:** medium
   **Problem:** `ResolveFormulas()` returns early when `len(winners)==0`.
   **Why wrong:** Stale symlinks are not cleaned when all formulas disappear, leaving obsolete formulas active.
   **Correct behavior:** Always run stale-symlink cleanup even when there are zero winners.

3. **File:** `internal/config/revision.go:37`
   **Severity:** critical
   **Problem:** Revision hashing only checks `r.Topology` per rig.
   **Why wrong:** `RigTopologies` and `Includes` are valid topology inputs (`EffectiveRigTopologies`) but are not hashed, so config changes can be missed.
   **Correct behavior:** Hash all effective rig topology refs, not only singular `Topology`.

4. **File:** `internal/config/revision.go:37`
   **Severity:** critical
   **Problem:** Revision hashing omits workspace-level topologies.
   **Why wrong:** `workspace.topology`, `workspace.topologies`, and `workspace.includes` influence effective config but are not included in topology content hashing.
   **Correct behavior:** Include all effective city topology refs in revision computation.

5. **File:** `internal/config/revision.go:72`
   **Severity:** critical
   **Problem:** Watch dir construction for topologies only uses `r.Topology`.
   **Why wrong:** Changes under rig `topologies`/`includes` are not watched, so hot reload can miss real config changes.
   **Correct behavior:** Add watch dirs for all effective rig topology refs.

6. **File:** `internal/config/revision.go:72`
   **Severity:** critical
   **Problem:** Workspace topology dirs are not added to watch set.
   **Why wrong:** City-level topology file edits may not trigger reload.
   **Correct behavior:** Add watch dirs for all effective city topology refs.

7. **File:** `cmd/gc/controller.go:428`
   **Severity:** medium
   **Problem:** Controller writes `.gc/daemon.pid` as primary daemon status signal.
   **Why wrong:** PID files can become stale and violate `CLAUDE.md` guidance to query live state instead of status files.
   **Correct behavior:** Determine controller liveness from live system primitives (socket/lock/process identity), not PID file truth.

8. **File:** `cmd/gc/cmd_daemon.go:252`
   **Severity:** medium
   **Problem:** `gc daemon status` treats PID file + signal-0 as authoritative.
   **Why wrong:** PID reuse can produce false positives (“running” for unrelated process).
   **Correct behavior:** Validate against controller identity (lock/socket handshake), not PID existence alone.

9. **File:** `cmd/gc/cmd_citystatus.go:80`
   **Severity:** medium
   **Problem:** City status controller state uses PID file + signal-0 check.
   **Why wrong:** Same stale/reused PID false-positive risk in user-facing status output.
   **Correct behavior:** Report controller state from live controller endpoint/lock ownership.

10. **File:** `cmd/gc/cmd_daemon_unix.go:12`
    **Severity:** low
    **Problem:** `isDaemonAlive` only checks process existence via signal 0.
    **Why wrong:** Existence is not identity; any process reusing the PID can satisfy this check.
    **Correct behavior:** Pair liveness with identity verification (command/socket/lock owner).

11. **File:** `internal/config/config.go:963`
    **Severity:** medium
    **Problem:** `DefaultCity` hardcodes role name `"mayor"`.
    **Why wrong:** Violates `CLAUDE.md` zero-hardcoded-role rule in Go code.
    **Correct behavior:** Default agent identity should come from configurable scaffolding/templates, not fixed role literals.

12. **File:** `internal/config/config.go:981`
    **Severity:** medium
    **Problem:** `WizardCity` hardcodes role name `"mayor"`.
    **Why wrong:** Same architectural violation (role logic embedded in framework code).
    **Correct behavior:** Wizard output should be role-agnostic or user-configured.

13. **File:** `internal/session/tmux/tmux.go:65`
    **Severity:** medium
    **Problem:** Hardcoded role-to-emoji map (`mayor`, `deacon`, `witness`, `polecat`, etc.).
    **Why wrong:** Encodes role semantics directly in Go, conflicting with role-configurability constraints.
    **Correct behavior:** Status icon mapping should come from config/theme metadata, not fixed role literals.

14. **File:** `internal/session/tmux/theme.go:33`
    **Severity:** medium
    **Problem:** `MayorTheme()` defines a dedicated hardcoded role theme.
    **Why wrong:** Role-specific framework behavior in code conflicts with zero-hardcoded-role architecture.
    **Correct behavior:** Theme selection should be config-driven and role-agnostic.

15. **File:** `internal/session/tmux/theme.go:39`
    **Severity:** medium
    **Problem:** `DeaconTheme()` (and related role-specific theme helpers) hardcode role semantics.
    **Why wrong:** Same architectural drift from role-as-config principle.
    **Correct behavior:** Remove role-special-case theme functions in favor of generic configured palettes.
