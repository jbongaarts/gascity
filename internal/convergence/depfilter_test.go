package convergence

import "testing"

func TestMatchesDependencyFilter_EmptyFilter(t *testing.T) {
	meta := map[string]string{"key": "value"}
	if !MatchesDependencyFilter(meta, nil) {
		t.Error("empty filter (nil) should always match")
	}
	if !MatchesDependencyFilter(meta, map[string]string{}) {
		t.Error("empty filter (empty map) should always match")
	}
}

func TestMatchesDependencyFilter_Match(t *testing.T) {
	meta := map[string]string{
		"convergence.state":           "terminated",
		"convergence.terminal_reason": "approved",
	}
	filter := map[string]string{
		"convergence.terminal_reason": "approved",
	}
	if !MatchesDependencyFilter(meta, filter) {
		t.Error("expected filter to match")
	}
}

func TestMatchesDependencyFilter_Mismatch(t *testing.T) {
	meta := map[string]string{
		"convergence.state":           "terminated",
		"convergence.terminal_reason": "stopped",
	}
	filter := map[string]string{
		"convergence.terminal_reason": "approved",
	}
	if MatchesDependencyFilter(meta, filter) {
		t.Error("expected filter to not match")
	}
}

func TestMatchesDependencyFilter_MissingKey(t *testing.T) {
	meta := map[string]string{
		"convergence.state": "terminated",
	}
	filter := map[string]string{
		"convergence.terminal_reason": "approved",
	}
	if MatchesDependencyFilter(meta, filter) {
		t.Error("expected filter to not match when key is missing")
	}
}

func TestMatchesDependencyFilter_EmptyStringVsMissing(t *testing.T) {
	// Filter for empty string should NOT match when the key is absent.
	metaMissing := map[string]string{
		"convergence.state": "terminated",
	}
	filter := map[string]string{
		"convergence.waiting_reason": "",
	}
	if MatchesDependencyFilter(metaMissing, filter) {
		t.Error("empty-string filter should NOT match when key is absent")
	}

	// Filter for empty string SHOULD match when key is present and empty.
	metaPresent := map[string]string{
		"convergence.state":          "terminated",
		"convergence.waiting_reason": "",
	}
	if !MatchesDependencyFilter(metaPresent, filter) {
		t.Error("empty-string filter should match when key is present and empty")
	}
}

func TestMatchesDependencyFilter_MultipleKeys(t *testing.T) {
	meta := map[string]string{
		"convergence.state":           "terminated",
		"convergence.terminal_reason": "approved",
		"convergence.target":          "agent-a",
	}

	// All match.
	filter := map[string]string{
		"convergence.state":           "terminated",
		"convergence.terminal_reason": "approved",
	}
	if !MatchesDependencyFilter(meta, filter) {
		t.Error("expected multi-key filter to match")
	}

	// One key mismatches.
	filter2 := map[string]string{
		"convergence.state":           "terminated",
		"convergence.terminal_reason": "stopped",
	}
	if MatchesDependencyFilter(meta, filter2) {
		t.Error("expected multi-key filter to not match with one mismatch")
	}
}
