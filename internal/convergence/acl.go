package convergence

import "strings"

// ProtectedPrefix is the metadata key prefix requiring a controller token.
const ProtectedPrefix = "convergence."

// agentWritableKeys is the set of convergence.* keys that agents can
// write without a controller token.
var agentWritableKeys = map[string]bool{
	FieldAgentVerdict:     true,
	FieldAgentVerdictWisp: true,
}

// RequiresToken reports whether writing to the given metadata key
// requires a controller token. Returns true for convergence.* keys
// except the agent-writable verdict keys, and for var.* keys.
func RequiresToken(key string) bool {
	if strings.HasPrefix(key, VarPrefix) {
		return true
	}
	if !strings.HasPrefix(key, ProtectedPrefix) {
		return false
	}
	return !agentWritableKeys[key]
}

// ScrubTokenEnv returns a copy of the environment map with the
// controller token variable removed. Used when spawning agent sessions.
func ScrubTokenEnv(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if k != TokenEnvVar {
			out[k] = v
		}
	}
	return out
}
