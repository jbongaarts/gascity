package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHandleProviderReadinessReturnsConfiguredStatuses(t *testing.T) {
	homeDir := t.TempDir()
	binDir := filepath.Join(homeDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	writeExecutable(t, binDir, "claude", `#!/bin/sh
printf '%s\n' '{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}'
`)
	writeExecutable(t, binDir, "codex", "#!/bin/sh\nexit 0\n")
	writeExecutable(t, binDir, "gemini", "#!/bin/sh\nexit 0\n")

	if err := os.MkdirAll(filepath.Join(homeDir, ".codex"), 0o755); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(homeDir, ".codex", "auth.json"),
		[]byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"token"}}`),
		0o600,
	); err != nil {
		t.Fatalf("write codex auth: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(homeDir, ".gemini"), 0o755); err != nil {
		t.Fatalf("mkdir gemini dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(homeDir, ".gemini", "settings.json"),
		[]byte(`{"security":{"auth":{"selectedType":"oauth-personal"}}}`),
		0o600,
	); err != nil {
		t.Fatalf("write gemini settings: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(homeDir, ".gemini", "oauth_creds.json"),
		[]byte(`{"refresh_token":"token"}`),
		0o600,
	); err != nil {
		t.Fatalf("write gemini creds: %v", err)
	}

	t.Setenv("HOME", homeDir)
	originalPathEnv := providerProbePathEnv
	originalCommandContext := providerProbeCommandContext
	providerProbePathEnv = binDir
	providerProbeCommandContext = exec.CommandContext
	defer func() {
		providerProbePathEnv = originalPathEnv
		providerProbeCommandContext = originalCommandContext
	}()

	srv := New(newFakeState(t))
	req := httptest.NewRequest(http.MethodGet, "/v0/provider-readiness?providers=claude,codex,gemini", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp providerReadinessResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp.Providers["claude"].Status; got != probeStatusConfigured {
		t.Errorf("claude status = %q, want %q", got, probeStatusConfigured)
	}
	if got := resp.Providers["codex"].Status; got != probeStatusConfigured {
		t.Errorf("codex status = %q, want %q", got, probeStatusConfigured)
	}
	if got := resp.Providers["gemini"].Status; got != probeStatusConfigured {
		t.Errorf("gemini status = %q, want %q", got, probeStatusConfigured)
	}
}

func TestHandleProviderReadinessRejectsUnknownProviders(t *testing.T) {
	srv := New(newFakeState(t))
	req := httptest.NewRequest(http.MethodGet, "/v0/provider-readiness?providers=claude,unknown", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func writeExecutable(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
