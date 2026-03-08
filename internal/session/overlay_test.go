package session

import (
	"strings"
	"testing"
)

func TestOverlay_AllowedKeys(t *testing.T) {
	err := ValidateOverlay(
		map[string]string{"model": "claude-sonnet-4-6", "title": "My Session"},
		[]string{"model", "title"},
		nil,
	)
	if err != nil {
		t.Errorf("expected no error for allowed keys, got: %v", err)
	}
}

func TestOverlay_BannedKeys(t *testing.T) {
	for _, key := range []string{
		"command", "provider", "session_key", "state",
		"generation", "instance_token", "resume_flag", "resume_style",
	} {
		err := ValidateOverlay(
			map[string]string{key: "value"},
			[]string{key}, // even if explicitly allowed
			nil,
		)
		if err == nil {
			t.Errorf("expected error for banned key %q", key)
		}
		if err != nil && !strings.Contains(err.Error(), "banned") {
			t.Errorf("expected 'banned' in error for %q, got: %v", key, err)
		}
	}
}

func TestOverlay_EnvAllowlist(t *testing.T) {
	// Allowed env key.
	err := ValidateOverlay(
		map[string]string{"env.MY_VAR": "value"},
		nil,
		[]string{"MY_VAR"},
	)
	if err != nil {
		t.Errorf("expected no error for allowed env key, got: %v", err)
	}

	// Disallowed env key.
	err = ValidateOverlay(
		map[string]string{"env.SECRET": "value"},
		nil,
		[]string{"MY_VAR"}, // SECRET not in list
	)
	if err == nil {
		t.Error("expected error for disallowed env key")
	}
}

func TestOverlay_EnvKeyRegex(t *testing.T) {
	tests := []struct {
		key     string
		wantErr bool
	}{
		{"MY_VAR", false},
		{"A", false},
		{"ABC123_DEF", false},
		{"lowercase", true}, // must start with uppercase
		{"123START", true},  // must start with letter
		{"_LEADING", true},  // must start with letter
		{"HAS SPACE", true}, // no spaces
		{"HAS-DASH", true},  // no dashes
	}

	for _, tt := range tests {
		err := ValidateOverlay(
			map[string]string{"env." + tt.key: "val"},
			nil,
			[]string{tt.key},
		)
		if tt.wantErr && err == nil {
			t.Errorf("env key %q: expected error", tt.key)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("env key %q: unexpected error: %v", tt.key, err)
		}
	}
}

func TestOverlay_PromptCap(t *testing.T) {
	// Prompt within limit.
	err := ValidateOverlay(
		map[string]string{"prompt": "short prompt"},
		[]string{"prompt"},
		nil,
	)
	if err != nil {
		t.Errorf("expected no error for short prompt, got: %v", err)
	}

	// Prompt exceeding limit.
	bigPrompt := strings.Repeat("x", maxPromptOverlayBytes+1)
	err = ValidateOverlay(
		map[string]string{"prompt": bigPrompt},
		[]string{"prompt"},
		nil,
	)
	if err == nil {
		t.Error("expected error for oversized prompt")
	}
}

func TestOverlay_PromptRequiresOptIn(t *testing.T) {
	err := ValidateOverlay(
		map[string]string{"prompt": "my prompt"},
		nil, // no allow_overlay
		nil,
	)
	if err == nil {
		t.Error("expected error for prompt without opt-in")
	}
	if err != nil && !strings.Contains(err.Error(), "opt-in") {
		t.Errorf("expected 'opt-in' in error, got: %v", err)
	}
}

func TestOverlay_EmptyOverrides(t *testing.T) {
	err := ValidateOverlay(nil, nil, nil)
	if err != nil {
		t.Errorf("expected no error for empty overrides, got: %v", err)
	}
}

func TestOverlay_UnknownKeyRejected(t *testing.T) {
	err := ValidateOverlay(
		map[string]string{"unknown_field": "value"},
		[]string{"model"}, // unknown_field not in list
		nil,
	)
	if err == nil {
		t.Error("expected error for unknown key not in allow_overlay")
	}
}
