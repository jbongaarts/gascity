package convergence

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMarshalPayload_CreatedPayload(t *testing.T) {
	retrySource := "gc-conv-old"
	p := CreatedPayload{
		Formula:       "mol-design-review",
		Target:        "author-agent",
		GateMode:      GateModeHybrid,
		MaxIterations: 5,
		Title:         "Design: auth service v2",
		FirstWispID:   "gc-w-1",
		RetrySource:   &retrySource,
	}
	data := MarshalPayload(p)
	if data == nil {
		t.Fatal("MarshalPayload returned nil")
	}

	var decoded CreatedPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Formula != p.Formula {
		t.Errorf("formula = %q, want %q", decoded.Formula, p.Formula)
	}
	if decoded.RetrySource == nil || *decoded.RetrySource != retrySource {
		t.Errorf("retry_source = %v, want %q", decoded.RetrySource, retrySource)
	}
}

func TestMarshalPayload_IterationPayload_NullFields(t *testing.T) {
	p := IterationPayload{
		Iteration:     1,
		WispID:        "gc-w-1",
		AgentVerdict:  "approve",
		GateMode:      GateModeManual,
		GateOutcome:   nil, // null for manual mode
		GateResult:    nil, // null for manual mode
		Action:        "waiting_manual",
		WaitingReason: NullableString("manual"),
	}
	data := MarshalPayload(p)
	if data == nil {
		t.Fatal("MarshalPayload returned nil")
	}

	// Verify null fields are present in JSON.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	// gate_outcome should be present with value null.
	if v, ok := raw["gate_outcome"]; !ok {
		t.Error("gate_outcome should be present in JSON")
	} else if string(v) != "null" {
		t.Errorf("gate_outcome = %s, want null", v)
	}
	// gate_result should be present with value null.
	if v, ok := raw["gate_result"]; !ok {
		t.Error("gate_result should be present in JSON")
	} else if string(v) != "null" {
		t.Errorf("gate_result = %s, want null", v)
	}
}

func TestMarshalPayload_TerminatedPayload(t *testing.T) {
	p := TerminatedPayload{
		TerminalReason:       TerminalApproved,
		TotalIterations:      3,
		FinalStatus:          "closed",
		Actor:                "controller",
		CumulativeDurationMs: 180000,
	}
	data := MarshalPayload(p)
	if data == nil {
		t.Fatal("MarshalPayload returned nil")
	}

	var decoded TerminatedPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.TerminalReason != TerminalApproved {
		t.Errorf("terminal_reason = %q, want %q", decoded.TerminalReason, TerminalApproved)
	}
	if decoded.TotalIterations != 3 {
		t.Errorf("total_iterations = %d, want 3", decoded.TotalIterations)
	}
	if decoded.FinalStatus != "closed" {
		t.Errorf("final_status = %q, want %q", decoded.FinalStatus, "closed")
	}
}

func TestMarshalPayload_WaitingManualPayload(t *testing.T) {
	timeout := GateTimeout
	exitCode := 1
	p := WaitingManualPayload{
		Iteration:    2,
		WispID:       "gc-w-5",
		AgentVerdict: "block",
		GateMode:     GateModeCondition,
		GateOutcome:  &timeout,
		GateResult: &GateResultPayload{
			ExitCode:   &exitCode,
			Stdout:     "check failed",
			Stderr:     "",
			DurationMs: 55000,
			Truncated:  false,
		},
		Reason:               WaitTimeout,
		IterationDurationMs:  60000,
		CumulativeDurationMs: 120000,
	}
	data := MarshalPayload(p)
	if data == nil {
		t.Fatal("MarshalPayload returned nil")
	}

	var decoded WaitingManualPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Reason != WaitTimeout {
		t.Errorf("reason = %q, want %q", decoded.Reason, WaitTimeout)
	}
	if decoded.GateOutcome == nil || *decoded.GateOutcome != GateTimeout {
		t.Errorf("gate_outcome = %v, want %q", decoded.GateOutcome, GateTimeout)
	}
}

func TestMarshalPayload_ManualActionPayload(t *testing.T) {
	nextWisp := "gc-w-10"
	p := ManualActionPayload{
		Actor:      "operator:alice",
		PriorState: StateWaitingManual,
		NewState:   StateActive,
		Iteration:  3,
		WispID:     NullableString("gc-w-5"),
		NextWispID: &nextWisp,
	}
	data := MarshalPayload(p)
	if data == nil {
		t.Fatal("MarshalPayload returned nil")
	}

	var decoded ManualActionPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Actor != "operator:alice" {
		t.Errorf("actor = %q, want %q", decoded.Actor, "operator:alice")
	}
	if decoded.NextWispID == nil || *decoded.NextWispID != nextWisp {
		t.Errorf("next_wisp_id = %v, want %q", decoded.NextWispID, nextWisp)
	}
}

func TestGateResultPayload_NullExitCode(t *testing.T) {
	p := GateResultPayload{
		ExitCode:   nil, // timeout — process killed
		Stdout:     "",
		Stderr:     "signal: killed",
		DurationMs: 60000,
		Truncated:  false,
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if string(raw["exit_code"]) != "null" {
		t.Errorf("exit_code = %s, want null", raw["exit_code"])
	}
}

func TestDeliveryTiers(t *testing.T) {
	// Verify tier assignments match the spec.
	if TierCritical != "critical" {
		t.Errorf("TierCritical = %q, want %q", TierCritical, "critical")
	}
	if TierRecoverable != "recoverable" {
		t.Errorf("TierRecoverable = %q, want %q", TierRecoverable, "recoverable")
	}
	if TierBestEffort != "best_effort" {
		t.Errorf("TierBestEffort = %q, want %q", TierBestEffort, "best_effort")
	}
}

func TestGateResultToPayload_WithDuration(t *testing.T) {
	code := 0
	r := GateResult{
		Outcome:   GatePass,
		ExitCode:  &code,
		Duration:  2500 * time.Millisecond,
		Stdout:    "ok",
		Truncated: false,
	}
	p := GateResultToPayload(r)
	if p == nil {
		t.Fatal("expected non-nil payload")
	}
	if p.DurationMs != 2500 {
		t.Errorf("duration_ms = %d, want 2500", p.DurationMs)
	}
}
