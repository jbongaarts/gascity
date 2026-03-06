package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/telemetry"
)

// CityRuntime holds all running state for a single city's reconciliation
// loop. It encapsulates the per-city lifecycle that was previously spread
// across runController and controllerLoop. A machine-wide supervisor can
// instantiate multiple CityRuntimes — one per registered city.
type CityRuntime struct {
	cityPath  string
	cityName  string
	tomlPath  string
	watchDirs []string

	cfg     *config.City
	sp      session.Provider
	buildFn func(*config.City, session.Provider) []agent.Agent

	rops reconcileOps
	dops drainOps
	ct   crashTracker
	it   idleTracker
	wg   wispGC
	ad   automationDispatcher

	rec events.Recorder
	cs  *controllerState // nil when API is disabled

	poolSessions      map[string]time.Duration
	poolDeathHandlers map[string]poolDeathInfo
	suspendedNames    map[string]bool

	stdout, stderr io.Writer
}

// CityRuntimeParams holds the caller-provided parameters for creating a
// CityRuntime. Internal components (reconcileOps, crashTracker, etc.) are
// built by the constructor from these inputs.
type CityRuntimeParams struct {
	CityPath  string
	CityName  string
	TomlPath  string
	WatchDirs []string

	Cfg     *config.City
	SP      session.Provider
	BuildFn func(*config.City, session.Provider) []agent.Agent
	Dops    drainOps

	Rec       events.Recorder
	EventProv events.Provider

	PoolSessions      map[string]time.Duration
	PoolDeathHandlers map[string]poolDeathInfo

	Stdout, Stderr io.Writer
}

// newCityRuntime creates a CityRuntime, building internal components
// (reconcileOps, crash tracker, idle tracker, wisp GC, automation
// dispatcher) from the provided parameters.
func newCityRuntime(p CityRuntimeParams) *CityRuntime {
	rops := newReconcileOps(p.SP)

	var ct crashTracker
	if maxR := p.Cfg.Daemon.MaxRestartsOrDefault(); maxR > 0 {
		ct = newCrashTracker(maxR, p.Cfg.Daemon.RestartWindowDuration())
	}

	it := buildIdleTracker(p.Cfg, p.CityName, p.SP)

	var wg wispGC
	if p.Cfg.Daemon.WispGCEnabled() {
		wg = newWispGC(p.Cfg.Daemon.WispGCIntervalDuration(),
			p.Cfg.Daemon.WispTTLDuration(), beads.ExecCommandRunner())
	}

	ad := buildAutomationDispatcher(p.CityPath, p.Cfg, beads.ExecCommandRunner(), p.Rec, p.Stderr)

	suspendedNames := computeSuspendedNames(p.Cfg, p.CityName, p.CityPath)

	return &CityRuntime{
		cityPath:          p.CityPath,
		cityName:          p.CityName,
		tomlPath:          p.TomlPath,
		watchDirs:         p.WatchDirs,
		cfg:               p.Cfg,
		sp:                p.SP,
		buildFn:           p.BuildFn,
		rops:              rops,
		dops:              p.Dops,
		ct:                ct,
		it:                it,
		wg:                wg,
		ad:                ad,
		rec:               p.Rec,
		poolSessions:      p.PoolSessions,
		poolDeathHandlers: p.PoolDeathHandlers,
		suspendedNames:    suspendedNames,
		stdout:            p.Stdout,
		stderr:            p.Stderr,
	}
}

// setControllerState sets the API state for this city. The controller
// state is managed by the caller (who also owns the API server) and
// passed in after construction.
func (cr *CityRuntime) setControllerState(cs *controllerState) {
	cr.cs = cs
}

// crashTracker returns the crash tracker for API server wiring.
func (cr *CityRuntime) crashTrack() crashTracker {
	return cr.ct
}

// run executes the reconciliation loop until ctx is canceled. This is
// the per-city main loop — it watches config, reconciles agents, runs
// wisp GC, and dispatches automations.
func (cr *CityRuntime) run(ctx context.Context) {
	dirty := &atomic.Bool{}
	if cr.tomlPath != "" {
		dirs := cr.watchDirs
		if len(dirs) == 0 {
			dirs = []string{filepath.Dir(cr.tomlPath)}
		}
		cleanup := watchConfigDirs(dirs, dirty, cr.stderr)
		defer cleanup()
	}

	// Track effective provider name for hot-reload detection.
	lastProviderName := cr.cfg.Session.Provider
	if v := os.Getenv("GC_SESSION"); v != "" {
		lastProviderName = v
	}

	observePaths := observeSearchPaths(cr.cfg.Daemon.ObservePaths)

	// Initial reconciliation.
	agents := cr.buildFn(cr.cfg, cr.sp)
	doReconcileAgents(agents, cr.sp, cr.rops, cr.dops, cr.ct, cr.it, cr.rec,
		cr.poolSessions, cr.suspendedNames,
		cr.cfg.Daemon.DriftDrainTimeoutDuration(), cr.cfg.Session.StartupTimeoutDuration(),
		cr.stdout, cr.stderr, ctx)
	ensureObservers(agents, observePaths, cr.rec)
	fmt.Fprintln(cr.stdout, "City started.") //nolint:errcheck // best-effort stdout

	cityRoot := filepath.Dir(cr.tomlPath)
	interval := cr.cfg.Daemon.PatrolIntervalDuration()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Track pool instance liveness for death detection.
	var prevPoolRunning map[string]bool

	for {
		select {
		case <-ticker.C:
			cr.tick(ctx, dirty, &lastProviderName, &observePaths, cityRoot, &prevPoolRunning)
		case <-ctx.Done():
			return
		}
	}
}

// tick performs one reconciliation tick: pool death detection, config
// reload (if dirty), agent reconciliation, wisp GC, and automation
// dispatch.
func (cr *CityRuntime) tick(
	ctx context.Context,
	dirty *atomic.Bool,
	lastProviderName *string,
	observePaths *[]string,
	cityRoot string,
	prevPoolRunning *map[string]bool,
) {
	// Detect pool instance deaths since last tick.
	if len(cr.poolDeathHandlers) > 0 {
		currentRunning, _ := cr.rops.listRunning("")
		currentSet := make(map[string]bool, len(currentRunning))
		for _, name := range currentRunning {
			currentSet[name] = true
		}
		if *prevPoolRunning != nil {
			for sn, info := range cr.poolDeathHandlers {
				if (*prevPoolRunning)[sn] && !currentSet[sn] {
					if _, err := shellScaleCheck(info.Command, info.Dir); err != nil {
						fmt.Fprintf(cr.stderr, "on_death %s: %v\n", sn, err) //nolint:errcheck // best-effort stderr
					}
				}
			}
		}
		*prevPoolRunning = make(map[string]bool)
		for sn := range cr.poolDeathHandlers {
			if currentSet[sn] {
				(*prevPoolRunning)[sn] = true
			}
		}
	}

	if dirty.Swap(false) {
		cr.reloadConfig(ctx, lastProviderName, observePaths, cityRoot)
	}

	agents := cr.buildFn(cr.cfg, cr.sp)
	doReconcileAgents(agents, cr.sp, cr.rops, cr.dops, cr.ct, cr.it, cr.rec,
		cr.poolSessions, cr.suspendedNames,
		cr.cfg.Daemon.DriftDrainTimeoutDuration(), cr.cfg.Session.StartupTimeoutDuration(),
		cr.stdout, cr.stderr, ctx)
	ensureObservers(agents, *observePaths, cr.rec)

	// Wisp GC: purge expired closed molecules.
	if cr.wg != nil && cr.wg.shouldRun(time.Now()) {
		purged, gcErr := cr.wg.runGC(cityRoot, time.Now())
		if gcErr != nil {
			fmt.Fprintf(cr.stderr, "gc start: wisp gc: %v\n", gcErr) //nolint:errcheck // best-effort stderr
		} else if purged > 0 {
			fmt.Fprintf(cr.stdout, "Bead GC: purged %d expired bead(s)\n", purged) //nolint:errcheck // best-effort stdout
		}
	}

	// Automation dispatch.
	if cr.ad != nil {
		cr.ad.dispatch(ctx, cityRoot, time.Now())
	}
}

// reloadConfig attempts to reload city.toml and update all internal
// components. On error, the old config is kept.
func (cr *CityRuntime) reloadConfig(
	ctx context.Context,
	lastProviderName *string,
	observePaths *[]string,
	cityRoot string,
) {
	result, err := tryReloadConfig(cr.tomlPath, cr.cityName, cityRoot, cr.stderr)
	if err != nil {
		fmt.Fprintf(cr.stderr, "gc start: config reload: %v (keeping old config)\n", err) //nolint:errcheck // best-effort stderr
		telemetry.RecordConfigReload(ctx, "", err)
		return
	}

	oldAgentCount := len(cr.cfg.Agents)
	oldRigCount := len(cr.cfg.Rigs)
	cr.cfg = result.Cfg

	// Detect session provider change.
	newProviderName := cr.cfg.Session.Provider
	if v := os.Getenv("GC_SESSION"); v != "" {
		newProviderName = v
	}
	if newProviderName != *lastProviderName {
		if running, lErr := cr.rops.listRunning(""); lErr == nil && len(running) > 0 {
			fmt.Fprintf(cr.stdout, "Provider changed (%s → %s), stopping %d agent(s)...\n", //nolint:errcheck
				displayProviderName(*lastProviderName), displayProviderName(newProviderName), len(running))
			gracefulStopAll(running, cr.sp, cr.cfg.Daemon.ShutdownTimeoutDuration(), cr.rec, cr.stdout, cr.stderr)
		}
		newSp, spErr := newSessionProviderByName(newProviderName, cr.cfg.Session, cr.cityName)
		if spErr != nil {
			fmt.Fprintf(cr.stderr, "gc start: new session provider %q: %v (keeping old provider)\n", //nolint:errcheck
				newProviderName, spErr)
		} else {
			cr.sp = newSp
			cr.rops = newReconcileOps(cr.sp)
			cr.dops = newDrainOps(cr.sp)
			cr.rec.Record(events.Event{
				Type:    events.ProviderSwapped,
				Actor:   "gc",
				Message: fmt.Sprintf("%s → %s", displayProviderName(*lastProviderName), displayProviderName(newProviderName)),
			})
			fmt.Fprintf(cr.stdout, "Session provider swapped to %s.\n", displayProviderName(newProviderName)) //nolint:errcheck
			*lastProviderName = newProviderName
		}
	}

	// Re-materialize and prepend system formulas.
	sysDir, _ := MaterializeSystemFormulas(systemFormulasFS, "system_formulas", cityRoot)
	if sysDir != "" {
		cr.cfg.FormulaLayers.City = append([]string{sysDir}, cr.cfg.FormulaLayers.City...)
		for rigName, layers := range cr.cfg.FormulaLayers.Rigs {
			cr.cfg.FormulaLayers.Rigs[rigName] = append([]string{sysDir}, layers...)
		}
	}
	if err := config.ValidateRigs(cr.cfg.Rigs, cr.cityName); err != nil {
		fmt.Fprintf(cr.stderr, "gc start: config reload: %v\n", err) //nolint:errcheck
	}
	resolveRigPaths(cityRoot, cr.cfg.Rigs)
	if err := startBeadsLifecycle(cityRoot, cr.cityName, cr.cfg, cr.stderr); err != nil {
		fmt.Fprintf(cr.stderr, "gc start: config reload: %v\n", err) //nolint:errcheck
	}
	if len(cr.cfg.FormulaLayers.City) > 0 {
		if err := ResolveFormulas(cityRoot, cr.cfg.FormulaLayers.City); err != nil {
			fmt.Fprintf(cr.stderr, "gc start: config reload: city formulas: %v\n", err) //nolint:errcheck
		}
	}
	for _, r := range cr.cfg.Rigs {
		if layers, ok := cr.cfg.FormulaLayers.Rigs[r.Name]; ok && len(layers) > 0 {
			if err := ResolveFormulas(r.Path, layers); err != nil {
				fmt.Fprintf(cr.stderr, "gc start: config reload: rig %q formulas: %v\n", r.Name, err) //nolint:errcheck
			}
		}
	}

	cr.poolSessions = computePoolSessions(cr.cfg, cr.cityName, cr.sp)
	cr.poolDeathHandlers = computePoolDeathHandlers(cr.cfg, cr.cityName, cityRoot, cr.sp)
	cr.suspendedNames = computeSuspendedNames(cr.cfg, cr.cityName, cr.cityPath)

	// Rebuild crash tracker if config values changed.
	newMaxR := cr.cfg.Daemon.MaxRestartsOrDefault()
	newWindow := cr.cfg.Daemon.RestartWindowDuration()
	switch {
	case newMaxR <= 0:
		cr.ct = nil
	case cr.ct == nil:
		cr.ct = newCrashTracker(newMaxR, newWindow)
	default:
		oldMaxR, oldWindow := cr.ct.limits()
		if newMaxR != oldMaxR || newWindow != oldWindow {
			cr.ct = newCrashTracker(newMaxR, newWindow)
		}
	}
	if cr.cs != nil {
		cr.cs.mu.Lock()
		cr.cs.ct = cr.ct
		cr.cs.mu.Unlock()
	}

	cr.it = buildIdleTracker(cr.cfg, cr.cityName, cr.sp)

	if cr.cfg.Daemon.WispGCEnabled() {
		cr.wg = newWispGC(cr.cfg.Daemon.WispGCIntervalDuration(),
			cr.cfg.Daemon.WispTTLDuration(), beads.ExecCommandRunner())
	} else {
		cr.wg = nil
	}

	cr.ad = buildAutomationDispatcher(cityRoot, cr.cfg, beads.ExecCommandRunner(), cr.rec, cr.stderr)
	*observePaths = observeSearchPaths(cr.cfg.Daemon.ObservePaths)

	if cr.cs != nil {
		cr.cs.update(cr.cfg, cr.sp)
	}

	fmt.Fprintf(cr.stdout, "Config reloaded: %s (rev %s)\n", //nolint:errcheck
		configReloadSummary(oldAgentCount, oldRigCount, len(cr.cfg.Agents), len(cr.cfg.Rigs)),
		shortRev(result.Revision))
	telemetry.RecordConfigReload(ctx, result.Revision, nil)
}

// shutdown performs graceful two-pass agent shutdown for this city.
func (cr *CityRuntime) shutdown() {
	timeout := cr.cfg.Daemon.ShutdownTimeoutDuration()
	if cr.rops != nil {
		running, _ := cr.rops.listRunning("")
		gracefulStopAll(running, cr.sp, timeout, cr.rec, cr.stdout, cr.stderr)
	} else {
		var names []string
		for _, a := range cr.buildFn(cr.cfg, cr.sp) {
			if a.IsRunning() {
				names = append(names, a.SessionName())
			}
		}
		gracefulStopAll(names, cr.sp, timeout, cr.rec, cr.stdout, cr.stderr)
	}
}
