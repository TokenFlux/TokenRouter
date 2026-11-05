package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/TokenFlux/TokenRouter/internal/app/bootstrap"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// setup.go initializes storage and administrator access, then writes
// installation configuration and markers.
// Installation remains disabled when either its configuration file or its
// separate marker exists. Setup probes storage, applies migrations, creates an
// initial administrator only for an empty user database, writes runtime
// configuration, and finally writes the marker. Connection probing can create
// the target PostgreSQL database before later stages begin.
//
// These stages use separate operations and have persistent partial results when
// a later stage fails. An already-written configuration file disables another
// ordinary Install attempt even if marker creation failed. HTTP callers
// serialize installation in their handler; automatic setup relies on the
// startup caller to check NeedsSetup before this sequence. Administrator
// credentials are used during creation and omitted from the generated
// configuration.

const (
	// ConfigFileName and InstallLockFile identify separate persisted evidence of
	// installation; NeedsSetup requires both to be absent before allowing ordinary setup.
	ConfigFileName  = "config.yaml" // generated runtime configuration filename
	InstallLockFile = ".installed"  // separate installation marker filename

	// These defaults give a new administrator five concurrent requests and bound
	// setup migrations to one minute when no positive custom budget is supplied.
	defaultUserConcurrency  = 5
	defaultMigrationTimeout = 60 * time.Second

	// These identity bootstrap reasons distinguish an existing administrator from
	// a nonempty user database whose credentials setup also leaves unchanged.
	adminBootstrapReasonAdminExists            = "admin_exists"              // existing administrator skip reason
	adminBootstrapReasonUsersExistWithoutAdmin = "users_exist_without_admin" // nonempty user database skip reason
)

// SetupConfig holds installation inputs for storage probes, initial identity
// creation, and configuration output.
type SetupConfig struct {
	// SecretEncryptionKey supplies a stable encryption key for later application starts.
	// Empty input generates a new key before configuration is persisted during setup.
	SecretEncryptionKey string `json:"-" yaml:"-"`
	// environmentReferences is set only by automatic setup after reading actual
	// credentials and hosts for connection probes; file output retains their names
	// so container dependency addresses can change between application starts.
	environmentReferences bool

	Database DatabaseConfig `json:"database" yaml:"database"` // installation PostgreSQL connection settings
	Redis    RedisConfig    `json:"redis" yaml:"redis"`       // installation Redis connection settings
	// Admin is passed to empty-database bootstrap during installation. Runtime YAML
	// omits these credentials when writeConfigFile emits the configuration for later startup.
	Admin  AdminConfig  `json:"admin" yaml:"-"`
	Server ServerConfig `json:"server" yaml:"server"` // persisted HTTP listener settings
	JWT    JWTConfig    `json:"jwt" yaml:"jwt"`       // persisted signing and expiry settings
	// Timezone selects local-day rules written into runtime YAML. An empty value
	// is replaced by Asia/Shanghai when writeConfigFile builds that document.
	Timezone string `json:"timezone" yaml:"timezone"`
	// MigrationTimeoutSeconds overrides the setup migration budget when positive.
	// Nonpositive values use the default minute, and writeConfigFile omits this setup-only option.
	MigrationTimeoutSeconds int `json:"migration_timeout_seconds" yaml:"migration_timeout_seconds,omitempty"`
}

// DatabaseConfig aliases bootstrap's temporary PostgreSQL connection settings
// used by installation operations.
type DatabaseConfig = bootstrap.SetupDatabaseConfig

// RedisConfig aliases bootstrap's temporary Redis connection settings used by
// installation probes.
type RedisConfig = bootstrap.SetupRedisConfig

// AdminConfig carries credentials considered when the empty database needs its
// initial administrator.
type AdminConfig struct {
	Email string `json:"email"` // initial administrator email text
	// Password is used when initial creation proceeds. A blank password lets
	// identity bootstrap invoke createAdminUser's generator and update this field.
	Password string `json:"password"`
}

// ServerConfig contains the listener and Gin mode written into the startup
// configuration.
type ServerConfig struct {
	Host string `json:"host" yaml:"host"` // configured listener host text
	Port int    `json:"port" yaml:"port"` // configured listener TCP port
	Mode string `json:"mode" yaml:"mode"` // configured Gin mode text
}

// JWTConfig holds signing and expiry settings persisted for subsequent
// application startup.
type JWTConfig struct {
	// Secret is persisted for application startup. Install replaces a literally
	// empty value with generated hexadecimal text before beginning its storage probes.
	Secret     string `json:"secret" yaml:"secret"`
	ExpireHour int    `json:"expire_hour" yaml:"expire_hour"` // access-token lifetime in hours
}

// setupDefaultAdminConcurrency returns the fixed concurrency granted to a newly
// bootstrapped administrator.
func setupDefaultAdminConcurrency() int {
	return defaultUserConcurrency
}

// GetDataDir returns the shared configuration data-directory selection.
// DATA_DIR takes precedence; otherwise a writable container directory is selected
// before the current directory. Configuration file selection can independently
// use CONFIG_FILE or a discovered system location.
func GetDataDir() string { return config.DataDir() }

// GetConfigFilePath returns the shared destination for newly installed configuration.
// An explicit CONFIG_FILE path wins even when absent. Otherwise the file lives
// in the selected data directory, alongside the independent installation marker.
func GetConfigFilePath() string {
	return config.DefaultConfigFilePath()
}

// GetInstallLockPath returns the marker filename under the directory selected
// by GetDataDir. Directory selection can perform its /app/data write probe, and
// the marker independently records completion of setup.
func GetInstallLockPath() string {
	return GetDataDir() + "/" + InstallLockFile
}

// skipSetupEnabled reports whether trimmed, case-insensitive SKIP_SETUP text
// selects true, one, or yes. Missing and other values leave file-based setup
// detection active so deployment startup can inspect installation evidence.
func skipSetupEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SKIP_SETUP"))) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// NeedsSetup reports whether ordinary installation is enabled and both
// persisted evidence paths are absent. SKIP_SETUP disables it before file
// checks, using the normalized values accepted by skipSetupEnabled.
//
// Either existing file or a stat error other than nonexistence rejects setup.
// This prevents removal of just the configuration or just the marker from
// reopening installation, and avoids admitting setup when filesystem errors
// leave prior installation uncertain. Path lookup may perform GetDataDir's
// container-directory probe; callers coordinate any installation that can race
// with these checks.
func NeedsSetup() bool {
	if skipSetupEnabled() {
		logging.L().Debug("setup.needs_setup_bypassed", zap.String("reason", "skip_setup_enabled"))
		return false
	}

	// Treat existing or uncertain installation evidence as a closed setup path.
	// Both paths must report nonexistence before another installation can be admitted.
	if _, exists, err := config.ResolveConfigFile(); exists || err != nil {
		return false
	}

	if _, err := os.Stat(GetInstallLockPath()); !os.IsNotExist(err) {
		return false
	}

	return true
}

// TestDatabaseConnection delegates maintenance and target probing to bootstrap
// with nonnil cfg. The probe can create a missing target database; callers
// supply a validated database identifier and libpq-compatible field text for
// its interpolated DSNs.
func TestDatabaseConnection(cfg *DatabaseConfig) error {
	return bootstrap.TestSetupDatabaseConnection(cfg)
}

// TestRedisConnection delegates a temporary Redis Ping probe using nonnil cfg.
// Bootstrap applies optional TLS, closes the client, and uses go-redis
// option-based socket budgets plus eligible retries, so its supplied
// five-second context is not a strict end-to-end cap.
func TestRedisConnection(cfg *RedisConfig) error { return bootstrap.TestSetupRedisConnection(cfg) }

// Install performs ordinary first installation after checking NeedsSetup. cfg
// must be nonnil and contain validated storage and administrator inputs. This
// sequence performs no complete configuration validation or cross-process
// installation locking.
//
// An empty JWT secret is generated and assigned to cfg before probing
// PostgreSQL and Redis. An io.ReadFull error in crypto/rand.Read terminates the
// process before either probe. Database probing can create the target database,
// migrations follow successful probes, and administrator bootstrap keeps
// existing user databases unchanged. Configuration is written before the
// separate marker. Earlier database effects survive later errors, and a written
// configuration file already disables later ordinary setup even if marker
// creation fails.
func Install(cfg *SetupConfig) error {
	if !NeedsSetup() {
		return fmt.Errorf("system is already installed, re-installation is not allowed")
	}

	if cfg.JWT.Secret == "" {
		secret, err := generateSecret(32)
		if err != nil {
			return fmt.Errorf("failed to generate jwt secret: %w", err)
		}
		cfg.JWT.Secret = secret
		logging.LegacyPrintf("setup", "%s", "Warning: JWT secret auto-generated. Consider setting a fixed secret for production.")
	}

	if err := TestDatabaseConnection(&cfg.Database); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}

	if err := TestRedisConnection(&cfg.Redis); err != nil {
		return fmt.Errorf("redis connection failed: %w", err)
	}

	if err := initializeDatabase(cfg); err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}

	// Migrations establish the schema before identity bootstrap counts existing users.
	// Existing users skip administrator creation, retaining their stored passwords and roles.
	if _, _, err := createAdminUser(cfg); err != nil {
		return fmt.Errorf("admin user creation failed: %w", err)
	}

	if err := writeConfigFile(cfg); err != nil {
		return fmt.Errorf("config file creation failed: %w", err)
	}

	// Configuration is already persisted before the marker is attempted. The marker
	// independently keeps setup closed after configuration removal, and its failure leaves that configuration present.
	if err := createInstallLock(); err != nil {
		return fmt.Errorf("failed to create install lock: %w", err)
	}

	return nil
}

// createInstallLock writes the UTC installation timestamp at
// GetInstallLockPath. A newly created marker receives owner-read permission;
// os.WriteFile retains an existing file's mode, and write errors are returned
// after any prior configuration write.
func createInstallLock() error {
	content := fmt.Sprintf("installed_at=%s\n", time.Now().UTC().Format(time.RFC3339))
	return os.WriteFile(GetInstallLockPath(), []byte(content), 0o400) // Requested creation mode permits owner reads.
}

// initializeDatabase applies embedded migrations through bootstrap using a
// nonnil setup configuration. It supplies a Background-derived migration
// timeout, while bootstrap owns the temporary target pool and returns migration
// or connection failure.
func initializeDatabase(cfg *SetupConfig) error {
	return bootstrap.InitializeSetupDatabase(context.Background(), &cfg.Database, cfg.migrationTimeout())
}

// migrationTimeout returns a positive configured number of seconds as a
// duration. Nil cfg and nonpositive values use the default minute; callers keep
// large positive values within time.Duration conversion limits.
func (cfg *SetupConfig) migrationTimeout() time.Duration {
	if cfg != nil && cfg.MigrationTimeoutSeconds > 0 {
		// Convert setup seconds to the duration consumed by bootstrap migration context.
		return time.Duration(cfg.MigrationTimeoutSeconds) * time.Second
	}
	return defaultMigrationTimeout
}

// createAdminUser delegates identity bootstrap and returns its creation flag,
// decision reason, and error. A nonnil cfg supplies storage and credentials,
// while existing user data causes bootstrap to skip creation.
//
// When an empty or whitespace-only password needs generation, the callback
// assigns sixteen random bytes as hexadecimal text into cfg.Admin.Password and
// prints it for the operator during the creation attempt. Bootstrap owns its
// temporary pool and operation context; a generated password can be printed
// before a later creation error.
func createAdminUser(cfg *SetupConfig) (bool, string, error) {
	return bootstrap.InitializeSetupAdmin(context.Background(), &cfg.Database, identity.InitialAdminInput{Email: cfg.Admin.Email, Password: cfg.Admin.Password, Concurrency: setupDefaultAdminConcurrency(), Now: time.Now}, func() (string, error) {
		password, err := generateSecret(16)
		if err != nil {
			return "", err
		}
		cfg.Admin.Password = password
		fmt.Printf("Generated admin password (one-time): %s\n", password)
		fmt.Println("IMPORTANT: Save this password! It will not be shown again.")
		return password, nil
	})
}

// writeConfigFile persists version 2 configuration after installation succeeds.
// Administrator credentials and migration timeout stay outside the runtime file.
// A missing encryption key is generated once for this output; automatic setup
// retains explicit environment references for supplied credentials and dependency
// hosts while manual installation writes escaped literal values. Host references
// allow Apple Container's newly discovered addresses to take effect after restart.
//
// Encoding finishes before replacement begins, then the shared configuration
// writer syncs and renames a sibling temporary file. Single-file mounts can
// require an in-place fallback, so a write failure may leave setup evidence.
// The separate installation marker is written only after this function succeeds.
func writeConfigFile(cfg *SetupConfig) error {
	if cfg.SecretEncryptionKey == "" {
		secret, err := generateSecret(32)
		if err != nil {
			return err
		}
		cfg.SecretEncryptionKey = secret
	}
	tz := cfg.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	yamlConfig := struct {
		Version  int            `yaml:"config_version"` // persisted configuration schema version
		Timezone string         `yaml:"timezone"`       // application local calendar zone
		Server   ServerConfig   `yaml:"server"`         // configured HTTP listener settings
		Database DatabaseConfig `yaml:"database"`       // PostgreSQL connection probe settings
		Redis    RedisConfig    `yaml:"redis"`          // Redis connection probe settings
		Auth     struct {
			JWT struct {
				Secret                string `yaml:"secret"`                   // token signing secret text
				AccessTokenTTLMinutes int    `yaml:"access_token_ttl_minutes"` // access token lifetime minutes
			} `yaml:"jwt"` // authentication token signing settings
		} `yaml:"auth"` // application authentication process settings
		Security struct {
			SecretEncryptionKey string `yaml:"secret_encryption_key"` // persistent AES encryption key
		} `yaml:"security"` // encrypted credential storage settings
	}{Version: config.CurrentConfigVersion, Timezone: tz, Server: cfg.Server, Database: cfg.Database, Redis: cfg.Redis}
	yamlConfig.Auth.JWT.Secret = cfg.JWT.Secret
	// Setup accepts hours while the runtime file records one minute-based lifetime.
	yamlConfig.Auth.JWT.AccessTokenTTLMinutes = cfg.JWT.ExpireHour * 60
	yamlConfig.Security.SecretEncryptionKey = cfg.SecretEncryptionKey
	var document yaml.Node
	if err := document.Encode(yamlConfig); err != nil {
		return err
	}
	// Manual inputs are literal values even when they contain reference syntax.
	// Escape them before adding the automatic installer's credential and host references.
	escapeSetupLiterals(&document)
	if cfg.environmentReferences {
		for _, entry := range []struct {
			Name string   // referenced environment variable name
			Path []string // serialized configuration field path
		}{
			{"DATABASE_HOST", []string{"database", "host"}},
			{"REDIS_HOST", []string{"redis", "host"}},
			{"DATABASE_PASSWORD", []string{"database", "password"}},
			{"REDIS_PASSWORD", []string{"redis", "password"}},
			{"JWT_SECRET", []string{"auth", "jwt", "secret"}},
			{"TOTP_ENCRYPTION_KEY", []string{"security", "secret_encryption_key"}},
		} {
			if os.Getenv(entry.Name) == "" {
				continue
			}
			node := &document
			for _, part := range entry.Path {
				for i := 0; i < len(node.Content); i += 2 {
					if node.Content[i].Value == part {
						node = node.Content[i+1]
						break
					}
				}
			}
			node.Value = "${" + entry.Name + "}"
		}
	}
	var data bytes.Buffer
	encoder := yaml.NewEncoder(&data)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return config.WriteConfigFile(GetConfigFilePath(), data.Bytes())
}

// escapeSetupLiterals preserves reference-looking installer input as literal text.
// Only scalar values are changed; keys remain fixed by the installer output
// structure, and automatic environment references are added after this traversal.
func escapeSetupLiterals(node *yaml.Node) {
	if node.Kind == yaml.MappingNode {
		for i := 1; i < len(node.Content); i += 2 {
			escapeSetupLiterals(node.Content[i])
		}
		return
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		node.Value = strings.ReplaceAll(node.Value, "${", "$${")
	}
	for _, child := range node.Content {
		escapeSetupLiterals(child)
	}
}

// ListenAddress selects the web installer's environment-based listener address.
// SERVER_HOST defaults to all interfaces and invalid SERVER_PORT values use
// 8080. Installed application listeners instead use their validated configuration.
func ListenAddress() string {
	host := getEnvOrDefault("SERVER_HOST", "0.0.0.0")
	port := getEnvIntOrDefault("SERVER_PORT", 8080)
	if port <= 0 || port > 65535 {
		port = 8080
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// generateSecret returns lowercase hexadecimal text from length
// cryptographically random bytes with nil error. The result has twice that many
// characters; zero gives empty text and negative length panics on allocation.
// crypto/rand.Read terminates the process when its io.ReadFull operation
// reports an error.
func generateSecret(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// AutoSetupEnabled reports whether AUTO_SETUP exactly equals true, one, or yes.
// Unlike skipSetupEnabled, it performs no trimming or case folding, so other
// spelling leaves the automatic startup path disabled.
func AutoSetupEnabled() bool {
	val := os.Getenv("AUTO_SETUP")
	return val == "true" || val == "1" || val == "yes"
}

// getEnvOrDefault returns a nonempty environment value as supplied or
// defaultValue. Whitespace remains a configured value, and this helper applies
// no validation to the text passed into automatic setup.
func getEnvOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

// getEnvIntOrDefault returns an environment integer accepted by strconv.Atoi or
// the supplied default. Missing, empty, or malformed text uses the default;
// accepted negative values remain for downstream handling.
func getEnvIntOrDefault(key string, defaultValue int) int {
	if val := os.Getenv(key); val != "" {
		// Malformed integer text has no usable deployment value. Use the supplied
		// default while retaining successfully parsed values for subsequent setup handling.
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultValue
}

// AutoSetupFromEnv performs installation using deployment environment values
// and writes runtime configuration plus its marker. The startup caller checks
// NeedsSetup first because this automatic path does not repeat that guard or
// acquire the HTTP installation mutex.
//
// TZ selects the installation timezone, while missing or malformed integer
// variables use their field defaults. Supplied text and accepted integers are
// passed to bootstrap as given, so the deployment owner provides valid
// connection fields. JWT generation precedes probes; migration and
// empty-database administrator creation precede configuration and marker
// writes. Failure retains completed database or filesystem effects, and
// creation skip reasons determine the operator log messages.
func AutoSetupFromEnv() error {
	logging.LegacyPrintf("setup", "%s", "Auto setup enabled, configuring from environment variables...")
	logging.LegacyPrintf("setup", "Data directory: %s", GetDataDir())

	tz := getEnvOrDefault("TZ", "Asia/Shanghai")

	cfg := &SetupConfig{
		environmentReferences: true,
		SecretEncryptionKey:   getEnvOrDefault("TOTP_ENCRYPTION_KEY", ""),
		Database: DatabaseConfig{
			Host:     getEnvOrDefault("DATABASE_HOST", "localhost"),
			Port:     getEnvIntOrDefault("DATABASE_PORT", 5432),
			User:     getEnvOrDefault("DATABASE_USER", "postgres"),
			Password: getEnvOrDefault("DATABASE_PASSWORD", ""),
			DBName:   getEnvOrDefault("DATABASE_DBNAME", "tokenrouter"),
			SSLMode:  getEnvOrDefault("DATABASE_SSLMODE", "disable"),
		},
		Redis: RedisConfig{
			Host:      getEnvOrDefault("REDIS_HOST", "localhost"),
			Port:      getEnvIntOrDefault("REDIS_PORT", 6379),
			Username:  getEnvOrDefault("REDIS_USERNAME", ""),
			Password:  getEnvOrDefault("REDIS_PASSWORD", ""),
			DB:        getEnvIntOrDefault("REDIS_DB", 0),
			EnableTLS: getEnvOrDefault("REDIS_ENABLE_TLS", "false") == "true",
		},
		Admin: AdminConfig{
			Email:    getEnvOrDefault("ADMIN_EMAIL", "admin@tokenrouter.local"),
			Password: getEnvOrDefault("ADMIN_PASSWORD", ""),
		},
		Server: ServerConfig{
			Host: getEnvOrDefault("SERVER_HOST", "0.0.0.0"),
			Port: getEnvIntOrDefault("SERVER_PORT", 8080),
			Mode: getEnvOrDefault("SERVER_MODE", "release"),
		},
		JWT: JWTConfig{
			Secret:     getEnvOrDefault("JWT_SECRET", ""),
			ExpireHour: getEnvIntOrDefault("JWT_EXPIRE_HOUR", 24),
		},
		Timezone:                tz,
		MigrationTimeoutSeconds: getEnvIntOrDefault("SETUP_MIGRATION_TIMEOUT_SECONDS", 0),
	}

	if cfg.JWT.Secret == "" {
		secret, err := generateSecret(32)
		if err != nil {
			return fmt.Errorf("failed to generate jwt secret: %w", err)
		}
		cfg.JWT.Secret = secret
		logging.LegacyPrintf("setup", "%s", "Warning: JWT secret auto-generated. Consider setting a fixed secret for production.")
	}

	logging.LegacyPrintf("setup", "%s", "Testing database connection...")
	if err := TestDatabaseConnection(&cfg.Database); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	logging.LegacyPrintf("setup", "%s", "Database connection successful")

	logging.LegacyPrintf("setup", "%s", "Testing Redis connection...")
	if err := TestRedisConnection(&cfg.Redis); err != nil {
		return fmt.Errorf("redis connection failed: %w", err)
	}
	logging.LegacyPrintf("setup", "%s", "Redis connection successful")

	// Probe success precedes migration and administrator creation, and both database
	// stages finish before configuration is written. Later failures retain earlier persistent effects.
	logging.LegacyPrintf("setup", "%s", "Initializing database...")
	if err := initializeDatabase(cfg); err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}
	logging.LegacyPrintf("setup", "%s", "Database initialized successfully")

	logging.LegacyPrintf("setup", "%s", "Creating admin user...")
	created, reason, err := createAdminUser(cfg)
	if err != nil {
		return fmt.Errorf("admin user creation failed: %w", err)
	}
	if created {
		logging.LegacyPrintf("setup", "Admin user created: %s", cfg.Admin.Email)
	} else {
		switch reason {
		case adminBootstrapReasonAdminExists:
			logging.LegacyPrintf("setup", "%s", "Admin user already exists, skipping admin bootstrap")
		case adminBootstrapReasonUsersExistWithoutAdmin:
			logging.LegacyPrintf("setup", "%s", "Database already has user data; skipping auto admin bootstrap to avoid password overwrite")
		default:
			logging.LegacyPrintf("setup", "%s", "Admin bootstrap skipped")
		}
	}

	logging.LegacyPrintf("setup", "%s", "Writing configuration file...")
	if err := writeConfigFile(cfg); err != nil {
		return fmt.Errorf("config file creation failed: %w", err)
	}
	logging.LegacyPrintf("setup", "%s", "Configuration file created")

	// The written configuration already disables file-based setup detection. Attempt
	// the separate marker afterward so either persisted path can keep installation closed.
	if err := createInstallLock(); err != nil {
		return fmt.Errorf("failed to create install lock: %w", err)
	}
	logging.LegacyPrintf("setup", "%s", "Installation lock created")

	logging.LegacyPrintf("setup", "%s", "Auto setup completed successfully!")
	return nil
}
