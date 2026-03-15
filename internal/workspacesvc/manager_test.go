package workspacesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/supervisor"
)

type testRuntime struct {
	cityPath string
	cityName string
	cfg      *config.City
	sp       runtime.Provider
	store    beads.Store
	pubCfg   supervisor.PublicationConfig
}

func (t *testRuntime) CityPath() string     { return t.cityPath }
func (t *testRuntime) CityName() string     { return t.cityName }
func (t *testRuntime) Config() *config.City { return t.cfg }
func (t *testRuntime) PublicationConfig() supervisor.PublicationConfig {
	return t.pubCfg
}
func (t *testRuntime) SessionProvider() runtime.Provider { return t.sp }
func (t *testRuntime) BeadStore(string) beads.Store      { return t.store }
func (t *testRuntime) Poke()                             {}

type testInstance struct {
	status     Status
	handleHTTP func(w http.ResponseWriter, r *http.Request, subpath string) bool
	closed     bool
	closeErr   error
}

func (t *testInstance) Status() Status { return t.status }

func (t *testInstance) HandleHTTP(w http.ResponseWriter, r *http.Request, subpath string) bool {
	if t.handleHTTP == nil {
		return false
	}
	return t.handleHTTP(w, r, subpath)
}

func (t *testInstance) Tick(context.Context, time.Time) {}

func (t *testInstance) Close() error {
	t.closed = true
	return t.closeErr
}

func uniqueContract(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test.%s.%d", strings.ReplaceAll(t.Name(), "/", "."), time.Now().UnixNano())
}

func registerWorkflowContractForTest(t *testing.T, contract string, factory WorkflowFactory) {
	t.Helper()
	RegisterWorkflowContract(contract, factory)
	t.Cleanup(func() {
		workflowFactoriesMu.Lock()
		delete(workflowFactories, contract)
		workflowFactoriesMu.Unlock()
	})
}

func TestManagerReloadWorkflowServiceCreatesStateRootAndDirectURL(t *testing.T) {
	contract := uniqueContract(t)
	registerWorkflowContractForTest(t, contract, func(_ RuntimeContext, svc config.Service) (Instance, error) {
		return &testInstance{
			status: Status{
				ServiceName:      svc.Name,
				WorkflowContract: contract,
				ServiceState:     "ready",
				LocalState:       "ready",
			},
		}, nil
	})

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			API: config.APIConfig{Bind: "127.0.0.1", Port: 9443},
			Services: []config.Service{{
				Name:        "review-intake",
				PublishMode: "direct",
				Workflow:    config.ServiceWorkflowConfig{Contract: contract},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	status, ok := mgr.Get("review-intake")
	if !ok {
		t.Fatal("service status missing")
	}
	if status.PublicURL != "http://127.0.0.1:9443/svc/review-intake" {
		t.Errorf("PublicURL = %q, want %q", status.PublicURL, "http://127.0.0.1:9443/svc/review-intake")
	}
	if status.PublicationState != "direct" {
		t.Errorf("PublicationState = %q, want direct", status.PublicationState)
	}

	stateRoot := filepath.Join(rt.cityPath, status.StateRoot)
	for _, want := range []string{
		stateRoot,
		filepath.Join(stateRoot, "data"),
		filepath.Join(stateRoot, "run"),
		filepath.Join(stateRoot, "logs"),
		filepath.Join(stateRoot, "secrets"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("expected %s to exist: %v", want, err)
		}
	}
}

func TestManagerReloadWorkflowServicePublishesWithSupervisorConfig(t *testing.T) {
	contract := uniqueContract(t)
	registerWorkflowContractForTest(t, contract, func(_ RuntimeContext, svc config.Service) (Instance, error) {
		return &testInstance{
			status: Status{
				ServiceName:      svc.Name,
				WorkflowContract: contract,
				ServiceState:     "ready",
				LocalState:       "ready",
			},
		}, nil
	})

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Workspace: config.Workspace{Name: "demo-app"},
			Services: []config.Service{{
				Name: "review-intake",
				Publication: config.ServicePublicationConfig{
					Visibility: "public",
				},
				Workflow: config.ServiceWorkflowConfig{Contract: contract},
			}},
		},
		pubCfg: supervisor.PublicationConfig{
			Provider:         "hosted",
			TenantSlug:       "acme",
			PublicBaseDomain: "apps.example.com",
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	status, ok := mgr.Get("review-intake")
	if !ok {
		t.Fatal("service status missing")
	}
	if !strings.HasPrefix(status.PublicURL, "https://review-intake--demo-app--acme--") {
		t.Fatalf("PublicURL = %q, want review-intake--demo-app--acme prefix", status.PublicURL)
	}
	if status.PublicationState != "published" {
		t.Errorf("PublicationState = %q, want published", status.PublicationState)
	}
	if status.Visibility != "public" {
		t.Errorf("Visibility = %q, want public", status.Visibility)
	}
	if status.Reason != "route_active" {
		t.Errorf("Reason = %q, want route_active", status.Reason)
	}
}

func TestManagerReloadWorkflowServiceBlocksPublicationWithoutSupervisor(t *testing.T) {
	contract := uniqueContract(t)
	registerWorkflowContractForTest(t, contract, func(_ RuntimeContext, svc config.Service) (Instance, error) {
		return &testInstance{
			status: Status{
				ServiceName:      svc.Name,
				WorkflowContract: contract,
				ServiceState:     "ready",
				LocalState:       "ready",
			},
		}, nil
	})

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Workspace: config.Workspace{Name: "demo-app"},
			Services: []config.Service{{
				Name: "review-intake",
				Publication: config.ServicePublicationConfig{
					Visibility: "public",
				},
				Workflow: config.ServiceWorkflowConfig{Contract: contract},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	status, ok := mgr.Get("review-intake")
	if !ok {
		t.Fatal("service status missing")
	}
	if status.PublicURL != "" {
		t.Fatalf("PublicURL = %q, want empty", status.PublicURL)
	}
	if status.PublicationState != "blocked" {
		t.Errorf("PublicationState = %q, want blocked", status.PublicationState)
	}
	if status.Reason != "publication_requires_supervisor" {
		t.Errorf("Reason = %q, want publication_requires_supervisor", status.Reason)
	}
}

func TestManagerReloadUnsupportedContractDegradesService(t *testing.T) {
	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Services: []config.Service{{
				Name:     "review-intake",
				Workflow: config.ServiceWorkflowConfig{Contract: "missing.contract"},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	status, ok := mgr.Get("review-intake")
	if !ok {
		t.Fatal("service status missing")
	}
	if status.ServiceState != "degraded" {
		t.Errorf("ServiceState = %q, want degraded", status.ServiceState)
	}
	if status.LocalState != "config_error" {
		t.Errorf("LocalState = %q, want config_error", status.LocalState)
	}
	if !strings.Contains(status.StateReason, "unsupported workflow contract") {
		t.Errorf("StateReason = %q, want unsupported workflow contract", status.StateReason)
	}
}

func TestManagerReloadReusesUnchangedInstances(t *testing.T) {
	contract := uniqueContract(t)
	first := &testInstance{}
	callCount := 0
	registerWorkflowContractForTest(t, contract, func(_ RuntimeContext, svc config.Service) (Instance, error) {
		callCount++
		first.status = Status{
			ServiceName:      svc.Name,
			WorkflowContract: contract,
			ServiceState:     "ready",
			LocalState:       "ready",
		}
		return first, nil
	})

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Services: []config.Service{{
				Name:     "review-intake",
				Workflow: config.ServiceWorkflowConfig{Contract: contract},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("first Reload: %v", err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatalf("second Reload: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("callCount = %d, want 1", callCount)
	}
	if first.closed {
		t.Fatal("expected unchanged instance to remain open")
	}
}

func TestManagerReloadClosesChangedInstances(t *testing.T) {
	firstContract := uniqueContract(t)
	secondContract := uniqueContract(t)
	first := &testInstance{}
	second := &testInstance{}
	registerWorkflowContractForTest(t, firstContract, func(_ RuntimeContext, svc config.Service) (Instance, error) {
		first.status = Status{
			ServiceName:      svc.Name,
			WorkflowContract: firstContract,
			ServiceState:     "ready",
			LocalState:       "ready",
		}
		return first, nil
	})
	registerWorkflowContractForTest(t, secondContract, func(_ RuntimeContext, svc config.Service) (Instance, error) {
		second.status = Status{
			ServiceName:      svc.Name,
			WorkflowContract: secondContract,
			ServiceState:     "ready",
			LocalState:       "ready",
		}
		return second, nil
	})

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Services: []config.Service{{
				Name:     "review-intake",
				Workflow: config.ServiceWorkflowConfig{Contract: firstContract},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("first Reload: %v", err)
	}
	rt.cfg.Services[0].Workflow.Contract = secondContract
	if err := mgr.Reload(); err != nil {
		t.Fatalf("second Reload: %v", err)
	}
	if !first.closed {
		t.Fatal("expected first instance to be closed on changed reload")
	}
	if second.closed {
		t.Fatal("expected replacement instance to remain open")
	}
}

func TestManagerServeHTTPRoutesToWorkflowInstance(t *testing.T) {
	contract := uniqueContract(t)
	registerWorkflowContractForTest(t, contract, func(_ RuntimeContext, svc config.Service) (Instance, error) {
		return &testInstance{
			status: Status{
				ServiceName:      svc.Name,
				WorkflowContract: contract,
				ServiceState:     "ready",
				LocalState:       "ready",
			},
			handleHTTP: func(w http.ResponseWriter, r *http.Request, subpath string) bool {
				if subpath != "/hooks/github" {
					t.Errorf("subpath = %q, want /hooks/github", subpath)
				}
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(r.Method + " " + subpath))
				return true
			},
		}, nil
	})

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Services: []config.Service{{
				Name:     "review-intake",
				Workflow: config.ServiceWorkflowConfig{Contract: contract},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/svc/review-intake/hooks/github", nil)
	rec := httptest.NewRecorder()
	if ok := mgr.ServeHTTP(rec, req); !ok {
		t.Fatal("ServeHTTP returned false, want true")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if strings.TrimSpace(rec.Body.String()) != "POST /hooks/github" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "POST /hooks/github")
	}
}

func TestManagerServeHTTPUsesBuiltinHealthzWorkflow(t *testing.T) {
	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Services: []config.Service{{
				Name:     "healthz",
				Workflow: config.ServiceWorkflowConfig{Contract: HealthzWorkflowContract},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/svc/healthz/healthz", nil)
	rec := httptest.NewRecorder()
	if ok := mgr.ServeHTTP(rec, req); !ok {
		t.Fatal("ServeHTTP returned false, want true")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["service"] != "healthz" {
		t.Fatalf("service = %#v, want healthz", got["service"])
	}
	if got["contract"] != HealthzWorkflowContract {
		t.Fatalf("contract = %#v, want %s", got["contract"], HealthzWorkflowContract)
	}
}

func TestManagerCloseStopsRoutingAndProjectsStoppedStatus(t *testing.T) {
	contract := uniqueContract(t)
	inst := &testInstance{
		status: Status{
			ServiceState: "ready",
			LocalState:   "ready",
		},
		handleHTTP: func(http.ResponseWriter, *http.Request, string) bool {
			t.Fatal("closed service should not receive requests")
			return false
		},
	}
	registerWorkflowContractForTest(t, contract, func(_ RuntimeContext, svc config.Service) (Instance, error) {
		inst.status.ServiceName = svc.Name
		inst.status.WorkflowContract = contract
		return inst, nil
	})

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Services: []config.Service{{
				Name:     "review-intake",
				Workflow: config.ServiceWorkflowConfig{Contract: contract},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !inst.closed {
		t.Fatal("expected instance to be closed")
	}

	status, ok := mgr.Get("review-intake")
	if !ok {
		t.Fatal("service status missing after close")
	}
	if status.ServiceState != "stopped" {
		t.Fatalf("ServiceState = %q, want stopped", status.ServiceState)
	}
	if status.LocalState != "stopped" {
		t.Fatalf("LocalState = %q, want stopped", status.LocalState)
	}
	if status.StateReason != "service_closed" {
		t.Fatalf("StateReason = %q, want service_closed", status.StateReason)
	}

	req := httptest.NewRequest(http.MethodGet, "/svc/review-intake/healthz", nil)
	rec := httptest.NewRecorder()
	if ok := mgr.AuthorizeAndServeHTTP("review-intake", rec, req, nil); ok {
		t.Fatal("AuthorizeAndServeHTTP returned true after close, want false")
	}
}

func TestManagerCloseProjectsCloseErrorWithoutRouting(t *testing.T) {
	contract := uniqueContract(t)
	inst := &testInstance{
		status: Status{
			ServiceState: "ready",
			LocalState:   "ready",
		},
		closeErr: errors.New("close failed"),
	}
	registerWorkflowContractForTest(t, contract, func(_ RuntimeContext, svc config.Service) (Instance, error) {
		inst.status.ServiceName = svc.Name
		inst.status.WorkflowContract = contract
		return inst, nil
	})

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Services: []config.Service{{
				Name:     "review-intake",
				Workflow: config.ServiceWorkflowConfig{Contract: contract},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if err := mgr.Close(); err == nil {
		t.Fatal("Close returned nil, want error")
	}
	if !inst.closed {
		t.Fatal("expected instance close attempt")
	}

	status, ok := mgr.Get("review-intake")
	if !ok {
		t.Fatal("service status missing after failed close")
	}
	if status.ServiceState != "degraded" {
		t.Fatalf("ServiceState = %q, want degraded", status.ServiceState)
	}
	if status.LocalState != "close_error" {
		t.Fatalf("LocalState = %q, want close_error", status.LocalState)
	}
	if status.StateReason != "close failed" {
		t.Fatalf("StateReason = %q, want close failed", status.StateReason)
	}

	req := httptest.NewRequest(http.MethodGet, "/svc/review-intake/healthz", nil)
	rec := httptest.NewRecorder()
	if ok := mgr.AuthorizeAndServeHTTP("review-intake", rec, req, nil); ok {
		t.Fatal("AuthorizeAndServeHTTP returned true after failed close, want false")
	}
}

func TestManagerCloseRetriesFailedInstance(t *testing.T) {
	contract := uniqueContract(t)
	inst := &testInstance{
		status: Status{
			ServiceState: "ready",
			LocalState:   "ready",
		},
		closeErr: errors.New("close failed"),
	}
	registerWorkflowContractForTest(t, contract, func(_ RuntimeContext, svc config.Service) (Instance, error) {
		inst.status.ServiceName = svc.Name
		inst.status.WorkflowContract = contract
		return inst, nil
	})

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Services: []config.Service{{
				Name:     "review-intake",
				Workflow: config.ServiceWorkflowConfig{Contract: contract},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if err := mgr.Close(); err == nil {
		t.Fatal("first Close returned nil, want error")
	}

	inst.closeErr = nil
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	status, ok := mgr.Get("review-intake")
	if !ok {
		t.Fatal("service status missing after retry")
	}
	if status.ServiceState != "stopped" {
		t.Fatalf("ServiceState = %q, want stopped", status.ServiceState)
	}
	if status.LocalState != "stopped" {
		t.Fatalf("LocalState = %q, want stopped", status.LocalState)
	}
	if status.StateReason != "service_closed" {
		t.Fatalf("StateReason = %q, want service_closed", status.StateReason)
	}
}

func TestManagerAuthorizeAndServeHTTPRunsAuthorizationWithoutInstance(t *testing.T) {
	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Services: []config.Service{{
				Name:     "review-intake",
				Workflow: config.ServiceWorkflowConfig{Contract: "missing.contract"},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/svc/review-intake/hooks/github", nil)
	called := false
	rec := httptest.NewRecorder()
	if ok := mgr.AuthorizeAndServeHTTP("review-intake", rec, req, func(Status) bool {
		called = true
		return false
	}); !ok {
		t.Fatal("AuthorizeAndServeHTTP returned false, want true when authorization handled the request")
	}
	if !called {
		t.Fatal("expected authorization callback to run without an active instance")
	}
}
