package config

import (
	"os"
	"strings"
	"testing"
)

// remote-restart-enabled is written to config.yml, off, like the other
// local-only switches, and read back from what the host's administrator set.
func TestRemoteRestartIntegrationDefaultsOff(t *testing.T) {
	t.Parallel()
	m := newTestManager(t)
	if m.IsIntegrationEnabled(RemoteRestartIntegration) {
		t.Fatal("remote restarts are enabled by default")
	}
	b, err := os.ReadFile(m.GetConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "remote-restart-enabled: false") {
		t.Fatalf("config.yml lacks the switch, off:\n%s", b)
	}

	enabled := strings.Replace(string(b), "remote-restart-enabled: false", "remote-restart-enabled: true", 1)
	if err := os.WriteFile(m.GetConfigFile(), []byte(enabled), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	if !m.IsIntegrationEnabled(RemoteRestartIntegration) {
		t.Fatal("the switch set in config.yml is not honoured")
	}
}

// Only editing config.yml changes a local-only switch: the generic setter,
// which server-driven updates go through, refuses them.
func TestLocalOnlyIntegrationsCannotBeSet(t *testing.T) {
	t.Parallel()
	m := newTestManager(t)
	for _, name := range LocalOnlyIntegrations {
		if err := m.SetIntegrationEnabled(name, true); err == nil || !strings.Contains(err.Error(), "editing") {
			t.Errorf("SetIntegrationEnabled(%s) = %v, want a refusal", name, err)
		}
		if m.IsIntegrationEnabled(name) {
			t.Errorf("%s was turned on", name)
		}
	}
	for _, name := range []string{"ssh-proxy-enabled", "rdp-proxy-enabled", RemoteRestartIntegration} {
		if !IsLocalOnlyIntegration(name) {
			t.Errorf("%s is not local-only", name)
		}
	}
	if IsLocalOnlyIntegration("docker") || IsLocalOnlyIntegration("compliance") {
		t.Error("a server-synced integration is local-only")
	}
}
