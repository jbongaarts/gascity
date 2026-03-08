package convergence

import (
	"fmt"
	"strings"
	"unicode"
)

// Formula represents the convergence-relevant subset of a formula definition.
type Formula struct {
	Name           string
	Convergence    bool     // must be true for convergence use
	RequiredVars   []string // var.* keys required at creation
	EvaluatePrompt string   // optional custom evaluate prompt path
	StepNames      []string // names of all declared steps
}

// ValidateForConvergence checks that a formula is valid for convergence use.
// Returns an error describing all validation failures.
// Checks:
//  1. Convergence flag must be true
//  2. No step named "evaluate" (reserved for controller injection)
//  3. If EvaluatePrompt is set, the file must contain both "bd meta set" and
//     "convergence.agent_verdict" as literal substrings
func ValidateForConvergence(f Formula, cityPath string, readFile func(string) ([]byte, error)) error {
	var errs []string

	if !f.Convergence {
		errs = append(errs, fmt.Sprintf("formula %q: convergence flag must be true", f.Name))
	}

	for _, name := range f.StepNames {
		if name == EvaluateStepName {
			errs = append(errs, fmt.Sprintf("formula %q: step name %q is reserved for controller injection", f.Name, name))
		}
	}

	// Validate the evaluate prompt (custom or default). The default
	// prompt lives in the user's workspace and can be edited, so it
	// must be validated too. Skip only when no city path or readFile
	// is provided (unit tests without a city context).
	if cityPath != "" && readFile != nil {
		resolved, resolveErr := ResolveEvaluateStep(cityPath, f)
		if resolveErr != nil {
			errs = append(errs, fmt.Sprintf("formula %q: %v", f.Name, resolveErr))
		} else {
			data, err := readFile(resolved.PromptPath)
			if err != nil {
				errs = append(errs, fmt.Sprintf("formula %q: reading evaluate prompt %q: %v", f.Name, resolved.PromptPath, err))
			} else if vErr := ValidateEvaluatePrompt(data); vErr != nil {
				errs = append(errs, fmt.Sprintf("formula %q: evaluate prompt %q: %v", f.Name, resolved.PromptPath, vErr))
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("convergence validation failed:\n  - %s", strings.Join(errs, "\n  - "))
}

// ValidateRequiredVars checks that all required_vars are present in the
// provided vars map. Var keys must be valid Go identifiers (letters, digits,
// underscores).
func ValidateRequiredVars(required []string, vars map[string]string) error {
	var errs []string

	for _, key := range required {
		if !ValidateVarKey(key) {
			errs = append(errs, fmt.Sprintf("invalid var key %q: must be a valid identifier (letters, digits, underscores)", key))
			continue
		}
		if _, ok := vars[key]; !ok {
			errs = append(errs, fmt.Sprintf("missing required var %q", key))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("required vars validation failed:\n  - %s", strings.Join(errs, "\n  - "))
}

// ValidateVarKey checks that a var key is a valid Go identifier:
// non-empty, starts with a letter or underscore, and contains only
// letters, digits, and underscores.
func ValidateVarKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}
