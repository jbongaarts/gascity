package convergence

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveEvaluateStep_DefaultPath(t *testing.T) {
	f := Formula{Name: "test"}
	step, err := ResolveEvaluateStep("/home/user/city", f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if step.Name != EvaluateStepName {
		t.Errorf("Name = %q, want %q", step.Name, EvaluateStepName)
	}
	want := filepath.Join("/home/user/city", DefaultEvaluatePromptPath)
	if step.PromptPath != want {
		t.Errorf("PromptPath = %q, want %q", step.PromptPath, want)
	}
}

func TestResolveEvaluateStep_CustomPath(t *testing.T) {
	f := Formula{
		Name:           "test",
		EvaluatePrompt: "custom/my-evaluate.md",
	}
	step, err := ResolveEvaluateStep("/home/user/city", f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if step.Name != EvaluateStepName {
		t.Errorf("Name = %q, want %q", step.Name, EvaluateStepName)
	}
	want := filepath.Join("/home/user/city", "custom/my-evaluate.md")
	if step.PromptPath != want {
		t.Errorf("PromptPath = %q, want %q", step.PromptPath, want)
	}
}

func TestResolveEvaluateStep_PathTraversal(t *testing.T) {
	f := Formula{
		Name:           "test",
		EvaluatePrompt: "../../etc/passwd",
	}
	_, err := ResolveEvaluateStep("/home/user/city", f)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("error should mention path escaping, got: %v", err)
	}
}

func TestValidateEvaluatePrompt_Valid(t *testing.T) {
	content := []byte("Run bd meta set to record convergence.agent_verdict with the result.")
	if err := ValidateEvaluatePrompt(content); err != nil {
		t.Errorf("expected no error for valid content, got: %v", err)
	}
}

func TestValidateEvaluatePrompt_MissingBdMetaSet(t *testing.T) {
	content := []byte("Record convergence.agent_verdict for this evaluation.")
	err := ValidateEvaluatePrompt(content)
	if err == nil {
		t.Fatal("expected error for missing 'bd meta set'")
	}
	if !strings.Contains(err.Error(), "bd meta set") {
		t.Errorf("error should mention missing 'bd meta set', got: %v", err)
	}
}

func TestValidateEvaluatePrompt_MissingAgentVerdict(t *testing.T) {
	content := []byte("Use bd meta set to store the evaluation outcome.")
	err := ValidateEvaluatePrompt(content)
	if err == nil {
		t.Fatal("expected error for missing 'convergence.agent_verdict'")
	}
	if !strings.Contains(err.Error(), "convergence.agent_verdict") {
		t.Errorf("error should mention missing 'convergence.agent_verdict', got: %v", err)
	}
}

func TestValidateEvaluatePrompt_MissingBoth(t *testing.T) {
	content := []byte("This prompt has neither required substring.")
	err := ValidateEvaluatePrompt(content)
	if err == nil {
		t.Fatal("expected error for missing both substrings")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "bd meta set") {
		t.Errorf("error should mention missing 'bd meta set', got: %v", err)
	}
	if !strings.Contains(errStr, "convergence.agent_verdict") {
		t.Errorf("error should mention missing 'convergence.agent_verdict', got: %v", err)
	}
}

func TestValidateEvaluatePrompt_EmptyContent(t *testing.T) {
	err := ValidateEvaluatePrompt([]byte{})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "bd meta set") {
		t.Errorf("error should mention missing 'bd meta set', got: %v", err)
	}
	if !strings.Contains(errStr, "convergence.agent_verdict") {
		t.Errorf("error should mention missing 'convergence.agent_verdict', got: %v", err)
	}
}
