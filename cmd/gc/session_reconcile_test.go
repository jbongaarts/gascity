package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// testStore wraps a bead slice for SetMetadata tracking in tests.
type testStore struct {
	beads.Store
	metadata map[string]map[string]string // id -> key -> value
}

func newTestStore() *testStore {
	return &testStore{metadata: make(map[string]map[string]string)}
}

func (s *testStore) SetMetadata(id, key, value string) error {
	if s.metadata[id] == nil {
		s.metadata[id] = make(map[string]string)
	}
	s.metadata[id][key] = value
	return nil
}

func (s *testStore) SetMetadataBatch(id string, kvs map[string]string) error {
	for k, v := range kvs {
		if err := s.SetMetadata(id, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *testStore) Ping() error {
	return nil
}

func (s *testStore) Get(id string) (beads.Bead, error) {
	return beads.Bead{ID: id}, nil
}

func makeBead(id string, meta map[string]string) beads.Bead {
	if meta == nil {
		meta = make(map[string]string)
	}
	return beads.Bead{
		ID:       id,
		Status:   "open",
		Metadata: meta,
	}
}

func TestWakeReasons_ConfigPresence(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker"},
		},
	}

	session := makeBead("b1", map[string]string{
		"template":     "worker",
		"session_name": "test-worker",
	})

	reasons := wakeReasons(session, cfg, nil, nil, clk)
	if len(reasons) != 1 || reasons[0] != WakeConfig {
		t.Errorf("expected [WakeConfig], got %v", reasons)
	}
}

func TestWakeReasons_NoConfig(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "other"},
		},
	}

	session := makeBead("b1", map[string]string{
		"template":     "worker",
		"session_name": "test-worker",
	})

	reasons := wakeReasons(session, cfg, nil, nil, clk)
	if len(reasons) != 0 {
		t.Errorf("expected no reasons, got %v", reasons)
	}
}

func TestWakeReasons_HeldUntil(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker"},
		},
	}

	// Hold until future — suppresses all reasons.
	session := makeBead("b1", map[string]string{
		"template":     "worker",
		"session_name": "test-worker",
		"held_until":   now.Add(1 * time.Hour).Format(time.RFC3339),
	})

	reasons := wakeReasons(session, cfg, nil, nil, clk)
	if len(reasons) != 0 {
		t.Errorf("held session should have no reasons, got %v", reasons)
	}
}

func TestWakeReasons_HoldExpired(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker"},
		},
	}

	// Hold expired — should produce reasons.
	session := makeBead("b1", map[string]string{
		"template":     "worker",
		"session_name": "test-worker",
		"held_until":   now.Add(-1 * time.Hour).Format(time.RFC3339),
	})

	reasons := wakeReasons(session, cfg, nil, nil, clk)
	if len(reasons) != 1 || reasons[0] != WakeConfig {
		t.Errorf("expired hold should allow reasons, got %v", reasons)
	}
}

func TestWakeReasons_Quarantined(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker"},
		},
	}

	session := makeBead("b1", map[string]string{
		"template":          "worker",
		"session_name":      "test-worker",
		"quarantined_until": now.Add(5 * time.Minute).Format(time.RFC3339),
	})

	reasons := wakeReasons(session, cfg, nil, nil, clk)
	if len(reasons) != 0 {
		t.Errorf("quarantined session should have no reasons, got %v", reasons)
	}
}

func TestWakeReasons_PoolWithinDesired(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", Pool: &config.PoolConfig{Min: 1, Max: 5}},
		},
	}

	session := makeBead("b1", map[string]string{
		"template":     "worker",
		"session_name": "test-worker-1",
		"pool_slot":    "1",
	})

	poolDesired := map[string]int{"worker": 3}

	reasons := wakeReasons(session, cfg, nil, poolDesired, clk)
	if len(reasons) != 1 || reasons[0] != WakeConfig {
		t.Errorf("pool slot within desired should wake, got %v", reasons)
	}
}

func TestWakeReasons_PoolExceedsDesired(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", Pool: &config.PoolConfig{Min: 1, Max: 5}},
		},
	}

	session := makeBead("b1", map[string]string{
		"template":     "worker",
		"session_name": "test-worker-4",
		"pool_slot":    "4",
	})

	poolDesired := map[string]int{"worker": 3}

	reasons := wakeReasons(session, cfg, nil, poolDesired, clk)
	if len(reasons) != 0 {
		t.Errorf("pool slot exceeding desired should not wake, got %v", reasons)
	}
}

func TestWakeReasons_Attached(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	cfg := &config.City{} // no agents — so no WakeConfig

	sp := runtime.NewFake()
	_ = sp.Start(context.Background(), "test-worker", runtime.Config{})
	sp.SetAttached("test-worker", true)

	session := makeBead("b1", map[string]string{
		"template":     "worker",
		"session_name": "test-worker",
	})

	reasons := wakeReasons(session, cfg, sp, nil, clk)
	if len(reasons) != 1 || reasons[0] != WakeAttached {
		t.Errorf("attached session should get WakeAttached, got %v", reasons)
	}
}

func TestHealExpiredTimers_ClearsExpiredHold(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()

	session := makeBead("b1", map[string]string{
		"held_until":   now.Add(-1 * time.Hour).Format(time.RFC3339),
		"sleep_reason": "user-hold",
	})

	healExpiredTimers(&session, store, clk)

	if session.Metadata["held_until"] != "" {
		t.Error("expected held_until to be cleared")
	}
	if session.Metadata["sleep_reason"] != "" {
		t.Error("expected sleep_reason to be cleared")
	}
}

func TestHealExpiredTimers_KeepsActiveHold(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()

	future := now.Add(1 * time.Hour).Format(time.RFC3339)
	session := makeBead("b1", map[string]string{
		"held_until":   future,
		"sleep_reason": "user-hold",
	})

	healExpiredTimers(&session, store, clk)

	if session.Metadata["held_until"] != future {
		t.Error("active hold should not be cleared")
	}
}

func TestHealExpiredTimers_ClearsExpiredQuarantine(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()

	session := makeBead("b1", map[string]string{
		"quarantined_until": now.Add(-1 * time.Minute).Format(time.RFC3339),
		"wake_attempts":     "5",
		"sleep_reason":      "quarantine",
	})

	healExpiredTimers(&session, store, clk)

	if session.Metadata["quarantined_until"] != "" {
		t.Error("expected quarantined_until to be cleared")
	}
	if session.Metadata["wake_attempts"] != "0" {
		t.Errorf("expected wake_attempts to be 0, got %q", session.Metadata["wake_attempts"])
	}
	if session.Metadata["sleep_reason"] != "" {
		t.Error("expected sleep_reason to be cleared")
	}
}

func TestCheckStability_AliveReturnsFalse(t *testing.T) {
	clk := &clock.Fake{Time: time.Now()}
	store := newTestStore()
	dt := newDrainTracker()

	session := makeBead("b1", map[string]string{
		"last_woke_at": clk.Now().Add(-10 * time.Second).Format(time.RFC3339),
	})

	if checkStability(&session, true, dt, store, clk) {
		t.Error("alive session should not report stability failure")
	}
}

func TestCheckStability_RapidExit(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()
	dt := newDrainTracker()

	session := makeBead("b1", map[string]string{
		"last_woke_at":  now.Add(-10 * time.Second).Format(time.RFC3339),
		"wake_attempts": "0",
	})

	if !checkStability(&session, false, dt, store, clk) {
		t.Error("rapid exit should report stability failure")
	}

	// wake_attempts should be incremented.
	if session.Metadata["wake_attempts"] != "1" {
		t.Errorf("wake_attempts = %q, want 1", session.Metadata["wake_attempts"])
	}

	// last_woke_at should be cleared (edge-triggered).
	if session.Metadata["last_woke_at"] != "" {
		t.Error("last_woke_at should be cleared after rapid exit detection")
	}
}

func TestCheckStability_DrainingNotCounted(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()
	dt := newDrainTracker()
	dt.set("b1", &drainState{reason: "idle"})

	session := makeBead("b1", map[string]string{
		"last_woke_at": now.Add(-10 * time.Second).Format(time.RFC3339),
	})

	if checkStability(&session, false, dt, store, clk) {
		t.Error("draining session death should not count as stability failure")
	}
}

func TestCheckStability_StableSession(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()
	dt := newDrainTracker()

	// Woke long ago — past stability threshold.
	session := makeBead("b1", map[string]string{
		"last_woke_at": now.Add(-2 * time.Minute).Format(time.RFC3339),
	})

	if checkStability(&session, false, dt, store, clk) {
		t.Error("session that lived past threshold should not be stability failure")
	}
}

func TestRecordWakeFailure_Quarantine(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()

	session := makeBead("b1", map[string]string{
		"wake_attempts": "4", // one below threshold
	})

	recordWakeFailure(&session, store, clk)

	if session.Metadata["wake_attempts"] != "5" {
		t.Errorf("wake_attempts = %q, want 5", session.Metadata["wake_attempts"])
	}
	if session.Metadata["quarantined_until"] == "" {
		t.Error("expected quarantine to be set at max attempts")
	}
	if session.Metadata["sleep_reason"] != "quarantine" {
		t.Errorf("sleep_reason = %q, want quarantine", session.Metadata["sleep_reason"])
	}
}

func TestRecordWakeFailure_BelowThreshold(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()

	session := makeBead("b1", map[string]string{
		"wake_attempts": "1",
	})

	recordWakeFailure(&session, store, clk)

	if session.Metadata["wake_attempts"] != "2" {
		t.Errorf("wake_attempts = %q, want 2", session.Metadata["wake_attempts"])
	}
	if session.Metadata["quarantined_until"] != "" {
		t.Error("should not quarantine below threshold")
	}
}

func TestClearWakeFailures(t *testing.T) {
	store := newTestStore()

	session := makeBead("b1", map[string]string{
		"wake_attempts":     "5",
		"quarantined_until": "2026-03-08T12:00:00Z",
	})

	clearWakeFailures(&session, store)

	if session.Metadata["wake_attempts"] != "0" {
		t.Errorf("wake_attempts = %q, want 0", session.Metadata["wake_attempts"])
	}
	if session.Metadata["quarantined_until"] != "" {
		t.Error("quarantined_until should be cleared")
	}
}

func TestStableLongEnough(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	tests := []struct {
		name     string
		lastWoke string
		want     bool
	}{
		{"no last_woke_at", "", false},
		{"recent wake", now.Add(-10 * time.Second).Format(time.RFC3339), false},
		{"exactly at threshold", now.Add(-stabilityThreshold).Format(time.RFC3339), true},
		{"past threshold", now.Add(-2 * time.Minute).Format(time.RFC3339), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := makeBead("b1", map[string]string{
				"last_woke_at": tt.lastWoke,
			})
			got := stableLongEnough(session, clk)
			if got != tt.want {
				t.Errorf("stableLongEnough = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSessionIsQuarantined(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	tests := []struct {
		name string
		qVal string
		want bool
	}{
		{"not set", "", false},
		{"future", now.Add(5 * time.Minute).Format(time.RFC3339), true},
		{"past", now.Add(-5 * time.Minute).Format(time.RFC3339), false},
		{"invalid", "not-a-time", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := makeBead("b1", map[string]string{
				"quarantined_until": tt.qVal,
			})
			got := sessionIsQuarantined(session, clk)
			if got != tt.want {
				t.Errorf("sessionIsQuarantined = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsPoolExcess(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", Pool: &config.PoolConfig{Min: 1, Max: 5}},
			{Name: "singleton"},
		},
	}
	poolDesired := map[string]int{"worker": 3}

	tests := []struct {
		name     string
		template string
		slot     string
		want     bool
	}{
		{"within desired", "worker", "2", false},
		{"at desired", "worker", "3", false},
		{"exceeds desired", "worker", "4", true},
		{"singleton", "singleton", "", false},
		{"unknown template", "missing", "1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := makeBead("b1", map[string]string{
				"template":  tt.template,
				"pool_slot": tt.slot,
			})
			got := isPoolExcess(session, cfg, poolDesired)
			if got != tt.want {
				t.Errorf("isPoolExcess = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHealState(t *testing.T) {
	store := newTestStore()

	session := makeBead("b1", map[string]string{
		"state": "asleep",
	})

	healState(&session, true, store)
	if session.Metadata["state"] != "awake" {
		t.Errorf("state = %q, want awake", session.Metadata["state"])
	}

	healState(&session, false, store)
	if session.Metadata["state"] != "asleep" {
		t.Errorf("state = %q, want asleep", session.Metadata["state"])
	}

	// No-op when already correct.
	prevCalls := len(store.metadata["b1"])
	healState(&session, false, store)
	if len(store.metadata["b1"]) != prevCalls {
		t.Error("healState should not write when state unchanged")
	}
}

func TestTopoOrder_NoDeps(t *testing.T) {
	sessions := []beads.Bead{
		makeBead("b1", map[string]string{"template": "a"}),
		makeBead("b2", map[string]string{"template": "b"}),
	}

	result := topoOrder(sessions, nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(result))
	}
}

func TestTopoOrder_WithDeps(t *testing.T) {
	sessions := []beads.Bead{
		makeBead("b1", map[string]string{"template": "frontend"}),
		makeBead("b2", map[string]string{"template": "api"}),
		makeBead("b3", map[string]string{"template": "database"}),
	}

	deps := map[string][]string{
		"frontend": {"api"},
		"api":      {"database"},
	}

	result := topoOrder(sessions, deps)
	if len(result) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(result))
	}

	// database should come before api, api before frontend.
	idx := make(map[string]int)
	for i, s := range result {
		idx[s.Metadata["template"]] = i
	}
	if idx["database"] > idx["api"] {
		t.Errorf("database (idx %d) should come before api (idx %d)", idx["database"], idx["api"])
	}
	if idx["api"] > idx["frontend"] {
		t.Errorf("api (idx %d) should come before frontend (idx %d)", idx["api"], idx["frontend"])
	}
}

func TestTopoOrder_CycleFallback(t *testing.T) {
	sessions := []beads.Bead{
		makeBead("b1", map[string]string{"template": "a"}),
		makeBead("b2", map[string]string{"template": "b"}),
	}

	deps := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}

	result := topoOrder(sessions, deps)
	// Should return original order (fallback on cycle).
	if len(result) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(result))
	}
	if result[0].ID != "b1" || result[1].ID != "b2" {
		t.Error("cycle fallback should return original order")
	}
}

func TestReverseBeads(t *testing.T) {
	beadSlice := []beads.Bead{
		makeBead("b1", nil),
		makeBead("b2", nil),
		makeBead("b3", nil),
	}

	reversed := reverseBeads(beadSlice)
	if reversed[0].ID != "b3" || reversed[1].ID != "b2" || reversed[2].ID != "b1" {
		t.Errorf("expected reversed order, got %s %s %s",
			reversed[0].ID, reversed[1].ID, reversed[2].ID)
	}

	// Original unchanged.
	if beadSlice[0].ID != "b1" {
		t.Error("original should not be modified")
	}
}

func TestSessionWakeAttempts(t *testing.T) {
	tests := []struct {
		val  string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"3", 3},
		{"invalid", 0},
	}
	for _, tt := range tests {
		session := makeBead("b1", map[string]string{"wake_attempts": tt.val})
		got := sessionWakeAttempts(session)
		if got != tt.want {
			t.Errorf("sessionWakeAttempts(%q) = %d, want %d", tt.val, got, tt.want)
		}
	}
}

func TestFindAgentByTemplate(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker"},
			{Name: "mayor"},
		},
	}

	if a := findAgentByTemplate(cfg, "worker"); a == nil || a.Name != "worker" {
		t.Error("expected to find worker")
	}
	if a := findAgentByTemplate(cfg, "missing"); a != nil {
		t.Error("expected nil for missing template")
	}
	if a := findAgentByTemplate(nil, "worker"); a != nil {
		t.Error("expected nil for nil config")
	}
	if a := findAgentByTemplate(cfg, ""); a != nil {
		t.Error("expected nil for empty template")
	}
}

// --- isKnownState tests (Phase 0b: forward compatibility) ---

func TestIsKnownState_KnownStates(t *testing.T) {
	known := []string{
		"active", "asleep", "awake", "stopped", "suspended",
		"orphaned", "closed", "quarantined", "creating", "",
	}
	for _, state := range known {
		session := makeBead("b1", map[string]string{"state": state})
		if !isKnownState(session) {
			t.Errorf("state %q should be known", state)
		}
	}
}

func TestIsKnownState_UnknownStates(t *testing.T) {
	unknown := []string{"draining", "archived", "future-state"}
	for _, state := range unknown {
		session := makeBead("b1", map[string]string{"state": state})
		if isKnownState(session) {
			t.Errorf("state %q should be unknown", state)
		}
	}
}

func TestForwardCompatibility_UnknownState(t *testing.T) {
	env := newReconcilerTestEnv()
	env.cfg = &config.City{Agents: []config.Agent{{Name: "worker"}}}
	env.addAgent("worker", false)

	// Create a session bead with a future state that the current reconciler
	// doesn't understand.
	session := env.createSessionBead("worker", "worker")
	_ = env.store.SetMetadata(session.ID, "state", "draining")
	session.Metadata["state"] = "draining"

	// Should not panic, should skip the unknown-state bead.
	woken := env.reconcile([]beads.Bead{session})
	if woken != 0 {
		t.Errorf("expected 0 woken for unknown state, got %d", woken)
	}

	// The warning should appear in stderr.
	if !strings.Contains(env.stderr.String(), "unknown state") {
		t.Errorf("expected warning about unknown state in stderr, got: %s", env.stderr.String())
	}
}
