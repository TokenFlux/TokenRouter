package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/bootstrap"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"

	_ "github.com/lib/pq"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Config paths
const (
	ConfigFileName             = "config.yaml"
	InstallLockFile            = ".installed"
	defaultUserConcurrency     = 5
	simpleModeAdminConcurrency = 30
	defaultMigrationTimeout    = 60 * time.Second
)

func setupDefaultAdminConcurrency() int {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("RUN_MODE")), config.RunModeSimple) {
		return simpleModeAdminConcurrency
	}
	return defaultUserConcurrency
}

// GetDataDir returns the data directory for storing config and lock files.
// Priority: DATA_DIR env > /app/data (if exists and writable) > current directory
func GetDataDir() string {
	// Check DATA_DIR environment variable first
	if dir := os.Getenv("DATA_DIR"); dir != "" {
		return dir
	}

	// Check if /app/data exists and is writable (Docker environment)
	dockerDataDir := "/app/data"
	if info, err := os.Stat(dockerDataDir); err == nil && info.IsDir() {
		// Try to check if writable by creating a temp file
		testFile := dockerDataDir + "/.write_test"
		if f, err := os.Create(testFile); err == nil {
			_ = f.Close()
			_ = os.Remove(testFile)
			return dockerDataDir
		}
	}

	// Default to current directory
	return "."
}

// GetConfigFilePath returns the full path to config.yaml
func GetConfigFilePath() string {
	return GetDataDir() + "/" + ConfigFileName
}

// GetInstallLockPath returns the full path to .installed lock file
func GetInstallLockPath() string {
	return GetDataDir() + "/" + InstallLockFile
}

// SetupConfig 保存初始设置所需的配置。
type SetupConfig struct {
	Database                DatabaseConfig `json:"database" yaml:"database"`
	Redis                   RedisConfig    `json:"redis" yaml:"redis"`
	Admin                   AdminConfig    `json:"admin" yaml:"-"` // 不写入配置文件。
	Server                  ServerConfig   `json:"server" yaml:"server"`
	JWT                     JWTConfig      `json:"jwt" yaml:"jwt"`
	Timezone                string         `json:"timezone" yaml:"timezone"` // 例如 "Asia/Shanghai" 或 "UTC"。
	MigrationTimeoutSeconds int            `json:"migration_timeout_seconds" yaml:"migration_timeout_seconds,omitempty"`
}

type DatabaseConfig = bootstrap.SetupDatabaseConfig

type RedisConfig = bootstrap.SetupRedisConfig

type AdminConfig struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type ServerConfig struct {
	Host string `json:"host" yaml:"host"`
	Port int    `json:"port" yaml:"port"`
	Mode string `json:"mode" yaml:"mode"`
}

type JWTConfig struct {
	Secret     string `json:"secret" yaml:"secret"`
	ExpireHour int    `json:"expire_hour" yaml:"expire_hour"`
}

const (
	adminBootstrapReasonEmptyDatabase          = "empty_database"
	adminBootstrapReasonAdminExists            = "admin_exists"
	adminBootstrapReasonUsersExistWithoutAdmin = "users_exist_without_admin"
)

type adminBootstrapDecision struct {
	shouldCreate bool
	reason       string
}

func decideAdminBootstrap(totalUsers, adminUsers int64) adminBootstrapDecision {
	create, reason := identity.DecideAdminBootstrap(totalUsers, adminUsers)
	return adminBootstrapDecision{shouldCreate: create, reason: reason}
}

// skipSetupEnabled 解析显式跳过首次安装向导的环境开关。
func skipSetupEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SKIP_SETUP"))) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// NeedsSetup checks if the system needs initial setup
// Uses multiple checks to prevent attackers from forcing re-setup by deleting config
func NeedsSetup() bool {
	if skipSetupEnabled() {
		logger.L().Debug("setup.needs_setup_bypassed", zap.String("reason", "skip_setup_enabled"))
		return false
	}

	// Check 1: Config file must not exist
	if _, err := os.Stat(GetConfigFilePath()); !os.IsNotExist(err) {
		return false // Config exists, no setup needed
	}

	// Check 2: Installation lock file (harder to bypass)
	if _, err := os.Stat(GetInstallLockPath()); !os.IsNotExist(err) {
		return false // Lock file exists, already installed
	}

	return true
}

func buildDatabaseConnectionDSNs(cfg *DatabaseConfig) (bootstrapDSN, targetDSN string) {
	return bootstrap.BuildDatabaseConnectionDSNs(cfg)
}
func TestDatabaseConnection(cfg *DatabaseConfig) error {
	return bootstrap.TestSetupDatabaseConnection(cfg)
}
func TestRedisConnection(cfg *RedisConfig) error { return bootstrap.TestSetupRedisConnection(cfg) }
func Install(cfg *SetupConfig) error {
	// Security check: prevent re-installation if already installed
	if !NeedsSetup() {
		return fmt.Errorf("system is already installed, re-installation is not allowed")
	}

	// Generate JWT secret if not provided
	if cfg.JWT.Secret == "" {
		secret, err := generateSecret(32)
		if err != nil {
			return fmt.Errorf("failed to generate jwt secret: %w", err)
		}
		cfg.JWT.Secret = secret
		logger.LegacyPrintf("setup", "%s", "Warning: JWT secret auto-generated. Consider setting a fixed secret for production.")
	}

	// Test connections
	if err := TestDatabaseConnection(&cfg.Database); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}

	if err := TestRedisConnection(&cfg.Redis); err != nil {
		return fmt.Errorf("redis connection failed: %w", err)
	}

	// Initialize database
	if err := initializeDatabase(cfg); err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}

	// Create admin user (only when database is empty and no admin exists).
	if _, _, err := createAdminUser(cfg); err != nil {
		return fmt.Errorf("admin user creation failed: %w", err)
	}

	// Write config file
	if err := writeConfigFile(cfg); err != nil {
		return fmt.Errorf("config file creation failed: %w", err)
	}

	// Create installation lock file to prevent re-setup attacks
	if err := createInstallLock(); err != nil {
		return fmt.Errorf("failed to create install lock: %w", err)
	}

	return nil
}

// createInstallLock creates a lock file to prevent re-installation attacks
func createInstallLock() error {
	content := fmt.Sprintf("installed_at=%s\n", time.Now().UTC().Format(time.RFC3339))
	return os.WriteFile(GetInstallLockPath(), []byte(content), 0400) // Read-only for owner
}

func initializeDatabase(cfg *SetupConfig) error {
	return bootstrap.InitializeSetupDatabase(context.Background(), &cfg.Database, cfg.migrationTimeout())
}
func (cfg *SetupConfig) migrationTimeout() time.Duration {
	if cfg != nil && cfg.MigrationTimeoutSeconds > 0 {
		return time.Duration(cfg.MigrationTimeoutSeconds) * time.Second
	}
	return defaultMigrationTimeout
}

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

func writeConfigFile(cfg *SetupConfig) error {
	// Ensure timezone has a default value
	tz := cfg.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}

	// Prepare config for YAML (exclude sensitive data and admin config)
	yamlConfig := struct {
		Server   ServerConfig   `yaml:"server"`
		Database DatabaseConfig `yaml:"database"`
		Redis    RedisConfig    `yaml:"redis"`
		JWT      struct {
			Secret     string `yaml:"secret"`
			ExpireHour int    `yaml:"expire_hour"`
		} `yaml:"jwt"`
		Default struct {
			UserConcurrency int     `yaml:"user_concurrency"`
			UserBalance     float64 `yaml:"user_balance"`
			APIKeyPrefix    string  `yaml:"api_key_prefix"`
			RateMultiplier  float64 `yaml:"rate_multiplier"`
		} `yaml:"default"`
		RateLimit struct {
			RequestsPerMinute int `yaml:"requests_per_minute"`
			BurstSize         int `yaml:"burst_size"`
		} `yaml:"rate_limit"`
		Timezone string `yaml:"timezone"`
	}{
		Server:   cfg.Server,
		Database: cfg.Database,
		Redis:    cfg.Redis,
		JWT: struct {
			Secret     string `yaml:"secret"`
			ExpireHour int    `yaml:"expire_hour"`
		}{
			Secret:     cfg.JWT.Secret,
			ExpireHour: cfg.JWT.ExpireHour,
		},
		Default: struct {
			UserConcurrency int     `yaml:"user_concurrency"`
			UserBalance     float64 `yaml:"user_balance"`
			APIKeyPrefix    string  `yaml:"api_key_prefix"`
			RateMultiplier  float64 `yaml:"rate_multiplier"`
		}{
			UserConcurrency: defaultUserConcurrency,
			UserBalance:     0,
			APIKeyPrefix:    "sk-",
			RateMultiplier:  1.0,
		},
		RateLimit: struct {
			RequestsPerMinute int `yaml:"requests_per_minute"`
			BurstSize         int `yaml:"burst_size"`
		}{
			RequestsPerMinute: 60,
			BurstSize:         10,
		},
		Timezone: tz,
	}

	data, err := yaml.Marshal(&yamlConfig)
	if err != nil {
		return err
	}

	return os.WriteFile(GetConfigFilePath(), data, 0600)
}

func generateSecret(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// =============================================================================
// Auto Setup for Docker Deployment
// =============================================================================

// AutoSetupEnabled checks if auto setup is enabled via environment variable
func AutoSetupEnabled() bool {
	val := os.Getenv("AUTO_SETUP")
	return val == "true" || val == "1" || val == "yes"
}

// getEnvOrDefault gets environment variable or returns default value
func getEnvOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

// getEnvIntOrDefault gets environment variable as int or returns default value
func getEnvIntOrDefault(key string, defaultValue int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultValue
}

// AutoSetupFromEnv performs automatic setup using environment variables
// This is designed for Docker deployment where all config is passed via env vars
func AutoSetupFromEnv() error {
	logger.LegacyPrintf("setup", "%s", "Auto setup enabled, configuring from environment variables...")
	logger.LegacyPrintf("setup", "Data directory: %s", GetDataDir())

	// Get timezone from TZ or TIMEZONE env var (TZ is standard for Docker)
	tz := getEnvOrDefault("TZ", "")
	if tz == "" {
		tz = getEnvOrDefault("TIMEZONE", "Asia/Shanghai")
	}

	// Build config from environment variables
	cfg := &SetupConfig{
		Database: DatabaseConfig{
			Host:     getEnvOrDefault("DATABASE_HOST", "localhost"),
			Port:     getEnvIntOrDefault("DATABASE_PORT", 5432),
			User:     getEnvOrDefault("DATABASE_USER", "postgres"),
			Password: getEnvOrDefault("DATABASE_PASSWORD", ""),
			DBName:   getEnvOrDefault("DATABASE_DBNAME", "sub2api"),
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
			Email:    getEnvOrDefault("ADMIN_EMAIL", "admin@sub2api.local"),
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

	// Generate JWT secret if not provided
	if cfg.JWT.Secret == "" {
		secret, err := generateSecret(32)
		if err != nil {
			return fmt.Errorf("failed to generate jwt secret: %w", err)
		}
		cfg.JWT.Secret = secret
		logger.LegacyPrintf("setup", "%s", "Warning: JWT secret auto-generated. Consider setting a fixed secret for production.")
	}

	// Test database connection
	logger.LegacyPrintf("setup", "%s", "Testing database connection...")
	if err := TestDatabaseConnection(&cfg.Database); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	logger.LegacyPrintf("setup", "%s", "Database connection successful")

	// Test Redis connection
	logger.LegacyPrintf("setup", "%s", "Testing Redis connection...")
	if err := TestRedisConnection(&cfg.Redis); err != nil {
		return fmt.Errorf("redis connection failed: %w", err)
	}
	logger.LegacyPrintf("setup", "%s", "Redis connection successful")

	// Initialize database
	logger.LegacyPrintf("setup", "%s", "Initializing database...")
	if err := initializeDatabase(cfg); err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}
	logger.LegacyPrintf("setup", "%s", "Database initialized successfully")

	// Create admin user
	logger.LegacyPrintf("setup", "%s", "Creating admin user...")
	created, reason, err := createAdminUser(cfg)
	if err != nil {
		return fmt.Errorf("admin user creation failed: %w", err)
	}
	if created {
		logger.LegacyPrintf("setup", "Admin user created: %s", cfg.Admin.Email)
	} else {
		switch reason {
		case adminBootstrapReasonAdminExists:
			logger.LegacyPrintf("setup", "%s", "Admin user already exists, skipping admin bootstrap")
		case adminBootstrapReasonUsersExistWithoutAdmin:
			logger.LegacyPrintf("setup", "%s", "Database already has user data; skipping auto admin bootstrap to avoid password overwrite")
		default:
			logger.LegacyPrintf("setup", "%s", "Admin bootstrap skipped")
		}
	}

	// Write config file
	logger.LegacyPrintf("setup", "%s", "Writing configuration file...")
	if err := writeConfigFile(cfg); err != nil {
		return fmt.Errorf("config file creation failed: %w", err)
	}
	logger.LegacyPrintf("setup", "%s", "Configuration file created")

	// Create installation lock file
	if err := createInstallLock(); err != nil {
		return fmt.Errorf("failed to create install lock: %w", err)
	}
	logger.LegacyPrintf("setup", "%s", "Installation lock created")

	logger.LegacyPrintf("setup", "%s", "Auto setup completed successfully!")
	return nil
}
