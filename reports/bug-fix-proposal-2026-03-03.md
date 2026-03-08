# Bug Fix Proposal — 2026-03-03

Cross-referenced findings from 5 random-walk audit agents with the
prior `top-15-findings-2026-03-03.md` report. Filtered to **real bugs**
(functional impact, security, correctness). Excludes convention violations
(hardcoded role names), design-preference deviations (PID files), and
low-impact style issues.

## Tier 1 — Critical (fix immediately)

### Bug 1: Pool deep-copy missing `Suspended`, `IdleTimeout`, `InstallAgentHooks`

**File:** `cmd/gc/pool.go:170-191`
**Confirmed by:** 4 of 5 agents + prior report context

Pool instances silently drop three parent fields:
- `Suspended` pool spawns instances anyway
- `IdleTimeout` is never enforced on pool instances
- `InstallAgentHooks` aren't installed for instances

**Fix:** Add the three fields to the `instanceAgent` struct literal:
```go
Suspended:         cfgAgent.Suspended,
IdleTimeout:       cfgAgent.IdleTimeout,
```
Plus a deep-copy of `InstallAgentHooks` matching the existing slice copy pattern.

---

### Bug 2: Missing `validateSessionName` in `NewSessionWithCommandAndEnv`

**File:** `internal/session/tmux/tmux.go:232`
**Confirmed by:** 1 agent

This is the primary session creation path (adapter calls it). Both
`NewSession` and `NewSessionWithCommand` validate, but this one skips it.
Invalid session names could cause tmux command injection.

**Fix:** Add `if err := validateSessionName(name); err != nil { return err }`
as the first line.

---

### Bug 3: `Revision()` and `WatchDirs()` miss plural topologies + city-level topologies

**File:** `internal/config/revision.go:37-47, 72-78`
**Confirmed by:** 2 agents + prior report (#3, #4, #5, #6)

`Revision()` only hashes `r.Topology` (singular). Rigs using `topologies = [...]`
or `includes = [...]` are invisible. City-level topology dirs are also excluded.
Result: controller hot-reload silently misses real config changes.

**Fix:** Replace the per-rig `r.Topology` check with `EffectiveRigTopologies(r)`
iteration. Add city-level topology dirs from `cfg.Workspace`. Apply same fix to
`WatchDirs()`. The function signature may need to accept the full `City` struct
(or at minimum the workspace topology refs).

---

### Bug 4: Exec bead store formula resolver points to wrong directory

**File:** `cmd/gc/main.go:214`
**Confirmed by:** prior report (#1)

```go
s.SetFormulaResolver(formula.DirResolver(filepath.Join(cityPath, "formulas")))
```
Runtime formulas are materialized under `.gc/formulas`, not `formulas/`.

**Fix:** Change to `filepath.Join(cityPath, ".gc", "formulas")`.

---

## Tier 2 — Medium (fix soon)

### Bug 5: `BdStore.Get()` and `.Close()` mask all errors as `ErrNotFound`

**File:** `internal/beads/bdstore.go:322, 367`
**Confirmed by:** 1 agent

Network errors, DB corruption, permission failures all become `ErrNotFound`.
Callers that branch on `errors.Is(err, ErrNotFound)` make wrong decisions.

**Fix:** Wrap the original error instead:
```go
// Get:
return Bead{}, fmt.Errorf("getting bead %q: %w", id, err)
// Close:
return fmt.Errorf("closing bead %q: %w", id, err)
```

---

### Bug 6: Shell injection in `SendNotificationBanner`

**File:** `internal/session/tmux/tmux.go:1618-1634`
**Confirmed by:** 1 agent

`from` and `subject` are interpolated into `echo '...'` without escaping
single quotes. A value like `O'Malley` breaks shell quoting; a crafted
value enables arbitrary command execution.

**Fix:** Escape single quotes in both values using `strings.ReplaceAll(s, "'", "'\\''")`,
or switch to a tmux `display-message` approach that doesn't go through shell eval.

---

### Bug 7: `ListDatabasesCity` doesn't validate `noms/manifest`

**File:** `internal/dolt/gascity.go:386-395`
**Confirmed by:** 1 agent

Corrupted DB dirs (`.dolt/` present but no `noms/manifest`) pass through.
The Gastown `ListDatabases` checks this. Corrupted entries crash the Dolt server.

**Fix:** Add the same `noms/manifest` existence check that `ListDatabases` has
(doltserver.go:1157-1166). Log a warning for corrupted entries and exclude them.

---

### Bug 8: `CheckServerReachable` uses `DefaultConfig` (wrong env vars + wrong error hint)

**File:** `internal/dolt/gascity.go:789` → `doltserver.go:426-433`
**Confirmed by:** 1 agent

`startCityServer` calls `CheckServerReachable(config.TownRoot)` which internally
constructs `DefaultConfig` (reads `GT_DOLT_*` env vars) and shows `"gt dolt start"`
in error hints. Gas City should use `GC_DOLT_*` env vars and `"gc dolt start"`.

**Fix:** Add a `CheckServerReachableCity(cityPath)` wrapper (like the other City
wrappers already added) that uses `GasCityConfig` and the correct error message.

---

### Bug 9: `CommitServerWorkingSet` incomplete SQL escaping

**File:** `internal/dolt/doltserver.go:2692`
**Confirmed by:** 1 agent

Only escapes `'` → `''`, not backslashes. `EscapeSQL` in the same package does
both. A commit message ending with `\` breaks the SQL (the `\'` becomes a literal
single quote).

**Fix:** Replace `strings.ReplaceAll(message, "'", "''")` with `EscapeSQL(message)`.

---

### Bug 10: `GetMeta` swallows all errors as "key not set"

**File:** `internal/session/tmux/adapter.go:135-141`
**Confirmed by:** 1 agent

Tmux server crash, session-not-found, and real infrastructure failures all
return `("", nil)`. Higher-level code (reconciliation fingerprint checks) can
make wrong decisions when it can't distinguish "unset" from "broken".

**Fix:** Check for `ErrSessionNotFound`/`ErrNoServer` specifically (return
`("", nil)`). Propagate all other errors.

---

### Bug 11: `ResolveFormulas` skips stale symlink cleanup when winners is empty

**File:** `cmd/gc/formula_resolve.go:45-47`
**Confirmed by:** prior report (#2)

```go
if len(winners) == 0 {
    return nil  // skips cleanup at lines 80-102
}
```
When all formulas disappear from all layers, old symlinks stay active.

**Fix:** Move the early return to after the cleanup block, or restructure so
cleanup always runs when the symlink dir exists.

---

## Tier 3 — Low (fix when convenient)

### Bug 12: `FileStore.save()` race condition on temp file path

**File:** `internal/beads/filestore.go:104-123`

Hardcoded `.tmp` suffix means concurrent saves corrupt data. Acknowledged as
"fine for Tutorial 01 volumes" but the code advertises thread-safety.

**Fix:** Use a unique temp file per save (e.g., `os.CreateTemp`).

---

### Bug 13: `topologyDirHash` walks into `.git/` directories

**File:** `internal/config/topology_fetch.go:178-192`

No `filepath.SkipDir` for `.git` dirs. Also over-filters `.gitignore`/`.github/`
with `strings.HasPrefix(rel, ".git")`.

**Fix:** Return `filepath.SkipDir` for dirs named `.git`. Change file filter to
`rel == ".git" || strings.HasPrefix(rel, ".git/")` or better yet, only skip the
directory since files starting with `.git` (like `.gitignore`) should be hashed.

---

### Bug 14: `listDatabasesRemote` doesn't filter `dolt_cluster`

**File:** `internal/dolt/doltserver.go:1199`

Inconsistent with `parseShowDatabases` and `IsSystemDatabase` which filter
all three system databases.

**Fix:** Use `IsSystemDatabase()` to filter.

---

### Bug 15: Unquoted `USE %s` in `waitForCatalog` and `doltSQL`

**File:** `internal/dolt/doltserver.go:2585, 2622`

Rest of codebase uses `` USE `%s` `` with backticks. Database names with
hyphens (allowed by validation) could break without quoting.

**Fix:** Add backtick quoting for consistency.

---

## Not fixing (rationale)

| Finding | Why skip |
|---------|----------|
| Hardcoded role names (mayor, polecat, etc.) | Convention violation, not functional bug. Widespread (5+ files). Separate cleanup task. |
| daemon.pid file | PID is validated via process table. Lock file is the real liveness check. Pragmatic. |
| DOLT_UNHEALTHY status file | Circuit-breaker pattern, not liveness tracking. Explicitly documented. |
| `SetUnhealthy` non-atomic write | Best-effort signal file. Concurrent writes are unlikely. |
| `doDoltLogs`/`doDoltSQL` ignoring injected writers | Testability gap, not a functional bug. |
| Gas Town `gt` references in tmux.go | Naming/branding issue. Separate cleanup task. |
| Hardcoded `["node","claude"]` fallback process names | Only affects zombie detection for non-Claude providers. Low-traffic path. |
| `writeCityMetadata` dead code | Test-only helper. Harmless. |
| `dispatchWisp` bypassing Store interface | Works correctly via CLI. Coupling smell, not a bug. |
| "Row 5" comment | Cosmetic. |

## Implementation order

1. **Bug 2** (validateSessionName) — one line, zero risk
2. **Bug 1** (pool deep-copy) — three fields, zero risk
3. **Bug 4** (formula path) — one token change, zero risk
4. **Bug 5** (BdStore error masking) — two lines changed
5. **Bug 9** (SQL escaping) — one function call change
6. **Bug 6** (shell injection) — straightforward escaping
7. **Bug 11** (stale symlinks) — restructure early return
8. **Bug 7** (noms/manifest check) — add validation
9. **Bug 8** (CheckServerReachableCity) — new wrapper
10. **Bug 10** (GetMeta error handling) — moderate refactor
11. **Bug 3** (revision hash + watch dirs) — most complex, affects signatures
12-15. Tier 3 bugs in any order
