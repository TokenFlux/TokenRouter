package bootstrap

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

type SetupRedisConfig struct {
	Host      string `json:"host" yaml:"host"`
	Port      int    `json:"port" yaml:"port"`
	Username  string `json:"username" yaml:"username"`
	Password  string `json:"password" yaml:"password"`
	DB        int    `json:"db" yaml:"db"`
	EnableTLS bool   `json:"enable_tls" yaml:"enable_tls"`
}
type SetupDatabaseConfig struct {
	Host     string `json:"host" yaml:"host"`
	Port     int    `json:"port" yaml:"port"`
	User     string `json:"user" yaml:"user"`
	Password string `json:"password" yaml:"password"`
	DBName   string `json:"dbname" yaml:"dbname"`
	SSLMode  string `json:"sslmode" yaml:"sslmode"`
}

func BuildPostgresDSN(cfg *SetupDatabaseConfig, dbName string) string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, dbName, cfg.SSLMode,
	)
}

func BuildDatabaseConnectionDSNs(cfg *SetupDatabaseConfig) (bootstrapDSN, targetDSN string) {
	return BuildPostgresDSN(cfg, "postgres"), BuildPostgresDSN(cfg, cfg.DBName)
}

// 测试数据库连接，并在目标数据库不存在时创建它。

func TestSetupDatabaseConnection(cfg *SetupDatabaseConfig) error {
	// 先连接维护数据库，否则目标数据库尚未创建时会直接连接失败。
	defaultDSN, targetDSN := BuildDatabaseConnectionDSNs(cfg)

	db, err := postgresinfra.Open(defaultDSN, false)
	if err != nil {
		return fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	defer func() {
		if db == nil {
			return
		}
		if err := db.Close(); err != nil {
			logger.LegacyPrintf("setup", "failed to close postgres connection: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}

	// Check if target database exists
	var exists bool
	row := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", cfg.DBName)
	if err := row.Scan(&exists); err != nil {
		return fmt.Errorf("failed to check database existence: %w", err)
	}

	// 目标数据库不存在时创建它。
	if !exists {
		// 注意：数据库名不能参数化，依赖前置输入校验保障安全。
		_, err := db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", cfg.DBName))
		if err != nil {
			return fmt.Errorf("failed to create database '%s': %w", cfg.DBName, err)
		}
		logger.LegacyPrintf("setup", "Database '%s' created successfully", cfg.DBName)
	}

	// 再连接目标数据库，验证创建后的真实可用性。
	if err := db.Close(); err != nil {
		logger.LegacyPrintf("setup", "failed to close postgres connection: %v", err)
	}
	db = nil

	targetDB, err := postgresinfra.Open(targetDSN, false)
	if err != nil {
		return fmt.Errorf("failed to connect to database '%s': %w", cfg.DBName, err)
	}

	defer func() {
		if err := targetDB.Close(); err != nil {
			logger.LegacyPrintf("setup", "failed to close postgres connection: %v", err)
		}
	}()

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	if err := targetDB.PingContext(ctx2); err != nil {
		return fmt.Errorf("ping target database failed: %w", err)
	}

	return nil
}

// TestSetupRedisConnection tests the Redis connection

func TestSetupRedisConnection(cfg *SetupRedisConfig) error {
	opts := &redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
	}

	if cfg.EnableTLS {
		opts.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: cfg.Host,
		}
	}

	rdb := redis.NewClient(opts)
	defer func() {
		if err := rdb.Close(); err != nil {
			logger.LegacyPrintf("setup", "failed to close redis client: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}

	return nil
}

// Install performs the installation with the given configuration

// InitializeSetupDatabase 只执行迁移并在任何出口关闭连接。
func InitializeSetupDatabase(ctx context.Context, cfg *SetupDatabaseConfig, timeout time.Duration) error {
	db, err := postgresinfra.Open(BuildPostgresDSN(cfg, cfg.DBName), false)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return ApplyMigrations(ctx, db)
}

// InitializeSetupAdmin 保持管理员初始化的独立连接和原五秒预算。
func InitializeSetupAdmin(ctx context.Context, cfg *SetupDatabaseConfig, input identity.InitialAdminInput, password func() (string, error)) (bool, string, error) {
	db, err := postgresinfra.Open(BuildPostgresDSN(cfg, cfg.DBName), false)
	if err != nil {
		return false, "", err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return CreateInitialAdmin(ctx, db, input, password)
}
