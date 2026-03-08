package main

import (
	"io"
	"os/exec"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionauto "github.com/gastownhall/gascity/internal/runtime/auto"
)

// agentBuildParams holds shared, per-city parameters for building agents.
// These are constant across all agents in a single buildAgents call.
type agentBuildParams struct {
	cityName        string
	cityPath        string
	workspace       *config.Workspace
	providers       map[string]config.ProviderSpec
	lookPath        config.LookPathFunc
	fs              fsys.FS
	sp              runtime.Provider
	rigs            []config.Rig
	sessionTemplate string
	beaconTime      time.Time
	packDirs        []string
	packOverlayDirs []string
	rigOverlayDirs  map[string][]string
	globalFragments []string
	stderr          io.Writer
}

// buildOneAgent resolves a config.Agent into an agent.Agent. This is the
// single canonical path for building agents — both fixed agents and pool
// instances flow through here. The caller is responsible for setting the
// correct Name and Dir on cfgAgent (pool callers modify these for each
// instance before calling).
//
// qualifiedName is the agent's canonical identity (e.g., "mayor" or
// "hello-world/polecat-2"). fpExtra carries additional data for config
// fingerprinting (e.g., pool bounds); pass nil for pool instances.
//
// Internally delegates to resolveTemplate() for pure parameter resolution,
// then handles ACP route registration (the only side effect) and creates
// the agent.Agent.
func buildOneAgent(p *agentBuildParams, cfgAgent *config.Agent, qualifiedName string, fpExtra map[string]string, onStop ...func() error) (agent.Agent, error) {
	params, err := resolveTemplate(p, cfgAgent, qualifiedName, fpExtra)
	if err != nil {
		return nil, err
	}

	// Register ACP route on the auto provider for dynamic sessions
	// (e.g., pool instances) not known at newSessionProvider() time.
	if params.IsACP {
		if autoSP, ok := p.sp.(*sessionauto.Provider); ok {
			autoSP.RouteACP(params.SessionName)
		}
	}

	return agent.New(qualifiedName, p.cityName, params.Command, params.Prompt,
		params.Env, params.Hints, params.WorkDir, p.sessionTemplate,
		params.FPExtra, p.sp, onStop...), nil
}

// newAgentBuildParams constructs agentBuildParams from the common startup values.
func newAgentBuildParams(cityName, cityPath string, cfg *config.City, sp runtime.Provider, beaconTime time.Time, stderr io.Writer) *agentBuildParams {
	return &agentBuildParams{
		cityName:        cityName,
		cityPath:        cityPath,
		workspace:       &cfg.Workspace,
		providers:       cfg.Providers,
		lookPath:        exec.LookPath,
		fs:              fsys.OSFS{},
		sp:              sp,
		rigs:            cfg.Rigs,
		sessionTemplate: cfg.Workspace.SessionTemplate,
		beaconTime:      beaconTime,
		packDirs:        cfg.PackDirs,
		packOverlayDirs: cfg.PackOverlayDirs,
		rigOverlayDirs:  cfg.RigOverlayDirs,
		globalFragments: cfg.Workspace.GlobalFragments,
		stderr:          stderr,
	}
}

// effectiveOverlayDirs merges city-level and rig-level pack overlay dirs.
// City dirs come first (lower priority), then rig-specific dirs.
func effectiveOverlayDirs(cityDirs []string, rigDirs map[string][]string, rigName string) []string {
	rigSpecific := rigDirs[rigName]
	if len(rigSpecific) == 0 {
		return cityDirs
	}
	if len(cityDirs) == 0 {
		return rigSpecific
	}
	merged := make([]string, 0, len(cityDirs)+len(rigSpecific))
	merged = append(merged, cityDirs...)
	merged = append(merged, rigSpecific...)
	return merged
}

// templateNameFor returns the configuration template name for an agent.
// For pool/multi instances, this is the original template name (PoolName).
// For regular agents, it's the qualified name.
func templateNameFor(cfgAgent *config.Agent, qualifiedName string) string {
	if cfgAgent.PoolName != "" {
		return cfgAgent.PoolName
	}
	return qualifiedName
}
