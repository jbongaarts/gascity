package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/convergence"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/telemetry"
)

// acquireControllerLock takes an exclusive flock on .gc/controller.lock.
// Returns the locked file (caller must defer Close) or an error if another
// controller is already running.
func acquireControllerLock(cityPath string) (*os.File, error) {
	path := filepath.Join(cityPath, ".gc", "controller.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening controller lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close() //nolint:errcheck // closing after flock failure
		return nil, fmt.Errorf("controller already running")
	}
	return f, nil
}

// startControllerSocket listens on a Unix socket at .gc/controller.sock.
// When a client sends "stop\n", cancelFn is called to shut down the
// controller loop. Returns the listener for cleanup.
func startControllerSocket(cityPath string, cancelFn context.CancelFunc) (net.Listener, error) {
	sockPath := filepath.Join(cityPath, ".gc", "controller.sock")
	// Remove stale socket from a previous crash.
	os.Remove(sockPath) //nolint:errcheck // stale socket cleanup
	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listening on controller socket: %w", err)
	}
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return // listener closed
			}
			go handleControllerConn(conn, cancelFn)
		}
	}()
	return lis, nil
}

// handleControllerConn reads from a connection and dispatches commands.
// Supported commands: "stop" (shutdown), "ping" (liveness check, returns PID).
func handleControllerConn(conn net.Conn, cancelFn context.CancelFunc) {
	defer conn.Close()                                     //nolint:errcheck // best-effort cleanup
	conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck // best-effort deadline
	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		switch scanner.Text() {
		case "stop":
			cancelFn()
			conn.Write([]byte("ok\n")) //nolint:errcheck // best-effort ack
		case "ping":
			fmt.Fprintf(conn, "%d\n", os.Getpid()) //nolint:errcheck // best-effort
		}
	}
}

// controllerAlive checks whether a controller is running by connecting
// to the controller.sock and sending a "ping". Returns the PID if alive,
// or 0 if not reachable.
func controllerAlive(cityPath string) int {
	sockPath := filepath.Join(cityPath, ".gc", "controller.sock")
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		return 0
	}
	defer conn.Close()                                    //nolint:errcheck // best-effort
	conn.Write([]byte("ping\n"))                          //nolint:errcheck // best-effort
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck // best-effort
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		return 0
	}
	return pid
}

// debounceDelay is the coalesce window for filesystem events. Multiple
// events within this window (vim atomic saves, git checkouts) produce a
// single dirty signal. Tests may override this for faster response.
var debounceDelay = 200 * time.Millisecond

// watchConfigDirs starts an fsnotify watcher on the given directories and
// sets dirty to true after a debounce window. Watches directories instead
// of individual files to handle vim/emacs rename-swap atomic saves.
// Returns a cleanup function. If the watcher cannot be created, returns a
// no-op cleanup (degraded to tick-only, no file watching).
func watchConfigDirs(dirs []string, dirty *atomic.Bool, stderr io.Writer) func() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintf(stderr, "gc start: config watcher: %v (reload on tick only)\n", err) //nolint:errcheck // best-effort stderr
		return func() {}
	}
	for _, dir := range dirs {
		if err := watcher.Add(dir); err != nil {
			fmt.Fprintf(stderr, "gc start: config watcher: cannot watch %s: %v\n", dir, err) //nolint:errcheck // best-effort stderr
		}
	}
	go func() {
		var debounce *time.Timer
		for {
			select {
			case _, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Debounce: reset timer on each event, fire after quiet period.
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(debounceDelay, func() {
					dirty.Store(true)
				})
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return func() { watcher.Close() } //nolint:errcheck // best-effort cleanup
}

// reloadResult holds the result of a config reload attempt.
type reloadResult struct {
	Cfg      *config.City
	Prov     *config.Provenance
	Revision string
}

// tryReloadConfig attempts to reload city.toml with includes and patches.
// Returns the new config, provenance, and revision on success, or an error
// on failure (parse error, validation error, cityName changed). Callers
// should keep the old config on error. Warnings are written to stderr;
// strict mode (default) makes them fatal — use --no-strict to disable.
func tryReloadConfig(tomlPath, lockedCityName, cityRoot string, stderr io.Writer) (*reloadResult, error) {
	// Auto-fetch remote packs before full config load (mirrors cmd_start).
	if quickCfg, qErr := config.Load(fsys.OSFS{}, tomlPath); qErr == nil && len(quickCfg.Packs) > 0 {
		if fErr := config.FetchPacks(quickCfg.Packs, cityRoot); fErr != nil {
			return nil, fmt.Errorf("fetching packs: %w", fErr)
		}
	}

	newCfg, prov, err := config.LoadWithIncludes(fsys.OSFS{}, tomlPath, extraConfigFiles...)
	if err != nil {
		return nil, fmt.Errorf("parsing city.toml: %w", err)
	}
	if strictMode && len(prov.Warnings) > 0 {
		for _, w := range prov.Warnings {
			fmt.Fprintf(stderr, "gc start: strict: %s\n", w) //nolint:errcheck // best-effort stderr
		}
		fmt.Fprintln(stderr, "gc start: use --no-strict to disable strict checking") //nolint:errcheck // best-effort stderr
		return nil, fmt.Errorf("strict mode: %d collision warning(s)", len(prov.Warnings))
	}
	for _, w := range prov.Warnings {
		fmt.Fprintf(stderr, "gc start: warning: %s\n", w) //nolint:errcheck // best-effort stderr
	}
	if err := config.ValidateAgents(newCfg.Agents); err != nil {
		return nil, fmt.Errorf("validating agents: %w", err)
	}
	newName := newCfg.Workspace.Name
	if newName == "" {
		newName = filepath.Base(filepath.Dir(tomlPath))
	}
	if newName != lockedCityName {
		return nil, fmt.Errorf("workspace.name changed from %q to %q (restart controller to apply)", lockedCityName, newName)
	}
	rev := config.Revision(fsys.OSFS{}, prov, newCfg, cityRoot)
	return &reloadResult{Cfg: newCfg, Prov: prov, Revision: rev}, nil
}

// gracefulStopAll performs two-pass graceful shutdown:
//  1. Send Interrupt (Ctrl-C) to all sessions
//  2. Wait shutdown_timeout
//  3. Stop (force-kill) any survivors
func gracefulStopAll(
	names []string,
	sp runtime.Provider,
	timeout time.Duration,
	rec events.Recorder,
	stdout, stderr io.Writer,
) {
	if timeout <= 0 || len(names) == 0 {
		// Immediate kill (no grace period).
		for _, name := range names {
			if err := sp.Stop(name); err != nil {
				fmt.Fprintf(stderr, "gc stop: stopping %s: %v\n", name, err) //nolint:errcheck // best-effort stderr
			} else {
				fmt.Fprintf(stdout, "Stopped agent '%s'\n", name) //nolint:errcheck // best-effort stdout
				rec.Record(events.Event{
					Type: events.AgentStopped, Actor: "gc", Subject: name,
				})
			}
		}
		return
	}

	// Pass 1: interrupt all.
	for _, name := range names {
		_ = sp.Interrupt(name) // best-effort
	}
	fmt.Fprintf(stdout, "Sent interrupt to %d agent(s), waiting %s...\n", //nolint:errcheck // best-effort stdout
		len(names), timeout)

	// Poll until all agents exit or timeout expires (avoid sleeping full duration).
	pollInterval := 500 * time.Millisecond
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allExited := true
		for _, name := range names {
			if sp.IsRunning(name) {
				allExited = false
				break
			}
		}
		if allExited {
			break
		}
		remaining := time.Until(deadline)
		if remaining < pollInterval {
			time.Sleep(remaining)
		} else {
			time.Sleep(pollInterval)
		}
	}

	// Pass 2: kill survivors.
	for _, name := range names {
		if !sp.IsRunning(name) {
			fmt.Fprintf(stdout, "Agent '%s' exited gracefully\n", name) //nolint:errcheck // best-effort stdout
			rec.Record(events.Event{
				Type: events.AgentStopped, Actor: "gc", Subject: name,
			})
			continue
		}
		if err := sp.Stop(name); err != nil {
			fmt.Fprintf(stderr, "gc stop: stopping %s: %v\n", name, err) //nolint:errcheck // best-effort stderr
		} else {
			fmt.Fprintf(stdout, "Stopped agent '%s'\n", name) //nolint:errcheck // best-effort stdout
			rec.Record(events.Event{
				Type: events.AgentStopped, Actor: "gc", Subject: name,
			})
		}
	}
}

// controllerLoop is a compatibility shim that wraps CityRuntime.run().
// Tests and runController use this entry point to preserve the existing
// call signature during the Phase 0 extraction. It will be removed once
// callers migrate to CityRuntime directly.
//
//nolint:unparam // compatibility shim — many params are nil in tests but varied in runController
func controllerLoop(
	ctx context.Context,
	interval time.Duration, // overrides cfg patrol interval when non-zero (used by tests)
	cfg *config.City,
	cityName string,
	tomlPath string,
	watchDirs []string,
	buildFn func(*config.City, runtime.Provider) []agent.Agent,
	sp runtime.Provider,
	rops reconcileOps,
	dops drainOps,
	ct crashTracker,
	it idleTracker,
	wg wispGC,
	ad automationDispatcher,
	rec events.Recorder,
	poolSessions map[string]time.Duration,
	poolDeathHandlers map[string]poolDeathInfo,
	suspendedNames map[string]bool,
	cs *controllerState, // nil when API disabled
	stdout, stderr io.Writer,
) {
	var cityPath string
	if tomlPath != "" {
		cityPath = filepath.Dir(tomlPath)
	}
	// Allow callers (tests) to override the patrol interval without
	// mutating the caller's config.
	loopCfg := cfg
	if interval > 0 {
		cfgCopy := *cfg
		cfgCopy.Daemon.PatrolInterval = interval.String()
		loopCfg = &cfgCopy
	}
	cr := &CityRuntime{
		cityPath:          cityPath,
		cityName:          cityName,
		tomlPath:          tomlPath,
		watchDirs:         watchDirs,
		cfg:               loopCfg,
		sp:                sp,
		buildFn:           buildFn,
		rops:              rops,
		dops:              dops,
		ct:                ct,
		it:                it,
		wg:                wg,
		ad:                ad,
		rec:               rec,
		cs:                cs,
		poolSessions:      poolSessions,
		poolDeathHandlers: poolDeathHandlers,
		suspendedNames:    suspendedNames,
		logPrefix:         "gc start",
		stdout:            stdout,
		stderr:            stderr,
	}
	cr.run(ctx)
}

// shortRev returns the first 12 characters of a revision hash.
func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// configReloadSummary returns a human-readable summary of what changed
// between config reloads.
func configReloadSummary(oldAgents, oldRigs, newAgents, newRigs int) string {
	var parts []string
	switch {
	case newAgents > oldAgents:
		parts = append(parts, fmt.Sprintf("%d agents (+%d)", newAgents, newAgents-oldAgents))
	case newAgents < oldAgents:
		parts = append(parts, fmt.Sprintf("%d agents (-%d)", newAgents, oldAgents-newAgents))
	default:
		parts = append(parts, fmt.Sprintf("%d agents", newAgents))
	}
	switch {
	case newRigs > oldRigs:
		parts = append(parts, fmt.Sprintf("%d rigs (+%d)", newRigs, newRigs-oldRigs))
	case newRigs < oldRigs:
		parts = append(parts, fmt.Sprintf("%d rigs (-%d)", newRigs, oldRigs-newRigs))
	default:
		parts = append(parts, fmt.Sprintf("%d rigs", newRigs))
	}
	return strings.Join(parts, ", ")
}

// runController runs the persistent controller loop. It acquires a lock,
// opens a control socket, runs the reconciliation loop, and on shutdown
// stops all agents. Returns an exit code. initialWatchDirs is the set of
// directories to watch for config changes (from initial provenance).
func runController(
	cityPath string,
	tomlPath string,
	cfg *config.City,
	buildFn func(*config.City, runtime.Provider) []agent.Agent,
	sp runtime.Provider,
	dops drainOps,
	poolSessions map[string]time.Duration,
	poolDeathHandlers map[string]poolDeathInfo,
	initialWatchDirs []string,
	rec events.Recorder,
	eventProv events.Provider,
	stdout, stderr io.Writer,
) int {
	lock, err := acquireControllerLock(cityPath)
	if err != nil {
		fmt.Fprintf(stderr, "gc start: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	defer lock.Close() //nolint:errcheck // best-effort cleanup

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handler: SIGINT/SIGTERM → cancel.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	sockPath := filepath.Join(cityPath, ".gc", "controller.sock")
	lis, err := startControllerSocket(cityPath, cancel)
	if err != nil {
		fmt.Fprintf(stderr, "gc start: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	defer lis.Close()         //nolint:errcheck // best-effort cleanup
	defer os.Remove(sockPath) //nolint:errcheck // best-effort cleanup

	// Generate and write the controller token for convergence loop ACL.
	// The token is written to .gc/controller.token and kept in memory only.
	// It is NOT set in os.Environ() to prevent leaking to child processes
	// (exec scripts, git commands, automation hooks). Future waves that need
	// the token from controller code use convergence.ReadToken() or pass it
	// explicitly through function parameters.
	controllerToken, err := convergence.GenerateToken()
	if err != nil {
		fmt.Fprintf(stderr, "gc start: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	_ = controllerToken // available for future waves via function parameters
	if err := convergence.WriteToken(cityPath, controllerToken); err != nil {
		fmt.Fprintf(stderr, "gc start: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	defer convergence.RemoveToken(cityPath) //nolint:errcheck // best-effort cleanup

	cityName := cfg.Workspace.Name
	if cityName == "" {
		cityName = filepath.Base(cityPath)
	}
	rec.Record(events.Event{Type: events.ControllerStarted, Actor: "gc"})
	telemetry.RecordControllerLifecycle(context.Background(), "started")
	fmt.Fprintln(stdout, "Controller started.") //nolint:errcheck // best-effort stdout

	cr := newCityRuntime(CityRuntimeParams{
		CityPath:          cityPath,
		CityName:          cityName,
		TomlPath:          tomlPath,
		WatchDirs:         initialWatchDirs,
		Cfg:               cfg,
		SP:                sp,
		BuildFn:           buildFn,
		Dops:              dops,
		Rec:               rec,
		PoolSessions:      poolSessions,
		PoolDeathHandlers: poolDeathHandlers,
		Stdout:            stdout,
		Stderr:            stderr,
	})

	// Start API server if configured.
	if cfg.API.Port > 0 {
		cs := newControllerState(cfg, sp, eventProv, cityName, cityPath)
		cs.ct = cr.crashTrack()
		cr.setControllerState(cs)
		bind := cfg.API.BindOrDefault()
		nonLocal := bind != "127.0.0.1" && bind != "localhost" && bind != "::1"
		var apiSrv *api.Server
		if nonLocal {
			apiSrv = api.NewReadOnly(cs)
			fmt.Fprintf(stderr, "api: binding to %s — mutation endpoints disabled (non-localhost)\n", bind) //nolint:errcheck
		} else {
			apiSrv = api.New(cs)
		}
		addr := net.JoinHostPort(bind, strconv.Itoa(cfg.API.Port))
		apiLis, apiErr := net.Listen("tcp", addr)
		if apiErr != nil {
			fmt.Fprintf(stderr, "api: WARNING: listen %s failed: %v — continuing without API server\n", addr, apiErr) //nolint:errcheck // best-effort stderr
		} else {
			go func() {
				if err := apiSrv.Serve(apiLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
					fmt.Fprintf(stderr, "api: %v\n", err) //nolint:errcheck // best-effort stderr
				}
			}()
			defer func() {
				shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				apiSrv.Shutdown(shutCtx) //nolint:errcheck // best-effort cleanup
			}()
			fmt.Fprintf(stdout, "API server listening on http://%s\n", addr) //nolint:errcheck // best-effort stdout
		}
	}

	runPoolOnBoot(cfg, cityPath, shellScaleCheck, stderr)
	cr.run(ctx)
	cr.shutdown()

	rec.Record(events.Event{Type: events.ControllerStopped, Actor: "gc"})
	telemetry.RecordControllerLifecycle(context.Background(), "stopped")
	fmt.Fprintln(stdout, "Controller stopped.") //nolint:errcheck // best-effort stdout
	return 0
}
