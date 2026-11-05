package config

import (
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ignored_env.go identifies residual deployment variables after successful loading.
// Current configuration uses file values and explicit references. Historical
// environment names remain recognizable for diagnostics, allowing operators to
// remove ineffective overrides without exposing their contents in logs.
//
// Installer inputs are compared with effective settings because Compose can
// legitimately retain them after installation. Explicit references exempt their
// named variables, including defaults, while ordinary legacy variables warn on
// every start. The two client-IP lists treat empty presence as meaningful, matching
// their historical ability to clear configured lists. Diagnostics combine and sort
// names so one startup produces one actionable warning.

var (
	// setupEnvironmentPaths maps retained installer inputs to their effective file
	// settings; mismatches indicate a stale deployment value after initial installation.
	setupEnvironmentPaths = map[string]string{
		"DATABASE_HOST": "database.host", "DATABASE_PORT": "database.port", "DATABASE_USER": "database.user", "DATABASE_PASSWORD": "database.password", "DATABASE_DBNAME": "database.dbname", "DATABASE_SSLMODE": "database.sslmode",
		"REDIS_HOST": "redis.host", "REDIS_PORT": "redis.port", "REDIS_USERNAME": "redis.username", "REDIS_PASSWORD": "redis.password", "REDIS_DB": "redis.db", "REDIS_ENABLE_TLS": "redis.enable_tls",
		"SERVER_HOST": "server.host", "SERVER_PORT": "server.port", "SERVER_MODE": "server.mode", "TZ": "timezone", "JWT_SECRET": "auth.jwt.secret", "TOTP_ENCRYPTION_KEY": "security.secret_encryption_key", "JWT_EXPIRE_HOUR": "auth.jwt.access_token_ttl_minutes",
	}

	// retiredEnvironmentNames includes ignored settings that no longer have decoded
	// fields, along with retired direct readers and WeChat compatibility inputs.
	retiredEnvironmentNames = []string{
		"GATEWAY_OPENAI_WS_ALLOW_STORE_RECOVERY",
		"GATEWAY_OPENAI_WS_APIKEY_ENABLED",
		"GATEWAY_OPENAI_WS_ENABLED",
		"GATEWAY_OPENAI_WS_EVENT_FLUSH_BATCH_SIZE",
		"GATEWAY_OPENAI_WS_EVENT_FLUSH_INTERVAL_MS",
		"GATEWAY_OPENAI_WS_FORCE_HTTP",
		"GATEWAY_OPENAI_WS_HTTP_BRIDGE_ENABLED",
		"GATEWAY_OPENAI_WS_INGRESS_MODE_DEFAULT",
		"GATEWAY_OPENAI_WS_OAUTH_ENABLED",
		"GATEWAY_OPENAI_WS_PAYLOAD_LOG_SAMPLE_RATE",
		"GATEWAY_OPENAI_WS_PREWARM_GENERATE_ENABLED",
		"GATEWAY_OPENAI_WS_RESPONSES_WEBSOCKETS",
		"GATEWAY_OPENAI_WS_RETRY_BACKOFF_INITIAL_MS",
		"GATEWAY_OPENAI_WS_RETRY_BACKOFF_MAX_MS",
		"GATEWAY_OPENAI_WS_RETRY_JITTER_RATIO",
		"GATEWAY_OPENAI_WS_RETRY_TOTAL_BUDGET_MS",
		"GATEWAY_OPENAI_WS_STORE_DISABLED_CONN_MODE",
		"GATEWAY_OPENAI_WS_STORE_DISABLED_FORCE_NEW_CONN",

		"RUN_MODE", "SUBSCRIPTION_MAINTENANCE_WORKER_COUNT", "SUBSCRIPTION_MAINTENANCE_QUEUE_SIZE", "GATEWAY_OPENAI_WS_FALLBACK_COOLDOWN_SECONDS", "BATCH_IMAGE_ENABLED", "BATCH_IMAGE_QUEUE_ENABLED", "BATCH_IMAGE_VERTEX_ENABLED", "PRICING_HASH_URL", "PRICING_UPDATE_INTERVAL_HOURS", "PRICING_OVERRIDE_FILE", "ALIPAY_MOBILE_PRECREATE_DEEP_LINK", "GEMINI_CLI_OAUTH_CLIENT_SECRET", "GATEWAY_ANTIGRAVITY_FALLBACK_COOLDOWN_SECONDS",
		"WECHAT_OAUTH_OPEN_APP_ID", "WECHAT_OAUTH_OPEN_APP_SECRET", "WECHAT_OAUTH_MP_APP_ID", "WECHAT_OAUTH_MP_APP_SECRET", "WECHAT_OAUTH_FRONTEND_REDIRECT_URL",
	}
)

// warnIgnoredEnvironment emits one warning containing only sorted ignored names.
// Call it after interpolation and validation so installer comparisons use the
// same effective values that application construction will receive.
func warnIgnoredEnvironment(cfg *Config, doc *yaml.Node, path string, env upgradeEnvironment) {
	names := ignoredEnvironmentNames(cfg, doc, env.LookupEnv)
	if len(names) > 0 {
		slog.Warn("environment variables no longer configure TokenRouter and were ignored; edit "+path+" instead", "names", names)
	}
}

// ignoredEnvironmentNames compares legacy presence with references and setup values.
// File references suppress warnings independently of their resolved contents.
// Setup variables warn only when the supplied value differs from the current
// typed field; nonempty ordinary legacy values always warn.
//
// Candidate names are deduplicated before inspection, keeping alias tables and
// direct-reader lists independent. Empty client-IP lists are the sole ordinary
// presence exception, because their old meaning was an explicitly empty policy.
// No returned string contains a resolved value or parser error that could reveal
// a password, token, proxy credential, or OAuth secret.
func ignoredEnvironmentNames(cfg *Config, doc *yaml.Node, lookup func(string) (string, bool)) []string {
	references := map[string]bool{}
	_ = visitConfigStrings(doc, "", func(key string, n *yaml.Node) error {
		_, names, err := expandConfigString(key, n.Value, nil)
		for _, name := range names {
			references[name] = true
		}
		return err
	})
	candidates := map[string]bool{}
	for _, key := range v1Keys {
		for _, name := range key.Env {
			candidates[name] = true
		}
	}
	for _, key := range extendedEnvironment {
		for _, name := range key.Names {
			candidates[name] = true
		}
	}
	for _, name := range retiredEnvironmentNames {
		candidates[name] = true
	}
	for name := range setupEnvironmentPaths {
		candidates[name] = true
	}
	var ignored []string
	for name := range candidates {
		raw, present := lookup(name)
		if !present || references[name] {
			continue
		}
		if raw == "" && name != "SERVER_TRUSTED_PROXIES" && name != "SECURITY_FORWARDED_CLIENT_IP_HEADERS" {
			continue
		}
		if path, setup := setupEnvironmentPaths[name]; setup {
			value := configValueAtPath(reflect.ValueOf(cfg).Elem(), path)
			if name == "JWT_EXPIRE_HOUR" {
				hours, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
				if err == nil && value.IsValid() && hours == value.Int()/60 && value.Int()%60 == 0 {
					continue
				}
			} else if value.IsValid() && environmentMatchesValue(raw, value) {
				continue
			}
		}
		ignored = append(ignored, name)
	}
	slices.Sort(ignored)
	return ignored
}

// configValueAtPath finds a decoded scalar by mapstructure path for setup comparisons.
// An absent field returns an invalid reflection value, allowing diagnostic callers
// to treat outdated mapping entries as mismatches without panicking.
func configValueAtPath(value reflect.Value, path string) reflect.Value {
	for part := range strings.SplitSeq(path, ".") {
		if value.Kind() != reflect.Struct {
			return reflect.Value{}
		}
		found := reflect.Value{}
		for field := range value.Type().Fields() {
			name, _, _ := strings.Cut(field.Tag.Get("mapstructure"), ",")
			if name == part {
				found = value.FieldByIndex(field.Index)
				break
			}
		}
		if !found.IsValid() {
			return found
		}
		value = found
	}
	return value
}

// environmentMatchesValue compares an installer string with its effective scalar value.
// Numeric and boolean spellings use their typed meaning, while strings retain
// exact contents so changed credentials are reported without being displayed.
func environmentMatchesValue(raw string, value reflect.Value) bool {
	switch value.Kind() {
	case reflect.String:
		return raw == value.String()
	case reflect.Bool:
		v, err := strconv.ParseBool(strings.TrimSpace(raw))
		return err == nil && v == value.Bool()
	case reflect.Int, reflect.Int64, reflect.Int32:
		v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		return err == nil && v == value.Int()
	default:
		return raw == fmt.Sprint(value.Interface())
	}
}
