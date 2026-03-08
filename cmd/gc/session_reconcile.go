// session_reconcile.go contains pure functions for the bead-driven session
// reconciler. All functions assume single-threaded execution within one
// reconciler tick. Map mutations on beads.Bead.Metadata are visible to
// callers by design (maps are reference types).
package main

import (
	"strconv"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// wakeReasons computes why a session should be awake.
// PURE FUNCTION — reads only, never writes metadata.
// poolDesired is the per-tick snapshot from pool evaluation.
// Returns nil if the session should be asleep.
func wakeReasons(
	session beads.Bead,
	cfg *config.City,
	sp runtime.Provider,
	poolDesired map[string]int,
	clk clock.Clock,
) []WakeReason {
	// User hold suppresses all reasons.
	if held := session.Metadata["held_until"]; held != "" {
		if t, err := time.Parse(time.RFC3339, held); err == nil && clk.Now().Before(t) {
			return nil
		}
		// Hold expired — treated as no hold. Cleared by healExpiredTimers().
	}

	// Quarantine suppresses all reasons.
	if q := session.Metadata["quarantined_until"]; q != "" {
		if t, err := time.Parse(time.RFC3339, q); err == nil && clk.Now().Before(t) {
			return nil
		}
		// Quarantine expired — treated as no quarantine. Cleared by healExpiredTimers().
	}

	var reasons []WakeReason

	// Config presence — per-instance for pools.
	template := session.Metadata["template"]
	if agent := findAgentByTemplate(cfg, template); agent != nil {
		if agent.Pool == nil {
			reasons = append(reasons, WakeConfig)
		} else {
			// Pool: only wake if slot is within desired count.
			slot, _ := strconv.Atoi(session.Metadata["pool_slot"])
			desired := poolDesired[template]
			if slot > 0 && slot <= desired {
				reasons = append(reasons, WakeConfig)
			}
		}
	}

	// WakeAttached: check if user terminal is connected.
	// No provider-level gate — composite providers (auto/hybrid) route
	// IsAttached per-session to the correct backend, which returns false
	// safely for backends that don't support attachment detection.
	if sp != nil {
		name := session.Metadata["session_name"]
		if name != "" && sp.IsAttached(name) {
			reasons = append(reasons, WakeAttached)
		}
	}

	// Phase 4: WakeWork — deferred until work-driven wake ships.

	return reasons
}

// findAgentByTemplate looks up a config agent by template name.
// Returns nil if not found.
func findAgentByTemplate(cfg *config.City, template string) *config.Agent {
	if cfg == nil || template == "" {
		return nil
	}
	for i := range cfg.Agents {
		if cfg.Agents[i].QualifiedName() == template {
			return &cfg.Agents[i]
		}
	}
	return nil
}

// healExpiredTimers clears expired held_until and quarantined_until.
// Separate from wakeReasons() to keep that function pure.
func healExpiredTimers(session *beads.Bead, store beads.Store, clk clock.Clock) {
	if h := session.Metadata["held_until"]; h != "" {
		if t, _ := time.Parse(time.RFC3339, h); !t.IsZero() && clk.Now().After(t) {
			batch := map[string]string{"held_until": ""}
			if session.Metadata["sleep_reason"] == "user-hold" {
				batch["sleep_reason"] = ""
			}
			if err := store.SetMetadataBatch(session.ID, batch); err == nil {
				for k, v := range batch {
					session.Metadata[k] = v
				}
			}
		}
	}
	if q := session.Metadata["quarantined_until"]; q != "" {
		if t, _ := time.Parse(time.RFC3339, q); !t.IsZero() && clk.Now().After(t) {
			batch := map[string]string{
				"quarantined_until": "",
				"wake_attempts":     "0",
			}
			if session.Metadata["sleep_reason"] == "quarantine" {
				batch["sleep_reason"] = ""
			}
			if err := store.SetMetadataBatch(session.ID, batch); err == nil {
				for k, v := range batch {
					session.Metadata[k] = v
				}
			}
		}
	}
}

// checkStability detects rapid exits. If a session was woken within
// stabilityThreshold and is already dead, counts as a crash.
// Returns true if a failure was recorded (caller should skip recordWakeFailure).
// Edge-triggered: clears last_woke_at after recording so the same crash
// is counted exactly once.
// Drain-aware: draining sessions died by request, not by crash.
func checkStability(session *beads.Bead, alive bool, dt *drainTracker, store beads.Store, clk clock.Clock) bool {
	if alive {
		return false
	}
	// Don't count intentional drains as crashes.
	if dt != nil && dt.get(session.ID) != nil {
		return false
	}
	lastWoke := session.Metadata["last_woke_at"]
	if lastWoke == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, lastWoke)
	if err != nil {
		return false
	}
	if clk.Now().Sub(t) < stabilityThreshold {
		recordWakeFailure(session, store, clk)
		// Clear last_woke_at so this crash is not re-counted next tick.
		_ = store.SetMetadata(session.ID, "last_woke_at", "")
		session.Metadata["last_woke_at"] = ""
		return true
	}
	return false
}

// recordWakeFailure increments wake_attempts and quarantines if threshold exceeded.
func recordWakeFailure(session *beads.Bead, store beads.Store, clk clock.Clock) {
	attempts, _ := strconv.Atoi(session.Metadata["wake_attempts"])
	attempts++

	if session.Metadata == nil {
		session.Metadata = make(map[string]string)
	}
	if attempts >= defaultMaxWakeAttempts {
		qUntil := clk.Now().Add(defaultQuarantineDuration).UTC().Format(time.RFC3339)
		batch := map[string]string{
			"wake_attempts":     strconv.Itoa(attempts),
			"quarantined_until": qUntil,
			"sleep_reason":      "quarantine",
		}
		if err := store.SetMetadataBatch(session.ID, batch); err == nil {
			for k, v := range batch {
				session.Metadata[k] = v
			}
		}
	} else {
		_ = store.SetMetadata(session.ID, "wake_attempts", strconv.Itoa(attempts))
		session.Metadata["wake_attempts"] = strconv.Itoa(attempts)
	}
}

// clearWakeFailures resets crash counter and quarantine for a stable session.
func clearWakeFailures(session *beads.Bead, store beads.Store) {
	batch := map[string]string{
		"wake_attempts":     "0",
		"quarantined_until": "",
	}
	if err := store.SetMetadataBatch(session.ID, batch); err == nil {
		if session.Metadata == nil {
			session.Metadata = make(map[string]string)
		}
		for k, v := range batch {
			session.Metadata[k] = v
		}
	}
}

// stableLongEnough returns true if the session has been alive past stabilityThreshold.
func stableLongEnough(session beads.Bead, clk clock.Clock) bool {
	lastWoke := session.Metadata["last_woke_at"]
	if lastWoke == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, lastWoke)
	if err != nil {
		return false
	}
	return clk.Now().Sub(t) >= stabilityThreshold
}

// sessionWakeAttempts returns the current wake attempt count.
func sessionWakeAttempts(session beads.Bead) int {
	n, _ := strconv.Atoi(session.Metadata["wake_attempts"])
	return n
}

// sessionIsQuarantined returns true if the session has an active quarantine.
func sessionIsQuarantined(session beads.Bead, clk clock.Clock) bool {
	q := session.Metadata["quarantined_until"]
	if q == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, q)
	if err != nil {
		return false
	}
	return clk.Now().Before(t)
}

// isPoolExcess returns true if this session is a pool instance whose slot
// exceeds the current desired count.
func isPoolExcess(session beads.Bead, cfg *config.City, poolDesired map[string]int) bool {
	template := session.Metadata["template"]
	agent := findAgentByTemplate(cfg, template)
	if agent == nil || agent.Pool == nil {
		return false
	}
	slot, _ := strconv.Atoi(session.Metadata["pool_slot"])
	desired := poolDesired[template]
	return slot > 0 && slot > desired
}

// healState updates advisory state metadata only when changed (dirty check).
func healState(session *beads.Bead, alive bool, store beads.Store) {
	target := "asleep"
	if alive {
		target = "awake"
	}
	if session.Metadata == nil {
		session.Metadata = make(map[string]string)
	}
	if session.Metadata["state"] != target {
		_ = store.SetMetadata(session.ID, "state", target)
		session.Metadata["state"] = target
	}
}

// topoOrder returns session beads in dependency order (dependencies first).
// deps maps template name -> list of dependency template names.
// If a cycle is detected (should not happen — validated at config load),
// falls back to original order.
func topoOrder(sessions []beads.Bead, deps map[string][]string) []beads.Bead {
	if len(deps) == 0 {
		return sessions
	}

	// Build template -> sessions index.
	templateSessions := make(map[string][]beads.Bead)
	for _, s := range sessions {
		template := s.Metadata["template"]
		templateSessions[template] = append(templateSessions[template], s)
	}

	// Collect unique templates present in sessions.
	var templates []string
	seen := make(map[string]bool)
	for _, s := range sessions {
		t := s.Metadata["template"]
		if !seen[t] {
			seen[t] = true
			templates = append(templates, t)
		}
	}

	// Topological sort via DFS with cycle detection.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(templates))
	var order []string
	hasCycle := false

	var visit func(t string)
	visit = func(t string) {
		if hasCycle {
			return
		}
		color[t] = gray
		for _, dep := range deps[t] {
			switch color[dep] {
			case gray:
				hasCycle = true
				return
			case white:
				if seen[dep] { // only visit templates present in sessions
					visit(dep)
				}
			}
		}
		color[t] = black
		order = append(order, t)
	}

	for _, t := range templates {
		if color[t] == white {
			visit(t)
		}
	}

	if hasCycle {
		return sessions // fallback: unordered
	}

	// order is in reverse-finish order (dependencies come first).
	var result []beads.Bead
	for _, t := range order {
		result = append(result, templateSessions[t]...)
	}
	return result
}

// knownSessionStates is the set of bead metadata "state" values that the
// current reconciler understands. Beads with unrecognized states are skipped
// during reconciliation to allow forward-compatible rollback from newer
// versions that add states like "draining" or "archived".
var knownSessionStates = map[string]bool{
	"active":      true,
	"asleep":      true,
	"awake":       true,
	"stopped":     true,
	"suspended":   true,
	"orphaned":    true,
	"closed":      true,
	"quarantined": true,
	"creating":    true,
	"":            true, // empty state is valid (legacy beads)
}

// isKnownState returns true if the bead's metadata state is recognized by
// the current reconciler. Unknown states (from a newer version) are skipped
// to prevent panics during rollback.
func isKnownState(session beads.Bead) bool {
	return knownSessionStates[session.Metadata["state"]]
}

// reverseBeads returns a reversed copy of the bead slice.
func reverseBeads(beadSlice []beads.Bead) []beads.Bead {
	n := len(beadSlice)
	result := make([]beads.Bead, n)
	for i, b := range beadSlice {
		result[n-1-i] = b
	}
	return result
}
