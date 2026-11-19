package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// config.go decodes, normalizes, and validates versioned process configuration.
// Startup selects one YAML document, upgrades historical input through the
// migration layer, rejects unknown keys, and resolves explicit references before
// decoding with local Viper state. Application construction receives the
// resulting settings through typed module options.
//
// Normalize canonicalizes trimmed strings, slices, Live session defaults,
// enabled WebAuthn origins, valid forwarded-IP header snapshots, and valid
// proxy-probe URLs upfront before Validate runs read-only domain checks in
// deterministic startup order. Bootstrap may defer JWT signing-key selection to
// persistent storage before calling Validate again.

// Config holds process startup settings decoded from Viper and supplied to
// application construction. Load normalizes and validates these values, while
// the forwarded-IP accessors separately publish copied live policy snapshots.
type Config struct {
	APIKey        APIKeyConfig        `mapstructure:"api_key"`        // api key settings
	Auth          AuthConfig          `mapstructure:"auth"`           // process auth settings
	BatchImage    BatchImageConfig    `mapstructure:"batch_image"`    // batch image settings
	Billing       BillingConfig       `mapstructure:"billing"`        // process billing settings
	ConfigVersion int                 `mapstructure:"config_version"` // Current configuration schema version.
	Creative      CreativeConfig      `mapstructure:"creative"`       // process creative settings
	Database      DatabaseConfig      `mapstructure:"database"`       // process database settings
	Debug         DebugConfig         `mapstructure:"debug"`          // process debug settings
	Egress        EgressConfig        `mapstructure:"egress"`         // process egress settings
	Environment   string              `mapstructure:"environment"`    // record environment label text
	Gateway       GatewayConfig       `mapstructure:"gateway"`        // process gateway settings
	Idempotency   IdempotencyConfig   `mapstructure:"idempotency"`    // process idempotency settings
	Log           LogConfig           `mapstructure:"log"`            // process log settings
	ModelCatalog  ModelCatalogConfig  `mapstructure:"model_catalog"`  // model catalog settings
	Ops           OpsConfig           `mapstructure:"ops"`            // process ops settings
	OutboundProxy OutboundProxyConfig `mapstructure:"outbound_proxy"` // outbound proxy settings
	Payment       PaymentConfig       `mapstructure:"payment"`        // process payment settings
	Plugins       PluginsConfig       `mapstructure:"plugins"`        // process plugins settings
	Provider      ProviderConfig      `mapstructure:"provider"`       // process provider settings
	Redis         RedisConfig         `mapstructure:"redis"`          // process redis settings
	RequestLog    RequestLogConfig    `mapstructure:"request_log"`    // request log settings
	Scheduling    SchedulingConfig    `mapstructure:"scheduling"`     // process scheduling settings
	Security      SecurityConfig      `mapstructure:"security"`       // process security settings
	Server        ServerConfig        `mapstructure:"server"`         // process server settings
	Site          SiteConfig          `mapstructure:"site"`           // process site settings
	Storage       StorageConfig       `mapstructure:"storage"`        // process storage settings
	Team          TeamConfig          `mapstructure:"team"`           // process team settings
	// Timezone selects the calendar zone initialized by app/bootstrap after
	// loading. The file value and bootstrap empty-value fallback use Asia/Shanghai
	// by default; invalid IANA names fail before time.Local is published.
	Timezone     string             `mapstructure:"timezone"`
	Update       UpdateConfig       `mapstructure:"update"`        // process update settings
	Usage        UsageConfig        `mapstructure:"usage"`         // process usage settings
	UserDefaults UserDefaultsConfig `mapstructure:"user_defaults"` // user defaults settings
	// MCPOAuthSigningSecret is loaded from persistent security storage during
	// bootstrap. File decoding never accepts or overwrites this signing material.
	// MCPOAuthSigningSecret is loaded from persistent security storage during
	// bootstrap. File decoding never accepts or overwrites this signing material.
	MCPOAuthSigningSecret string `mapstructure:"-" json:"-" yaml:"-"`
}

// firstNonEmptyString returns the first trimmed nonblank value in supplied
// order. It returns empty text when every value is blank, allowing credential
// compatibility code to apply its ordered source choices.
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// Load decodes normalized process configuration and requires a nonempty,
// sufficiently long JWT secret during validation. It uses process Viper state,
// so callers serialize loader use and handle its configuration-source,
// decoding, template-read, or validation errors.
func Load() (*Config, error) {
	return load(false)
}

// LoadForBootstrap decodes normalized process configuration while allowing a
// missing JWT secret in the returned value. Database bootstrap must supply the
// persisted secret and run full validation again before identity consumers use
// that configuration.
func LoadForBootstrap() (*Config, error) {
	return load(true)
}

// load resolves the process configuration path and delegates versioned loading.
// Path failures stop startup before installation or application construction can
// consume ambiguous input. Bootstrap callers may defer only the JWT secret.
// @project-doc docs/interfaces/configuration.md#configuration_sources
func load(allowMissingJWTSecret bool) (*Config, error) {
	path, exists, err := ResolveConfigFile()
	if err != nil {
		return nil, err
	}
	return loadFrom(path, exists, processUpgradeEnvironment(), allowMissingJWTSecret)
}

// loadFrom upgrades and validates one explicitly selected configuration file.
// The supplied environment is consulted only for version 1 imports and explicit
// references. Defaults use a local Viper instance, so independent loads do not
// share overrides or bindings. Bootstrap loading can defer JWT secret selection
// until persistent storage is available.
//
// Unknown keys fail before interpolation, and interpolation uses a copied YAML
// graph so disk references remain intact. Presence flags derive from the file
// itself, including explicitly empty lists and false OAuth choices. Validation
// completes before ignored-environment diagnostics describe residual deployment
// variables, and secret values never enter those diagnostics.
func loadFrom(path string, exists bool, env upgradeEnvironment, allowMissingJWTSecret bool) (*Config, error) {
	env = completeUpgradeEnvironment(env)
	doc, err := upgradeConfigFile(path, exists, env)
	if err != nil {
		return nil, err
	}
	if err = checkUnknownKeys(doc); err != nil {
		return nil, err
	}
	decoded, err := interpolateEnvReferences(doc, env.LookupEnv)
	if err != nil {
		return nil, err
	}
	v := viper.New()
	v.SetConfigType("yaml")
	setDefaults(v)
	if decoded != nil {
		raw, err := encodeConfigDocument(decoded)
		if err != nil {
			return nil, err
		}
		if err = v.ReadConfig(bytes.NewReader(raw)); err != nil {
			return nil, fmt.Errorf("read configuration %s", path)
		}
	}
	var cfg Config
	if err = v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("configuration %s contains an invalid value type", path)
	}
	cfg.Server.ClientIP.TrustedProxiesConfigured = v.InConfig("server.client_ip.trusted_proxies")
	cfg.Auth.OAuth.OIDC.UsePKCEExplicit = v.InConfig("auth.oauth.oidc.use_pkce")
	cfg.Auth.OAuth.OIDC.ValidateIDTokenExplicit = v.InConfig("auth.oauth.oidc.validate_id_token")
	cfg.Normalize()

	if _, err := NormalizeForwardedClientIPHeaders(cfg.Server.ClientIP.ForwardedHeaders); err != nil {
		return nil, fmt.Errorf("server.client_ip.forwarded_headers: %w", err)
	}
	if cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile != "" {
		content, err := os.ReadFile(cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile)
		if err != nil {
			return nil, fmt.Errorf("read forced codex instructions template %q: %w", cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile, err)
		}
		cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplate = string(content)
	}

	// Generate thirty-two random bytes as hexadecimal text when the trimmed TOTP
	// key is absent. Mark that key unconfigured so deployment-dependent features
	// can distinguish ephemeral generation from supplied encryption input.
	cfg.Security.SecretEncryptionKey = strings.TrimSpace(cfg.Security.SecretEncryptionKey)
	if cfg.Security.SecretEncryptionKey == "" {
		key, err := generateJWTSecret(32)
		if err != nil {
			return nil, fmt.Errorf("generate secret encryption key: %w", err)
		}
		cfg.Security.SecretEncryptionKey = key
		cfg.Security.SecretEncryptionKeyConfigured = false
		slog.Warn("security.secret_encryption_key is empty; generated an ephemeral key that cannot enable persistent credential storage.")
	} else {
		cfg.Security.SecretEncryptionKeyConfigured = true
	}

	originalJWTSecret := cfg.Auth.JWT.Secret
	if allowMissingJWTSecret && originalJWTSecret == "" {
		// Supply temporary length-valid text for the missing JWT input so other
		// startup fields can undergo full Validate. Restore absence after success,
		// leaving persistent secret selection to database bootstrap.
		cfg.Auth.JWT.Secret = strings.Repeat("0", 32)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config error: %w", err)
	}

	if allowMissingJWTSecret && originalJWTSecret == "" {
		cfg.Auth.JWT.Secret = ""
	}

	if !cfg.Security.URLAllowlist.Enabled {
		slog.Warn("security.url_allowlist.enabled=false; allowlist/SSRF checks disabled (minimal format validation only).")
	}
	if !cfg.Gateway.ResponseHeaders.Enabled {
		slog.Warn("gateway.upstream_response_headers.enabled=false; configurable header filtering disabled (default allowlist only).")
	}

	if cfg.Auth.JWT.Secret != "" && isWeakJWTSecret(cfg.Auth.JWT.Secret) {
		slog.Warn("JWT secret appears weak; use a 32+ character random secret in production.")
	}
	if len(cfg.Gateway.ResponseHeaders.AdditionalAllowed) > 0 || len(cfg.Gateway.ResponseHeaders.ForceRemove) > 0 {
		slog.Info("response header policy configured",
			"additional_allowed", cfg.Gateway.ResponseHeaders.AdditionalAllowed,
			"force_remove", cfg.Gateway.ResponseHeaders.ForceRemove,
		)
	}

	warnIgnoredEnvironment(&cfg, doc, path, env)
	return &cfg, nil
}

// Normalize canonicalizes strings, slice entries, directory fallbacks, Live
// session duration defaults, enabled WebAuthn origins, and valid forwarded-IP
// or proxy-probe lists in place. Invalid forwarded-IP headers or proxy-probe
// entries remain untouched so subsequent validation reports their exact errors.
func (c *Config) Normalize() {
	if c == nil {
		return
	}
	c.normalizeServer()
	c.Plugins.normalize(c.Storage.Dir)
	c.Usage.ExportTasks.normalize(c.Storage.Dir)
	c.Usage.DashboardCache.KeyPrefix = strings.TrimSpace(c.Usage.DashboardCache.KeyPrefix)
	c.normalizeAuth()
	c.normalizeGateway()
}

// Validate normalizes receiver fields upfront and then checks deployment
// constraints in deterministic startup order without further receiver mutation.
// Callers serialize Validate before sharing the configuration concurrently.
func (c *Config) Validate() error {
	c.Normalize()

	if err := c.validateServerMode(); err != nil {
		return err
	}
	if err := c.validateGatewayEarlyEnums(); err != nil {
		return err
	}
	if err := c.validateOpsWebSocket(); err != nil {
		return err
	}
	if err := c.Plugins.validate(); err != nil {
		return err
	}
	if err := c.validateRequestLog(); err != nil {
		return err
	}
	if err := c.validateServerIngress(); err != nil {
		return err
	}
	if err := c.validateAPIKeyAbuse(); err != nil {
		return err
	}
	if err := c.validateJWTSecret(); err != nil {
		return err
	}
	if err := c.validateLog(); err != nil {
		return err
	}
	if err := c.validateGeminiOAuthPair(); err != nil {
		return err
	}
	if err := c.validateFrontendURL(); err != nil {
		return err
	}
	if err := c.validateWebAuthn(); err != nil {
		return err
	}
	if err := c.validateJWTLifetimes(); err != nil {
		return err
	}
	if err := c.validateCSP(); err != nil {
		return err
	}
	if err := c.validateLinuxDoOAuth(); err != nil {
		return err
	}
	if err := c.validateWeChatOAuth(); err != nil {
		return err
	}
	if err := c.validateOIDC(); err != nil {
		return err
	}
	if err := c.validateRuntimeDomains(); err != nil {
		return err
	}
	if err := c.validateGateway(); err != nil {
		return err
	}
	if err := c.validateOpsCleanup(); err != nil {
		return err
	}
	if err := c.validateStreamingWaitPing(); err != nil {
		return err
	}
	if err := c.validateGrokFreeQuota(); err != nil {
		return err
	}
	if err := ValidateDingTalkConfig(c.Auth.OAuth.DingTalk); err != nil {
		return fmt.Errorf("auth.oauth.dingtalk: %w", err)
	}
	return nil
}

// normalizeStringSlice returns trimmed nonblank entries in their supplied
// order. Empty input retains its nil or allocated-empty shape; nonempty input
// gets new outer storage and can return an allocated empty result.
func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return values
	}
	normalized := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	return normalized
}
