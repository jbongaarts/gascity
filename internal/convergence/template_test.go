package convergence

import (
	"path/filepath"
	"testing"
)

func TestArtifactDirFor(t *testing.T) {
	tests := []struct {
		name      string
		cityPath  string
		beadID    string
		iteration int
		want      string
	}{
		{
			name:      "normal case",
			cityPath:  "/home/user/city",
			beadID:    "abc123",
			iteration: 3,
			want:      filepath.Join("/home/user/city", ".gc", "artifacts", "abc123", "iter-3"),
		},
		{
			name:      "iteration 1",
			cityPath:  "/tmp/test-city",
			beadID:    "bead-001",
			iteration: 1,
			want:      filepath.Join("/tmp/test-city", ".gc", "artifacts", "bead-001", "iter-1"),
		},
		{
			name:      "high iteration",
			cityPath:  "/data/city",
			beadID:    "xyz",
			iteration: 100,
			want:      filepath.Join("/data/city", ".gc", "artifacts", "xyz", "iter-100"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ArtifactDirFor(tt.cityPath, tt.beadID, tt.iteration)
			if got != tt.want {
				t.Errorf("ArtifactDirFor(%q, %q, %d) = %q, want %q",
					tt.cityPath, tt.beadID, tt.iteration, got, tt.want)
			}
		})
	}
}

func TestNewTemplateContext_WithVars(t *testing.T) {
	meta := map[string]string{
		"convergence.state":   "active",
		"convergence.formula": "deploy",
		"var.repo":            "my-repo",
		"var.branch":          "main",
		"other.key":           "ignored",
	}

	ctx := NewTemplateContext("/home/user/city", "bead-1", "wisp-1", "deploy", 2, meta, "")

	if ctx.BeadID != "bead-1" {
		t.Errorf("BeadID = %q, want %q", ctx.BeadID, "bead-1")
	}
	if ctx.WispID != "wisp-1" {
		t.Errorf("WispID = %q, want %q", ctx.WispID, "wisp-1")
	}
	if ctx.Iteration != 2 {
		t.Errorf("Iteration = %d, want 2", ctx.Iteration)
	}
	wantDir := filepath.Join("/home/user/city", ".gc", "artifacts", "bead-1", "iter-2")
	if ctx.ArtifactDir != wantDir {
		t.Errorf("ArtifactDir = %q, want %q", ctx.ArtifactDir, wantDir)
	}
	if ctx.Formula != "deploy" {
		t.Errorf("Formula = %q, want %q", ctx.Formula, "deploy")
	}
	if ctx.RetrySource != "" {
		t.Errorf("RetrySource = %q, want empty", ctx.RetrySource)
	}
	if len(ctx.Var) != 2 {
		t.Errorf("Var count = %d, want 2", len(ctx.Var))
	}
	if ctx.Var["repo"] != "my-repo" {
		t.Errorf("Var[repo] = %q, want %q", ctx.Var["repo"], "my-repo")
	}
	if ctx.Var["branch"] != "main" {
		t.Errorf("Var[branch] = %q, want %q", ctx.Var["branch"], "main")
	}
}

func TestNewTemplateContext_WithoutVars(t *testing.T) {
	meta := map[string]string{
		"convergence.state": "active",
	}

	ctx := NewTemplateContext("/city", "b1", "w1", "check", 1, meta, "")

	if len(ctx.Var) != 0 {
		t.Errorf("Var count = %d, want 0", len(ctx.Var))
	}
}

func TestNewTemplateContext_WithRetrySource(t *testing.T) {
	ctx := NewTemplateContext("/city", "b1", "w1", "fix", 3, nil, "prev-bead-id")

	if ctx.RetrySource != "prev-bead-id" {
		t.Errorf("RetrySource = %q, want %q", ctx.RetrySource, "prev-bead-id")
	}
}

func TestExtractVars_MixedMetadata(t *testing.T) {
	meta := map[string]string{
		"convergence.state":   "active",
		"convergence.formula": "deploy",
		"var.repo":            "my-repo",
		"var.branch":          "main",
		"var.target":          "production",
		"other.key":           "value",
	}

	vars := ExtractVars(meta)

	if len(vars) != 3 {
		t.Errorf("var count = %d, want 3", len(vars))
	}
	if vars["repo"] != "my-repo" {
		t.Errorf("vars[repo] = %q, want %q", vars["repo"], "my-repo")
	}
	if vars["branch"] != "main" {
		t.Errorf("vars[branch] = %q, want %q", vars["branch"], "main")
	}
	if vars["target"] != "production" {
		t.Errorf("vars[target] = %q, want %q", vars["target"], "production")
	}

	// Non-var keys should not be present.
	if _, ok := vars["convergence.state"]; ok {
		t.Error("non-var key should not be in vars map")
	}
	if _, ok := vars["other.key"]; ok {
		t.Error("non-var key should not be in vars map")
	}
}

func TestExtractVars_EmptyMap(t *testing.T) {
	vars := ExtractVars(map[string]string{})
	if len(vars) != 0 {
		t.Errorf("var count = %d, want 0", len(vars))
	}
}

func TestExtractVars_NilMap(t *testing.T) {
	vars := ExtractVars(nil)
	if vars == nil {
		t.Error("ExtractVars should return non-nil map even for nil input")
	}
	if len(vars) != 0 {
		t.Errorf("var count = %d, want 0", len(vars))
	}
}

func TestExtractVars_NoVarKeys(t *testing.T) {
	meta := map[string]string{
		"convergence.state":   "active",
		"convergence.formula": "deploy",
	}
	vars := ExtractVars(meta)
	if len(vars) != 0 {
		t.Errorf("var count = %d, want 0", len(vars))
	}
}
