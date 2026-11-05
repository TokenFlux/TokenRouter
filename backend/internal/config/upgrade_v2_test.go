package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestUpgradeV2SchemaCoverage checks every frozen key against current destinations.
// Historical fields must either move to a decoded key, remain recognized, or
// have an explicit removal rule; all move destinations must exist in the schema.
func TestUpgradeV2SchemaCoverage(t *testing.T) {
	known := knownConfigKeys()
	for _, move := range v2Moves {
		require.Contains(t, known, move.New, move.Old)
	}
	for _, key := range v1Keys {
		dropped := false
		for old := range v2Removed {
			if key.Path == old || strings.HasPrefix(key.Path, old+".") {
				dropped = true
				break
			}
		}
		if dropped {
			continue
		}
		target := key.Path
		for _, move := range v2Moves {
			if key.Path == move.Old || strings.HasPrefix(key.Path, move.Old+".") {
				target = move.New + strings.TrimPrefix(key.Path, move.Old)
				break
			}
		}
		require.Contains(t, known, target, key.Path)
	}
}

// TestUpgradeV2OriginalExample loads the frozen complete deployment example.
// Its upgraded document must contain only current keys, retain the configured
// body limit and explicit security choices, and pass ordinary startup validation.
func TestUpgradeV2OriginalExample(t *testing.T) {
	raw, err := os.ReadFile("testdata/upgrade/v1_example.yaml")
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(file, raw, 0o600))
	env := upgradeEnvironment{LookupEnv: func(name string) (string, bool) {
		if name == "JWT_SECRET" {
			return strings.Repeat("x", 32), true
		}
		return "", false
	}}
	cfg, err := loadFrom(file, true, env, false)
	require.NoError(t, err)
	require.Equal(t, CurrentConfigVersion, cfg.ConfigVersion)
	require.Positive(t, cfg.Gateway.MaxRequestBodyBytes)
	upgraded, err := os.ReadFile(file)
	require.NoError(t, err)
	require.NotContains(t, string(upgraded), "tls_fingerprint:")
	require.Contains(t, string(upgraded), "max_request_body_bytes:")
}

// TestUpgradeV2GoldenExample compares the full historical example with reviewed output.
// Node encoding must preserve the expected comments, field grouping, and values
// without relying on current defaults to reconstruct omitted legacy information.
func TestUpgradeV2GoldenExample(t *testing.T) {
	_, doc, _, _, err := prepareConfigUpgrade("testdata/upgrade/v1_example.yaml", true, completeUpgradeEnvironment(upgradeEnvironment{}))
	require.NoError(t, err)
	actual, err := encodeConfigDocument(doc)
	require.NoError(t, err)
	expected, err := os.ReadFile("testdata/upgrade/v2_example.yaml")
	require.NoError(t, err)
	require.Equal(t, string(expected), string(actual))
}

// TestUpgradeV2SemanticCases covers coupled settings and sensitive environment imports.
// Each case asserts concrete version 2 values independently of the move table,
// including false and zero precedence, nanosecond conversion, and secret references.
func TestUpgradeV2SemanticCases(t *testing.T) {
	cases := []struct {
		name     string            // migration scenario display name
		source   string            // original version one YAML
		env      map[string]string // injected legacy environment values
		expected map[string]any    // concrete version two values
	}{
		{name: "current alias false and zero win", source: "gateway:\n  max_account_switches: 8\n  max_provider_switches: 0\n  openai_ws:\n    dynamic_max_conns_by_account_concurrency_enabled: true\n    dynamic_max_conns_by_provider_concurrency_enabled: false\n", expected: map[string]any{"gateway.failover.max_provider_switches": 0, "gateway.openai.responses_ws.dynamic_max_conns_by_provider_concurrency_enabled": false}},
		{name: "old alias", source: "gateway:\n  max_account_switches: 8\n", expected: map[string]any{"gateway.failover.max_provider_switches": 8}},
		{name: "durations", source: "gateway:\n  scheduling:\n    sticky_session_wait_timeout: 500ms\n    fallback_wait_timeout: 1500000001\n    slot_cleanup_interval: 0\n", expected: map[string]any{"scheduling.sticky_provider_wait.timeout_seconds": 1, "scheduling.alternate_provider_wait.timeout_seconds": 2, "scheduling.slot_cleanup_interval_seconds": 0}},
		{name: "minute JWT wins", source: "jwt:\n  expire_hour: 12\n  access_token_expire_minutes: 3\n", expected: map[string]any{"auth.jwt.access_token_ttl_minutes": 3}},
		{name: "hour JWT", source: "jwt:\n  expire_hour: 12\n", expected: map[string]any{"auth.jwt.access_token_ttl_minutes": 720}},
		{name: "queue and affinity", source: "gateway:\n  user_message_queue:\n    mode: invalid\n    enabled: true\n  openai_ws:\n    sticky_response_id_ttl_seconds: 0\n    sticky_previous_response_ttl_seconds: 7200\n  openai_proxy_stream_circuit:\n    disabled: true\n", expected: map[string]any{"gateway.anthropic.user_message_queue.default_mode": "serialize", "scheduling.session_affinity.response_id_ttl_seconds": 7200, "gateway.openai.proxy_stream_circuit.enabled": false}},
		{name: "secret and literal syntax", source: "database:\n  user: '${literal}'\n", env: map[string]string{"DATABASE_PASSWORD": "0123${private}", "UPDATE_GITHUB_TOKEN": "private-token"}, expected: map[string]any{"database.user": "$${literal}", "database.password": "${DATABASE_PASSWORD}", "update.github_token": "${UPDATE_GITHUB_TOKEN}"}},
		{name: "secret reader whitespace precedence", env: map[string]string{"ANTIGRAVITY_OAUTH_CLIENT_SECRET": "   ", "YESCAPTCHA_CLIENT_KEY": "   ", "YESCAPTCHA_API_KEY": "fallback-secret"}, expected: map[string]any{"provider.antigravity.oauth_client_secret": "${ANTIGRAVITY_OAUTH_CLIENT_SECRET}", "provider.grok.yescaptcha_client_key": "${YESCAPTCHA_API_KEY}"}},
		{name: "wechat pair", env: map[string]string{"WECHAT_OAUTH_OPEN_APP_ID": "openid", "WECHAT_OAUTH_OPEN_APP_SECRET": "private"}, expected: map[string]any{"auth.oauth.wechat.enabled": true, "auth.oauth.wechat.open_enabled": true, "auth.oauth.wechat.open_app_secret": "${WECHAT_OAUTH_OPEN_APP_SECRET}", "auth.oauth.wechat.mobile_app_id": "openid"}},
		{name: "generic wechat modes", source: "wechat_connect:\n  enabled: true\n  mode: mp\n  app_id: generic\n  app_secret: secret\n", expected: map[string]any{"auth.oauth.wechat.mp_enabled": true, "auth.oauth.wechat.open_app_id": "generic", "auth.oauth.wechat.mobile_app_secret": "secret"}},
		{name: "cooldown and direct readers", source: "gateway:\n  antigravity_fallback_cooldown_minutes: 0\n", env: map[string]string{"XAI_ALLOW_UNSAFE_URL_OVERRIDES": "y", "GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL": "sandbox"}, expected: map[string]any{"provider.antigravity.fallback_cooldown_seconds": 30, "provider.antigravity.forward_endpoint": "daily", "provider.grok.allow_unsafe_url_overrides": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, _, err := parseConfigDocument([]byte(tc.source), "fixture.yaml")
			require.NoError(t, err)
			env := completeUpgradeEnvironment(upgradeEnvironment{LookupEnv: func(name string) (string, bool) { value, ok := tc.env[name]; return value, ok }})
			_, err = upgradeV1ToV2(doc, env)
			require.NoError(t, err)
			for path, want := range tc.expected {
				node := configPath(doc.Content[0], path)
				require.NotNil(t, node, path)
				var got any
				require.NoError(t, node.Decode(&got))
				require.Equal(t, want, got, path)
			}
		})
	}
}

// TestUpgradeV2OIDCEmptyEnvironmentPreservesFile checks explicit security choices with empty overrides.
// Historical file values must survive migration, while omitted values materialize
// true defaults and retain the explicit presence required by OIDC compatibility readers.
func TestUpgradeV2OIDCEmptyEnvironmentPreservesFile(t *testing.T) {
	for _, tc := range []struct {
		name   string // original security setting
		source string // version one input YAML
		want   bool   // effective security flag
	}{
		{name: "omitted", source: "{}\n", want: true},
		{name: "false", source: "oidc_connect:\n  use_pkce: false\n  validate_id_token: false\n"},
		{name: "true", source: "oidc_connect:\n  use_pkce: true\n  validate_id_token: true\n", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(file, []byte(tc.source), 0o600))
			env := upgradeEnvironment{LookupEnv: func(name string) (string, bool) {
				return "", name == "OIDC_CONNECT_USE_PKCE" || name == "OIDC_CONNECT_VALIDATE_ID_TOKEN"
			}}
			loaded, err := loadFrom(file, true, env, true)
			require.NoError(t, err)
			require.Equal(t, tc.want, loaded.Auth.OAuth.OIDC.UsePKCE)
			require.Equal(t, tc.want, loaded.Auth.OAuth.OIDC.ValidateIDToken)
			require.True(t, loaded.Auth.OAuth.OIDC.UsePKCEExplicit)
			require.True(t, loaded.Auth.OAuth.OIDC.ValidateIDTokenExplicit)
			reloaded, err := loadFrom(file, true, upgradeEnvironment{}, true)
			require.NoError(t, err)
			require.Equal(t, loaded.Auth.OAuth.OIDC, reloaded.Auth.OAuth.OIDC)
		})
	}
}

// TestUpgradeV2DependencyHostsFollowEnvironment models container address changes after migration.
// Host references must select each newly discovered address on restart without
// rewriting the current-version document or importing environment overrides again.
func TestUpgradeV2DependencyHostsFollowEnvironment(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte("database: {host: old-db}\nredis: {host: old-cache}\n"), 0o600))
	values := map[string]string{"DATABASE_HOST": "192.168.64.2", "REDIS_HOST": "192.168.64.3"}
	env := upgradeEnvironment{LookupEnv: func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}}
	first, err := loadFrom(file, true, env, true)
	require.NoError(t, err)
	require.Equal(t, values["DATABASE_HOST"], first.Database.Host)
	require.Equal(t, values["REDIS_HOST"], first.Redis.Host)
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Contains(t, string(raw), "${DATABASE_HOST}")
	require.Contains(t, string(raw), "${REDIS_HOST}")
	values["DATABASE_HOST"] = "192.168.64.8"
	values["REDIS_HOST"] = "192.168.64.9"
	reloaded, err := loadFrom(file, true, env, true)
	require.NoError(t, err)
	require.Equal(t, values["DATABASE_HOST"], reloaded.Database.Host)
	require.Equal(t, values["REDIS_HOST"], reloaded.Redis.Host)
	after, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, raw, after)
}

// TestUpgradeV2NullValuesUseDefaults covers all YAML spellings of an unset scalar.
// Lifetimes, durations, mode, and presence-sensitive security options must keep
// their historical defaults through migration and subsequent current-version loading.
func TestUpgradeV2NullValuesUseDefaults(t *testing.T) {
	for _, spelling := range []string{"null", "~", ""} {
		t.Run(spelling, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config.yaml")
			raw := strings.ReplaceAll(`server:
  mode: NULL_VALUE
  trusted_proxies: NULL_VALUE
jwt:
  expire_hour: NULL_VALUE
  access_token_expire_minutes: NULL_VALUE
oidc_connect:
  use_pkce: NULL_VALUE
  validate_id_token: NULL_VALUE
gateway:
  antigravity_fallback_cooldown_minutes: NULL_VALUE
  scheduling:
    sticky_session_wait_timeout: NULL_VALUE
    fallback_wait_timeout: NULL_VALUE
    slot_cleanup_interval: NULL_VALUE
  openai_ws:
    sticky_response_id_ttl_seconds: NULL_VALUE
    sticky_previous_response_ttl_seconds: NULL_VALUE
`, "NULL_VALUE", spelling)
			require.NoError(t, os.WriteFile(file, []byte(raw), 0o600))
			for range 2 {
				cfg, err := loadFrom(file, true, upgradeEnvironment{}, true)
				require.NoError(t, err)
				require.Equal(t, "release", cfg.Server.Mode)
				require.False(t, cfg.Server.ClientIP.TrustedProxiesConfigured)
				require.Equal(t, 1440, cfg.Auth.JWT.AccessTokenTTLMinutes)
				require.Equal(t, 120, cfg.Scheduling.StickyProviderWait.TimeoutSeconds)
				require.Equal(t, 30, cfg.Scheduling.AlternateProviderWait.TimeoutSeconds)
				require.Equal(t, 30, cfg.Scheduling.SlotCleanupIntervalSeconds)
				require.Equal(t, 3600, cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds)
				require.Equal(t, 60, cfg.Provider.Antigravity.FallbackCooldownSeconds)
				require.True(t, cfg.Auth.OAuth.OIDC.UsePKCE)
				require.True(t, cfg.Auth.OAuth.OIDC.ValidateIDToken)
				require.False(t, cfg.Auth.OAuth.OIDC.UsePKCEExplicit)
				require.False(t, cfg.Auth.OAuth.OIDC.ValidateIDTokenExplicit)
			}
			backup, err := os.ReadFile(file + ".v1.bak")
			require.NoError(t, err)
			require.Equal(t, raw, string(backup))
		})
	}
}

// TestUpgradeV2NullSectionsPreserveEnvironmentRules reproduces historical key enumeration around null containers.
// Implicit descendants stay hidden, while explicit bindings, client-IP lists,
// and OIDC presence retain their distinct effective values after migration.
func TestUpgradeV2NullSectionsPreserveEnvironmentRules(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte("database: null\nserver: null\njwt: null\ngateway: null\noidc_connect: null\n"), 0o600))
	values := map[string]string{
		"DATABASE_HOST": "ignored-host", "SERVER_MODE": "debug",
		"SERVER_TRUSTED_PROXIES": "127.0.0.1", "ENABLE_SERVER_TIMING": "true",
		"GATEWAY_MAX_ACCOUNT_SWITCHES": "8", "GATEWAY_MAX_BODY_SIZE": "1024",
		"OIDC_CONNECT_USE_PKCE": "false", "OIDC_CONNECT_VALIDATE_ID_TOKEN": "false",
	}
	cfg, err := loadFrom(file, true, upgradeEnvironment{LookupEnv: func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}}, true)
	require.NoError(t, err)
	require.Equal(t, "localhost", cfg.Database.Host)
	require.Equal(t, "release", cfg.Server.Mode)
	require.True(t, cfg.Server.EnableServerTiming)
	require.Equal(t, []string{"127.0.0.1"}, cfg.Server.ClientIP.TrustedProxies)
	require.Equal(t, 8, cfg.Gateway.Failover.MaxProviderSwitches)
	require.Greater(t, cfg.Gateway.MaxRequestBodyBytes, int64(1024))
	require.Equal(t, 1440, cfg.Auth.JWT.AccessTokenTTLMinutes)
	require.True(t, cfg.Auth.OAuth.OIDC.UsePKCE)
	require.True(t, cfg.Auth.OAuth.OIDC.ValidateIDToken)
	require.True(t, cfg.Auth.OAuth.OIDC.UsePKCEExplicit)
	require.True(t, cfg.Auth.OAuth.OIDC.ValidateIDTokenExplicit)
}

// TestUpgradeV2NullDoesNotEraseExplicitValues distinguishes absence from deliberately empty policy input.
// Quoted empty mode, an empty proxy list, false security flags, and zero limits
// must retain the same observable meanings after null cleanup runs.
func TestUpgradeV2NullDoesNotEraseExplicitValues(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	raw := "server: {mode: '', trusted_proxies: []}\noidc_connect: {use_pkce: false}\ngateway: {max_account_switches: 8, max_provider_switches: null, stream_keepalive_interval: 0}\n"
	require.NoError(t, os.WriteFile(file, []byte(raw), 0o600))
	cfg, err := loadFrom(file, true, upgradeEnvironment{}, true)
	require.NoError(t, err)
	require.Equal(t, "debug", cfg.Server.Mode)
	require.True(t, cfg.Server.ClientIP.TrustedProxiesConfigured)
	require.Empty(t, cfg.Server.ClientIP.TrustedProxies)
	require.False(t, cfg.Auth.OAuth.OIDC.UsePKCE)
	require.True(t, cfg.Auth.OAuth.OIDC.UsePKCEExplicit)
	require.Equal(t, 8, cfg.Gateway.Failover.MaxProviderSwitches)
	require.Zero(t, cfg.Gateway.Streaming.KeepaliveIntervalSeconds)
}

// TestUpgradeV2DottedMergePrecedence combines literal dotted keys with YAML merge sources.
// Explicit values and merge-sequence ordering must resolve before dotted keys
// become nested configuration paths, including an explicit null selecting defaults.
func TestUpgradeV2DottedMergePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string // merge precedence scenario
		raw  string // legacy configuration YAML
		port int    // effective listener port
	}{
		{name: "explicit", raw: "<<: {server.port: 9090}\nserver.port: 9191\n", port: 9191},
		{name: "sequence", raw: "<<: [{server.port: 9090}, {server.port: 9191}]\n", port: 9090},
		{name: "explicit null", raw: "<<: {server.port: 9090}\nserver.port: null\n", port: 8080},
		{name: "nested", raw: "server:\n  <<: {port: 9090}\n  port: 9191\n", port: 9191},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(file, []byte(tc.raw), 0o600))
			cfg, err := loadFrom(file, true, upgradeEnvironment{}, true)
			require.NoError(t, err)
			require.Equal(t, tc.port, cfg.Server.Port)
		})
	}
}

// TestUpgradeV2WeChatNumericFlags preserves the weak decoder's nonzero boolean semantics.
// An existing MP channel must remain authoritative without adding Open, and its
// identity projection must retain the MP login mode and consent scope.
func TestUpgradeV2WeChatNumericFlags(t *testing.T) {
	for _, number := range []string{"2", "-1", "0x2", "1.0", "0.5"} {
		t.Run(number, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config.yaml")
			raw := fmt.Sprintf("wechat_connect:\n  enabled: true\n  mp_enabled: %s\n  mp_app_id: fixture-id\n  mp_app_secret: fixture-secret\n", number)
			require.NoError(t, os.WriteFile(file, []byte(raw), 0o600))
			cfg, err := loadFrom(file, true, upgradeEnvironment{}, true)
			require.NoError(t, err)
			defaults := cfg.Auth.OAuth.WeChat.IdentityDefaults()
			require.True(t, defaults.MPEnabled)
			require.False(t, defaults.OpenEnabled)
			require.Equal(t, "mp", defaults.Mode)
			require.Equal(t, "snsapi_userinfo", defaults.Scopes)
		})
	}
}

// TestUpgradeV2WeChatBooleanValidation checks empty and invalid historical flag strings.
// Empty text remains false, while malformed flags stop migration before source
// replacement and never include the rejected input in their diagnostic.
func TestUpgradeV2WeChatBooleanValidation(t *testing.T) {
	for _, value := range []string{"''", "sensitive-invalid"} {
		file := filepath.Join(t.TempDir(), "config.yaml")
		raw := "wechat_connect: {enabled: " + value + "}\n"
		require.NoError(t, os.WriteFile(file, []byte(raw), 0o600))
		cfg, err := loadFrom(file, true, upgradeEnvironment{}, true)
		if value == "''" {
			require.NoError(t, err)
			require.False(t, cfg.Auth.OAuth.WeChat.Enabled)
			continue
		}
		require.Error(t, err)
		require.NotContains(t, err.Error(), value)
		after, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, raw, string(after))
		require.NoFileExists(t, file+".v1.bak")
	}
}

// TestUpgradeV2PreservesMovedComments checks all key comment positions through relocation.
// The new key must retain each comment while the original leaf disappears,
// allowing operators to keep their deployment-specific notes after automatic upgrade.
func TestUpgradeV2PreservesMovedComments(t *testing.T) {
	doc, _, err := parseConfigDocument([]byte("gateway:\n  max_body_size: 2048\n"), "fixture.yaml")
	require.NoError(t, err)
	key := doc.Content[0].Content[1].Content[0]
	key.HeadComment = "head note"
	key.LineComment = "line note"
	key.FootComment = "foot note"
	_, err = upgradeV1ToV2(doc, completeUpgradeEnvironment(upgradeEnvironment{}))
	require.NoError(t, err)
	root := configPath(doc.Content[0], "gateway")
	var moved *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "max_request_body_bytes" {
			moved = root.Content[i]
		}
	}
	require.NotNil(t, moved)
	require.Equal(t, "head note", moved.HeadComment)
	require.Equal(t, "line note", moved.LineComment)
	require.Equal(t, "foot note", moved.FootComment)
}

// TestUpgradeV2WeChatWhitespaceReference preserves the old trimmed credential precedence.
// A whitespace-only generic secret must not hide a valid mode-specific secret,
// and queue modes with surrounding spaces still use the old enabled fallback.
func TestUpgradeV2WeChatWhitespaceReference(t *testing.T) {
	doc, _, err := parseConfigDocument([]byte("wechat_connect:\n  open_app_secret: valid-mode-secret\ngateway:\n  user_message_queue:\n    mode: ' serialize '\n    enabled: false\n"), "fixture.yaml")
	require.NoError(t, err)
	env := completeUpgradeEnvironment(upgradeEnvironment{LookupEnv: func(name string) (string, bool) {
		if name == "WECHAT_CONNECT_APP_SECRET" {
			return "   ", true
		}
		return "", false
	}})
	_, err = upgradeV1ToV2(doc, env)
	require.NoError(t, err)
	require.Equal(t, "valid-mode-secret", configPath(doc.Content[0], "auth.oauth.wechat.mobile_app_secret").Value)
	require.Equal(t, "", configPath(doc.Content[0], "gateway.anthropic.user_message_queue.default_mode").Value)
}

// TestUpgradeV2CanonicalizesHistoricalYAML accepts the old loader's case-insensitive dotted paths.
// The resulting tree must retain the values and remove unknown structured-list
// fields that version 1 ignored, without changing user-defined map entry names.
func TestUpgradeV2CanonicalizesHistoricalYAML(t *testing.T) {
	doc, _, err := parseConfigDocument([]byte("GATEWAY.MAX_BODY_SIZE: 1234\nSecurity:\n  Proxy_Probe:\n    URLS:\n      - URL: https://example.test\n        Parser: ipify\n        retired_option: true\n"), "fixture.yaml")
	require.NoError(t, err)
	report, err := upgradeV1ToV2(doc, completeUpgradeEnvironment(upgradeEnvironment{}))
	require.NoError(t, err)
	require.Equal(t, "1234", configPath(doc.Content[0], "gateway.max_request_body_bytes").Value)
	require.NoError(t, checkUnknownKeys(doc))
	require.Contains(t, report.Dropped, "security.proxy_probe.urls[0].retired_option: unknown in config_version 1")
}

// TestUpgradeV2RetainsCatalogRecognition covers official, legacy, and lookalike URLs.
// Only the documented public catalog sources migrate, and the allowlist gains
// models.dev only when an existing legacy-host list accompanies a recognized rewrite.
func TestUpgradeV2RetainsCatalogRecognition(t *testing.T) {
	cases := []struct {
		source  string // historical catalog URL input
		migrate bool   // recognized source migrates flag
	}{
		{"https://models.dev/catalog.json", true},
		{"https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.json", true},
		{"https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/refs/heads/main//model_prices_and_context_window.json", true},
		{"https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json", true},
		{"https://mirror.example/catalog.json?source=raw.githubusercontent.com/wei-shaw/model-price-repo/main/model_prices_and_context_window.json", false},
		{"https://raw.githubusercontent.com.evil.example/BerriAI/litellm/main/model_prices_and_context_window.json", false},
		{"https://raw.githubusercontent.com/custom/litellm/main/model_prices_and_context_window.json", false},
		{"https://raw.githubusercontent.com/BerriAI/litellm-other/main/model_prices_and_context_window.json", false},
		{"https://user@raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			doc, _, err := parseConfigDocument([]byte("{}"), "fixture.yaml")
			require.NoError(t, err)
			require.NoError(t, setConfigPath(doc.Content[0], "pricing.remote_url", configValueNode(tc.source)))
			require.NoError(t, setConfigPath(doc.Content[0], "security.url_allowlist.pricing_hosts", configValueNode([]string{"raw.githubusercontent.com"})))
			_, err = upgradeV1ToV2(doc, completeUpgradeEnvironment(upgradeEnvironment{}))
			require.NoError(t, err)
			want := tc.source
			if tc.migrate {
				want = "https://models.dev/catalog.json?type=all"
			}
			require.Equal(t, want, configPath(doc.Content[0], "model_catalog.catalog_url").Value)
			if tc.migrate && strings.Contains(tc.source, "raw.githubusercontent.com") {
				require.Equal(t, "models.dev", configPath(doc.Content[0], "security.url_allowlist.model_catalog_hosts").Content[1].Value)
			}
		})
	}
}

// TestUpgradeV2CatalogFallbackOnlyMovesMissingFiles reproduces packaged supplement path handling.
// Missing known files select their replacement, while existing files, permission
// errors, and custom paths retain the administrator's original source spelling.
func TestUpgradeV2CatalogFallbackOnlyMovesMissingFiles(t *testing.T) {
	info, err := os.Stat("testdata/upgrade/v1_example.yaml")
	require.NoError(t, err)
	for _, tc := range []struct {
		path    string // historical supplement file path
		statErr error  // injected source stat result
		want    string // expected migrated supplement path
	}{
		{"./resources/model-pricing/model_prices_and_context_window.json", os.ErrNotExist, "resources/model-pricing/model_pricing_supplements.json"},
		{"/app/resources/model-pricing/model_prices_and_context_window.json", os.ErrNotExist, "/app/resources/model-pricing/model_pricing_supplements.json"},
		{"./resources/model-pricing/model_prices_and_context_window.json", nil, "./resources/model-pricing/model_prices_and_context_window.json"},
		{"./resources/model-pricing/model_prices_and_context_window.json", os.ErrPermission, "./resources/model-pricing/model_prices_and_context_window.json"},
		{"./custom/model_prices_and_context_window.json", os.ErrNotExist, "./custom/model_prices_and_context_window.json"},
	} {
		doc, _, err := parseConfigDocument([]byte("{}"), "fixture.yaml")
		require.NoError(t, err)
		require.NoError(t, setConfigPath(doc.Content[0], "pricing.fallback_file", configValueNode(tc.path)))
		_, err = upgradeV1ToV2(doc, completeUpgradeEnvironment(upgradeEnvironment{Stat: func(string) (os.FileInfo, error) { return info, tc.statErr }}))
		require.NoError(t, err)
		require.Equal(t, tc.want, configPath(doc.Content[0], "model_catalog.supplement_file").Value)
	}
}

// TestUpgradeV2WeChatLegacyModes retains historical mode inference and credential fallback.
// Runtime identity defaults must match the old normalized channel selection,
// including mixed case modes, independently missing credential members, and blank redirects.
func TestUpgradeV2WeChatLegacyModes(t *testing.T) {
	for _, tc := range []struct {
		raw   string  // supplied legacy mode text
		mode  string  // expected normalized stored mode
		flags [3]bool // expected channel enablement flags
	}{
		{"OPEN", "open", [3]bool{true, false, false}},
		{" Mp ", "mp", [3]bool{false, true, false}},
		{"MoBiLe", "mobile", [3]bool{false, false, true}},
		{"invalid", "open", [3]bool{true, false, false}},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			fixture := newConfigFixture(t)
			fixture.SetValue("wechat_connect", map[string]any{"enabled": true, "mode": tc.raw, "open_app_id": " open-id ", "mp_app_secret": " mp-secret ", "mobile_app_id": " mobile-id ", "mobile_app_secret": " mobile-secret ", "frontend_redirect_url": ""})
			cfg, err := fixture.Load()
			require.NoError(t, err)
			value := cfg.Auth.OAuth.WeChat.IdentityDefaults()
			require.Equal(t, tc.mode, value.Mode)
			require.Equal(t, tc.flags, [3]bool{value.OpenEnabled, value.MPEnabled, value.MobileEnabled})
			require.Equal(t, [6]string{"open-id", "mp-secret", "open-id", "mp-secret", "mobile-id", "mobile-secret"}, [6]string{value.OpenAppID, value.OpenAppSecret, value.MPAppID, value.MPAppSecret, value.MobileAppID, value.MobileAppSecret})
			require.Equal(t, "/auth/wechat/callback", value.FrontendRedirectURL)
		})
	}
}

// TestUpgradeV2CurrentKeysHaveSources audits the migration in the reverse direction.
// Each decoded leaf must originate in the frozen schema, a consolidation rule,
// or an explicitly imported module setting, preventing accidental orphan fields.
func TestUpgradeV2CurrentKeysHaveSources(t *testing.T) {
	sources := map[string]bool{"config_version": true}
	for _, entry := range extendedEnvironment {
		sources[entry.Path] = true
	}
	for _, entry := range v1Keys {
		for _, move := range v2Moves {
			if entry.Path == move.Old || strings.HasPrefix(entry.Path, move.Old+".") {
				target := move.New + strings.TrimPrefix(entry.Path, move.Old)
				sources[target] = true
				if entry.Kind == kindStructList {
					sources[target+".url"] = true
					sources[target+".parser"] = true
				}
				break
			}
		}
	}
	for _, reason := range v2Removed {
		if path, ok := strings.CutPrefix(reason, "merged into "); ok {
			sources[path] = true
		}
	}
	for path, typ := range knownConfigKeys() {
		if typ.Kind() != reflect.Struct {
			require.True(t, sources[path], path)
		}
	}
}

// TestUpgradeV2DurationPrecision retains nanoseconds beyond floating-point integer precision.
// Rounding must add a second when needed and reject results that would overflow
// when application consumers convert the new whole-second field back to a duration.
func TestUpgradeV2DurationPrecision(t *testing.T) {
	for _, tc := range []struct {
		value   any    // historical duration scalar input
		want    string // expected whole second representation
		invalid bool   // unrepresentable converted duration flag
	}{
		{"3000h0m0.000000001s", "10800001", false},
		{int64(10800000000000001), "10800001", false},
		{int64(9223372036000000000), "9223372036", false},
		{int64(9223372036000000001), "", true},
		{1e30, "", true},
	} {
		var report upgradeReport
		seconds, err := legacyDurationSeconds(configValueNode(tc.value), "gateway.scheduling.slot_cleanup_interval", &report)
		if tc.invalid {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tc.want, fmt.Sprint(seconds))
	}
}
