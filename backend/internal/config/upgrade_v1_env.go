package config

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
)

// upgrade_v1_env.go imports the implicit environment accepted by version 1.
// Candidate order is frozen with the historical schema. Explicit empty proxy
// lists retain their presence, while ordinary empty variables remain absent.
// Sensitive inputs become variable references so the upgraded configuration
// never serializes their resolved values into the deployment's file. Database
// and Redis host inputs also remain references because container restarts can
// assign new dependency addresses while retaining the installed configuration.
//
// WeChat compatibility considers current file and environment presence before
// accepting retired credential pairs. Additional module-owned variables follow
// their former parsing rules and write directly to version 2 destinations.
// Invalid typed scalar inputs stop migration without printing their contents;
// permissive legacy options instead retain defaults and record names only.

var (
	// v1ReferenceKeys retains credentials and container dependency addresses as
	// explicit references so secrets stay private and discovered hosts remain current.
	v1ReferenceKeys = map[string]bool{
		"database.host": true, "redis.host": true,
		"database.password": true, "redis.password": true, "jwt.secret": true, "totp.encryption_key": true,
		"linuxdo_connect.client_secret": true, "wechat_connect.app_secret": true, "wechat_connect.open_app_secret": true,
		"wechat_connect.mp_app_secret": true, "wechat_connect.mobile_app_secret": true, "oidc_connect.client_secret": true,
		"dingtalk_connect.client_secret": true, "github_oauth.client_secret": true, "google_oauth.client_secret": true,
		"gemini.oauth.client_secret": true, "update.proxy_url": true, "plugins.download_proxy": true, "plugins.plugin_http_proxy": true,
	}

	// extendedEnvironment lists variables formerly read outside the main loader.
	// The conversion rule retains each old reader's accepted values and defaults.
	extendedEnvironment = []extendedEnvKey{
		{[]string{"UPDATE_GITHUB_TOKEN"}, "update.github_token", "secret"},
		{[]string{"PAYMENT_RESUME_SIGNING_KEY"}, "payment.resume_signing_key", "secret"},
		{[]string{"ANTIGRAVITY_OAUTH_CLIENT_SECRET"}, "provider.antigravity.oauth_client_secret", "secret"},
		{[]string{"ANTIGRAVITY_USER_AGENT_VERSION"}, "provider.antigravity.user_agent_version", "version"},
		{[]string{"GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL"}, "provider.antigravity.forward_endpoint", "endpoint"},
		{[]string{"XAI_OAUTH_AUTHORIZE_URL"}, "provider.grok.oauth.authorize_url", "string"},
		{[]string{"XAI_OAUTH_TOKEN_URL"}, "provider.grok.oauth.token_url", "string"},
		{[]string{"XAI_OAUTH_CLIENT_ID"}, "provider.grok.oauth.client_id", "string"},
		{[]string{"XAI_OAUTH_SCOPE"}, "provider.grok.oauth.scope", "string"},
		{[]string{"XAI_OAUTH_REDIRECT_URI"}, "provider.grok.oauth.redirect_uri", "string"},
		{[]string{"XAI_BASE_URL"}, "provider.grok.base_url", "string"},
		{[]string{"XAI_ALLOW_UNSAFE_URL_OVERRIDES"}, "provider.grok.allow_unsafe_url_overrides", "grok_bool"},
		{[]string{"XAI_GROK_UNSAFE_ALLOW_CONCURRENCY_GT_ONE"}, "provider.grok.unsafe_allow_concurrency_above_one", "grok_bool"},
		{[]string{"XAI_GROK_CLI_VERSION"}, "provider.grok.cli_version", "string"},
		{[]string{"YESCAPTCHA_CLIENT_KEY", "YESCAPTCHA_API_KEY"}, "provider.grok.yescaptcha_client_key", "secret"},
		{[]string{"TOKENROUTER_CLAUDE_CLI_VERSION", "SUB2API_CLAUDE_CLI_VERSION"}, "provider.anthropic.claude_cli_version", "string"},
		{[]string{"OPS_WS_TRUST_PROXY"}, "ops.websocket.trust_proxy", "bool"},
		{[]string{"OPS_WS_TRUSTED_PROXIES"}, "ops.websocket.trusted_proxies", "proxies"},
		{[]string{"OPS_WS_ORIGIN_POLICY"}, "ops.websocket.origin_policy", "origin"},
		{[]string{"OPS_WS_MAX_CONNS"}, "ops.websocket.max_conns", "positive"},
		{[]string{"OPS_WS_MAX_CONNS_PER_IP"}, "ops.websocket.max_conns_per_ip", "nonnegative"},
		{[]string{"TOKENROUTER_DEBUG_MODEL_ROUTING", "SUB2API_DEBUG_MODEL_ROUTING"}, "debug.model_routing_log", "truthy"},
		{[]string{"TOKENROUTER_DEBUG_GATEWAY_BODY", "SUB2API_DEBUG_GATEWAY_BODY"}, "debug.gateway_body_log_file", "string"},
		{[]string{"TOKENROUTER_DEBUG_CLAUDE_MIMIC", "SUB2API_DEBUG_CLAUDE_MIMIC"}, "debug.claude_mimic_log", "truthy"},
	}

	// migrationUserAgentVersion accepts the decimal identity format used by the
	// Antigravity reader without introducing an upstream dependency into configuration.
	migrationUserAgentVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// extendedEnvKey describes ordered legacy names and their target conversion rule.
type extendedEnvKey struct {
	Names []string // ordered legacy variable names
	Path  string   // version two destination path
	Rule  string   // historical scalar conversion rule
}

// hasV1Environment reports whether missing-file migration has a legacy input.
// Ordinary names require nonempty values; the two client-IP list variables use
// presence alone because explicit emptiness clears their effective lists.
func hasV1Environment(lookup func(string) (string, bool)) bool {
	for _, key := range v1Keys {
		for _, name := range key.Env {
			value, present := lookup(name)
			if present && (value != "" || name == "SERVER_TRUSTED_PROXIES" || name == "SECURITY_FORWARDED_CLIENT_IP_HEADERS") {
				return true
			}
		}
	}
	for _, key := range extendedEnvironment {
		for _, name := range key.Names {
			if value, _ := lookup(name); value != "" {
				return true
			}
		}
	}
	for _, name := range []string{"GATEWAY_ANTIGRAVITY_FALLBACK_COOLDOWN_SECONDS", "WECHAT_OAUTH_OPEN_APP_ID", "WECHAT_OAUTH_OPEN_APP_SECRET", "WECHAT_OAUTH_MP_APP_ID", "WECHAT_OAUTH_MP_APP_SECRET", "WECHAT_OAUTH_FRONTEND_REDIRECT_URL"} {
		if value, _ := lookup(name); value != "" {
			return true
		}
	}
	return false
}

// importV1Environment overlays historical file values with eligible environment input.
// Candidate names follow the frozen precedence table, preserving false and zero
// while ordinary empty values fall through to later candidates. The two client-IP
// lists use presence alone, and empty OIDC security variables retain existing
// file values or materialize defaults when absent. Credentials and dependency
// hosts become references to the selected name; ordinary strings escape
// reference-looking text before entering the document.
//
// Null sections suppress ordinary implicit bindings as Viper's key enumeration
// did. Explicitly bound fields remain eligible, and direct OIDC presence checks
// retain their defaults before those sections become ordinary mappings.
// Unsupported aggregate bindings warn by name, and malformed typed scalars fail
// before the upgrade framework can write the changed document.
func importV1Environment(root *yaml.Node, lookup func(string) (string, bool), report *upgradeReport) error {
	nullParents := v1NullParentKeys(root)
	for _, key := range v1Keys {
		for _, name := range key.Env {
			raw, present := lookup(name)
			if !present {
				continue
			}
			explicitList := name == "SERVER_TRUSTED_PROXIES" || name == "SECURITY_FORWARDED_CLIENT_IP_HEADERS"
			explicitOIDC := name == "OIDC_CONNECT_USE_PKCE" || name == "OIDC_CONNECT_VALIDATE_ID_TOKEN"
			if nullParents[key.Path] && len(key.Env) == 1 && !explicitList {
				if !explicitOIDC {
					continue
				}
				// Null sections hid implicit value decoding, but OIDC still observed the
				// variable's presence directly and retained its default true security flag.
				raw = ""
			}
			if name == "TZ" {
				raw = strings.TrimSpace(raw)
			}
			if raw == "" && !explicitList && !explicitOIDC {
				continue
			}
			var value any
			var err error
			switch {
			case explicitList:
				value = normalizeStringSlice(strings.Split(raw, ","))
			case explicitOIDC && raw == "":
				// Viper ignored empty scalar overrides, so a file's explicit false must
				// survive; only an omitted field needs its default persisted for presence.
				if configPath(root, key.Path) != nil {
					continue
				}
				value = true
			case v1ReferenceKeys[key.Path]:
				value = "${" + name + "}"
			default:
				switch key.Kind {
				case kindString, kindDuration:
					value = strings.ReplaceAll(raw, "${", "$${")
				case kindBool:
					value, err = strconv.ParseBool(strings.TrimSpace(raw))
				case kindInt:
					value, err = strconv.ParseInt(strings.TrimSpace(raw), 0, 64)
				case kindFloat:
					value, err = strconv.ParseFloat(strings.TrimSpace(raw), 64)
				case kindStringList:
					parts := strings.Split(raw, ",")
					for i := range parts {
						parts[i] = strings.ReplaceAll(parts[i], "${", "$${")
					}
					value = parts
				default:
					report.Warnings = append(report.Warnings, name+": non-scalar legacy environment binding skipped")
					continue
				}
			}
			if err != nil {
				return fmt.Errorf("environment variable %s has an invalid value for %s", name, key.Path)
			}
			if err = setConfigPath(root, key.Path, configValueNode(value)); err != nil {
				return err
			}
			report.ImportedEnv = append(report.ImportedEnv, name)
			break
		}
	}
	return importLegacyWeChatEnvironment(root, lookup, report)
}

// v1NullParentKeys records fields hidden by a null section before import mutates it.
// Viper enumerated such sections as single keys, so their default descendants
// could not discover ordinary implicit environment overrides. Explicit bindings
// remained enumerated: the frozen multi-name entries and two client-IP lists
// retain that distinction in importV1Environment, while OIDC has separate presence rules.
//
// Snapshotting every affected field first prevents an earlier imported child
// from accidentally enabling implicit overrides for its remaining sibling fields.
func v1NullParentKeys(root *yaml.Node) map[string]bool {
	hidden := map[string]bool{}
	for _, key := range v1Keys {
		parts := strings.Split(key.Path, ".")
		for index := 1; index < len(parts); index++ {
			parent := configPath(root, strings.Join(parts[:index], "."))
			if parent != nil && parent.Tag == "!!null" {
				hidden[key.Path] = true
				break
			}
		}
	}
	return hidden
}

// importLegacyWeChatEnvironment retains the retired Open and MP credential rules.
// Current YAML or environment presence blocks each corresponding assignment,
// including empty variables. Generic current credentials also block retired
// mode credentials, so a legacy pair cannot replace a deliberate current choice.
//
// Complete eligible pairs may enable login and their mode; Open wins mode and
// scope selection when both pairs qualify. Individual current flags, mode, scope,
// and frontend redirect remain authoritative independently. Imported secrets use
// references while credential completeness is determined from actual environment
// values before those references enter the document.
func importLegacyWeChatEnvironment(root *yaml.Node, lookup func(string) (string, bool), report *upgradeReport) error {
	eligible := func(field string) bool {
		_, present := lookup("WECHAT_CONNECT_" + strings.ToUpper(field))
		return configPath(root, "wechat_connect."+field) == nil && !present
	}
	write := func(field string, value any) error {
		return setConfigPath(root, "wechat_connect."+field, configValueNode(value))
	}
	pairs := map[string]bool{}
	for _, mode := range []string{"open", "mp"} {
		complete := true
		for _, suffix := range []string{"app_id", "app_secret"} {
			name := "WECHAT_OAUTH_" + strings.ToUpper(mode+"_"+suffix)
			raw, _ := lookup(name)
			raw = strings.TrimSpace(raw)
			if !eligible(mode+"_"+suffix) || !eligible(suffix) || raw == "" {
				complete = false
				continue
			}
			value := strings.ReplaceAll(raw, "${", "$${")
			if suffix == "app_secret" {
				value = "${" + name + "}"
			}
			if err := write(mode+"_"+suffix, value); err != nil {
				return err
			}
			report.ImportedEnv = append(report.ImportedEnv, name)
		}
		pairs[mode] = complete
	}
	if raw, _ := lookup("WECHAT_OAUTH_FRONTEND_REDIRECT_URL"); strings.TrimSpace(raw) != "" && eligible("frontend_redirect_url") {
		if err := write("frontend_redirect_url", strings.ReplaceAll(strings.TrimSpace(raw), "${", "$${")); err != nil {
			return err
		}
		report.ImportedEnv = append(report.ImportedEnv, "WECHAT_OAUTH_FRONTEND_REDIRECT_URL")
	}
	if !pairs["open"] && !pairs["mp"] {
		return nil
	}
	for _, field := range []string{"enabled", "open_enabled", "mp_enabled"} {
		active := field == "enabled" || field == "open_enabled" && pairs["open"] || field == "mp_enabled" && pairs["mp"]
		if active && eligible(field) {
			if err := write(field, true); err != nil {
				return err
			}
		}
	}
	mode := "mp"
	if pairs["open"] {
		mode = "open"
	}
	if eligible("mode") {
		if err := write("mode", mode); err != nil {
			return err
		}
	}
	if eligible("scopes") {
		return write("scopes", authconfig.DefaultWeChatConnectScopesForMode(mode))
	}
	return nil
}

// importExtendedEnvironment imports settings previously read by individual modules.
// Each reader retains its own trimming and validation rules. Current product-name
// aliases keep precedence for nonempty raw input, including whitespace, while
// ordinary URL readers can fall through on trimmed emptiness. A selected invalid
// permissive value warns and keeps the default instead of trying another alias.
// Secrets remain references to their selected variable names, and reports never
// retain resolved values. The resulting fields are written directly at their
// version 2 paths before normal decoding and validation.
func importExtendedEnvironment(root *yaml.Node, lookup func(string) (string, bool), report *upgradeReport) error {
	for _, key := range extendedEnvironment {
		for _, name := range key.Names {
			raw, present := lookup(name)
			if !present || raw == "" {
				continue
			}
			trimmed := strings.TrimSpace(raw)
			// Most secret readers distinguish raw presence from empty input. YesCaptcha
			// trims before trying its alternate name, so retain that separate alias rule.
			preserveSecret := key.Rule == "secret" && key.Path != "provider.grok.yescaptcha_client_key"
			if trimmed == "" && !preserveSecret && !strings.HasPrefix(name, "TOKENROUTER_") && !strings.HasPrefix(name, "SUB2API_") {
				continue
			}
			raw = trimmed
			value, valid := convertExtendedEnvironment(key.Rule, raw, name)
			if !valid {
				report.Warnings = append(report.Warnings, name+": invalid legacy value ignored")
				break
			}
			if err := setConfigPath(root, key.Path, configValueNode(value)); err != nil {
				return err
			}
			report.ImportedEnv = append(report.ImportedEnv, name)
			break
		}
	}
	return nil
}

// convertExtendedEnvironment applies one former module reader's conversion rule.
// Invalid permissive values return false without exposing their contents. URL
// trust decisions remain the responsibility of the configured upstream consumer.
func convertExtendedEnvironment(rule, raw, name string) (any, bool) {
	switch rule {
	case "secret":
		return "${" + name + "}", true
	case "version":
		return raw, migrationUserAgentVersion.MatchString(raw)
	case "endpoint":
		if raw == "" {
			return nil, false
		}
		if value := strings.ToLower(raw); value == "daily" || value == "sandbox" {
			return "daily", true
		}
		return "prod", true
	case "truthy", "grok_bool":
		value := strings.ToLower(raw)
		return value == "1" || value == "true" || value == "yes" || value == "on" || rule == "grok_bool" && value == "y", true
	case "bool":
		value, err := strconv.ParseBool(raw)
		return value, err == nil
	case "positive", "nonnegative":
		value, err := strconv.Atoi(raw)
		return value, err == nil && (value > 0 || rule == "nonnegative" && value == 0)
	case "origin":
		value := strings.ToLower(raw)
		return value, value == "strict" || value == "permissive"
	case "proxies":
		var values []string
		for part := range strings.SplitSeq(raw, ",") {
			part = strings.TrimSpace(part)
			prefix, err := netip.ParsePrefix(part)
			if err == nil {
				values = append(values, prefix.Masked().String())
				continue
			}
			address, err := netip.ParseAddr(part)
			if err == nil {
				values = append(values, netip.PrefixFrom(address.Unmap(), address.Unmap().BitLen()).String())
			}
		}
		return values, len(values) > 0
	default:
		return strings.ReplaceAll(raw, "${", "$${"), true
	}
}
