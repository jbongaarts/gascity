package convergence

import "context"

// EvaluateHybrid determines the gate result for hybrid mode.
// If no condition is configured, returns a result indicating manual fallback.
// Otherwise runs the condition with the agent verdict in the environment.
func EvaluateHybrid(ctx context.Context, cfg GateConfig, env ConditionEnv, verdict string) GateResult {
	if HybridNeedsManual(cfg) {
		return GateManualResult()
	}

	// Set the agent verdict in the environment for the condition script.
	env.AgentVerdict = verdict

	retryBudget := 0
	if cfg.TimeoutAction == TimeoutActionRetry {
		retryBudget = MaxGateRetries
	}

	return RunCondition(ctx, cfg.Condition, env, cfg.Timeout, retryBudget)
}

// HybridNeedsManual returns true when hybrid mode should fall back to
// waiting_manual. This happens when no condition script is configured.
func HybridNeedsManual(cfg GateConfig) bool {
	return cfg.Condition == ""
}
