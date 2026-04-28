// This file pins the desired post-fix behavior for rate-limit-blind respawns.

package main

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/clock"
)

// TestCheckStability_RateLimitScreen_DoesNotCountAsCrash pins the desired
// post-fix behavior of checkStability when the agent's pane shows a
// Claude/Gemini rate-limit screen.
//
// When an agent CLI exits at the rate-limit screen, the session reconciler
// sees process_alive==false, calls checkStability, which sees last_woke_at
// within stabilityThreshold and counts it as a crash via recordWakeFailure.
// Five consecutive rate-limit exits within 30s trigger a 5-minute quarantine,
// so the system burns 5 wake/prime/--resume cycles before backing off, even
// though every wake will hit the same rate limit and produce zero useful work.
//
// Fix: extend checkStability to accept a peek callback (matching the shape
// already used by AcceptStartupDialogs* in internal/runtime/dialog.go). When
// peek returns content matching runtime.ContainsRateLimitDialog, the function
// records a rate-limit quarantine (longer back-off, distinct
// sleep_reason="rate_limit") instead of a crash, and does NOT increment
// wake_attempts.
func TestCheckStability_RateLimitScreen_DoesNotCountAsCrash(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()
	dt := newDrainTracker()

	session := makeBead("b1", map[string]string{
		"last_woke_at":        now.Add(-10 * time.Second).Format(time.RFC3339),
		"session_key":         "keep-session",
		"started_config_hash": "keep-hash",
		"wake_attempts":       "3", // a real crash would push us to 4
	})

	paneContent := "You've hit your limit, Pro plan\n\n/rate-limit-options"
	var gotLines int
	peek := func(lines int) (string, error) {
		gotLines = lines
		return paneContent, nil
	}

	if !checkStability(&session, nil, false, dt, store, clk, peek) {
		t.Fatal("checkStability should return true when it records a rate-limit hold")
	}

	if got := session.Metadata["wake_attempts"]; got != "3" {
		t.Errorf("wake_attempts = %q, want 3; rate-limit exit must not count as a crash", got)
	}

	if got := session.Metadata["sleep_reason"]; got != "rate_limit" {
		t.Errorf("sleep_reason = %q, want %q", got, "rate_limit")
	}
	if got := session.Metadata["state"]; got != "asleep" {
		t.Errorf("state = %q, want asleep", got)
	}

	qUntil, err := time.Parse(time.RFC3339, session.Metadata["quarantined_until"])
	if err != nil {
		t.Fatalf("quarantined_until parse: %v", err)
	}
	if want := now.Add(defaultRateLimitQuarantineDuration); !qUntil.Equal(want) {
		t.Errorf("quarantined_until = %s, want %s", qUntil.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	if gotLines != rateLimitPeekLines {
		t.Errorf("peek lines = %d, want %d", gotLines, rateLimitPeekLines)
	}

	if got := session.Metadata["session_key"]; got != "keep-session" {
		t.Errorf("session_key = %q, want preserved", got)
	}
	if got := session.Metadata["started_config_hash"]; got != "keep-hash" {
		t.Errorf("started_config_hash = %q, want preserved", got)
	}

	// last_woke_at should be cleared (edge-triggered, mirroring the existing
	// crash path) so the rate-limit detection isn't re-triggered next tick.
	if session.Metadata["last_woke_at"] != "" {
		t.Error("last_woke_at should be cleared after rate-limit detection")
	}
}

func TestCheckRateLimitStability_BeforeHealPreservesResumeMetadata(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()
	dt := newDrainTracker()

	session := makeBead("b1", map[string]string{
		"state":               "active",
		"last_woke_at":        now.Add(-10 * time.Second).Format(time.RFC3339),
		"session_key":         "keep-session",
		"started_config_hash": "keep-hash",
	})

	peek := func(_ int) (string, error) {
		return "You've hit your limit, Pro plan\n\n/rate-limit-options", nil
	}

	if !checkRateLimitStability(&session, nil, false, dt, store, clk, peek) {
		t.Fatal("rate-limit rapid exit should be recorded before advisory state healing")
	}

	healState(&session, false, store, clk)

	if got := session.Metadata["session_key"]; got != "keep-session" {
		t.Errorf("session_key = %q, want preserved", got)
	}
	if got := session.Metadata["started_config_hash"]; got != "keep-hash" {
		t.Errorf("started_config_hash = %q, want preserved", got)
	}
	if got := session.Metadata["continuation_reset_pending"]; got != "" {
		t.Errorf("continuation_reset_pending = %q, want empty", got)
	}
	if got := session.Metadata["state"]; got != "asleep" {
		t.Errorf("state = %q, want asleep", got)
	}
	if got := session.Metadata["sleep_reason"]; got != "rate_limit" {
		t.Errorf("sleep_reason = %q, want rate_limit", got)
	}
}

// TestCheckStability_RateLimitScreen_EmptyPaneStillCountsAsCrash ensures the
// rate-limit detection requires positive evidence in the pane. If peek
// returns nothing matching the rate-limit signature, behavior matches the
// existing crash path: count as a crash, increment wake_attempts.
func TestCheckStability_RateLimitScreen_EmptyPaneStillCountsAsCrash(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()
	dt := newDrainTracker()

	session := makeBead("b1", map[string]string{
		"last_woke_at":  now.Add(-10 * time.Second).Format(time.RFC3339),
		"wake_attempts": "0",
	})

	peek := func(_ int) (string, error) { return "", nil }

	if !checkStability(&session, nil, false, dt, store, clk, peek) {
		t.Error("rapid exit with no rate-limit signature should report stability failure")
	}
	if got := session.Metadata["wake_attempts"]; got != "1" {
		t.Errorf("wake_attempts = %q, want 1", got)
	}
}

// TestCheckStability_RateLimitScreen_NilPeekFallsBackToCrash ensures
// backward compatibility for call sites that don't supply a peek (subprocess
// providers, test paths). When peek is nil, behavior matches the legacy
// crash-only path.
func TestCheckStability_RateLimitScreen_NilPeekFallsBackToCrash(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}
	store := newTestStore()
	dt := newDrainTracker()

	session := makeBead("b1", map[string]string{
		"last_woke_at":  now.Add(-10 * time.Second).Format(time.RFC3339),
		"wake_attempts": "0",
	})

	if !checkStability(&session, nil, false, dt, store, clk, nil) {
		t.Error("rapid exit with nil peek should fall back to crash-counting behavior")
	}
	if got := session.Metadata["wake_attempts"]; got != "1" {
		t.Errorf("wake_attempts = %q, want 1", got)
	}
}
