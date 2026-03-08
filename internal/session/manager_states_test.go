package session

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
)

func createTestSession(t *testing.T, m *Manager, template string) string {
	t.Helper()
	sp := m.sp.(*runtime.Fake)
	_ = sp // ensure fake provider available

	b, err := m.store.Create(beads.Bead{
		Title: template,
		Type:  BeadType,
		Labels: []string{
			LabelSession,
			"template:" + template,
		},
		Metadata: map[string]string{
			"template":     template,
			"state":        string(StateActive),
			"session_name": "s-test-" + template,
		},
	})
	if err != nil {
		t.Fatalf("creating test bead: %v", err)
	}
	return b.ID
}

func getState(t *testing.T, m *Manager, id string) State {
	t.Helper()
	b, err := m.store.Get(id)
	if err != nil {
		t.Fatalf("getting bead: %v", err)
	}
	return State(b.Metadata["state"])
}

func TestConformance_CreatingState(t *testing.T) {
	store := beads.NewMemStore()
	sp := runtime.NewFake()
	m := NewManager(store, sp)

	// Create a bead in creating state.
	b, err := store.Create(beads.Bead{
		Title:  "worker",
		Type:   BeadType,
		Labels: []string{LabelSession},
		Metadata: map[string]string{
			"template": "worker",
			"state":    string(StateCreating),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm creation transitions to active.
	if err := m.ConfirmCreation(b.ID); err != nil {
		t.Fatal(err)
	}
	if s := getState(t, m, b.ID); s != StateActive {
		t.Errorf("state = %q, want %q", s, StateActive)
	}
	// Check state_reason.
	got, _ := store.Get(b.ID)
	if got.Metadata["state_reason"] != "creation_complete" {
		t.Errorf("state_reason = %q, want creation_complete", got.Metadata["state_reason"])
	}
}

func TestConformance_DrainState(t *testing.T) {
	store := beads.NewMemStore()
	sp := runtime.NewFake()
	m := NewManager(store, sp)

	id := createTestSession(t, m, "worker")

	// Begin drain.
	if err := m.BeginDrain(id, "config-drift"); err != nil {
		t.Fatal(err)
	}
	if s := getState(t, m, id); s != StateDraining {
		t.Errorf("state = %q, want %q", s, StateDraining)
	}
	b, _ := store.Get(id)
	if b.Metadata["state_reason"] != "config-drift" {
		t.Errorf("state_reason = %q, want config-drift", b.Metadata["state_reason"])
	}
	if b.Metadata["drain_at"] == "" {
		t.Error("drain_at should be set")
	}

	// Archive after drain.
	if err := m.Archive(id, "drain_complete"); err != nil {
		t.Fatal(err)
	}
	if s := getState(t, m, id); s != StateArchived {
		t.Errorf("state = %q, want %q", s, StateArchived)
	}
	b, _ = store.Get(id)
	if b.Metadata["archived_at"] == "" {
		t.Error("archived_at should be set")
	}
}

func TestConformance_QuarantineState(t *testing.T) {
	store := beads.NewMemStore()
	sp := runtime.NewFake()
	m := NewManager(store, sp)

	id := createTestSession(t, m, "worker")

	until := time.Now().Add(5 * time.Minute)
	if err := m.Quarantine(id, until, 3); err != nil {
		t.Fatal(err)
	}
	if s := getState(t, m, id); s != StateQuarantined {
		t.Errorf("state = %q, want %q", s, StateQuarantined)
	}
	b, _ := store.Get(id)
	if b.Metadata["quarantine_cycle"] != "3" {
		t.Errorf("quarantine_cycle = %q, want 3", b.Metadata["quarantine_cycle"])
	}
	if b.Metadata["quarantined_until"] == "" {
		t.Error("quarantined_until should be set")
	}
}

func TestConformance_ArchivedReactivation(t *testing.T) {
	store := beads.NewMemStore()
	sp := runtime.NewFake()
	m := NewManager(store, sp)

	id := createTestSession(t, m, "worker")

	// Archive first.
	if err := m.Archive(id, "scale-down"); err != nil {
		t.Fatal(err)
	}
	if s := getState(t, m, id); s != StateArchived {
		t.Fatalf("state = %q, want %q", s, StateArchived)
	}

	// Reactivate.
	if err := m.Reactivate(id); err != nil {
		t.Fatal(err)
	}
	if s := getState(t, m, id); s != StateActive {
		t.Errorf("state = %q, want %q after reactivation", s, StateActive)
	}
	b, _ := store.Get(id)
	if b.Metadata["state_reason"] != "reactivated" {
		t.Errorf("state_reason = %q, want reactivated", b.Metadata["state_reason"])
	}
	if b.Metadata["archived_at"] != "" {
		t.Error("archived_at should be cleared on reactivation")
	}
}
