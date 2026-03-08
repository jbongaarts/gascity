package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/agent"
)

func TestConfigHash_Canonical(t *testing.T) {
	// Same params should produce the same hash regardless of call order.
	params := TemplateParams{
		Command: "claude --dangerously-skip-permissions",
		Prompt:  "You are a helpful agent.",
		Env:     map[string]string{"FOO": "bar", "BAZ": "qux"},
		Hints: agent.StartupHints{
			PreStart:     []string{"echo setup"},
			SessionSetup: []string{"echo ready"},
		},
		WorkDir:     "/home/user/project",
		SessionName: "test-session",
	}

	h1 := canonicalConfigHash(params, nil)
	h2 := canonicalConfigHash(params, nil)
	if h1 != h2 {
		t.Errorf("same params produced different hashes: %q vs %q", h1, h2)
	}
	if len(h1) != 16 {
		t.Errorf("hash length = %d, want 16", len(h1))
	}
}

func TestConfigHash_Behavioral(t *testing.T) {
	base := TemplateParams{
		Command:      "claude",
		Prompt:       "prompt",
		Env:          map[string]string{"KEY": "val"},
		WorkDir:      "/work",
		SessionName:  "s1",
		TemplateName: "worker",
		RigName:      "my-rig",
	}
	baseHash := canonicalConfigHash(base, nil)

	// Changing non-behavioral fields should NOT change hash.
	nonBehavioral := base
	nonBehavioral.SessionName = "s2"        // name excluded
	nonBehavioral.TemplateName = "overseer" // template excluded
	nonBehavioral.RigName = "other-rig"     // rig excluded

	if h := canonicalConfigHash(nonBehavioral, nil); h != baseHash {
		t.Errorf("non-behavioral change produced different hash: %q vs %q", h, baseHash)
	}

	// Changing behavioral fields SHOULD change hash.
	behavioral := base
	behavioral.Command = "gemini"
	if h := canonicalConfigHash(behavioral, nil); h == baseHash {
		t.Error("command change should produce different hash")
	}

	envChanged := base
	envChanged.Env = map[string]string{"KEY": "different"}
	if h := canonicalConfigHash(envChanged, nil); h == baseHash {
		t.Error("env change should produce different hash")
	}
}

func TestConfigHash_Overlay(t *testing.T) {
	// template + overlay should produce the same hash as an equivalent
	// flat config.
	params := TemplateParams{
		Command: "claude",
		Prompt:  "base prompt",
		Env:     map[string]string{"KEY": "val"},
		WorkDir: "/work",
	}

	// Overlay overrides command and adds an env var.
	overlay := map[string]string{
		"command":  "gemini",
		"env.TOOL": "hammer",
	}

	overlayHash := canonicalConfigHash(params, overlay)

	// Equivalent flat config (as if overlay was pre-applied).
	flat := TemplateParams{
		Command: "gemini",
		Prompt:  "base prompt",
		Env:     map[string]string{"KEY": "val", "TOOL": "hammer"},
		WorkDir: "/work",
	}
	flatHash := canonicalConfigHash(flat, nil)

	if overlayHash != flatHash {
		t.Errorf("overlay hash %q != flat hash %q", overlayHash, flatHash)
	}
}

func TestConfigHash_DifferentPrompts(t *testing.T) {
	p1 := TemplateParams{Command: "claude", Prompt: "prompt A"}
	p2 := TemplateParams{Command: "claude", Prompt: "prompt B"}

	if canonicalConfigHash(p1, nil) == canonicalConfigHash(p2, nil) {
		t.Error("different prompts should produce different hashes")
	}
}
