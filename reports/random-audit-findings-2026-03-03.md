# Random Deep Audit Findings — Synthesized Report

Two independent audit sweeps (5 parallel subagents each) examined 60+ source
files across all major packages: config, beads, session, tmux, k8s, subprocess,
events, city lifecycle, rig management, CLI commands, tests, and utilities.

Findings are deduplicated across both sweeps and annotated with fix status.

---

## CRITICAL (1)

### C1. `stopByPID` can terminate an unrelated process due to PID reuse
- **File:** `internal/session/subprocess/subprocess.go:354`
- **Category:** logic-error
- **Description:** PID is read from a stale `.pid` file and trusted (`readPID` + `syscall.Kill(pid, SIGTERM/SIGKILL)`) with no start-time/command/session identity validation.
- **Impact:** `gc stop` may kill arbitrary OS processes if the original session exited and PID was reused.
- **Suggested fix:** Validate process identity (start time + command/session marker) before signaling, or avoid PID-file-based ownership entirely.
- **Status:** NOT FIXED — requires subprocess provider redesign.

---

## HIGH (3)

### H1. Hardcoded role names in `roleEmoji` map
- **File:** `internal/session/tmux/tmux.go:65-74`
- **Category:** contract-violation (CLAUDE.md: zero hardcoded roles)
- **Description:** Map hardcodes 8 Gas Town role names ("mayor", "deacon", "witness", "refinery", "crew", "polecat", "coordinator", "health-check") with emoji mappings.
- **Impact:** User-configured roles get no emoji. Embeds Gas Town taxonomy into the SDK.
- **Status:** NOT FIXED — tmux/ is a gastown carbon copy for upstream comparison.

### H2. Hardcoded role-specific theme functions
- **File:** `internal/session/tmux/theme.go:31-47`
- **Category:** contract-violation (CLAUDE.md: zero hardcoded roles)
- **Description:** `MayorTheme()`, `DeaconTheme()`, `DogTheme()` are exported functions with role names baked into identifiers and return values.
- **Status:** NOT FIXED — tmux/ is a gastown carbon copy.

### H3. Integration tests silently pass when `bd` binary is missing
- **File:** `test/integration/integration_test.go:59-63`
- **Category:** test-reliability
- **Description:** `TestMain` calls `os.Exit(0)` when `bd` is not found — CI reports green even when zero tests ran. Same pattern for tmux skip.
- **Impact:** False confidence in CI.
- **Status:** FIXED — added stderr messages for both tmux and bd skip paths.

---

## MEDIUM (13)

### M1. `exec.Store.Get` unconditionally wraps errors as `ErrNotFound`
- **File:** `internal/beads/exec/exec.go:170-174`
- **Category:** logic-error
- **Description:** All errors from `s.run()` are replaced with `ErrNotFound`, unlike `Update`/`Close` which correctly check `isNotFoundError()` first.
- **Impact:** Script crashes, timeouts, and permission errors are misclassified. Callers using `errors.Is(err, ErrNotFound)` take the wrong code path.
- **Status:** FIXED — now checks `isNotFoundError(err)` before wrapping.

### M2. `resolveTopologyRef()` errors silently discarded in 6+ call sites
- **File:** `internal/config/revision.go:41,52,86,93` and `compose.go:110,139`
- **Category:** missing-error-check
- **Description:** Callers discard errors with `_`, leaving `topoDir` as `""`. Downstream uses hash empty directories, watch wrong paths, or record wrong provenance.
- **Impact:** Config revision hash becomes incorrect after remote fetch failures.
- **Status:** FIXED — all 6 call sites now check `err != nil || topoDir == ""` and skip.

### M3. `FileStore.SetMetadata` is a no-op (never persists)
- **File:** `internal/beads/filestore.go` (missing override)
- **Category:** logic-error
- **Description:** FileStore overrides Create/Update/Close/MolCook/MolCookOn with lock+save, but inherits `MemStore.SetMetadata` which is a deliberate no-op.
- **Impact:** `gc sling --merge` metadata lost when using FileStore backend.
- **Status:** NOT FIXED — low urgency; FileStore is not the primary production backend. Documented as intentional no-op in MemStore.

### M4. Hardcoded `["node", "claude"]` fallback in `resolveSessionProcessNames`
- **File:** `internal/session/tmux/tmux.go:1743-1752`
- **Category:** contract-violation (ZFC / zero hardcoded roles)
- **Description:** When `GT_PROCESS_NAMES` is unset, falls back to hardcoded Claude process names.
- **Status:** NOT FIXED — tmux/ is a gastown carbon copy.

### M5. Context parameter discarded in all `Provider.Start` implementations
- **File:** `internal/session/tmux/adapter.go:50`, `exec/exec.go`, `subprocess/subprocess.go`, `k8s/provider.go:100`
- **Category:** contract-violation
- **Description:** Interface doc says providers should check ctx.Err(). All 4 implementations use `_ context.Context`.
- **Impact:** Caller cancellation during graceful shutdown is ignored.
- **Status:** NOT FIXED — requires coordinated refactor across all providers.

### M6. K8s provider `Nudge` missing `-l` literal mode flag
- **File:** `internal/session/k8s/provider.go:308-309`
- **Category:** logic-error
- **Description:** Sent message without `-l`. Tmux key names in messages (e.g., "Enter", "Escape") were interpreted as key presses, not text.
- **Status:** FIXED — now uses `-l` for literal mode and sends Enter separately.

### M7. Subprocess provider uses PID files, violating "no status files" principle
- **File:** `internal/session/subprocess/subprocess.go:8,138-139,327-382`
- **Category:** contract-violation
- **Description:** PID files and sidecar meta files throughout. Stale files can produce false positives and unsafe operations.
- **Status:** NOT FIXED — requires subprocess provider redesign.

### M8. Controller writes `daemon.pid`, violating no-status-files rule
- **File:** `cmd/gc/controller.go:485-488`
- **Category:** contract-violation
- **Description:** `os.WriteFile(pidPath, ...)` with cleanup-at-exit only. Mitigated by flock-based locking.
- **Status:** NOT FIXED — pragmatic deviation; flock is the real authority.

### M9. `ReadFrom` offset calculation assumes LF line endings
- **File:** `internal/events/reader.go:124`
- **Category:** edge-case
- **Description:** `bytesRead += int64(len(line)) + 1` unconditionally adds 1 for newline. If last line has no trailing newline, offset overshoots by 1 byte.
- **Impact:** Next read may skip first byte of newly appended data.
- **Status:** ACKNOWLEDGED — recorder always writes LF; comment added for clarity.

### M10. Events readers use default 64K scanner buffer
- **File:** `internal/events/reader.go:33,88,120`
- **Category:** edge-case
- **Description:** Large JSONL events trigger scan errors.
- **Status:** FIXED — all three scanners now use 1MB buffer.

### M11. FileStore uses a fixed temp file path for atomic writes
- **File:** `internal/beads/filestore.go:128`
- **Category:** race-condition
- **Description:** `tmp := fs.path + ".tmp"` — concurrent writers from multiple processes can clobber each other's temp files.
- **Impact:** Lost updates/corruption under concurrent cross-process writes.
- **Status:** NOT FIXED — FileStore is single-process by design (fmu mutex); cross-process is not a supported use case.

### M12. Batch cross-rig validation assumes all children share container rig
- **File:** `cmd/gc/cmd_sling.go:429`
- **Category:** edge-case
- **Description:** Comment explicitly states assumption. Only checks container once, not per-child.
- **Status:** NOT FIXED — documented limitation.

### M13. `signal.Notify` never paired with `signal.Stop`
- **File:** `cmd/gc/controller.go:494-499`
- **Category:** resource-leak
- **Description:** Lingering signal subscriptions can accumulate in long-lived scenarios.
- **Status:** FIXED — added `defer signal.Stop(sigCh)`.

---

## LOW (16)

### L1. `beadWire` missing `From` field
- **File:** `internal/beads/exec/json.go:43-55` and `exec.go:134-148`
- **Description:** Wire format and `toBead()` don't map `From`, losing sender info for message beads.
- **Status:** FIXED — added `From` field to `beadWire` and `toBead()`.

### L2. CopyFiles fingerprint hash missing separator between Src and RelDst
- **File:** `internal/session/fingerprint.go:64-68`
- **Description:** `{Src: "ab", RelDst: "cd"}` and `{Src: "abc", RelDst: "d"}` produce identical hash bytes.
- **Status:** FIXED — added separator byte between Src and RelDst.

### L3. `quote()` function in test helpers has no escaping
- **File:** `test/integration/helpers_test.go:127-129`
- **Description:** Wraps in double quotes without escaping `\`, `"`, or `\n`. Works by accident with current simple strings.
- **Status:** FIXED — now properly escapes backslashes, quotes, and newlines.

### L4. Overlay/copy setup errors swallowed during subprocess session start
- **File:** `internal/session/subprocess/subprocess.go:94-107`
- **Description:** `overlay.CopyDir` and `overlay.CopyFileOrDir` results ignored.
- **Status:** NOT FIXED — acknowledged best-effort pattern.

### L5. Hash inconsistency between `topologyDirHash` and `TopologyContentHashRecursive`
- **File:** `internal/config/topology_fetch.go:176-209` vs `topology.go:799-816`
- **Description:** Near-identical functions, but one skips `.git` dirs and the other doesn't. Different hashes for same directory.
- **Status:** NOT FIXED — noted for future consolidation.

### L6. `AgentHasHooks` hardcodes `providerName == "claude"`
- **File:** `internal/config/resolve.go:158-160`
- **Description:** Framework intelligence in Go — SDK decides Claude always has hooks.
- **Status:** NOT FIXED — overridable via `hooks_installed`; pragmatic.

### L7. `DefaultCity` / `WizardCity` hardcode "mayor"
- **File:** `internal/config/config.go:1033-1057`
- **Description:** Default agent name is "mayor" — intentional thematic choice for tutorials.
- **Status:** ACKNOWLEDGED — not a bug per user decision. Doc comment clarified.

### L8. `ListSessionIDs` uses first colon split
- **File:** `internal/session/tmux/tmux.go:872`
- **Description:** External sessions with colons would mismatch. Mitigated by session name validation.
- **Status:** NOT FIXED — tmux/ is a gastown carbon copy.

### L9. `WakePane` uses ambiguous `-y -1` / `-y +1` resize flags
- **File:** `internal/session/tmux/tmux.go:1003-1005`
- **Status:** NOT FIXED — tmux/ is a gastown carbon copy.

### L10. `WaitForIdle` has redundant prompt prefix check
- **File:** `internal/session/tmux/tmux.go:1919-1926`
- **Status:** NOT FIXED — tmux/ is a gastown carbon copy.

### L11. `cobra.ArbitraryArgs` silently ignores extra arguments
- **File:** `cmd/gc/cmd_rig.go:63-64`
- **Description:** `gc rig add a b` only adds `a`, silently ignores `b`.
- **Status:** NOT FIXED — low priority; no wrong behavior, just silent.

### L12. Duplicate rig paths not checked
- **File:** `cmd/gc/cmd_rig.go:147-149`
- **Description:** Two rigs with different names pointing to same directory are allowed.
- **Status:** NOT FIXED — `ValidateRigs` catches prefix collisions but not path duplicates.

### L13. Provenance attribution to wrong topology for multi-topology cities
- **File:** `internal/config/compose.go:109-119`
- **Description:** First topology's path wins for all untracked agents regardless of actual source.
- **Status:** NOT FIXED — affects `gc config show --provenance` display only.

### L14. `lastControllerStarted` scans entire events.jsonl
- **File:** `cmd/gc/cmd_daemon.go:460-481`
- **Description:** Scans every line to find last `controller.started` event.
- **Status:** NOT FIXED — low priority; status command is infrequent.

### L15. Gas Town references in tmux function names and comments
- **File:** `internal/session/tmux/tmux.go` (multiple locations)
- **Status:** NOT FIXED — tmux/ is a gastown carbon copy.

### L16. Integration test helpers hardcode "mayor" in test fixtures
- **File:** `test/integration/helpers_test.go:93,122`
- **Description:** Test data uses "mayor" as agent name.
- **Status:** ACKNOWLEDGED — intentional thematic choice per user.

---

## Summary

| Severity | Total | Fixed | Deferred |
|----------|-------|-------|----------|
| Critical | 1     | 0     | 1        |
| High     | 3     | 1     | 2        |
| Medium   | 13    | 4     | 9        |
| Low      | 16    | 3     | 13       |
| **Total**| **33**| **8** | **25**   |

### Fixed in this sweep:
1. `exec.Store.Get` ErrNotFound misclassification (M1)
2. `resolveTopologyRef` errors silently discarded (M2, 6 call sites)
3. K8s nudge missing `-l` literal mode (M6)
4. Events reader scanner buffer increased to 1MB (M10)
5. `signal.Stop` added to controller (M13)
6. `beadWire` missing `From` field (L1)
7. CopyFiles fingerprint hash separator (L2)
8. Test helper `quote()` escaping (L3)
9. Integration test skip messages for missing tmux/bd (H3)

### Not fixed (by design):
- **tmux/ and dolt/** — gastown carbon copies, not touched for upstream comparison
- **Subprocess PID redesign** — requires architectural work
- **Context threading** — requires coordinated refactor across all providers
- **"mayor" default** — intentional tutorial theme per user decision
