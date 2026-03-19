// Package workspacesvc provides the generic workspace-owned service runtime.
package workspacesvc

import (
	"context"
	"net/http"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/supervisor"
)

// Status is the API-facing state projection for one workspace service.
type Status struct {
	ServiceName      string `json:"service_name"`
	Kind             string `json:"kind,omitempty"`
	WorkflowContract string `json:"workflow_contract,omitempty"`
	MountPath        string `json:"mount_path"`
	PublishMode      string `json:"publish_mode"`
	Visibility       string `json:"visibility,omitempty"`
	Hostname         string `json:"hostname,omitempty"`
	StateRoot        string `json:"state_root"`
	// URL is the published-service URL.
	URL string `json:"url,omitempty"`
	// State is the service state.
	State            string `json:"state,omitempty"`
	LocalState       string `json:"local_state"`
	PublicationState string `json:"publication_state"`
	// Reason is the human/actionable reason for State.
	Reason          string    `json:"reason,omitempty"`
	AllowWebSockets bool      `json:"allow_websockets,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// RuntimeContext provides the runtime hooks a workspace service can use.
type RuntimeContext interface {
	CityPath() string
	CityName() string
	PublicationStorePath() string
	Config() *config.City
	PublicationConfig() supervisor.PublicationConfig
	SessionProvider() runtime.Provider
	BeadStore(rig string) beads.Store
	Poke()
}

// Instance is one runtime service implementation.
type Instance interface {
	Status() Status
	HandleHTTP(w http.ResponseWriter, r *http.Request, subpath string) bool
	Tick(ctx context.Context, now time.Time)
	Close() error
}

// Registry is the controller-owned workspace service registry.
type Registry interface {
	List() []Status
	Get(name string) (Status, bool)
	AuthorizeAndServeHTTP(name string, w http.ResponseWriter, r *http.Request, authorize func(Status) bool) bool
	Restart(name string) error
}

// WorkflowFactory constructs a workflow service for a known contract.
type WorkflowFactory func(rt RuntimeContext, svc config.Service) (Instance, error)
