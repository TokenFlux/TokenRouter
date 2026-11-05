package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIgnoredEnvironmentNamesSeparatesReferencesAndInstallerInputs supplies retained deployment variables.
// Matching installer inputs and explicit references stay quiet, while mismatches,
// ordinary legacy overrides, and present empty proxy lists produce sorted names only.
func TestIgnoredEnvironmentNamesSeparatesReferencesAndInstallerInputs(t *testing.T) {
	cfg := &Config{Database: DatabaseConfig{Host: "database", Password: "private-password"}, Server: ServerConfig{Port: 8080}, Auth: AuthConfig{JWT: JWTConfig{AccessTokenTTLMinutes: 1440}}}
	doc, _, err := parseConfigDocument([]byte("config_version: 2\nupdate:\n  github_token: ${UPDATE_GITHUB_TOKEN}\n"), "fixture.yaml")
	require.NoError(t, err)
	values := map[string]string{"DATABASE_HOST": "database", "DATABASE_PASSWORD": "private-password", "SERVER_PORT": "8081", "JWT_EXPIRE_HOUR": "24", "GATEWAY_MAX_BODY_SIZE": "private-ignored", "UPDATE_GITHUB_TOKEN": "private-token", "SERVER_TRUSTED_PROXIES": "", "ENABLE_SERVER_TIMING": "", "AUTO_SETUP": "true", "ADMIN_PASSWORD": "private-admin"}
	names := ignoredEnvironmentNames(cfg, doc, func(name string) (string, bool) { v, ok := values[name]; return v, ok })
	require.Equal(t, []string{"GATEWAY_MAX_BODY_SIZE", "SERVER_PORT", "SERVER_TRUSTED_PROXIES"}, names)
}
