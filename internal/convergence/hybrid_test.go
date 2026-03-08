package convergence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvaluateHybridWithCondition(t *testing.T) {
	dir := t.TempDir()

	// Create a script that passes only if GC_AGENT_VERDICT=approve.
	script := filepath.Join(dir, "hybrid.sh")
	content := `#!/bin/sh
if [ "$GC_AGENT_VERDICT" = "approve" ]; then
  echo "approved"
  exit 0
else
  echo "rejected: $GC_AGENT_VERDICT" >&2
  exit 1
fi
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	env := ConditionEnv{
		BeadID:      "bh1",
		CityPath:    dir,
		WispID:      "wh1",
		ArtifactDir: dir,
	}

	cfg := GateConfig{
		Mode:          GateModeHybrid,
		Condition:     script,
		Timeout:       5 * time.Second,
		TimeoutAction: TimeoutActionIterate,
	}

	t.Run("approve and pass", func(t *testing.T) {
		result := EvaluateHybrid(context.Background(), cfg, env, VerdictApprove)
		if result.Outcome != GatePass {
			t.Errorf("Outcome = %q, want %q; stderr: %s", result.Outcome, GatePass, result.Stderr)
		}
		if !strings.Contains(result.Stdout, "approved") {
			t.Errorf("Stdout = %q, want to contain 'approved'", result.Stdout)
		}
	})

	t.Run("approve and fail (script rejects)", func(t *testing.T) {
		// Script that always fails regardless of verdict.
		failScript := filepath.Join(dir, "always_fail.sh")
		if err := os.WriteFile(failScript, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		failCfg := cfg
		failCfg.Condition = failScript

		result := EvaluateHybrid(context.Background(), failCfg, env, VerdictApprove)
		if result.Outcome != GateFail {
			t.Errorf("Outcome = %q, want %q", result.Outcome, GateFail)
		}
	})

	t.Run("block and pass (script approves)", func(t *testing.T) {
		// Script that always passes regardless of verdict.
		passScript := filepath.Join(dir, "always_pass.sh")
		if err := os.WriteFile(passScript, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		passCfg := cfg
		passCfg.Condition = passScript

		result := EvaluateHybrid(context.Background(), passCfg, env, VerdictBlock)
		if result.Outcome != GatePass {
			t.Errorf("Outcome = %q, want %q", result.Outcome, GatePass)
		}
	})

	t.Run("block and fail", func(t *testing.T) {
		result := EvaluateHybrid(context.Background(), cfg, env, VerdictBlock)
		if result.Outcome != GateFail {
			t.Errorf("Outcome = %q, want %q", result.Outcome, GateFail)
		}
	})

	t.Run("empty verdict", func(t *testing.T) {
		result := EvaluateHybrid(context.Background(), cfg, env, "")
		if result.Outcome != GateFail {
			t.Errorf("Outcome = %q, want %q (empty verdict should fail)", result.Outcome, GateFail)
		}
	})
}

func TestEvaluateHybridWithoutCondition(t *testing.T) {
	cfg := GateConfig{
		Mode:          GateModeHybrid,
		Condition:     "", // no condition
		Timeout:       5 * time.Second,
		TimeoutAction: TimeoutActionIterate,
	}

	env := ConditionEnv{
		BeadID:      "bh2",
		CityPath:    t.TempDir(),
		WispID:      "wh2",
		ArtifactDir: t.TempDir(),
	}

	result := EvaluateHybrid(context.Background(), cfg, env, VerdictApprove)
	if result.Outcome != GatePass {
		t.Errorf("Outcome = %q, want %q for manual fallback", result.Outcome, GatePass)
	}
	// Should not have executed anything.
	if result.ExitCode != nil {
		t.Errorf("ExitCode = %v, want nil for manual fallback", result.ExitCode)
	}
	if result.Duration != 0 {
		t.Errorf("Duration = %v, want 0 for manual fallback", result.Duration)
	}
}

func TestHybridNeedsManual(t *testing.T) {
	tests := []struct {
		name string
		cfg  GateConfig
		want bool
	}{
		{
			name: "with condition",
			cfg:  GateConfig{Mode: GateModeHybrid, Condition: "/path/to/gate.sh"},
			want: false,
		},
		{
			name: "without condition",
			cfg:  GateConfig{Mode: GateModeHybrid, Condition: ""},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HybridNeedsManual(tt.cfg)
			if got != tt.want {
				t.Errorf("HybridNeedsManual() = %v, want %v", got, tt.want)
			}
		})
	}
}
