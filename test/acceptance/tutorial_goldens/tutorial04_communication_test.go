//go:build acceptance_c

package tutorialgoldens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTutorial04Communication(t *testing.T) {
	ws := newTutorialWorkspace(t)
	ws.attachDiagnostics(t, "tutorial-04")

	myCity := expandHome(ws.home(), "~/my-city")
	myProject := expandHome(ws.home(), "~/my-project")
	var tutorialMailID string
	mustMkdirAll(t, myProject)

	out, err := ws.runShell("gc init ~/my-city --provider claude --skip-provider-readiness", "")
	if err != nil {
		t.Fatalf("seed city init: %v\n%s", err, out)
	}
	ws.setCWD(myCity)

	for _, cmd := range []string{"gc rig add ~/my-project"} {
		if out, err := ws.runShell(cmd, ""); err != nil {
			t.Fatalf("seed rig add %q: %v\n%s", cmd, err, out)
		}
	}

	if out, err := ws.runShell("gc agent add --name reviewer --dir my-project", ""); err != nil {
		t.Fatalf("seed reviewer scaffold: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(myCity, "agents", "reviewer", "agent.toml"), "dir = \"my-project\"\nprovider = \""+tutorialReviewerProvider()+"\"\n", 0o644)
	writeFile(t, filepath.Join(myCity, "agents", "reviewer", "prompt.template.md"), "# Reviewer\nReview code.\n", 0o644)
	ws.noteWarning("TODO(issue #632): once bare agent names reliably resolve to the enclosing rig in acceptance-style paths, simplify tutorial 04's rig-local reviewer references from `my-project/reviewer` to bare `reviewer` where the shell is already in the rig")

	if _, err := ws.waitForSessionByTemplateOrTarget("mayor", "mayor", 30*time.Second, time.Second); err != nil {
		t.Fatalf("resolve mayor session bead: %v", err)
	}
	wakeMayor := func(context string) {
		t.Helper()
		out, err := ws.runShell("gc session wake mayor", "")
		if err != nil {
			t.Fatalf("%s: %v\n%s", context, err, out)
		}
	}
	mayorReady := func() bool {
		peekOut, peekErr := ws.runShell("gc session peek mayor --lines 1", "")
		return peekErr == nil && strings.TrimSpace(peekOut) != ""
	}
	waitForMayorReady := func(context string) {
		t.Helper()
		if _, err := ws.waitForSessionByTemplateOrTarget("mayor", "mayor", 30*time.Second, time.Second); err != nil {
			t.Fatalf("resolve mayor session bead %s: %v", context, err)
		}
		if !waitForCondition(t, 30*time.Second, 1*time.Second, mayorReady) {
			out, _ := ws.runShell("gc session list", "")
			t.Fatalf("mayor session did not become peekable %s:\n%s", context, out)
		}
	}
	restartCity := func(context string) {
		ws.noteWarning("tutorial 04 runtime workaround: %s, so the page driver performs a hidden gc stop/gc start cycle before retrying the visible communication flow", context)
		if out, err := ws.runShell("gc stop", ""); err != nil {
			t.Fatalf("hidden gc stop during tutorial 04 recovery: %v\n%s", err, out)
		}
		if out, err := ws.runShell("gc start", ""); err != nil {
			t.Fatalf("hidden gc start during tutorial 04 recovery: %v\n%s", err, out)
		}
		wakeMayor("wake mayor after tutorial 04 hidden restart")
	}
	if !waitForCondition(t, 30*time.Second, 1*time.Second, mayorReady) {
		ws.noteWarning("tutorial 04 runtime workaround: gc init can leave mayor mid-restart, so the page driver explicitly wakes it before bootstrapping a fresh headless submit")
		wakeMayor("wake mayor during tutorial 04 bootstrap")
		if out, err := ws.runShell(`gc session submit mayor "__tutorial04_bootstrap__"`, ""); err != nil {
			t.Fatalf("seed mayor submit bootstrap: %v\n%s", err, out)
		}
	}
	if !waitForCondition(t, 30*time.Second, 1*time.Second, mayorReady) {
		restartCity("gc init left mayor unpeekable during communication bootstrap")
		if out, err := ws.runShell(`gc session submit mayor "__tutorial04_bootstrap__"`, ""); err != nil {
			t.Fatalf("seed mayor submit bootstrap after hidden restart: %v\n%s", err, out)
		}
	}
	waitForMayorReady("during tutorial 04 seed bootstrap")

	t.Run(`gc mail send mayor -s "Review needed" -m "Please look at the auth module changes in my-project"`, func(t *testing.T) {
		out, err := ws.runShell(`gc mail send mayor -s "Review needed" -m "Please look at the auth module changes in my-project"`, "")
		if err != nil {
			t.Fatalf("gc mail send mayor: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Sent message") {
			t.Fatalf("mail send output mismatch:\n%s", out)
		}
		fields := strings.Fields(out)
		if len(fields) < 4 {
			t.Fatalf("mail send output did not include a message ID:\n%s", out)
		}
		tutorialMailID = fields[2]
	})

	t.Run("gc mail check mayor", func(t *testing.T) {
		out, err := ws.runShell("gc mail check mayor", "")
		if err != nil {
			t.Fatalf("gc mail check mayor: %v\n%s", err, out)
		}
		if !strings.Contains(strings.ToLower(out), "unread") {
			t.Fatalf("mail check output mismatch:\n%s", out)
		}
	})

	t.Run("gc mail inbox mayor", func(t *testing.T) {
		out, err := ws.runShell("gc mail inbox mayor", "")
		if err != nil {
			t.Fatalf("gc mail inbox mayor: %v\n%s", err, out)
		}
		for _, want := range []string{"Review needed", "auth module changes in my-project"} {
			if !strings.Contains(out, want) {
				t.Fatalf("mail inbox missing %q:\n%s", want, out)
			}
		}
	})

	communicationNudge := `Check mail and hook status, then act accordingly`
	communicationFollowUp := `You already processed the earlier message. Continue the reviewer coordination from that prior request.`
	communicationPeekTimeout := 90 * time.Second
	communicationRetryTimeout := 90 * time.Second
	communicationSettleTimeout := 10 * time.Second
	nudgeMayor := func(context string) {
		out, err := ws.runShell(`gc session nudge mayor "`+communicationNudge+`"`, "")
		if err != nil {
			t.Fatalf("%s: %v\n%s", context, err, out)
		}
		if !strings.Contains(out, "Nudged mayor") && !strings.Contains(out, "Queued nudge for mayor") {
			t.Fatalf("%s output mismatch:\n%s", context, out)
		}
	}
	submitMayorFollowUp := func(context string) {
		t.Helper()
		out, err := ws.runShell(`gc session submit mayor "`+communicationFollowUp+`" --intent follow_up`, "")
		if err != nil {
			t.Fatalf("%s: %v\n%s", context, err, out)
		}
		if !strings.Contains(out, "Queued follow-up for mayor") &&
			!strings.Contains(out, "Submitted follow-up to mayor") {
			t.Fatalf("%s output mismatch:\n%s", context, out)
		}
	}
	mayorMailConsumed := func() bool {
		if tutorialMailID == "" {
			return false
		}
		out, err := ws.runShell("bd show "+tutorialMailID+" --json", "")
		if err == nil {
			var beads []struct {
				ID     string   `json:"id"`
				Status string   `json:"status"`
				Labels []string `json:"labels"`
			}
			if err := json.Unmarshal([]byte(out), &beads); err == nil && len(beads) == 1 && beads[0].ID == tutorialMailID {
				if beads[0].Status != "" && beads[0].Status != "open" {
					return true
				}
				for _, label := range beads[0].Labels {
					if label == "read" {
						return true
					}
				}
			}
		}
		countOut, countErr := ws.runShell("gc mail count mayor", "")
		if countErr == nil && strings.Contains(countOut, "0 unread") {
			return true
		}
		return false
	}
	waitForMailConsumption := func() bool {
		return waitForCondition(t, communicationSettleTimeout, 1*time.Second, mayorMailConsumed)
	}

	t.Run(`gc session nudge mayor "Check mail and hook status, then act accordingly"`, func(t *testing.T) {
		nudgeMayor("gc session nudge mayor")
	})

	t.Run("gc session peek mayor --lines 6", func(t *testing.T) {
		var out string
		mayorCommunicationVisible := func() bool {
			var err error
			out, err = ws.runShell("gc session peek mayor --lines 6", "")
			if err != nil {
				return false
			}
			return strings.Contains(out, "Review needed") ||
				strings.Contains(out, "auth module changes in my-project") ||
				strings.Contains(out, "Review the auth module changes") ||
				(strings.Contains(out, "my-project/reviewer") && strings.Contains(out, "auth module"))
		}
		if waitForCondition(t, communicationPeekTimeout, 2*time.Second, mayorCommunicationVisible) {
			return
		}
		if waitForMailConsumption() {
			// Once the exact tutorial mail bead is marked read or archived, hook delivery is proven.
			// The follow-up only exists to make that already-consumed request visible in peek.
			ws.noteWarning("tutorial 04 runtime workaround: hooks already consumed the tutorial mail, so the page driver submits an explicit follow-up that surfaces the completed review request in peek output")
			submitMayorFollowUp("submit follow-up after mayor consumed tutorial 04 mail")
		} else {
			ws.noteWarning("tutorial 04 runtime workaround: the visible nudge can leave mayor with injected mail but no rendered coordination step yet, so the page driver explicitly wakes mayor and requeues the same mail-driven nudge before retrying the visible peek step")
			wakeMayor("wake mayor before communication retry")
			nudgeMayor("re-nudge mayor before communication retry")
		}
		if waitForCondition(t, communicationRetryTimeout, 2*time.Second, mayorCommunicationVisible) {
			return
		}
		if waitForMailConsumption() {
			ws.noteWarning("tutorial 04 runtime workaround: after the wake retry the tutorial mail is already consumed, so the page driver submits one more explicit follow-up instead of restarting the city and losing the active communication context")
			submitMayorFollowUp("submit follow-up after wake retry")
		} else {
			ws.noteWarning("tutorial 04 runtime workaround: after the wake retry the tutorial mail is still unread, so the page driver performs one final wake and nudge instead of restarting the city mid-conversation")
			wakeMayor("wake mayor before final communication retry")
			nudgeMayor("re-nudge mayor before final communication retry")
		}
		if !waitForCondition(t, communicationRetryTimeout, 2*time.Second, mayorCommunicationVisible) {
			t.Fatalf("peek mayor did not surface communication flow in time:\n%s", out)
		}
	})

	if mayorPeek, err := ws.runShell("gc session peek mayor --lines 12", ""); err == nil {
		ws.noteDiagnostic("final mayor peek:\n%s", mayorPeek)
	}
	if mayorLogs, err := ws.runShell("gc session logs mayor --tail 5", ""); err == nil {
		ws.noteDiagnostic("final mayor logs:\n%s", mayorLogs)
	}
	if tutorialMailID != "" {
		if mailBead, err := ws.runShell("bd show "+tutorialMailID+" --json", ""); err == nil {
			ws.noteDiagnostic("tutorial mail bead:\n%s", mailBead)
		}
	}
	if data, err := os.ReadFile(filepath.Join(myCity, "city.toml")); err == nil {
		ws.noteDiagnostic("final city.toml:\n%s", string(data))
	}
}
