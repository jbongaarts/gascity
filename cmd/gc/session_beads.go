package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// sessionBeadLabel is the label applied to all session beads for config agents.
const sessionBeadLabel = "gc:agent_session"

// sessionBeadType is the bead type for config agent session beads.
const sessionBeadType = "agent_session"

// newSessionBeadLabel is the label for session-first beads (type="session").
const newSessionBeadLabel = "gc:session"

// legacyStateMap maps legacy bead states to the session-first state model.
// Legacy beads (type="agent_session") used different state names.
var legacyStateMap = map[string]string{
	"stopped":  "closed",
	"orphaned": "suspended",
	// "active" and "suspended" pass through unchanged.
}

// loadSessionBeads queries both gc:agent_session and gc:session labeled beads,
// deduplicates by session_name, and returns the unified list. New-type beads
// take precedence when both exist for the same session_name.
//
// Legacy beads are normalized: their state is mapped via legacyStateMap so
// downstream reconciliation sees a uniform state vocabulary. Legacy beads
// also count against pool occupancy (creating + active + suspended + quarantined).
func loadSessionBeads(store beads.Store) ([]beads.Bead, error) {
	if store == nil {
		return nil, nil
	}

	// Query both bead types.
	legacy, err := store.ListByLabel(sessionBeadLabel, 0)
	if err != nil {
		return nil, fmt.Errorf("listing legacy session beads: %w", err)
	}
	newer, err := store.ListByLabel(newSessionBeadLabel, 0)
	if err != nil {
		return nil, fmt.Errorf("listing session beads: %w", err)
	}

	// Index new-type beads by session_name for dedup.
	newByName := make(map[string]beads.Bead, len(newer))
	for _, b := range newer {
		if b.Status == "closed" {
			continue
		}
		if sn := b.Metadata["session_name"]; sn != "" {
			newByName[sn] = b
		}
	}

	// Build result: start with new-type beads.
	result := make([]beads.Bead, 0, len(newer)+len(legacy))
	for _, b := range newer {
		if b.Status == "closed" {
			continue
		}
		result = append(result, b)
	}

	// Add legacy beads, skipping those with a new-type counterpart.
	for _, b := range legacy {
		if b.Status == "closed" {
			continue
		}
		sn := b.Metadata["session_name"]
		if sn == "" {
			continue
		}
		if _, hasNew := newByName[sn]; hasNew {
			continue // new-type bead takes precedence
		}
		// Normalize legacy state.
		if mapped, ok := legacyStateMap[b.Metadata["state"]]; ok {
			b.Metadata["state"] = mapped
		}
		result = append(result, b)
	}

	return result, nil
}

// syncSessionBeads ensures every config agent has a corresponding session bead.
// This is an additive side-effect — it creates beads for agents that don't have
// them and updates metadata for those that do. It does NOT change agent behavior;
// the existing reconciler continues to manage agent lifecycle.
//
// configuredNames is the set of ALL configured agent session names (including
// suspended agents). Beads for names not in this set are marked "orphaned".
// Beads for names in configuredNames but not in the agents slice are marked
// "suspended" (the agent exists in config but isn't currently runnable).
//
// Phase 1 (additive): beads record reality alongside the existing reconciler.
// Phase 2 (lifecycle): beads are closed when agents are orphaned or suspended,
// completing the bead lifecycle. A fresh bead is created when the agent returns.
// Phase 3 (bead-driven): returns session_name → bead_id index for open beads,
// enabling beadReconcileOps to store/retrieve config hashes from beads.
//
// When called pre-reconcile (the daemon tick pattern), bead state metadata
// reflects the previous tick's reality — agents not yet started/stopped by
// the current tick's reconciliation. State converges on the next tick.
//
// When skipClose is true, orphan/suspended beads are NOT closed. This is
// used when the bead-driven reconciler is active — it handles drain/stop
// for orphan sessions before closing their beads.
//
// Returns a map of session_name → bead_id for all open session beads after
// sync. Callers that don't need the index can ignore the return value.
func syncSessionBeads(
	store beads.Store,
	agents []agent.Agent,
	configuredNames map[string]bool,
	cfg *config.City,
	clk clock.Clock,
	stderr io.Writer,
	skipClose bool,
) map[string]string {
	if store == nil {
		return nil
	}

	// Load existing session beads.
	existing, err := store.ListByLabel(sessionBeadLabel, 0)
	if err != nil {
		fmt.Fprintf(stderr, "session beads: listing existing: %v\n", err) //nolint:errcheck
		return nil
	}

	// Index by session_name for O(1) lookup. Skip closed beads — a closed
	// bead is a completed lifecycle record, not a live session. If an agent
	// restarts after its bead was closed, we create a fresh bead.
	bySessionName := make(map[string]beads.Bead, len(existing))
	for _, b := range existing {
		if b.Status == "closed" {
			continue
		}
		if sn := b.Metadata["session_name"]; sn != "" {
			bySessionName[sn] = b
		}
	}

	// Build a set of desired session names for orphan detection.
	desired := make(map[string]bool, len(agents))

	// Track open bead IDs for the returned index.
	openIndex := make(map[string]string, len(agents))

	now := clk.Now().UTC()

	for _, a := range agents {
		sn := a.SessionName()
		desired[sn] = true

		agentCfg := a.SessionConfig()
		coreHash := runtime.CoreFingerprint(agentCfg)
		liveHash := runtime.LiveFingerprint(agentCfg)

		// Use agent-level IsRunning which checks process liveness,
		// not just session existence.
		state := "stopped"
		if a.IsRunning() {
			state = "active"
		}

		b, exists := bySessionName[sn]
		if !exists {
			// Create a new session bead.
			meta := map[string]string{
				"session_name":   sn,
				"agent_name":     a.Name(),
				"config_hash":    coreHash,
				"live_hash":      liveHash,
				"generation":     "1",
				"instance_token": generateToken(),
				"state":          state,
				"synced_at":      now.Format("2006-01-02T15:04:05Z07:00"),
			}
			// Store template and pool_slot for bead-driven reconciler.
			tmpl := resolveAgentTemplate(a.Name(), cfg)
			meta["template"] = tmpl
			if slot := resolvePoolSlot(a.Name(), tmpl); slot > 0 {
				meta["pool_slot"] = strconv.Itoa(slot)
			}
			newBead, createErr := store.Create(beads.Bead{
				Title:    a.Name(),
				Type:     sessionBeadType,
				Labels:   []string{sessionBeadLabel, "agent:" + a.Name()},
				Metadata: meta,
			})
			if createErr != nil {
				fmt.Fprintf(stderr, "session beads: creating bead for %s: %v\n", a.Name(), createErr) //nolint:errcheck
			} else {
				openIndex[sn] = newBead.ID
			}
			continue
		}

		// Record existing open bead in index.
		openIndex[sn] = b.ID

		// Backfill template and pool_slot metadata for beads created
		// before Phase 2f. Each field is checked independently so a
		// partial failure (e.g., template succeeds but pool_slot fails)
		// is retried on the next sync.
		if b.Metadata["template"] == "" {
			tmpl := resolveAgentTemplate(a.Name(), cfg)
			if setMeta(store, b.ID, "template", tmpl, stderr) == nil {
				b.Metadata["template"] = tmpl
			}
		}
		if b.Metadata["pool_slot"] == "" {
			tmpl := b.Metadata["template"]
			if tmpl == "" {
				tmpl = resolveAgentTemplate(a.Name(), cfg)
			}
			if slot := resolvePoolSlot(a.Name(), tmpl); slot > 0 {
				if setMeta(store, b.ID, "pool_slot", strconv.Itoa(slot), stderr) == nil {
					b.Metadata["pool_slot"] = strconv.Itoa(slot)
				}
			}
		}

		// Update existing bead metadata.
		//
		// IMPORTANT: config_hash and live_hash are NOT updated here for
		// existing beads. These fields record what config the session was
		// STARTED with. The bead-driven reconciler (reconcileSessionBeads)
		// detects drift by comparing bead config_hash against the current
		// desired config. If we overwrote config_hash here, drift would
		// be undetectable. The reconciler writes config_hash after a
		// successful start/restart.
		//
		// For the legacy reconciler path (beadReconcileOps), config_hash
		// is updated after successful start via beadReconcileOps.Started().
		changed := false

		// Update state.
		if b.Metadata["state"] != state {
			if setMeta(store, b.ID, "state", state, stderr) == nil {
				changed = true
			}
		}

		// Clear stale close metadata from a failed closeBead attempt.
		// If closeBead partially wrote metadata before aborting (e.g.,
		// close_reason set but store.Close failed), and the agent is
		// now active again, clean up the stale terminal metadata.
		// Check both fields — either may exist independently if a
		// previous cleanup partially succeeded.
		if b.Metadata["close_reason"] != "" || b.Metadata["closed_at"] != "" {
			if setMeta(store, b.ID, "close_reason", "", stderr) == nil &&
				setMeta(store, b.ID, "closed_at", "", stderr) == nil {
				changed = true
			}
		}

		// Only update synced_at when something actually changed,
		// to avoid disk thrashing on every tick.
		if changed {
			setMeta(store, b.ID, "synced_at", now.Format("2006-01-02T15:04:05Z07:00"), stderr) //nolint:errcheck
		}
	}

	// Classify and close beads with no matching runnable agent.
	// - If the session name is in configuredNames but not in desired (runnable),
	//   the agent is suspended/disabled — close the bead with reason "suspended".
	// - If the session name is not in configuredNames at all, the agent was
	//   removed from config — close the bead with reason "orphaned". This
	//   includes pool/multi instances: they are ephemeral (not user-configured)
	//   and correctly become orphaned when their template is suspended or removed.
	//
	// Closing the bead completes its lifecycle record. When the agent returns
	// (e.g., resumed from suspension), a fresh bead is created automatically
	// because the indexing loop above skips closed beads.
	if !skipClose {
		for _, b := range existing {
			sn := b.Metadata["session_name"]
			if sn == "" || desired[sn] {
				continue
			}
			if b.Status == "closed" {
				continue
			}
			if configuredNames[sn] {
				// Still in config but not runnable (suspended/disabled).
				closeBead(store, b.ID, "suspended", now, stderr)
			} else {
				// Not in config at all — orphaned.
				closeBead(store, b.ID, "orphaned", now, stderr)
			}
		}
	}

	return openIndex
}

// configuredSessionNames builds the set of ALL configured agent session names
// from the config, including suspended agents. Used to distinguish "orphaned"
// (removed from config) from "suspended" (still in config, not runnable).
func configuredSessionNames(cfg *config.City, cityName string) map[string]bool {
	st := cfg.Workspace.SessionTemplate
	names := make(map[string]bool, len(cfg.Agents))
	for _, a := range cfg.Agents {
		names[agent.SessionNameFor(cityName, a.QualifiedName(), st)] = true
	}
	return names
}

// setMeta wraps store.SetMetadata with error logging. Returns the error
// so callers can abort dependent writes (e.g., skip config_hash on failure).
func setMeta(store beads.Store, id, key, value string, stderr io.Writer) error {
	if err := store.SetMetadata(id, key, value); err != nil {
		fmt.Fprintf(stderr, "session beads: setting %s on %s: %v\n", key, id, err) //nolint:errcheck
		return err
	}
	return nil
}

// closeBead sets final metadata on a session bead and closes it.
// This completes the bead's lifecycle record. The close_reason distinguishes
// why the bead was closed (e.g., "orphaned", "suspended").
//
// Follows the commit-signal pattern: metadata is written first, and Close
// is only called if all writes succeed. If any write fails, the bead stays
// open so the next tick retries the entire sequence.
func closeBead(store beads.Store, id, reason string, now time.Time, stderr io.Writer) {
	ts := now.Format("2006-01-02T15:04:05Z07:00")
	if setMeta(store, id, "state", reason, stderr) != nil {
		return
	}
	if setMeta(store, id, "close_reason", reason, stderr) != nil {
		return
	}
	if setMeta(store, id, "closed_at", ts, stderr) != nil {
		return
	}
	if setMeta(store, id, "synced_at", ts, stderr) != nil {
		return
	}
	if err := store.Close(id); err != nil {
		fmt.Fprintf(stderr, "session beads: closing %s: %v\n", id, err) //nolint:errcheck
	}
}

// resolveAgentTemplate returns the config agent template name for a given
// agent name. For non-pool agents, this is the agent's QualifiedName.
// For pool instances like "worker-3", this is the template "worker".
func resolveAgentTemplate(agentName string, cfg *config.City) string {
	if cfg == nil {
		return agentName
	}
	// Direct match: non-pool or singleton pool agent.
	for _, a := range cfg.Agents {
		if a.QualifiedName() == agentName {
			return a.QualifiedName()
		}
	}
	// Pool instance: name matches "{template}-{slot}".
	for _, a := range cfg.Agents {
		qn := a.QualifiedName()
		if a.IsPool() && strings.HasPrefix(agentName, qn+"-") {
			suffix := agentName[len(qn)+1:]
			if _, err := strconv.Atoi(suffix); err == nil {
				return qn
			}
		}
	}
	return agentName // fallback: treat agent name as template
}

// resolvePoolSlot extracts the pool slot number from a pool instance name.
// Returns 0 for non-pool agents or if template doesn't match.
func resolvePoolSlot(agentName, template string) int {
	if !strings.HasPrefix(agentName, template+"-") {
		return 0
	}
	suffix := agentName[len(template)+1:]
	slot, _ := strconv.Atoi(suffix)
	return slot
}

// generateToken returns a cryptographically random hex token.
// Panics on crypto/rand failure (standard Go pattern — indicates broken system).
func generateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("session beads: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
