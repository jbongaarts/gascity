package convergence

import (
	"fmt"
	"path/filepath"
	"strings"
)

// TemplateContext holds variables available to convergence formula step
// prompts.
type TemplateContext struct {
	BeadID      string            // Root convergence bead ID
	WispID      string            // Current wisp ID
	Iteration   int               // 1-based pass number
	ArtifactDir string            // .gc/artifacts/<bead-id>/iter-<N>/
	Formula     string            // Formula name
	RetrySource string            // Source bead ID if retry (empty otherwise)
	Var         map[string]string // Template variables from var.* metadata
}

// ArtifactDirFor returns the artifact directory path for a given bead and
// iteration. Format: <cityPath>/.gc/artifacts/<beadID>/iter-<iteration>/
func ArtifactDirFor(cityPath, beadID string, iteration int) string {
	return filepath.Join(cityPath, ".gc", "artifacts", beadID, fmt.Sprintf("iter-%d", iteration))
}

// NewTemplateContext builds a TemplateContext from convergence metadata
// on the root bead. beadMeta is the full metadata map from the root bead.
func NewTemplateContext(cityPath string, beadID, wispID, formulaName string, iteration int, beadMeta map[string]string, retrySource string) TemplateContext {
	return TemplateContext{
		BeadID:      beadID,
		WispID:      wispID,
		Iteration:   iteration,
		ArtifactDir: ArtifactDirFor(cityPath, beadID, iteration),
		Formula:     formulaName,
		RetrySource: retrySource,
		Var:         ExtractVars(beadMeta),
	}
}

// ExtractVars extracts var.* entries from a metadata map, stripping the
// "var." prefix. Returns a map of key -> value for template use.
func ExtractVars(meta map[string]string) map[string]string {
	vars := make(map[string]string)
	for k, v := range meta {
		if strings.HasPrefix(k, VarPrefix) {
			vars[strings.TrimPrefix(k, VarPrefix)] = v
		}
	}
	return vars
}
