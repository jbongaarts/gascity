package convergence

import (
	"testing"
	"time"
)

func TestNormalizeVerdict(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Canonical values pass through.
		{"approve", VerdictApprove},
		{"approve-with-risks", VerdictApproveWithRisks},
		{"block", VerdictBlock},

		// Past-tense mappings.
		{"approved", VerdictApprove},
		{"blocked", VerdictBlock},
		{"approve-with-risk", VerdictApproveWithRisks},
		{"approved-with-risks", VerdictApproveWithRisks},
		{"approved-with-risk", VerdictApproveWithRisks},

		// Case insensitivity.
		{"APPROVE", VerdictApprove},
		{"Approved", VerdictApprove},
		{"BLOCK", VerdictBlock},
		{"Blocked", VerdictBlock},
		{"Approve-With-Risks", VerdictApproveWithRisks},
		{"APPROVED-WITH-RISKS", VerdictApproveWithRisks},

		// Whitespace trimming.
		{"  approve  ", VerdictApprove},
		{"\tapproved\n", VerdictApprove},
		{" block ", VerdictBlock},

		// Empty → block.
		{"", VerdictBlock},
		{"   ", VerdictBlock},

		// Unknown → block.
		{"maybe", VerdictBlock},
		{"yes", VerdictBlock},
		{"reject", VerdictBlock},
		{"123", VerdictBlock},
	}
	for _, tt := range tests {
		got := NormalizeVerdict(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeVerdict(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEncodeDecodeInt(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{42, "42"},
		{999999, "999999"},
	}
	for _, tt := range tests {
		s := EncodeInt(tt.n)
		if s != tt.want {
			t.Errorf("EncodeInt(%d) = %q, want %q", tt.n, s, tt.want)
		}
		got, ok := DecodeInt(s)
		if !ok {
			t.Errorf("DecodeInt(%q) returned ok=false", s)
		}
		if got != tt.n {
			t.Errorf("DecodeInt(%q) = %d, want %d", s, got, tt.n)
		}
	}
}

func TestDecodeIntEdgeCases(t *testing.T) {
	// Empty string.
	n, ok := DecodeInt("")
	if ok || n != 0 {
		t.Errorf("DecodeInt(\"\") = (%d, %v), want (0, false)", n, ok)
	}

	// Not a number.
	n, ok = DecodeInt("abc")
	if ok || n != 0 {
		t.Errorf("DecodeInt(\"abc\") = (%d, %v), want (0, false)", n, ok)
	}

	// Float (not an int).
	n, ok = DecodeInt("3.14")
	if ok || n != 0 {
		t.Errorf("DecodeInt(\"3.14\") = (%d, %v), want (0, false)", n, ok)
	}
}

func TestEncodeDecodeDuration(t *testing.T) {
	tests := []time.Duration{
		0,
		time.Second,
		5 * time.Minute,
		2*time.Hour + 30*time.Minute,
		100 * time.Millisecond,
	}
	for _, d := range tests {
		s := EncodeDuration(d)
		got, ok := DecodeDuration(s)
		if !ok {
			t.Errorf("DecodeDuration(%q) returned ok=false", s)
		}
		if got != d {
			t.Errorf("DecodeDuration(%q) = %v, want %v", s, got, d)
		}
	}
}

func TestDecodeDurationEdgeCases(t *testing.T) {
	// Empty string.
	d, ok := DecodeDuration("")
	if ok || d != 0 {
		t.Errorf("DecodeDuration(\"\") = (%v, %v), want (0, false)", d, ok)
	}

	// Invalid format.
	d, ok = DecodeDuration("not-a-duration")
	if ok || d != 0 {
		t.Errorf("DecodeDuration(\"not-a-duration\") = (%v, %v), want (0, false)", d, ok)
	}
}

func TestMetadataPresent(t *testing.T) {
	meta := map[string]string{
		"convergence.state": "active",
		"empty_key":         "",
	}

	// Key with value.
	v, ok := MetadataPresent(meta, "convergence.state")
	if !ok {
		t.Error("expected present for convergence.state")
	}
	if v != "active" {
		t.Errorf("value = %q, want %q", v, "active")
	}

	// Key with empty value — present.
	v, ok = MetadataPresent(meta, "empty_key")
	if !ok {
		t.Error("expected present for empty_key")
	}
	if v != "" {
		t.Errorf("value = %q, want empty", v)
	}

	// Absent key.
	v, ok = MetadataPresent(meta, "nonexistent")
	if ok {
		t.Error("expected absent for nonexistent key")
	}
	if v != "" {
		t.Errorf("value = %q, want empty for absent key", v)
	}

	// Nil map.
	v, ok = MetadataPresent(nil, "any")
	if ok {
		t.Error("expected absent for nil map")
	}
	if v != "" {
		t.Errorf("value = %q, want empty for nil map", v)
	}
}
