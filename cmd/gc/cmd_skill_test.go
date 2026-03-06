package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSkillWorkOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"skill", "work"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("gc skill work exited %d: %s", code, stderr.String())
	}
	out := stdout.String()
	if out == "" {
		t.Fatal("gc skill work produced no output")
	}
	// Should contain bd commands.
	for _, want := range []string{"bd create", "bd list", "bd close", "bd ready"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestSkillListTopics(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"skill"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("gc skill exited %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, topic := range []string{"work", "dispatch", "agents", "rigs", "mail", "city"} {
		if !strings.Contains(out, topic) {
			t.Errorf("topic listing missing %q", topic)
		}
	}
}

func TestSkillUnknownTopic(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"skill", "bogus"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("gc skill bogus should fail")
	}
	if !strings.Contains(stderr.String(), "unknown topic") {
		t.Errorf("stderr = %q, want 'unknown topic'", stderr.String())
	}
}

func TestSkillAllTopicsReadable(t *testing.T) {
	// Verify every registered topic has a matching embedded file.
	for _, topic := range skillTopics {
		var stdout, stderr bytes.Buffer
		code := run([]string{"skill", topic.Arg}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("gc skill %s failed: %s", topic.Arg, stderr.String())
		}
		if stdout.Len() == 0 {
			t.Errorf("gc skill %s produced no output", topic.Arg)
		}
	}
}
