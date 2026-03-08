package convergence

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// --- Test Helpers for manual commands ---

// setupWaitingManualHandler creates a handler with a root bead in
// waiting_manual state, with one closed child wisp (iteration 1).
func setupWaitingManualHandler(t *testing.T, extraMeta map[string]string) (*Handler, *fakeStore, *fakeEmitter) {
	t.Helper()

	store := newFakeStore()
	emitter := &fakeEmitter{}

	rootMeta := map[string]string{
		FieldState:             StateWaitingManual,
		FieldIteration:         "1",
		FieldMaxIterations:     "5",
		FieldFormula:           "test-formula",
		FieldTarget:            "test-agent",
		FieldGateMode:          GateModeManual,
		FieldWaitingReason:     WaitManual,
		FieldLastProcessedWisp: "wisp-iter-1",
	}
	for k, v := range extraMeta {
		rootMeta[k] = v
	}

	store.addBead("root-1", "in_progress", "", "", rootMeta)
	store.addBead("wisp-iter-1", "closed", "root-1",
		IdempotencyKey("root-1", 1), nil)

	handler := &Handler{
		Store:   store,
		Emitter: emitter,
		Clock:   time.Now,
	}

	return handler, store, emitter
}

// --- ApproveHandler Tests ---

func TestApproveHandler_HappyPath(t *testing.T) {
	handler, store, emitter := setupWaitingManualHandler(t, nil)

	result, err := handler.ApproveHandler(context.Background(), "root-1", "alice", "looks good")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionApproved {
		t.Errorf("Action = %q, want %q", result.Action, ActionApproved)
	}
	if result.Iteration != 1 {
		t.Errorf("Iteration = %d, want 1", result.Iteration)
	}

	// Verify terminal state in metadata.
	meta, _ := store.GetMetadata("root-1")
	if meta[FieldState] != StateTerminated {
		t.Errorf("state = %q, want %q", meta[FieldState], StateTerminated)
	}
	if meta[FieldTerminalReason] != TerminalApproved {
		t.Errorf("terminal_reason = %q, want %q", meta[FieldTerminalReason], TerminalApproved)
	}
	if meta[FieldTerminalActor] != "operator:alice" {
		t.Errorf("terminal_actor = %q, want %q", meta[FieldTerminalActor], "operator:alice")
	}
	if meta[FieldWaitingReason] != "" {
		t.Errorf("waiting_reason should be cleared, got %q", meta[FieldWaitingReason])
	}

	// Verify bead is closed.
	beadInfo, _ := store.GetBead("root-1")
	if beadInfo.Status != "closed" {
		t.Errorf("bead status = %q, want %q", beadInfo.Status, "closed")
	}

	// Verify events.
	if _, ok := emitter.findEvent(EventManualApprove); !ok {
		t.Error("expected ConvergenceManualApprove event")
	}
	if _, ok := emitter.findEvent(EventTerminated); !ok {
		t.Error("expected ConvergenceTerminated event")
	}
}

func TestApproveHandler_WrongState_Active(t *testing.T) {
	handler, _, _ := setupWaitingManualHandler(t, map[string]string{
		FieldState: StateActive,
	})

	_, err := handler.ApproveHandler(context.Background(), "root-1", "alice", "")
	if err == nil {
		t.Fatal("expected error for wrong state")
	}
	if !contains(err.Error(), StateWaitingManual) {
		t.Errorf("error should mention expected state %q, got: %v", StateWaitingManual, err)
	}
	if !contains(err.Error(), StateActive) {
		t.Errorf("error should mention current state %q, got: %v", StateActive, err)
	}
}

func TestApproveHandler_WrongState_Terminated(t *testing.T) {
	handler, _, _ := setupWaitingManualHandler(t, map[string]string{
		FieldState:          StateTerminated,
		FieldTerminalReason: TerminalNoConvergence,
	})

	_, err := handler.ApproveHandler(context.Background(), "root-1", "alice", "")
	if err == nil {
		t.Fatal("expected error for terminated with non-approved reason")
	}
	if !contains(err.Error(), StateWaitingManual) {
		t.Errorf("error should mention expected state, got: %v", err)
	}
}

func TestApproveHandler_Idempotent_AlreadyApproved(t *testing.T) {
	handler, store, emitter := setupWaitingManualHandler(t, map[string]string{
		FieldState:          StateTerminated,
		FieldTerminalReason: TerminalApproved,
		FieldTerminalActor:  "operator:bob",
	})
	_ = store

	result, err := handler.ApproveHandler(context.Background(), "root-1", "alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionApproved {
		t.Errorf("Action = %q, want %q", result.Action, ActionApproved)
	}

	// No events should be emitted for idempotent no-op.
	if _, ok := emitter.findEvent(EventManualApprove); ok {
		t.Error("should not emit event for idempotent no-op")
	}
}

func TestApproveHandler_WriteOrdering(t *testing.T) {
	handler, store, _ := setupWaitingManualHandler(t, nil)

	result, err := handler.ApproveHandler(context.Background(), "root-1", "alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionApproved {
		t.Fatalf("Action = %q, want %q", result.Action, ActionApproved)
	}

	// Verify write ordering: terminal_reason, terminal_actor before state,
	// and last_processed_wisp LAST.
	commitKeys := extractCommitKeys(store.WriteLog)

	// last_processed_wisp must be the very last write.
	lastKey := store.WriteLog[len(store.WriteLog)-1]
	if lastKey != FieldLastProcessedWisp {
		t.Errorf("last write = %q, want %q (dedup marker must be last)", lastKey, FieldLastProcessedWisp)
	}

	// terminal_reason and terminal_actor must appear before state.
	reasonIdx, actorIdx, stateIdx := -1, -1, -1
	for i, key := range commitKeys {
		switch key {
		case FieldTerminalReason:
			if reasonIdx == -1 {
				reasonIdx = i
			}
		case FieldTerminalActor:
			if actorIdx == -1 {
				actorIdx = i
			}
		case FieldState:
			if stateIdx == -1 {
				stateIdx = i
			}
		}
	}
	if reasonIdx >= stateIdx {
		t.Errorf("terminal_reason (idx %d) must be written before state (idx %d)", reasonIdx, stateIdx)
	}
	if actorIdx >= stateIdx {
		t.Errorf("terminal_actor (idx %d) must be written before state (idx %d)", actorIdx, stateIdx)
	}
}

func TestApproveHandler_EventPayloads(t *testing.T) {
	handler, _, emitter := setupWaitingManualHandler(t, nil)

	_, err := handler.ApproveHandler(context.Background(), "root-1", "alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check ManualApprove event.
	approveEv, ok := emitter.findEvent(EventManualApprove)
	if !ok {
		t.Fatal("expected ConvergenceManualApprove event")
	}
	if approveEv.EventID != EventIDManualApprove("root-1") {
		t.Errorf("event_id = %q, want %q", approveEv.EventID, EventIDManualApprove("root-1"))
	}
	if approveEv.BeadID != "root-1" {
		t.Errorf("bead_id = %q, want %q", approveEv.BeadID, "root-1")
	}

	var approvePayload ManualActionPayload
	if err := json.Unmarshal(approveEv.Payload, &approvePayload); err != nil {
		t.Fatalf("unmarshal approve payload: %v", err)
	}
	if approvePayload.Actor != "operator:alice" {
		t.Errorf("actor = %q, want %q", approvePayload.Actor, "operator:alice")
	}
	if approvePayload.PriorState != StateWaitingManual {
		t.Errorf("prior_state = %q, want %q", approvePayload.PriorState, StateWaitingManual)
	}
	if approvePayload.NewState != StateTerminated {
		t.Errorf("new_state = %q, want %q", approvePayload.NewState, StateTerminated)
	}

	// Check Terminated event.
	termEv, ok := emitter.findEvent(EventTerminated)
	if !ok {
		t.Fatal("expected ConvergenceTerminated event")
	}

	var termPayload TerminatedPayload
	if err := json.Unmarshal(termEv.Payload, &termPayload); err != nil {
		t.Fatalf("unmarshal terminated payload: %v", err)
	}
	if termPayload.TerminalReason != TerminalApproved {
		t.Errorf("terminal_reason = %q, want %q", termPayload.TerminalReason, TerminalApproved)
	}
	if termPayload.Actor != "operator:alice" {
		t.Errorf("actor = %q, want %q", termPayload.Actor, "operator:alice")
	}
	if termPayload.FinalStatus != "closed" {
		t.Errorf("final_status = %q, want %q", termPayload.FinalStatus, "closed")
	}
}

// --- IterateHandler Tests ---

func TestIterateHandler_HappyPath(t *testing.T) {
	handler, store, emitter := setupWaitingManualHandler(t, nil)

	result, err := handler.IterateHandler(context.Background(), "root-1", "alice", "try again")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionIterate {
		t.Errorf("Action = %q, want %q", result.Action, ActionIterate)
	}
	if result.NextWispID == "" {
		t.Error("expected NextWispID to be set")
	}
	if result.Iteration != 2 {
		t.Errorf("Iteration = %d, want 2", result.Iteration)
	}

	// Verify state is back to active.
	meta, _ := store.GetMetadata("root-1")
	if meta[FieldState] != StateActive {
		t.Errorf("state = %q, want %q", meta[FieldState], StateActive)
	}
	if meta[FieldWaitingReason] != "" {
		t.Errorf("waiting_reason should be cleared, got %q", meta[FieldWaitingReason])
	}
	if meta[FieldActiveWisp] != result.NextWispID {
		t.Errorf("active_wisp = %q, want %q", meta[FieldActiveWisp], result.NextWispID)
	}

	// Verify ManualIterate event.
	if _, ok := emitter.findEvent(EventManualIterate); !ok {
		t.Error("expected ConvergenceManualIterate event")
	}
}

func TestIterateHandler_WrongState_Active(t *testing.T) {
	handler, _, _ := setupWaitingManualHandler(t, map[string]string{
		FieldState: StateActive,
	})

	_, err := handler.IterateHandler(context.Background(), "root-1", "alice", "")
	if err == nil {
		t.Fatal("expected error for wrong state")
	}
	if !contains(err.Error(), StateWaitingManual) {
		t.Errorf("error should mention expected state %q, got: %v", StateWaitingManual, err)
	}
}

func TestIterateHandler_WrongState_Terminated(t *testing.T) {
	handler, _, _ := setupWaitingManualHandler(t, map[string]string{
		FieldState: StateTerminated,
	})

	_, err := handler.IterateHandler(context.Background(), "root-1", "alice", "")
	if err == nil {
		t.Fatal("expected error for wrong state")
	}
	if !contains(err.Error(), StateWaitingManual) {
		t.Errorf("error should mention expected state %q, got: %v", StateWaitingManual, err)
	}
}

func TestIterateHandler_AtMaxIterations(t *testing.T) {
	handler, _, _ := setupWaitingManualHandler(t, map[string]string{
		FieldMaxIterations: "1", // max is 1, iteration count is 1 (one closed child)
	})

	_, err := handler.IterateHandler(context.Background(), "root-1", "alice", "")
	if err == nil {
		t.Fatal("expected error for max iterations")
	}
	if !contains(err.Error(), "max iterations") {
		t.Errorf("error should mention max iterations, got: %v", err)
	}
}

func TestIterateHandler_ClearsVerdictScopedToLastWisp(t *testing.T) {
	handler, store, _ := setupWaitingManualHandler(t, map[string]string{
		FieldAgentVerdict:     "block",
		FieldAgentVerdictWisp: "wisp-iter-1", // matches last_processed_wisp
	})

	result, err := handler.IterateHandler(context.Background(), "root-1", "alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionIterate {
		t.Fatalf("Action = %q, want %q", result.Action, ActionIterate)
	}

	// Verdict should be cleared.
	meta, _ := store.GetMetadata("root-1")
	if meta[FieldAgentVerdict] != "" {
		t.Errorf("agent_verdict should be cleared, got %q", meta[FieldAgentVerdict])
	}
	if meta[FieldAgentVerdictWisp] != "" {
		t.Errorf("agent_verdict_wisp should be cleared, got %q", meta[FieldAgentVerdictWisp])
	}
}

func TestIterateHandler_PreservesVerdictScopedToOtherWisp(t *testing.T) {
	handler, store, _ := setupWaitingManualHandler(t, map[string]string{
		FieldAgentVerdict:     "approve",
		FieldAgentVerdictWisp: "wisp-other", // does NOT match last_processed_wisp
	})

	result, err := handler.IterateHandler(context.Background(), "root-1", "alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionIterate {
		t.Fatalf("Action = %q, want %q", result.Action, ActionIterate)
	}

	// Verdict should NOT be cleared.
	meta, _ := store.GetMetadata("root-1")
	if meta[FieldAgentVerdict] != "approve" {
		t.Errorf("agent_verdict should be preserved, got %q", meta[FieldAgentVerdict])
	}
}

func TestIterateHandler_EventPayloads(t *testing.T) {
	handler, _, emitter := setupWaitingManualHandler(t, nil)

	result, err := handler.IterateHandler(context.Background(), "root-1", "alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ev, ok := emitter.findEvent(EventManualIterate)
	if !ok {
		t.Fatal("expected ConvergenceManualIterate event")
	}
	if ev.EventID != EventIDManualIterate("root-1", 2) {
		t.Errorf("event_id = %q, want %q", ev.EventID, EventIDManualIterate("root-1", 2))
	}

	var payload ManualActionPayload
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Actor != "operator:alice" {
		t.Errorf("actor = %q, want %q", payload.Actor, "operator:alice")
	}
	if payload.PriorState != StateWaitingManual {
		t.Errorf("prior_state = %q, want %q", payload.PriorState, StateWaitingManual)
	}
	if payload.NewState != StateActive {
		t.Errorf("new_state = %q, want %q", payload.NewState, StateActive)
	}
	if payload.NextWispID == nil || *payload.NextWispID != result.NextWispID {
		t.Errorf("next_wisp_id = %v, want %q", payload.NextWispID, result.NextWispID)
	}
}

func TestIterateHandler_PourWispFailure(t *testing.T) {
	store := newFakeStore()
	emitter := &fakeEmitter{}

	rootMeta := map[string]string{
		FieldState:             StateWaitingManual,
		FieldIteration:         "1",
		FieldMaxIterations:     "5",
		FieldFormula:           "test-formula",
		FieldTarget:            "test-agent",
		FieldGateMode:          GateModeManual,
		FieldWaitingReason:     WaitManual,
		FieldLastProcessedWisp: "wisp-iter-1",
	}
	store.addBead("root-1", "in_progress", "", "", rootMeta)
	store.addBead("wisp-iter-1", "closed", "root-1",
		IdempotencyKey("root-1", 1), nil)

	store.PourWispFunc = func(_, _, _ string, _ map[string]string, _ string) (string, error) {
		return "", fmt.Errorf("sling failure")
	}

	handler := &Handler{Store: store, Emitter: emitter}

	_, err := handler.IterateHandler(context.Background(), "root-1", "alice", "")
	if err == nil {
		t.Fatal("expected error for pour wisp failure")
	}
	if !contains(err.Error(), "pouring next wisp") {
		t.Errorf("error should mention pouring wisp, got: %v", err)
	}
}

// --- StopHandler Tests ---

func TestStopHandler_HappyPath_WaitingManual(t *testing.T) {
	handler, store, emitter := setupWaitingManualHandler(t, nil)

	result, err := handler.StopHandler(context.Background(), "root-1", "alice", "shutting down")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionStopped {
		t.Errorf("Action = %q, want %q", result.Action, ActionStopped)
	}
	if result.Iteration != 1 {
		t.Errorf("Iteration = %d, want 1", result.Iteration)
	}

	// Verify terminal state.
	meta, _ := store.GetMetadata("root-1")
	if meta[FieldState] != StateTerminated {
		t.Errorf("state = %q, want %q", meta[FieldState], StateTerminated)
	}
	if meta[FieldTerminalReason] != TerminalStopped {
		t.Errorf("terminal_reason = %q, want %q", meta[FieldTerminalReason], TerminalStopped)
	}
	if meta[FieldTerminalActor] != "operator:alice" {
		t.Errorf("terminal_actor = %q, want %q", meta[FieldTerminalActor], "operator:alice")
	}
	if meta[FieldWaitingReason] != "" {
		t.Errorf("waiting_reason should be cleared, got %q", meta[FieldWaitingReason])
	}

	// Verify bead is closed.
	beadInfo, _ := store.GetBead("root-1")
	if beadInfo.Status != "closed" {
		t.Errorf("bead status = %q, want %q", beadInfo.Status, "closed")
	}

	// Verify events.
	if _, ok := emitter.findEvent(EventManualStop); !ok {
		t.Error("expected ConvergenceManualStop event")
	}
	if _, ok := emitter.findEvent(EventTerminated); !ok {
		t.Error("expected ConvergenceTerminated event")
	}
}

func TestStopHandler_HappyPath_Active(t *testing.T) {
	handler, store, emitter := setupWaitingManualHandler(t, map[string]string{
		FieldState: StateActive,
	})

	result, err := handler.StopHandler(context.Background(), "root-1", "alice", "abort")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionStopped {
		t.Errorf("Action = %q, want %q", result.Action, ActionStopped)
	}

	// Verify terminal state.
	meta, _ := store.GetMetadata("root-1")
	if meta[FieldState] != StateTerminated {
		t.Errorf("state = %q, want %q", meta[FieldState], StateTerminated)
	}
	if meta[FieldTerminalReason] != TerminalStopped {
		t.Errorf("terminal_reason = %q, want %q", meta[FieldTerminalReason], TerminalStopped)
	}

	_ = store
	_ = emitter
}

func TestStopHandler_WrongState_Terminated_NotStopped(t *testing.T) {
	handler, _, _ := setupWaitingManualHandler(t, map[string]string{
		FieldState:          StateTerminated,
		FieldTerminalReason: TerminalApproved,
	})

	_, err := handler.StopHandler(context.Background(), "root-1", "alice", "")
	if err == nil {
		t.Fatal("expected error for terminated bead")
	}
	if !contains(err.Error(), StateActive) {
		t.Errorf("error should mention expected state %q, got: %v", StateActive, err)
	}
	if !contains(err.Error(), StateWaitingManual) {
		t.Errorf("error should mention expected state %q, got: %v", StateWaitingManual, err)
	}
}

func TestStopHandler_Idempotent_AlreadyStopped(t *testing.T) {
	handler, store, emitter := setupWaitingManualHandler(t, map[string]string{
		FieldState:          StateTerminated,
		FieldTerminalReason: TerminalStopped,
		FieldTerminalActor:  "operator:bob",
	})
	_ = store

	result, err := handler.StopHandler(context.Background(), "root-1", "alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionStopped {
		t.Errorf("Action = %q, want %q", result.Action, ActionStopped)
	}

	// No events should be emitted for idempotent no-op.
	if _, ok := emitter.findEvent(EventManualStop); ok {
		t.Error("should not emit event for idempotent no-op")
	}
}

func TestStopHandler_WriteOrdering(t *testing.T) {
	handler, store, _ := setupWaitingManualHandler(t, nil)

	result, err := handler.StopHandler(context.Background(), "root-1", "alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionStopped {
		t.Fatalf("Action = %q, want %q", result.Action, ActionStopped)
	}

	// last_processed_wisp must be the very last write.
	lastKey := store.WriteLog[len(store.WriteLog)-1]
	if lastKey != FieldLastProcessedWisp {
		t.Errorf("last write = %q, want %q (dedup marker must be last)", lastKey, FieldLastProcessedWisp)
	}

	// terminal_reason and terminal_actor must appear before state.
	commitKeys := extractCommitKeys(store.WriteLog)
	reasonIdx, actorIdx, stateIdx := -1, -1, -1
	for i, key := range commitKeys {
		switch key {
		case FieldTerminalReason:
			if reasonIdx == -1 {
				reasonIdx = i
			}
		case FieldTerminalActor:
			if actorIdx == -1 {
				actorIdx = i
			}
		case FieldState:
			if stateIdx == -1 {
				stateIdx = i
			}
		}
	}
	if reasonIdx >= stateIdx {
		t.Errorf("terminal_reason (idx %d) must be written before state (idx %d)", reasonIdx, stateIdx)
	}
	if actorIdx >= stateIdx {
		t.Errorf("terminal_actor (idx %d) must be written before state (idx %d)", actorIdx, stateIdx)
	}
}

func TestStopHandler_EventPayloads(t *testing.T) {
	handler, _, emitter := setupWaitingManualHandler(t, nil)

	_, err := handler.StopHandler(context.Background(), "root-1", "alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check ManualStop event.
	stopEv, ok := emitter.findEvent(EventManualStop)
	if !ok {
		t.Fatal("expected ConvergenceManualStop event")
	}
	if stopEv.EventID != EventIDManualStop("root-1") {
		t.Errorf("event_id = %q, want %q", stopEv.EventID, EventIDManualStop("root-1"))
	}

	var stopPayload ManualActionPayload
	if err := json.Unmarshal(stopEv.Payload, &stopPayload); err != nil {
		t.Fatalf("unmarshal stop payload: %v", err)
	}
	if stopPayload.Actor != "operator:alice" {
		t.Errorf("actor = %q, want %q", stopPayload.Actor, "operator:alice")
	}
	if stopPayload.PriorState != StateWaitingManual {
		t.Errorf("prior_state = %q, want %q", stopPayload.PriorState, StateWaitingManual)
	}
	if stopPayload.NewState != StateTerminated {
		t.Errorf("new_state = %q, want %q", stopPayload.NewState, StateTerminated)
	}

	// Check Terminated event.
	termEv, ok := emitter.findEvent(EventTerminated)
	if !ok {
		t.Fatal("expected ConvergenceTerminated event")
	}

	var termPayload TerminatedPayload
	if err := json.Unmarshal(termEv.Payload, &termPayload); err != nil {
		t.Fatalf("unmarshal terminated payload: %v", err)
	}
	if termPayload.TerminalReason != TerminalStopped {
		t.Errorf("terminal_reason = %q, want %q", termPayload.TerminalReason, TerminalStopped)
	}
	if termPayload.Actor != "operator:alice" {
		t.Errorf("actor = %q, want %q", termPayload.Actor, "operator:alice")
	}
}

func TestStopHandler_StopFromActive_PriorStateInEvent(t *testing.T) {
	handler, _, emitter := setupWaitingManualHandler(t, map[string]string{
		FieldState: StateActive,
	})

	_, err := handler.StopHandler(context.Background(), "root-1", "alice", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stopEv, ok := emitter.findEvent(EventManualStop)
	if !ok {
		t.Fatal("expected ConvergenceManualStop event")
	}

	var payload ManualActionPayload
	if err := json.Unmarshal(stopEv.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	// Prior state should reflect the actual state before stop.
	if payload.PriorState != StateActive {
		t.Errorf("prior_state = %q, want %q", payload.PriorState, StateActive)
	}
}
