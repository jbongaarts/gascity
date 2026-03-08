package convergence

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateForConvergence_ConvergenceFalse(t *testing.T) {
	f := Formula{
		Name:        "test-formula",
		Convergence: false,
		StepNames:   []string{"build", "test"},
	}
	err := ValidateForConvergence(f, "", nil)
	if err == nil {
		t.Fatal("expected error for convergence=false")
	}
	if !strings.Contains(err.Error(), "convergence flag must be true") {
		t.Errorf("error should mention convergence flag, got: %v", err)
	}
}

func TestValidateForConvergence_ReservedStepName(t *testing.T) {
	f := Formula{
		Name:        "test-formula",
		Convergence: true,
		StepNames:   []string{"build", "evaluate", "test"},
	}
	err := ValidateForConvergence(f, "", nil)
	if err == nil {
		t.Fatal("expected error for reserved step name")
	}
	if !strings.Contains(err.Error(), "reserved for controller injection") {
		t.Errorf("error should mention reserved step, got: %v", err)
	}
}

func TestValidateForConvergence_Valid(t *testing.T) {
	f := Formula{
		Name:        "deploy",
		Convergence: true,
		StepNames:   []string{"build", "test", "ship"},
	}
	if err := ValidateForConvergence(f, "", nil); err != nil {
		t.Errorf("expected no error for valid formula, got: %v", err)
	}
}

func TestValidateForConvergence_CustomEvaluatePromptValid(t *testing.T) {
	content := []byte("Use bd meta set to record convergence.agent_verdict for this step.")
	readFile := func(path string) ([]byte, error) {
		if path == "/city/custom/evaluate.md" {
			return content, nil
		}
		return nil, fmt.Errorf("not found: %s", path)
	}

	f := Formula{
		Name:           "deploy",
		Convergence:    true,
		StepNames:      []string{"build"},
		EvaluatePrompt: "custom/evaluate.md",
	}
	if err := ValidateForConvergence(f, "/city", readFile); err != nil {
		t.Errorf("expected no error for valid custom prompt, got: %v", err)
	}
}

func TestValidateForConvergence_CustomEvaluatePromptMissingSubstrings(t *testing.T) {
	readFile := func(_ string) ([]byte, error) {
		return []byte("This prompt has no required substrings."), nil
	}

	f := Formula{
		Name:           "deploy",
		Convergence:    true,
		StepNames:      []string{"build"},
		EvaluatePrompt: "custom/evaluate.md",
	}
	err := ValidateForConvergence(f, "/city", readFile)
	if err == nil {
		t.Fatal("expected error for missing substrings in custom prompt")
	}
	if !strings.Contains(err.Error(), "bd meta set") {
		t.Errorf("error should mention missing 'bd meta set', got: %v", err)
	}
	if !strings.Contains(err.Error(), "convergence.agent_verdict") {
		t.Errorf("error should mention missing 'convergence.agent_verdict', got: %v", err)
	}
}

func TestValidateForConvergence_CustomEvaluatePromptInvalid(t *testing.T) {
	// Only has one of the two required substrings.
	readFile := func(_ string) ([]byte, error) {
		return []byte("Use bd meta set to record your findings."), nil
	}

	f := Formula{
		Name:           "deploy",
		Convergence:    true,
		StepNames:      []string{"build"},
		EvaluatePrompt: "custom/evaluate.md",
	}
	err := ValidateForConvergence(f, "/city", readFile)
	if err == nil {
		t.Fatal("expected error for incomplete custom prompt")
	}
	if !strings.Contains(err.Error(), "convergence.agent_verdict") {
		t.Errorf("error should mention missing 'convergence.agent_verdict', got: %v", err)
	}
}

func TestValidateForConvergence_CustomEvaluatePromptReadError(t *testing.T) {
	readFile := func(_ string) ([]byte, error) {
		return nil, fmt.Errorf("file not found")
	}

	f := Formula{
		Name:           "deploy",
		Convergence:    true,
		StepNames:      []string{"build"},
		EvaluatePrompt: "missing/evaluate.md",
	}
	err := ValidateForConvergence(f, "/city", readFile)
	if err == nil {
		t.Fatal("expected error when prompt file cannot be read")
	}
	if !strings.Contains(err.Error(), "reading evaluate prompt") {
		t.Errorf("error should mention reading failure, got: %v", err)
	}
}

func TestValidateForConvergence_MultipleErrors(t *testing.T) {
	f := Formula{
		Name:        "broken",
		Convergence: false,
		StepNames:   []string{"evaluate", "build"},
	}
	err := ValidateForConvergence(f, "", nil)
	if err == nil {
		t.Fatal("expected error for multiple violations")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "convergence flag must be true") {
		t.Errorf("error should mention convergence flag, got: %v", err)
	}
	if !strings.Contains(errStr, "reserved for controller injection") {
		t.Errorf("error should mention reserved step, got: %v", err)
	}
}

func TestValidateRequiredVars_AllPresent(t *testing.T) {
	required := []string{"repo", "branch"}
	vars := map[string]string{
		"repo":   "my-repo",
		"branch": "main",
		"extra":  "value",
	}
	if err := ValidateRequiredVars(required, vars); err != nil {
		t.Errorf("expected no error when all vars present, got: %v", err)
	}
}

func TestValidateRequiredVars_Missing(t *testing.T) {
	required := []string{"repo", "branch", "target"}
	vars := map[string]string{
		"repo": "my-repo",
	}
	err := ValidateRequiredVars(required, vars)
	if err == nil {
		t.Fatal("expected error for missing vars")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "branch") {
		t.Errorf("error should mention missing 'branch', got: %v", err)
	}
	if !strings.Contains(errStr, "target") {
		t.Errorf("error should mention missing 'target', got: %v", err)
	}
}

func TestValidateRequiredVars_EmptyMap(t *testing.T) {
	required := []string{"repo"}
	err := ValidateRequiredVars(required, map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing vars in empty map")
	}
	if !strings.Contains(err.Error(), "repo") {
		t.Errorf("error should mention missing 'repo', got: %v", err)
	}
}

func TestValidateRequiredVars_NilMap(t *testing.T) {
	required := []string{"repo"}
	err := ValidateRequiredVars(required, nil)
	if err == nil {
		t.Fatal("expected error for missing vars in nil map")
	}
}

func TestValidateRequiredVars_InvalidKeyNames(t *testing.T) {
	required := []string{"valid_key", "invalid.key"}
	vars := map[string]string{
		"valid_key":   "ok",
		"invalid.key": "nope",
	}
	err := ValidateRequiredVars(required, vars)
	if err == nil {
		t.Fatal("expected error for invalid key name")
	}
	if !strings.Contains(err.Error(), "invalid.key") {
		t.Errorf("error should mention the invalid key, got: %v", err)
	}
}

func TestValidateRequiredVars_NoRequired(t *testing.T) {
	if err := ValidateRequiredVars(nil, map[string]string{"a": "b"}); err != nil {
		t.Errorf("expected no error for empty required list, got: %v", err)
	}
}

func TestValidateVarKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		// Valid identifiers.
		{"repo", true},
		{"branch_name", true},
		{"_private", true},
		{"x", true},
		{"abc123", true},
		{"A", true},
		{"camelCase", true},
		{"_", true},
		{"__double", true},
		{"a1b2c3", true},

		// Invalid identifiers.
		{"", false},
		{"invalid.key", false},
		{"has space", false},
		{"with-hyphen", false},
		{"123start", false},
		{"a/b", false},
		{"a=b", false},
		{"hello world", false},
		{" leading", false},
	}
	for _, tt := range tests {
		got := ValidateVarKey(tt.key)
		if got != tt.want {
			t.Errorf("ValidateVarKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}
