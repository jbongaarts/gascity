package workspacesvc

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/supervisor"
)

func TestDerivePublishedURL(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{
		Provider:         "hosted",
		TenantSlug:       "Acme",
		PublicBaseDomain: "apps.example.com",
	}, "Demo City", config.Service{
		Name: "review_intake",
		Publication: config.ServicePublicationConfig{
			Visibility: "public",
		},
	})
	if reason != "route_active" {
		t.Fatalf("reason = %q, want route_active", reason)
	}
	if !strings.HasPrefix(url, "https://review-intake--demo-city--acme--") {
		t.Fatalf("url = %q, want review-intake--demo-city--acme prefix", url)
	}
	if !strings.HasSuffix(url, ".apps.example.com") {
		t.Fatalf("url = %q, want apps.example.com suffix", url)
	}
}

func TestDerivePublishedURLRequiresSupervisor(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{}, "Demo", config.Service{
		Name: "review-intake",
		Publication: config.ServicePublicationConfig{
			Visibility: "public",
		},
	})
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
	if reason != "publication_requires_supervisor" {
		t.Fatalf("reason = %q, want publication_requires_supervisor", reason)
	}
}

func TestDerivePublishedURLRequiresTenantAuthForTenantVisibility(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{
		Provider:         "hosted",
		TenantSlug:       "acme",
		TenantBaseDomain: "tenant.apps.example.com",
	}, "Demo", config.Service{
		Name: "review-intake",
		Publication: config.ServicePublicationConfig{
			Visibility: "tenant",
		},
	})
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
	if reason != "publication_tenant_auth_policy_missing" {
		t.Fatalf("reason = %q, want publication_tenant_auth_policy_missing", reason)
	}
}
