package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/runtime"
)

// allConfigured is a helper that builds configuredNames from agent session names.
func allConfigured(agents []agent.Agent) map[string]bool {
	m := make(map[string]bool, len(agents))
	for _, a := range agents {
		m[a.SessionName()] = true
	}
	return m
}

func TestSyncSessionBeads_CreatesNewBeads(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "mayor",
			FakeSessionName:   "mayor",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	if stderr.Len() > 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}

	all, err := store.ListByLabel(sessionBeadLabel, 0)
	if err != nil {
		t.Fatalf("listing beads: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 bead, got %d", len(all))
	}

	b := all[0]
	if b.Type != sessionBeadType {
		t.Errorf("type = %q, want %q", b.Type, sessionBeadType)
	}
	if b.Metadata["session_name"] != "mayor" {
		t.Errorf("session_name = %q, want %q", b.Metadata["session_name"], "mayor")
	}
	if b.Metadata["state"] != "active" {
		t.Errorf("state = %q, want %q", b.Metadata["state"], "active")
	}
	if b.Metadata["generation"] != "1" {
		t.Errorf("generation = %q, want %q", b.Metadata["generation"], "1")
	}
	if b.Metadata["instance_token"] == "" {
		t.Error("instance_token is empty")
	}
	if b.Metadata["config_hash"] == "" {
		t.Error("config_hash is empty")
	}
}

func TestSyncSessionBeads_Idempotent(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "mayor",
			FakeSessionName:   "mayor",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	// Get the created bead's token and generation.
	all, _ := store.ListByLabel(sessionBeadLabel, 0)
	token1 := all[0].Metadata["instance_token"]
	gen1 := all[0].Metadata["generation"]

	// Run again — should be idempotent.
	clk.Advance(5 * time.Second)
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	all, _ = store.ListByLabel(sessionBeadLabel, 0)
	if len(all) != 1 {
		t.Fatalf("expected 1 bead after re-sync, got %d", len(all))
	}

	// Token and generation should NOT change when config is unchanged.
	if all[0].Metadata["instance_token"] != token1 {
		t.Error("instance_token changed on idempotent re-sync")
	}
	if all[0].Metadata["generation"] != gen1 {
		t.Error("generation changed on idempotent re-sync")
	}
}

func TestSyncSessionBeads_ConfigDrift(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "mayor",
			FakeSessionName:   "mayor",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	all, _ := store.ListByLabel(sessionBeadLabel, 0)
	token1 := all[0].Metadata["instance_token"]

	// Change config — different command.
	agents[0].(*agent.Fake).FakeSessionConfig = runtime.Config{Command: "gemini"}
	clk.Advance(5 * time.Second)
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	// syncSessionBeads no longer updates config_hash for existing beads.
	// The bead-driven reconciler (reconcileSessionBeads) detects drift by
	// comparing bead config_hash against the current desired config and
	// updates it only after successful restart.
	all, _ = store.ListByLabel(sessionBeadLabel, 0)
	if all[0].Metadata["generation"] != "1" {
		t.Errorf("generation = %q, want %q (should not change on sync)", all[0].Metadata["generation"], "1")
	}
	if all[0].Metadata["instance_token"] != token1 {
		t.Error("instance_token should NOT change on sync (drift handled by reconciler)")
	}
	// config_hash should still be the original hash (set at creation).
	origHash := runtime.CoreFingerprint(runtime.Config{Command: "claude"})
	if all[0].Metadata["config_hash"] != origHash {
		t.Errorf("config_hash = %q, want original %q", all[0].Metadata["config_hash"], origHash)
	}
}

func TestSyncSessionBeads_OrphanDetection(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	// Create a bead for "old-agent".
	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "old-agent",
			FakeSessionName:   "old-agent",
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	// Now sync with a different agent list (old-agent removed from config too).
	agents = []agent.Agent{
		&agent.Fake{
			FakeName:          "new-agent",
			FakeSessionName:   "new-agent",
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}
	clk.Advance(5 * time.Second)
	// configuredNames only has new-agent — old-agent is truly orphaned.
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	// old-agent's bead should be closed with reason "orphaned".
	all, _ := store.ListByLabel(sessionBeadLabel, 0)
	var oldBead beads.Bead
	for _, b := range all {
		if b.Metadata["session_name"] == "old-agent" {
			oldBead = b
			break
		}
	}
	if oldBead.Status != "closed" {
		t.Errorf("old-agent status = %q, want %q", oldBead.Status, "closed")
	}
	if oldBead.Metadata["state"] != "orphaned" {
		t.Errorf("old-agent state = %q, want %q", oldBead.Metadata["state"], "orphaned")
	}
	if oldBead.Metadata["close_reason"] != "orphaned" {
		t.Errorf("old-agent close_reason = %q, want %q", oldBead.Metadata["close_reason"], "orphaned")
	}
	if oldBead.Metadata["closed_at"] == "" {
		t.Error("old-agent closed_at is empty")
	}
}

func TestSyncSessionBeads_NilStore(t *testing.T) {
	// Verify nil store does not panic.
	var stderr bytes.Buffer
	syncSessionBeads(nil, nil, nil, nil, &clock.Fake{}, &stderr, false)
	if stderr.Len() > 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestSyncSessionBeads_StoppedAgent(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "mayor",
			FakeSessionName:   "mayor",
			Running:           false,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	all, _ := store.ListByLabel(sessionBeadLabel, 0)
	if len(all) != 1 {
		t.Fatalf("expected 1 bead, got %d", len(all))
	}
	if all[0].Metadata["state"] != "stopped" {
		t.Errorf("state = %q, want %q", all[0].Metadata["state"], "stopped")
	}
}

func TestSyncSessionBeads_ClosedBeadCreatesNew(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "mayor",
			FakeSessionName:   "mayor",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer

	// First sync creates the bead.
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	all, _ := store.ListByLabel(sessionBeadLabel, 0)
	if len(all) != 1 {
		t.Fatalf("expected 1 bead, got %d", len(all))
	}

	// Close the bead to simulate a completed lifecycle.
	_ = store.Close(all[0].ID)

	// Re-sync should create a NEW bead, not reuse the closed one.
	clk.Advance(5 * time.Second)
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	all, _ = store.ListByLabel(sessionBeadLabel, 0)
	if len(all) != 2 {
		t.Fatalf("expected 2 beads (1 closed + 1 new), got %d", len(all))
	}

	// Find the open bead.
	var openBead beads.Bead
	for _, b := range all {
		if b.Status == "open" {
			openBead = b
			break
		}
	}
	if openBead.ID == "" {
		t.Fatal("no open bead found after re-sync")
	}
	if openBead.Metadata["state"] != "active" {
		t.Errorf("state = %q, want %q", openBead.Metadata["state"], "active")
	}
	if openBead.Metadata["generation"] != "1" {
		t.Errorf("generation = %q, want %q (fresh bead)", openBead.Metadata["generation"], "1")
	}
}

func TestSyncSessionBeads_PoolInstanceOrphaned(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	// Pool instances have session names like "city-worker-1", "city-worker-2".
	// These are ephemeral — not user-configured — so they're classified by
	// exact match against configuredNames. The template "city-worker" is
	// configured, but instances "city-worker-1" etc. are not.
	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "worker-1",
			FakeSessionName:   "city-worker-1",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
		&agent.Fake{
			FakeName:          "worker-2",
			FakeSessionName:   "city-worker-2",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer
	// configuredNames has the template name, not instance names.
	configuredNames := map[string]bool{"city-worker": true}
	syncSessionBeads(store, agents, configuredNames, nil, clk, &stderr, false)

	// Remove instances from runnable agents but keep template configured.
	clk.Advance(5 * time.Second)
	syncSessionBeads(store, nil, configuredNames, nil, clk, &stderr, false)

	// Pool instances are ephemeral (not user-configured), so they become
	// closed with reason "orphaned" when no longer running.
	all, _ := store.ListByLabel(sessionBeadLabel, 0)
	for _, b := range all {
		if b.Status != "closed" {
			t.Errorf("pool instance %s status = %q, want %q",
				b.Metadata["session_name"], b.Status, "closed")
		}
		if b.Metadata["state"] != "orphaned" {
			t.Errorf("pool instance %s state = %q, want %q",
				b.Metadata["session_name"], b.Metadata["state"], "orphaned")
		}
		if b.Metadata["close_reason"] != "orphaned" {
			t.Errorf("pool instance %s close_reason = %q, want %q",
				b.Metadata["session_name"], b.Metadata["close_reason"], "orphaned")
		}
	}
}

func TestSyncSessionBeads_ResumedAfterSuspension(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "worker",
			FakeSessionName:   "worker",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	// Suspend the agent: remove from runnable but keep in configuredNames.
	clk.Advance(5 * time.Second)
	configuredNames := map[string]bool{"worker": true}
	syncSessionBeads(store, nil, configuredNames, nil, clk, &stderr, false)

	// Verify the bead is closed.
	all, _ := store.ListByLabel(sessionBeadLabel, 0)
	if len(all) != 1 {
		t.Fatalf("expected 1 bead after suspension, got %d", len(all))
	}
	if all[0].Status != "closed" {
		t.Fatalf("bead status = %q, want %q", all[0].Status, "closed")
	}

	// Resume the agent: return it to the runnable set.
	clk.Advance(5 * time.Second)
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	// Should have 2 beads: 1 closed (old lifecycle) + 1 open (new lifecycle).
	all, _ = store.ListByLabel(sessionBeadLabel, 0)
	if len(all) != 2 {
		t.Fatalf("expected 2 beads after resume, got %d", len(all))
	}

	var closedCount, openCount int
	for _, b := range all {
		switch b.Status {
		case "closed":
			closedCount++
		case "open":
			openCount++
			if b.Metadata["state"] != "active" {
				t.Errorf("resumed bead state = %q, want %q", b.Metadata["state"], "active")
			}
			if b.Metadata["generation"] != "1" {
				t.Errorf("resumed bead generation = %q, want %q (fresh lifecycle)", b.Metadata["generation"], "1")
			}
		}
	}
	if closedCount != 1 || openCount != 1 {
		t.Errorf("expected 1 closed + 1 open, got %d closed + %d open", closedCount, openCount)
	}
}

func TestSyncSessionBeads_StaleCloseMetadataCleared(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "worker",
			FakeSessionName:   "worker",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	// Simulate a partially-failed closeBead: set close_reason on the
	// open bead as if setMeta("close_reason") succeeded but store.Close
	// failed. The bead stays open with stale terminal metadata.
	all, _ := store.ListByLabel(sessionBeadLabel, 0)
	_ = store.SetMetadata(all[0].ID, "close_reason", "orphaned")
	_ = store.SetMetadata(all[0].ID, "closed_at", "2026-03-07T12:00:05Z")

	// Agent resumes — sync should clear the stale close metadata.
	clk.Advance(5 * time.Second)
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	all, _ = store.ListByLabel(sessionBeadLabel, 0)
	if len(all) != 1 {
		t.Fatalf("expected 1 bead, got %d", len(all))
	}
	b := all[0]
	if b.Status != "open" {
		t.Errorf("status = %q, want %q", b.Status, "open")
	}
	if b.Metadata["state"] != "active" {
		t.Errorf("state = %q, want %q", b.Metadata["state"], "active")
	}
	if b.Metadata["close_reason"] != "" {
		t.Errorf("close_reason = %q, want empty (stale metadata not cleared)", b.Metadata["close_reason"])
	}
	if b.Metadata["closed_at"] != "" {
		t.Errorf("closed_at = %q, want empty (stale metadata not cleared)", b.Metadata["closed_at"])
	}
}

func TestSyncSessionBeads_SuspendedAgentNotOrphaned(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	// Create beads for both agents.
	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "mayor",
			FakeSessionName:   "mayor",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
		&agent.Fake{
			FakeName:          "worker",
			FakeSessionName:   "worker",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer
	syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	// Now "suspend" worker: remove from runnable agents but keep in configuredNames.
	runnableAgents := []agent.Agent{agents[0]} // only mayor
	configuredNames := map[string]bool{
		"mayor":  true,
		"worker": true, // still configured, just suspended
	}
	clk.Advance(5 * time.Second)
	syncSessionBeads(store, runnableAgents, configuredNames, nil, clk, &stderr, false)

	// Worker should be closed with reason "suspended", not "orphaned".
	all, _ := store.ListByLabel(sessionBeadLabel, 0)
	var workerBead beads.Bead
	for _, b := range all {
		if b.Metadata["session_name"] == "worker" {
			workerBead = b
			break
		}
	}
	if workerBead.Status != "closed" {
		t.Errorf("worker status = %q, want %q", workerBead.Status, "closed")
	}
	if workerBead.Metadata["state"] != "suspended" {
		t.Errorf("worker state = %q, want %q", workerBead.Metadata["state"], "suspended")
	}
	if workerBead.Metadata["close_reason"] != "suspended" {
		t.Errorf("worker close_reason = %q, want %q", workerBead.Metadata["close_reason"], "suspended")
	}
}

func TestSyncSessionBeads_ReturnsIndex(t *testing.T) {
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)}

	agents := []agent.Agent{
		&agent.Fake{
			FakeName:          "mayor",
			FakeSessionName:   "mayor",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
		&agent.Fake{
			FakeName:          "worker",
			FakeSessionName:   "worker",
			Running:           true,
			FakeSessionConfig: runtime.Config{Command: "claude"},
		},
	}

	var stderr bytes.Buffer
	idx := syncSessionBeads(store, agents, allConfigured(agents), nil, clk, &stderr, false)

	if stderr.Len() > 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}

	// Index should contain both agents.
	if len(idx) != 2 {
		t.Fatalf("index length = %d, want 2", len(idx))
	}
	if idx["mayor"] == "" {
		t.Error("index missing mayor")
	}
	if idx["worker"] == "" {
		t.Error("index missing worker")
	}

	// Verify IDs match actual beads.
	all, _ := store.ListByLabel(sessionBeadLabel, 0)
	beadIDs := make(map[string]string)
	for _, b := range all {
		beadIDs[b.Metadata["session_name"]] = b.ID
	}
	if idx["mayor"] != beadIDs["mayor"] {
		t.Errorf("mayor ID = %q, want %q", idx["mayor"], beadIDs["mayor"])
	}
	if idx["worker"] != beadIDs["worker"] {
		t.Errorf("worker ID = %q, want %q", idx["worker"], beadIDs["worker"])
	}

	// Suspend worker — closed beads excluded from index.
	clk.Advance(5 * time.Second)
	cfgNames := map[string]bool{"mayor": true, "worker": true}
	idx2 := syncSessionBeads(store, agents[:1], cfgNames, nil, clk, &stderr, false)

	if len(idx2) != 1 {
		t.Fatalf("after suspend, index length = %d, want 1", len(idx2))
	}
	if idx2["mayor"] == "" {
		t.Error("after suspend, index missing mayor")
	}
	if _, ok := idx2["worker"]; ok {
		t.Error("after suspend, index should not contain worker")
	}
}

// --- loadSessionBeads tests (Phase 0a: dual bead type recognition) ---

func TestLoadSessionBeads_LegacyOnly(t *testing.T) {
	store := beads.NewMemStore()

	// Create a legacy bead.
	_, err := store.Create(beads.Bead{
		Title:  "worker",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "worker",
			"state":        "active",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := loadSessionBeads(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 bead, got %d", len(result))
	}
	if result[0].Metadata["session_name"] != "worker" {
		t.Errorf("session_name = %q, want worker", result[0].Metadata["session_name"])
	}
}

func TestLoadSessionBeads_NewTypeOnly(t *testing.T) {
	store := beads.NewMemStore()

	_, err := store.Create(beads.Bead{
		Title:  "worker",
		Type:   "session",
		Labels: []string{newSessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "worker",
			"state":        "active",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := loadSessionBeads(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 bead, got %d", len(result))
	}
}

func TestLoadSessionBeads_DeduplicatesBySessionName(t *testing.T) {
	store := beads.NewMemStore()

	// Create both a legacy and a new-type bead with the same session_name.
	_, _ = store.Create(beads.Bead{
		Title:  "worker-legacy",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "worker",
			"state":        "active",
		},
	})
	_, _ = store.Create(beads.Bead{
		Title:  "worker-new",
		Type:   "session",
		Labels: []string{newSessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "worker",
			"state":        "active",
		},
	})

	result, err := loadSessionBeads(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 bead (deduped), got %d", len(result))
	}
	// New-type bead takes precedence.
	if result[0].Title != "worker-new" {
		t.Errorf("expected new-type bead to win, got title=%q", result[0].Title)
	}
}

func TestLoadSessionBeads_LegacyStateMapping(t *testing.T) {
	store := beads.NewMemStore()

	tests := []struct {
		legacyState string
		wantState   string
	}{
		{"stopped", "closed"},
		{"orphaned", "suspended"},
		{"active", "active"},
		{"suspended", "suspended"},
	}
	for _, tt := range tests {
		store := beads.NewMemStore() // fresh store per subtest
		_, _ = store.Create(beads.Bead{
			Title:  "worker",
			Type:   sessionBeadType,
			Labels: []string{sessionBeadLabel},
			Metadata: map[string]string{
				"session_name": "worker",
				"state":        tt.legacyState,
			},
		})

		result, err := loadSessionBeads(store)
		if err != nil {
			t.Fatalf("state=%q: %v", tt.legacyState, err)
		}
		if len(result) != 1 {
			t.Fatalf("state=%q: expected 1 bead, got %d", tt.legacyState, len(result))
		}
		if result[0].Metadata["state"] != tt.wantState {
			t.Errorf("state=%q: mapped to %q, want %q",
				tt.legacyState, result[0].Metadata["state"], tt.wantState)
		}
	}
	_ = store // suppress unused warning
}

func TestLoadSessionBeads_HybridPoolOccupancy(t *testing.T) {
	store := beads.NewMemStore()

	// Two legacy beads + one new-type bead, all for different session names.
	_, _ = store.Create(beads.Bead{
		Title:  "worker-1",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "worker-1",
			"template":     "worker",
			"state":        "active",
			"pool_slot":    "1",
		},
	})
	_, _ = store.Create(beads.Bead{
		Title:  "worker-2",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "worker-2",
			"template":     "worker",
			"state":        "active",
			"pool_slot":    "2",
		},
	})
	_, _ = store.Create(beads.Bead{
		Title:  "worker-3",
		Type:   "session",
		Labels: []string{newSessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "worker-3",
			"template":     "worker",
			"state":        "active",
			"pool_slot":    "3",
		},
	})

	result, err := loadSessionBeads(store)
	if err != nil {
		t.Fatal(err)
	}
	// All 3 should be returned (no name collision).
	if len(result) != 3 {
		t.Fatalf("expected 3 beads for pool occupancy, got %d", len(result))
	}
}

func TestLoadSessionBeads_NilStore(t *testing.T) {
	result, err := loadSessionBeads(nil)
	if err != nil {
		t.Fatalf("nil store should not error: %v", err)
	}
	if result != nil {
		t.Errorf("nil store should return nil, got %v", result)
	}
}

func TestLoadSessionBeads_SkipsClosedBeads(t *testing.T) {
	store := beads.NewMemStore()

	b, _ := store.Create(beads.Bead{
		Title:  "worker",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "worker",
			"state":        "active",
		},
	})
	_ = store.Close(b.ID)

	result, err := loadSessionBeads(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 beads (closed), got %d", len(result))
	}
}
