package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestCheckUnknownKeysReportsAllPaths supplies misspellings and two historical settings.
// Validation must report every offending leaf in sorted order with rename or
// removal guidance, before any reference could be interpolated by the loader.
func TestCheckUnknownKeysReportsAllPaths(t *testing.T) {
	doc, _, err := parseConfigDocument([]byte("config_version: 2\nz_unknown: 1\ngateway:\n  max_body_size: 2\n  models_list_cache_ttl_seconds: 20\n"), "fixture.yaml")
	require.NoError(t, err)
	err = checkUnknownKeys(doc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gateway.max_body_size (renamed to gateway.max_request_body_bytes in config_version 2)")
	require.Contains(t, err.Error(), "gateway.models_list_cache_ttl_seconds (removed in config_version 2")
	require.Contains(t, err.Error(), "z_unknown")
}

// TestCheckUnknownKeysHandlesDynamicMapsAndStructuredLists distinguishes schema containers.
// Quota map names remain deployment-defined, but unknown fields inside proxy-probe
// list entries must fail with an indexed diagnostic identifying the offending element.
func TestCheckUnknownKeysHandlesDynamicMapsAndStructuredLists(t *testing.T) {
	doc, _, err := parseConfigDocument([]byte("config_version: 2\nprovider:\n  gemini:\n    quota:\n      tiers:\n        custom-tier:\n          custom-value: 1\negress:\n  proxy_probe:\n    urls:\n      - url: https://example.test\n        parser: ipify\n"), "fixture.yaml")
	require.NoError(t, err)
	require.NoError(t, checkUnknownKeys(doc))
	require.NoError(t, setConfigPath(configPath(doc.Content[0], "egress.proxy_probe.urls").Content[0], "typo", configValueNode(true)))
	err = checkUnknownKeys(doc)
	require.ErrorContains(t, err, "egress.proxy_probe.urls[0].typo")
}

// TestVersionTwoIgnoresImplicitOverrides loads explicit current settings with old variables.
// Only a declared reference can read an environment value, while typed numbers,
// booleans, lists, and leading-zero secrets decode from their resolved strings.
func TestVersionTwoIgnoresImplicitOverrides(t *testing.T) {
	fixture := newConfigFixture(t)
	fixture.document.Content[0].Content = nil
	require.NoError(t, setConfigPath(fixture.document.Content[0], "config_version", configValueNode(2)))
	for key, value := range map[string]any{"auth.jwt.secret": "${SIGNING}", "server.port": "${PORT}", "server.enable_server_timing": "${TIMING}", "server.client_ip.trusted_proxies": "${PROXIES}", "database.password": "${PASSWORD}"} {
		require.NoError(t, setConfigPath(fixture.document.Content[0], key, configValueNode(value)))
	}
	fixture.values = map[string]string{"SIGNING": "abcdefghijklmnopqrstuvwxyz0123456789", "PORT": "8123", "TIMING": "true", "PROXIES": "127.0.0.1,::1", "PASSWORD": "0123", "SERVER_PORT": "9999"}
	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Equal(t, 8123, cfg.Server.Port)
	require.True(t, cfg.Server.EnableServerTiming)
	require.Equal(t, []string{"127.0.0.1", "::1"}, cfg.Server.ClientIP.TrustedProxies)
	require.Equal(t, "0123", cfg.Database.Password)
}

// TestDefaultsMatchCurrentSchema checks every registered default against decoded paths.
// Removed keys must leave the default registry too, so sparse current files
// cannot silently retain a historical setting outside the versioned schema.
func TestDefaultsMatchCurrentSchema(t *testing.T) {
	v := viper.New()
	setDefaults(v)
	var doc yaml.Node
	require.NoError(t, doc.Encode(v.AllSettings()))
	require.NoError(t, checkUnknownKeys(&doc))
}
