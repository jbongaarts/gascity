// template_resolve.go extracts a pure function for resolving agent config
// into session parameters. This is the data-only half of buildOneAgent:
// all steps that compute values (provider resolution, dir expansion, env
// merging, prompt rendering) without side effects (ACP route registration).
//
// resolveTemplate returns TemplateParams — a value type suitable for
// session.Manager.CreateFromParams or for building an agent.Agent.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/convergence"
	"github.com/gastownhall/gascity/internal/hooks"
	"github.com/gastownhall/gascity/internal/runtime"
)

// TemplateParams holds all resolved values needed to start a session.
// This is a pure data type — no side effects, no provider references.
type TemplateParams struct {
	// Command is the resolved provider command string.
	Command string
	// Prompt is the fully rendered prompt (with beacon).
	Prompt string
	// Env is the merged environment (passthrough + provider + agent + passthrough vars).
	Env map[string]string
	// Hints contains startup behavior (pre_start, session_setup, etc.).
	Hints agent.StartupHints
	// WorkDir is the resolved absolute working directory.
	WorkDir string
	// SessionName is the computed tmux session name.
	SessionName string
	// FPExtra carries additional fingerprint data (pool config, etc.).
	FPExtra map[string]string
	// ResolvedProvider is the resolved provider spec (for ACP routing, etc.).
	ResolvedProvider *config.ResolvedProvider
	// TemplateName is the config template name (pool name or qualified name).
	TemplateName string
	// RigName is the resolved rig association (empty if none).
	RigName string
	// IsACP is true if session = "acp".
	IsACP bool
}

// resolveTemplate computes all session parameters from a config.Agent without
// side effects. This is a pure extraction of steps 1-13 and 15-16 from
// buildOneAgent. The only side effect excluded is ACP route registration
// (step 14), which the caller handles.
//
// qualifiedName is the agent's canonical identity. fpExtra carries additional
// fingerprint data (e.g., pool bounds); pass nil for pool instances.
func resolveTemplate(p *agentBuildParams, cfgAgent *config.Agent, qualifiedName string, fpExtra map[string]string) (TemplateParams, error) {
	// Step 1: Resolve provider preset.
	resolved, err := config.ResolveProvider(cfgAgent, p.workspace, p.providers, p.lookPath)
	if err != nil {
		return TemplateParams{}, fmt.Errorf("agent %q: %w", qualifiedName, err)
	}

	// Step 2: Validate session vs provider compatibility.
	if cfgAgent.Session == "acp" && !resolved.SupportsACP {
		return TemplateParams{}, fmt.Errorf("agent %q: session = \"acp\" but provider %q does not support ACP (set supports_acp = true on the provider)", qualifiedName, resolved.Name)
	}

	// Step 3: Expand dir template.
	expandedDir := expandDirTemplate(cfgAgent.Dir, SessionSetupContext{
		Agent:    qualifiedName,
		Rig:      cfgAgent.Dir,
		CityRoot: p.cityPath,
		CityName: p.cityName,
	})
	workDir, err := resolveAgentDir(p.cityPath, expandedDir)
	if err != nil {
		return TemplateParams{}, fmt.Errorf("agent %q: %w", qualifiedName, err)
	}

	// Step 4: Install provider hooks (best-effort side effect, kept here
	// because it's idempotent and affects the filesystem, not runtime state).
	if ih := config.ResolveInstallHooks(cfgAgent, p.workspace); len(ih) > 0 {
		if hErr := hooks.Install(p.fs, p.cityPath, workDir, ih); hErr != nil {
			fmt.Fprintf(p.stderr, "agent %q: hooks: %v\n", qualifiedName, hErr) //nolint:errcheck
		}
	}

	// Step 5: Resolve overlay directory.
	overlayDir := resolveOverlayDir(cfgAgent.OverlayDir, p.cityPath)

	// Step 6: Build copy_files and command with settings args.
	var copyFiles []runtime.CopyEntry
	command := resolved.CommandString()
	if sa := settingsArgs(p.cityPath, resolved.Name); sa != "" {
		command = command + " " + sa
		settingsFile := filepath.Join(p.cityPath, ".gc", "settings.json")
		copyFiles = append(copyFiles, runtime.CopyEntry{Src: settingsFile, RelDst: filepath.Join(".gc", "settings.json")})
	}
	scriptsDir := filepath.Join(p.cityPath, ".gc", "scripts")
	if info, sErr := os.Stat(scriptsDir); sErr == nil && info.IsDir() {
		copyFiles = append(copyFiles, runtime.CopyEntry{Src: scriptsDir, RelDst: filepath.Join(".gc", "scripts")})
	}
	copyFiles = stageHookFiles(copyFiles, p.cityPath, workDir)

	// Step 7: Resolve rig association.
	rigName := resolveRigForAgent(workDir, p.rigs)

	// Step 8: Compute session name.
	sessName := agent.SessionNameFor(p.cityName, qualifiedName, p.sessionTemplate)

	// Step 9: Build agent environment.
	agentEnv := map[string]string{
		"GC_SESSION_NAME": sessName,
		"GC_TEMPLATE":     templateNameFor(cfgAgent, qualifiedName),
		"GC_AGENT":        qualifiedName,
		"GC_CITY":         p.cityPath,
		"GC_DIR":          workDir,
	}
	if rigName != "" {
		agentEnv["GC_RIG"] = rigName
	}

	// Step 10: Render prompt with beacon.
	var prompt string
	if resolved.PromptMode != "none" {
		fragments := mergeFragmentLists(p.globalFragments, cfgAgent.InjectFragments)
		prompt = renderPrompt(p.fs, p.cityPath, p.cityName, cfgAgent.PromptTemplate, PromptContext{
			CityRoot:      p.cityPath,
			AgentName:     qualifiedName,
			TemplateName:  cfgAgent.Name,
			RigName:       rigName,
			WorkDir:       workDir,
			IssuePrefix:   findRigPrefix(rigName, p.rigs),
			DefaultBranch: defaultBranchFor(workDir),
			WorkQuery:     cfgAgent.EffectiveWorkQuery(),
			SlingQuery:    cfgAgent.EffectiveSlingQuery(),
			Env:           cfgAgent.Env,
		}, p.sessionTemplate, p.stderr, p.packDirs, fragments)
		hasHooks := config.AgentHasHooks(cfgAgent, p.workspace, resolved.Name)
		beacon := runtime.FormatBeaconAt(p.cityName, qualifiedName, !hasHooks, p.beaconTime)
		if prompt != "" {
			prompt = beacon + "\n\n" + prompt
		} else {
			prompt = beacon
		}
	}

	// Step 11: Merge environment layers.
	env := convergence.ScrubTokenEnv(mergeEnv(passthroughEnv(), expandEnvMap(resolved.Env), expandEnvMap(cfgAgent.Env), agentEnv))

	// Step 12: Expand session setup templates.
	configDir := p.cityPath
	if cfgAgent.SourceDir != "" {
		configDir = cfgAgent.SourceDir
	}
	setupCtx := SessionSetupContext{
		Session:   sessName,
		Agent:     qualifiedName,
		Rig:       rigName,
		CityRoot:  p.cityPath,
		CityName:  p.cityName,
		WorkDir:   workDir,
		ConfigDir: configDir,
	}
	if strings.Contains(command, "{{") {
		expanded := expandSessionSetup([]string{command}, setupCtx)
		command = expanded[0]
	}
	expandedSetup := expandSessionSetup(cfgAgent.SessionSetup, setupCtx)
	resolvedScript := resolveSetupScript(cfgAgent.SessionSetupScript, p.cityPath)
	expandedPreStart := expandSessionSetup(cfgAgent.PreStart, setupCtx)
	expandedLive := expandSessionSetup(cfgAgent.SessionLive, setupCtx)

	// Step 13: Build startup hints.
	hints := agent.StartupHints{
		ReadyPromptPrefix:      resolved.ReadyPromptPrefix,
		ReadyDelayMs:           resolved.ReadyDelayMs,
		ProcessNames:           resolved.ProcessNames,
		EmitsPermissionWarning: resolved.EmitsPermissionWarning,
		Nudge:                  cfgAgent.Nudge,
		PreStart:               expandedPreStart,
		SessionSetup:           expandedSetup,
		SessionSetupScript:     resolvedScript,
		SessionLive:            expandedLive,
		PackOverlayDirs:        effectiveOverlayDirs(p.packOverlayDirs, p.rigOverlayDirs, rigName),
		OverlayDir:             overlayDir,
		CopyFiles:              copyFiles,
	}

	return TemplateParams{
		Command:          command,
		Prompt:           prompt,
		Env:              env,
		Hints:            hints,
		WorkDir:          workDir,
		SessionName:      sessName,
		FPExtra:          fpExtra,
		ResolvedProvider: resolved,
		TemplateName:     templateNameFor(cfgAgent, qualifiedName),
		RigName:          rigName,
		IsACP:            cfgAgent.Session == "acp",
	}, nil
}
