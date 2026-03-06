package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Activity color constants (inlined from upstream internal/activity).
const (
	colorGreen   = "green"
	colorYellow  = "yellow"
	colorRed     = "red"
	colorUnknown = "unknown"
)

// Activity thresholds for color coding.
const (
	thresholdActive = 5 * time.Minute  // Green threshold
	thresholdStale  = 10 * time.Minute // Yellow threshold (beyond this is red)
)

// Default GUPP violation timeout (30 min, same as upstream).
const defaultGUPPViolationTimeout = 30 * time.Minute

// calculateActivity computes activity info from a last-activity timestamp.
func calculateActivity(lastActivity time.Time) ActivityInfo {
	if lastActivity.IsZero() {
		return ActivityInfo{
			Display:    "unknown",
			ColorClass: colorUnknown,
		}
	}

	d := time.Since(lastActivity)
	if d < 0 {
		d = 0
	}

	return ActivityInfo{
		Display:    formatAge(d),
		ColorClass: colorForDuration(d),
	}
}

// formatAge formats a duration as a short human-readable string.
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// colorForDuration returns the color class for a given duration.
func colorForDuration(d time.Duration) string {
	switch {
	case d < thresholdActive:
		return colorGreen
	case d < thresholdStale:
		return colorYellow
	default:
		return colorRed
	}
}

// extractIssueID unwraps "external:prefix:id" to just "id".
func extractIssueID(id string) string {
	if strings.HasPrefix(id, "external:") {
		parts := strings.SplitN(id, ":", 3)
		if len(parts) == 3 {
			return parts[2]
		}
	}
	return id
}

// formatTimestamp formats a time as "Jan 26, 3:45 PM" (or "Jan 26 2006, 3:45 PM" if different year).
func formatTimestamp(t time.Time) string {
	now := time.Now()
	if t.Year() != now.Year() {
		return t.Format("Jan 2 2006, 3:04 PM")
	}
	return t.Format("Jan 2, 3:04 PM")
}

// formatAgentAddress shortens agent addresses for display.
// "rig/polecats/Toast" -> "Toast (rig)"
// "mayor/" -> "Mayor"
func formatAgentAddress(addr string) string {
	if addr == "" {
		return "\u2014" // em-dash
	}
	if addr == "mayor/" || addr == "mayor" {
		return "Mayor"
	}

	parts := strings.Split(addr, "/")
	if len(parts) >= 3 && parts[1] == "polecats" {
		return fmt.Sprintf("%s (%s)", parts[2], parts[0])
	}
	if len(parts) >= 3 && parts[1] == "crew" {
		return fmt.Sprintf("%s (%s/crew)", parts[2], parts[0])
	}
	if len(parts) >= 2 {
		return fmt.Sprintf("%s/%s", parts[0], parts[len(parts)-1])
	}
	return addr
}

// calculateWorkStatus determines the work status based on progress and activity.
// Returns: "complete", "active", "stale", "stuck", or "waiting"
func calculateWorkStatus(completed, total int, activityColor string) string {
	if total > 0 && completed == total {
		return "complete"
	}

	switch activityColor {
	case colorGreen:
		return "active"
	case colorYellow:
		return "stale"
	case colorRed:
		return "stuck"
	default:
		return "waiting"
	}
}

// calculateWorkerWorkStatus determines the worker's work status based on activity and assignment.
func calculateWorkerWorkStatus(activityAge time.Duration, issueID, workerName string, staleThreshold, stuckThreshold time.Duration) string {
	if workerName == "refinery" {
		return "working"
	}

	if issueID == "" {
		return "idle"
	}

	switch {
	case activityAge < staleThreshold:
		return "working"
	case activityAge < stuckThreshold:
		return "stale"
	default:
		return "stuck"
	}
}

// eventCategory classifies an event type for filtering/display.
func eventCategory(eventType string) string {
	switch eventType {
	case "spawn", "kill", "session_start", "session_end", "session_death", "mass_death", "nudge", "handoff":
		return "agent"
	case "sling", "hook", "unhook", "done", "merge_started", "merged", "merge_failed":
		return "work"
	case "mail", "escalation_sent", "escalation_acked", "escalation_closed":
		return "comms"
	case "boot", "halt", "patrol_started", "patrol_complete":
		return "system"
	default:
		return "system"
	}
}

// extractRig extracts the rig name from an actor address like "myrig/polecats/nux".
func extractRig(actor string) string {
	if actor == "" {
		return ""
	}
	parts := strings.SplitN(actor, "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// eventIcon returns an emoji for an event type.
func eventIcon(eventType string) string {
	icons := map[string]string{
		"sling":             "\U0001f3af", // target
		"hook":              "\U0001fa9d", // hook
		"unhook":            "\U0001f513", // unlocked
		"done":              "\u2705",     // check mark
		"mail":              "\U0001f4ec", // mailbox
		"spawn":             "\U0001f9a8", // skunk (polecat)
		"kill":              "\U0001f480", // skull
		"nudge":             "\U0001f449", // pointing right
		"handoff":           "\U0001f91d", // handshake
		"session_start":     "\u25b6\ufe0f",
		"session_end":       "\u23f9\ufe0f",
		"session_death":     "\u2620\ufe0f",
		"mass_death":        "\U0001f4a5", // collision
		"patrol_started":    "\U0001f50d", // magnifying glass
		"patrol_complete":   "\u2714\ufe0f",
		"escalation_sent":   "\u26a0\ufe0f",
		"escalation_acked":  "\U0001f44d", // thumbs up
		"escalation_closed": "\U0001f515", // bell slash
		"merge_started":     "\U0001f500", // shuffle
		"merged":            "\u2728",     // sparkles
		"merge_failed":      "\u274c",     // cross mark
		"boot":              "\U0001f680", // rocket
		"halt":              "\U0001f6d1", // stop sign
	}
	if icon, ok := icons[eventType]; ok {
		return icon
	}
	return "\U0001f4cb" // clipboard
}

// eventSummary generates a human-readable summary for an event.
func eventSummary(eventType, actor string, payload map[string]interface{}) string {
	shortActor := formatAgentAddress(actor)

	switch eventType {
	case "sling":
		bead, _ := payload["bead"].(string)
		target, _ := payload["target"].(string)
		return fmt.Sprintf("%s slung to %s", bead, formatAgentAddress(target))
	case "done":
		bead, _ := payload["bead"].(string)
		return fmt.Sprintf("%s completed %s", shortActor, bead)
	case "mail":
		to, _ := payload["to"].(string)
		subject, _ := payload["subject"].(string)
		if len(subject) > 25 {
			subject = subject[:22] + "..."
		}
		return fmt.Sprintf("\u2192 %s: %s", formatAgentAddress(to), subject)
	case "spawn":
		return fmt.Sprintf("%s spawned", shortActor)
	case "kill":
		return fmt.Sprintf("%s killed", shortActor)
	case "hook":
		bead, _ := payload["bead"].(string)
		return fmt.Sprintf("%s hooked %s", shortActor, bead)
	case "unhook":
		bead, _ := payload["bead"].(string)
		return fmt.Sprintf("%s unhooked %s", shortActor, bead)
	case "merged":
		branch, _ := payload["branch"].(string)
		return fmt.Sprintf("merged %s", branch)
	case "merge_failed":
		reason, _ := payload["reason"].(string)
		if len(reason) > 30 {
			reason = reason[:27] + "..."
		}
		return fmt.Sprintf("merge failed: %s", reason)
	case "escalation_sent":
		return "escalation created"
	case "session_death":
		role, _ := payload["role"].(string)
		return fmt.Sprintf("%s session died", formatAgentAddress(role))
	case "mass_death":
		count, _ := payload["count"].(float64)
		return fmt.Sprintf("%.0f sessions died", count)
	default:
		return eventType
	}
}

// runCmd executes a command with a timeout and returns stdout.
func runCmd(timeout time.Duration, name string, args ...string) (*bytes.Buffer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%s timed out after %v", name, timeout)
		}
		return nil, err
	}
	return &stdout, nil
}

// determineCIStatus evaluates the overall CI status from status checks.
func determineCIStatus(checks []struct {
	State      string `json:"state"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
},
) string {
	if len(checks) == 0 {
		return "pending"
	}

	hasFailure := false
	hasPending := false

	for _, check := range checks {
		switch check.Conclusion {
		case "failure", "cancelled", "timed_out", "action_required":
			hasFailure = true
		case "success", "skipped", "neutral":
			// Pass
		default:
			switch check.Status {
			case "queued", "in_progress", "waiting", "pending", "requested":
				hasPending = true
			}
			switch check.State {
			case "FAILURE", "ERROR":
				hasFailure = true
			case "PENDING", "EXPECTED":
				hasPending = true
			}
		}
	}

	if hasFailure {
		return "fail"
	}
	if hasPending {
		return "pending"
	}
	return "pass"
}

// determineMergeableStatus converts GitHub's mergeable field to display value.
func determineMergeableStatus(mergeable string) string {
	switch strings.ToUpper(mergeable) {
	case "MERGEABLE":
		return "ready"
	case "CONFLICTING":
		return "conflict"
	default:
		return "pending"
	}
}

// determineColorClass determines the row color based on CI and merge status.
func determineColorClass(ciStatus, mergeable string) string {
	if ciStatus == "fail" || mergeable == "conflict" {
		return "mq-red"
	}
	if ciStatus == "pending" || mergeable == "pending" {
		return "mq-yellow"
	}
	if ciStatus == "pass" && mergeable == "ready" {
		return "mq-green"
	}
	return "mq-yellow"
}

// determineCrewState determines state from activity and agent status.
func determineCrewState(activityAge time.Duration, isAgentRunning bool, hook string) string {
	if !isAgentRunning {
		if hook != "" {
			return "finished" // Had work, agent stopped = finished
		}
		return "ready" // No work, agent stopped = ready for work
	}

	// Agent is running
	switch {
	case activityAge < 2*time.Minute:
		return "spinning" // Active recently
	case activityAge < 10*time.Minute:
		return "spinning" // Still probably working
	default:
		return "questions" // Running but no activity = likely waiting for input
	}
}

// prResponse represents the JSON response from gh pr list.
type prResponse struct {
	Number            int    `json:"number"`
	Title             string `json:"title"`
	URL               string `json:"url"`
	Mergeable         string `json:"mergeable"`
	StatusCheckRollup []struct {
		State      string `json:"state"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"statusCheckRollup"`
}

// gitURLToRepoPath converts a git URL to owner/repo format.
func gitURLToRepoPath(gitURL string) string {
	if strings.HasPrefix(gitURL, "https://github.com/") {
		path := strings.TrimPrefix(gitURL, "https://github.com/")
		path = strings.TrimSuffix(path, ".git")
		return path
	}
	if strings.HasPrefix(gitURL, "git@github.com:") {
		path := strings.TrimPrefix(gitURL, "git@github.com:")
		path = strings.TrimSuffix(path, ".git")
		return path
	}
	return ""
}
