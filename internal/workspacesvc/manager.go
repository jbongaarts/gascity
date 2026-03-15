package workspacesvc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/supervisor"
)

// Manager owns the lifecycle and status projection for workspace services.
type Manager struct {
	rt RuntimeContext

	opMu    sync.Mutex
	mu      sync.RWMutex
	entries map[string]*entry
	closed  bool
}

type entry struct {
	spec   config.Service
	status Status
	inst   Instance
}

type closeTarget struct {
	name string
	inst Instance
}

// NewManager creates a service manager bound to one workspace runtime.
func NewManager(rt RuntimeContext) *Manager {
	return &Manager{
		rt:      rt,
		entries: make(map[string]*entry),
	}
}

// Reload reconciles the manager against the current config snapshot.
func (m *Manager) Reload() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	cfg := m.rt.Config()
	m.mu.RLock()
	oldEntries := make(map[string]*entry, len(m.entries))
	for name, e := range m.entries {
		oldEntries[name] = e
	}
	m.mu.RUnlock()
	next := make(map[string]*entry, len(cfg.Services))
	reused := make(map[string]bool, len(oldEntries))
	now := time.Now().UTC()

	for _, svc := range cfg.Services {
		base := baseStatus(m.rt.Config(), m.rt.PublicationConfig(), svc, now)
		stateRoot, err := ensureStateRoot(m.rt.CityPath(), svc)
		base.StateRoot = stateRoot
		if err != nil {
			base.ServiceState = "degraded"
			base.LocalState = "config_error"
			base.StateReason = err.Error()
			next[svc.Name] = &entry{spec: svc, status: base}
			continue
		}
		if existing, ok := oldEntries[svc.Name]; ok && existing.inst != nil && reflect.DeepEqual(existing.spec, svc) {
			existing.status = mergeStatus(base, existing.inst.Status())
			next[svc.Name] = existing
			reused[svc.Name] = true
			continue
		}

		switch svc.KindOrDefault() {
		case "workflow":
			factory := lookupWorkflowContract(svc.Workflow.Contract)
			if factory == nil {
				base.ServiceState = "degraded"
				base.LocalState = "config_error"
				base.StateReason = fmt.Sprintf("unsupported workflow contract %q", svc.Workflow.Contract)
				next[svc.Name] = &entry{spec: svc, status: base}
				continue
			}
			inst, err := factory(m.rt, svc)
			if err != nil {
				base.ServiceState = "degraded"
				base.LocalState = "config_error"
				base.StateReason = err.Error()
				next[svc.Name] = &entry{spec: svc, status: base}
				continue
			}
			base = mergeStatus(base, inst.Status())
			next[svc.Name] = &entry{spec: svc, status: base, inst: inst}
		case "proxy_process":
			inst, err := newProxyProcessInstance(m.rt, svc)
			if err != nil {
				base.ServiceState = "degraded"
				base.LocalState = "config_error"
				base.StateReason = err.Error()
				next[svc.Name] = &entry{spec: svc, status: base}
				continue
			}
			base = mergeStatus(base, inst.Status())
			next[svc.Name] = &entry{spec: svc, status: base, inst: inst}
		default:
			base.ServiceState = "degraded"
			base.LocalState = "config_error"
			base.StateReason = fmt.Sprintf("unsupported service kind %q", svc.Kind)
			next[svc.Name] = &entry{spec: svc, status: base}
		}
	}

	m.mu.Lock()
	m.entries = next
	m.mu.Unlock()

	var firstErr error
	for name, e := range oldEntries {
		if reused[name] {
			continue
		}
		if e.inst != nil {
			if err := e.inst.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// Tick runs one service reconciliation pass.
func (m *Manager) Tick(ctx context.Context, now time.Time) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return
	}
	entries := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	m.mu.RUnlock()

	for _, e := range entries {
		if e.inst == nil {
			continue
		}
		e.inst.Tick(ctx, now)
		status := mergeStatus(baseStatus(m.rt.Config(), m.rt.PublicationConfig(), e.spec, now), e.inst.Status())
		m.mu.Lock()
		if cur, ok := m.entries[e.spec.Name]; ok {
			cur.status = status
		}
		m.mu.Unlock()
	}
}

// Close closes all runtime service instances.
func (m *Manager) Close() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	now := time.Now().UTC()
	m.mu.Lock()
	m.closed = true
	targets := make([]closeTarget, 0, len(m.entries))
	for name, e := range m.entries {
		if e.inst != nil {
			targets = append(targets, closeTarget{name: name, inst: e.inst})
			e.status.ServiceState = "stopping"
			e.status.LocalState = "stopping"
			e.status.StateReason = "service_closing"
			e.status.UpdatedAt = now
			continue
		}
		e.status.ServiceState = "stopped"
		e.status.LocalState = "stopped"
		e.status.StateReason = "service_closed"
		e.status.UpdatedAt = now
	}
	m.mu.Unlock()
	if len(targets) == 0 {
		return nil
	}

	var firstErr error
	results := make(map[string]error, len(targets))
	for _, target := range targets {
		if err := target.inst.Close(); err != nil {
			results[target.name] = err
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	now = time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, target := range targets {
		e, ok := m.entries[target.name]
		if !ok {
			continue
		}
		if err := results[target.name]; err != nil {
			// Retain the instance so a subsequent Close() call can retry.
			e.inst = target.inst
			e.status.ServiceState = "degraded"
			e.status.LocalState = "close_error"
			e.status.StateReason = err.Error()
			e.status.UpdatedAt = now
			continue
		}
		e.inst = nil
		e.status.ServiceState = "stopped"
		e.status.LocalState = "stopped"
		e.status.StateReason = "service_closed"
		e.status.UpdatedAt = now
	}
	return firstErr
}

// List returns all current service statuses sorted by name.
func (m *Manager) List() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Status, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.status)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ServiceName < out[j].ServiceName
	})
	return out
}

// Get returns one current service status by name.
func (m *Manager) Get(name string) (Status, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	e, ok := m.entries[name]
	if !ok {
		return Status{}, false
	}
	return e.status, true
}

// AuthorizeAndServeHTTP routes /svc/{name}/... requests to the matching
// service instance using one registry snapshot for authorization and dispatch.
func (m *Manager) AuthorizeAndServeHTTP(name string, w http.ResponseWriter, r *http.Request, authorize func(Status) bool) bool {
	subpath, ok := serviceSubpath(r.URL.Path, name)
	if !ok {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	e, ok := m.entries[name]
	if !ok {
		return false
	}
	if authorize != nil && !authorize(e.status) {
		return true
	}
	if m.closed || e.inst == nil {
		return false
	}
	return e.inst.HandleHTTP(w, r, subpath)
}

// ServeHTTP routes /svc/{name}/... requests to the matching service instance.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/svc/")
	if path == r.URL.Path || path == "" {
		return false
	}
	name := path
	if i := strings.IndexByte(path, '/'); i >= 0 {
		name = path[:i]
	}
	return m.AuthorizeAndServeHTTP(name, w, r, nil)
}

func serviceSubpath(path, name string) (string, bool) {
	mountPath := "/svc/" + name
	switch {
	case name == "":
		return "", false
	case path == mountPath:
		return "/", true
	case strings.HasPrefix(path, mountPath+"/"):
		return path[len(mountPath):], true
	default:
		return "", false
	}
}

func baseStatus(cfg *config.City, pubCfg supervisor.PublicationConfig, svc config.Service, now time.Time) Status {
	visibility := svc.PublicationVisibilityOrDefault()
	status := Status{
		ServiceName:      svc.Name,
		Kind:             svc.KindOrDefault(),
		WorkflowContract: svc.Workflow.Contract,
		MountPath:        svc.MountPathOrDefault(),
		PublishMode:      svc.PublishModeOrDefault(),
		Visibility:       visibility,
		Hostname:         svc.PublicationHostnameOrDefault(),
		StateRoot:        svc.StateRootOrDefault(),
		ServiceState:     "ready",
		State:            "ready",
		LocalState:       "ready",
		PublicationState: "private",
		UpdatedAt:        now,
		AllowWebSockets:  svc.Publication.AllowWebSockets,
	}

	switch visibility {
	case "private":
		status.PublicationState = "private"
	default:
		publishedURL, publicationReason := derivePublishedURL(pubCfg, workspaceName(cfg), svc)
		if publishedURL != "" {
			status.PublicURL = publishedURL
			status.URL = publishedURL
			status.PublicationState = "published"
			status.StateReason = publicationReason
			status.Reason = publicationReason
			break
		}
		if status.PublishMode == "direct" {
			if baseURL := directBaseURL(cfg); baseURL != "" {
				status.PublicURL = strings.TrimRight(baseURL, "/") + status.MountPath
				status.URL = status.PublicURL
				status.PublicationState = "direct"
				status.StateReason = "route_active"
				status.Reason = "route_active"
				break
			}
			status.PublicationState = "blocked"
			status.StateReason = "direct_base_url_unavailable"
			status.Reason = status.StateReason
			break
		}
		status.PublicationState = "blocked"
		if publicationReason != "" {
			status.StateReason = publicationReason
			status.Reason = publicationReason
		} else {
			status.StateReason = "publication_unavailable"
			status.Reason = status.StateReason
		}
	}

	return status
}

func mergeStatus(base, override Status) Status {
	if override.ServiceName != "" {
		base.ServiceName = override.ServiceName
	}
	if override.Kind != "" {
		base.Kind = override.Kind
	}
	if override.WorkflowContract != "" {
		base.WorkflowContract = override.WorkflowContract
	}
	if override.MountPath != "" {
		base.MountPath = override.MountPath
	}
	if override.PublishMode != "" {
		base.PublishMode = override.PublishMode
	}
	if override.Visibility != "" {
		base.Visibility = override.Visibility
	}
	if override.Hostname != "" {
		base.Hostname = override.Hostname
	}
	if override.StateRoot != "" {
		base.StateRoot = override.StateRoot
	}
	if override.PublicURL != "" {
		base.PublicURL = override.PublicURL
		if base.URL == "" {
			base.URL = override.PublicURL
		}
	}
	if override.URL != "" {
		base.URL = override.URL
		if base.PublicURL == "" {
			base.PublicURL = override.URL
		}
	}
	if override.ServiceState != "" {
		base.ServiceState = override.ServiceState
		if base.State == "" {
			base.State = override.ServiceState
		}
	}
	if override.State != "" {
		base.State = override.State
	}
	if override.LocalState != "" {
		base.LocalState = override.LocalState
	}
	if override.PublicationState != "" {
		base.PublicationState = override.PublicationState
	}
	if override.StateReason != "" {
		base.StateReason = override.StateReason
		if base.Reason == "" {
			base.Reason = override.StateReason
		}
	}
	if override.Reason != "" {
		base.Reason = override.Reason
	}
	base.AllowWebSockets = base.AllowWebSockets || override.AllowWebSockets
	if !override.UpdatedAt.IsZero() {
		base.UpdatedAt = override.UpdatedAt
	}
	return base
}

func ensureStateRoot(cityPath string, svc config.Service) (string, error) {
	root := svc.StateRootOrDefault()
	absRoot := root
	if !filepath.IsAbs(absRoot) {
		absRoot = filepath.Join(cityPath, absRoot)
	}
	for _, dir := range []struct {
		path string
		mode os.FileMode
	}{
		{absRoot, 0o750},
		{filepath.Join(absRoot, "data"), 0o750},
		{filepath.Join(absRoot, "run"), 0o750},
		{filepath.Join(absRoot, "logs"), 0o750},
		{filepath.Join(absRoot, "secrets"), 0o700},
	} {
		if err := os.MkdirAll(dir.path, dir.mode); err != nil {
			return root, err
		}
		if err := os.Chmod(dir.path, dir.mode); err != nil {
			return root, err
		}
	}
	return root, nil
}

func directBaseURL(cfg *config.City) string {
	if cfg == nil || cfg.API.Port <= 0 {
		return ""
	}
	bind := cfg.API.BindOrDefault()
	switch bind {
	case "", "0.0.0.0", "::", "[::]":
		return ""
	}
	return "http://" + net.JoinHostPort(bind, strconv.Itoa(cfg.API.Port))
}

func workspaceName(cfg *config.City) string {
	if cfg == nil {
		return ""
	}
	if v := strings.TrimSpace(cfg.Workspace.Name); v != "" {
		return v
	}
	return ""
}
