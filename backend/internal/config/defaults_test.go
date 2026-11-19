package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadDefaultSecurityToggles inspects URL allowlist switches, configured
// supplier hosts, and response-header filtering. Loading must expose the
// expected disabled allowlist, permitted private/HTTP options, host entries,
// and enabled header filter.
func TestLoadDefaultSecurityToggles(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if cfg.Security.URLAllowlist.Enabled {
		t.Fatalf("URLAllowlist.Enabled = true, want false")
	}
	if !cfg.Security.URLAllowlist.AllowInsecureHTTP {
		t.Fatalf("URLAllowlist.AllowInsecureHTTP = false, want true")
	}
	if !cfg.Security.URLAllowlist.AllowPrivateHosts {
		t.Fatalf("URLAllowlist.AllowPrivateHosts = false, want true")
	}
	// Default upstream hosts include the Chinese suppliers' official endpoints.
	for _, host := range []string{
		"api.kimi.com",
		"api.moonshot.ai",
		"api.moonshot.cn",
		"open.bigmodel.cn",
		"api.z.ai",
		"api.deepseek.com",
	} {
		require.Contains(t, cfg.Security.URLAllowlist.UpstreamHosts, host)
	}
	if !cfg.Gateway.ResponseHeaders.Enabled {
		t.Fatalf("ResponseHeaders.Enabled = false, want true")
	}
}
