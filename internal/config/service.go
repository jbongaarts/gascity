package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gastownhall/gascity/internal/citylayout"
)

var (
	validServiceName      = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
	validPublicationLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

// Service declares a workspace-owned HTTP service mounted under /svc/{name}.
type Service struct {
	// Name is the unique service identifier within a workspace.
	Name string `toml:"name" jsonschema:"required"`
	// Kind selects how the service is implemented.
	Kind string `toml:"kind,omitempty" jsonschema:"enum=workflow,enum=proxy_process"`
	// PublishMode declares how the service is intended to be published.
	// v0 supports private services and direct reuse of the API listener.
	PublishMode string `toml:"publish_mode,omitempty" jsonschema:"enum=private,enum=direct"`
	// StateRoot overrides the managed service state root. Defaults to
	// .gc/services/{name}. The path must stay within .gc/services/.
	StateRoot string `toml:"state_root,omitempty"`
	// Publication declares generic publication intent. The platform decides
	// whether and how that intent becomes a public route.
	Publication ServicePublicationConfig `toml:"publication,omitempty"`
	// Workflow configures controller-owned workflow services.
	Workflow ServiceWorkflowConfig `toml:"workflow,omitempty"`
	// Process configures controller-supervised proxy services.
	Process ServiceProcessConfig `toml:"process,omitempty"`
	// SourceDir records pack provenance for pack-stamped services.
	SourceDir string `toml:"-" json:"-"`
}

// ServicePublicationConfig declares platform-neutral publication intent.
type ServicePublicationConfig struct {
	// Visibility selects whether the service is private to the workspace,
	// available publicly, or gated by tenant auth at the platform edge.
	Visibility string `toml:"visibility,omitempty" jsonschema:"enum=private,enum=public,enum=tenant"`
	// Hostname overrides the default hostname label derived from service.name.
	Hostname string `toml:"hostname,omitempty"`
	// AllowWebSockets permits websocket upgrades on the published route.
	AllowWebSockets bool `toml:"allow_websockets,omitempty"`
}

// KindOrDefault returns the normalized service kind.
func (s Service) KindOrDefault() string {
	if s.Kind == "" {
		return "workflow"
	}
	return s.Kind
}

// MountPathOrDefault returns the service mount path.
func (s Service) MountPathOrDefault() string {
	return "/svc/" + s.Name
}

// PublishModeOrDefault returns the normalized publish mode.
func (s Service) PublishModeOrDefault() string {
	if s.PublishMode == "" {
		return "private"
	}
	return s.PublishMode
}

// PublicationVisibilityOrDefault returns the normalized publication visibility.
func (s Service) PublicationVisibilityOrDefault() string {
	if v := strings.TrimSpace(strings.ToLower(s.Publication.Visibility)); v != "" {
		return v
	}
	if s.PublishModeOrDefault() == "direct" {
		return "public"
	}
	return "private"
}

// PublicationHostnameOrDefault returns the hostname label used for published
// service URLs.
func (s Service) PublicationHostnameOrDefault() string {
	if v := strings.TrimSpace(strings.ToLower(s.Publication.Hostname)); v != "" {
		return v
	}
	return normalizePublicationLabel(s.Name, "service")
}

// StateRootOrDefault returns the managed runtime root for the service.
func (s Service) StateRootOrDefault() string {
	if s.StateRoot != "" {
		return filepath.Clean(s.StateRoot)
	}
	return filepath.Join(citylayout.RuntimeServicesRoot, s.Name)
}

func normalizePublicationLabel(value, fallback string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var b strings.Builder
	prevDash := false
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			b.WriteRune(ch)
			prevDash = false
		case ch == '-' || ch == '_':
			if b.Len() == 0 || prevDash {
				continue
			}
			b.WriteByte('-')
			prevDash = true
		default:
			if b.Len() == 0 || prevDash {
				continue
			}
			b.WriteByte('-')
			prevDash = true
		}
		if b.Len() >= 63 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return fallback
	}
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	if out == "" {
		return fallback
	}
	return out
}

// ServiceWorkflowConfig configures controller-owned workflow services.
type ServiceWorkflowConfig struct {
	// Contract selects the built-in workflow handler.
	Contract string `toml:"contract,omitempty"`
}

// ServiceProcessConfig configures a controller-supervised local process
// that is reverse-proxied under /svc/{name}.
type ServiceProcessConfig struct {
	// Command is the argv used to start the local service process.
	Command []string `toml:"command,omitempty"`
	// HealthPath, when set, is probed on the local listener before the
	// service is marked ready.
	HealthPath string `toml:"health_path,omitempty"`
}

// ValidateServices checks workspace service declarations for configuration
// errors that would prevent runtime activation.
func ValidateServices(services []Service) error {
	seen := make(map[string]bool, len(services))
	for i, svc := range services {
		if svc.Name == "" {
			return fmt.Errorf("service[%d]: name is required", i)
		}
		if !validServiceName.MatchString(svc.Name) {
			return fmt.Errorf("service %q: name must match [a-zA-Z0-9][a-zA-Z0-9_-]*", svc.Name)
		}
		if seen[svc.Name] {
			if svc.SourceDir != "" {
				return fmt.Errorf("service %q: duplicate name (from %q)", svc.Name, svc.SourceDir)
			}
			return fmt.Errorf("service %q: duplicate name", svc.Name)
		}
		seen[svc.Name] = true

		switch svc.KindOrDefault() {
		case "workflow", "proxy_process":
		default:
			return fmt.Errorf("service %q: kind must be \"workflow\" or \"proxy_process\", got %q", svc.Name, svc.Kind)
		}
		switch svc.PublishModeOrDefault() {
		case "private", "direct":
		default:
			return fmt.Errorf("service %q: publish_mode must be \"private\" or \"direct\", got %q", svc.Name, svc.PublishMode)
		}
		switch svc.PublicationVisibilityOrDefault() {
		case "private", "public", "tenant":
		default:
			return fmt.Errorf("service %q: publication.visibility must be \"private\", \"public\", or \"tenant\", got %q", svc.Name, svc.Publication.Visibility)
		}
		if svc.PublishMode == "direct" && svc.Publication.Visibility != "" && svc.PublicationVisibilityOrDefault() != "public" {
			return fmt.Errorf("service %q: publish_mode=direct requires publication.visibility to be omitted or \"public\"", svc.Name)
		}
		if hostname := strings.TrimSpace(strings.ToLower(svc.Publication.Hostname)); hostname != "" && !validPublicationLabel.MatchString(hostname) {
			return fmt.Errorf("service %q: publication.hostname must be a single DNS label, got %q", svc.Name, svc.Publication.Hostname)
		}

		root := filepath.ToSlash(filepath.Clean(svc.StateRootOrDefault()))
		prefix := filepath.ToSlash(citylayout.RuntimeServicesRoot) + "/"
		if !strings.HasPrefix(root, prefix) {
			return fmt.Errorf("service %q: state_root must stay under %s/, got %q", svc.Name, filepath.ToSlash(citylayout.RuntimeServicesRoot), svc.StateRootOrDefault())
		}
		if strings.Contains(root, "../") || strings.HasSuffix(root, "/..") {
			return fmt.Errorf("service %q: state_root may not traverse upward, got %q", svc.Name, svc.StateRootOrDefault())
		}

		switch svc.KindOrDefault() {
		case "workflow":
			if svc.Workflow.Contract == "" {
				return fmt.Errorf("service %q: workflow.contract is required for workflow services", svc.Name)
			}
		case "proxy_process":
			if len(svc.Process.Command) == 0 || strings.TrimSpace(svc.Process.Command[0]) == "" {
				return fmt.Errorf("service %q: process.command is required for proxy_process services", svc.Name)
			}
		}
	}
	return nil
}
