package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/shellquote"
)

func TestShellJoinArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"empty slice", nil, ""},
		{"single arg no metachar", []string{"--model"}, "--model"},
		{"two clean args", []string{"--model", "opus"}, "--model opus"},
		{"arg with space", []string{"hello world"}, "'hello world'"},
		{"arg with single quote", []string{"it's"}, "'it'\\''s'"},
		{"empty string arg", []string{""}, "''"},
		{"mixed clean and dirty", []string{"--flag", "value with space", "--other"}, "--flag 'value with space' --other"},
		{"arg with special chars", []string{"$(whoami)"}, "'$(whoami)'"},
		{"arg with semicolon", []string{"foo;bar"}, "'foo;bar'"},
		{"multiple special", []string{"a b", "c'd", "e|f"}, "'a b' 'c'\\''d' 'e|f'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellquote.Join(tt.args)
			if got != tt.want {
				t.Errorf("shellquote.Join(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestBuildSessionResumeUsesResolvedProviderCommand(t *testing.T) {
	fs := newSessionFakeState(t)
	fs.cfg = &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{
			{Name: "mayor", Provider: "wrapped"},
		},
		Providers: map[string]config.ProviderSpec{
			"wrapped": {
				DisplayName:       "Wrapped Gemini",
				Command:           "aimux",
				Args:              []string{"run", "gemini", "--", "--approval-mode", "yolo"},
				PathCheck:         "true", // use /usr/bin/true so LookPath succeeds in CI
				ReadyPromptPrefix: "> ",
				Env: map[string]string{
					"GC_HOME": "/tmp/gc-accept-home",
				},
			},
		},
	}

	srv := New(fs)
	info := session.Info{
		ID:       "gc-1",
		Template: "mayor",
		Command:  "gemini --approval-mode yolo",
		Provider: "wrapped",
		WorkDir:  "/tmp/workdir",
	}

	cmd, hints := srv.buildSessionResume(info)
	if got, want := cmd, "aimux run gemini -- --approval-mode yolo"; got != want {
		t.Fatalf("resume command = %q, want %q", got, want)
	}
	if got, want := hints.WorkDir, "/tmp/workdir"; got != want {
		t.Fatalf("hints.WorkDir = %q, want %q", got, want)
	}
	if got, want := hints.ReadyPromptPrefix, "> "; got != want {
		t.Fatalf("hints.ReadyPromptPrefix = %q, want %q", got, want)
	}
	if got, want := hints.Env["GC_HOME"], "/tmp/gc-accept-home"; got != want {
		t.Fatalf("hints.Env[GC_HOME] = %q, want %q", got, want)
	}
}

func TestBuildSessionResumePreservesStoredResolvedCommand(t *testing.T) {
	fs := newSessionFakeState(t)
	fs.cfg = &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{
			{Name: "mayor", Provider: "wrapped"},
		},
		Providers: map[string]config.ProviderSpec{
			"wrapped": {
				DisplayName: "Wrapped Claude",
				Command:     "claude",
				PathCheck:   "true",
			},
		},
	}

	srv := New(fs)
	info := session.Info{
		ID:       "gc-1",
		Template: "mayor",
		Command:  "claude --dangerously-skip-permissions --settings /tmp/settings.json",
		Provider: "wrapped",
		WorkDir:  "/tmp/workdir",
	}

	cmd, _ := srv.buildSessionResume(info)
	if got, want := cmd, "claude --dangerously-skip-permissions --settings /tmp/settings.json"; got != want {
		t.Fatalf("resume command = %q, want %q", got, want)
	}
}

// TestBuildSessionResumeRebuildsBareStoredCommandForPoolClaudeAgent is a
// regression test for gastownhall/gascity#799: when a pool-agent session
// resumed through the control-dispatcher path has only the bare
// provider binary ("claude") as its stored command, the API must
// re-inject schema defaults (--dangerously-skip-permissions) and the
// provider-owned --settings path from the current resolved config.
// Before the fix, the bare stored command was preserved as-is and pool
// workers wedged on interactive permission prompts on resume.
func TestBuildSessionResumeRebuildsBareStoredCommandForPoolClaudeAgent(t *testing.T) {
	fs := newSessionFakeState(t)
	claude := config.BuiltinProviders()["claude"]
	maxActive := 3
	gcDir := filepath.Join(fs.cityPath, ".gc")
	if err := os.MkdirAll(gcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gcDir, "settings.json"), []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fs.cfg = &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{
			{
				Name:              "perspective_planner",
				Provider:          "claude",
				MaxActiveSessions: &maxActive,
			},
		},
		Providers: map[string]config.ProviderSpec{
			"claude": claude,
		},
	}

	srv := New(fs)
	info := session.Info{
		ID:         "gc-1",
		Template:   "perspective_planner",
		Command:    "claude",
		Provider:   "claude",
		WorkDir:    fs.cityPath,
		SessionKey: "abc-123",
		ResumeFlag: "--resume",
	}

	cmd, _ := srv.buildSessionResume(info)
	if !strings.Contains(cmd, "--dangerously-skip-permissions") {
		t.Fatalf("resume command missing default args:\n  got: %s", cmd)
	}
	if !strings.Contains(cmd, "--resume abc-123") {
		t.Fatalf("resume command missing resume flag:\n  got: %s", cmd)
	}
	if !strings.Contains(cmd, "--settings") {
		t.Fatalf("resume command missing settings arg:\n  got: %s", cmd)
	}
}
