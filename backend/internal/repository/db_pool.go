// Package repository 包含持久化基础设施辅助逻辑。
//
// 数据库连接池生命周期在这里做兜底裁剪，因为 lib/pq 会为带 context 的查询启动
// watchCancel goroutine。如果云代理静默丢弃空闲 TCP 且不发送 RST/FIN，这些 goroutine
// 会一直阻塞在 Read，直到 database/sql 回收连接。这里是短期缓解，长期方案是迁移到
// jackc/pgx/v5/stdlib。
package repository

import (
	"database/sql"
	"log/slog"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
)

const (
	defaultConnMaxLifetime = 30 * time.Minute
	defaultConnMaxIdleTime = 5 * time.Minute
	maxConfiguredConnAge   = 24 * time.Hour
)

type dbPoolSettings struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func clampDBPoolSettings(cfg *config.Config) dbPoolSettings {
	return dbPoolSettings{
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: clampDBPoolDuration("database.conn_max_lifetime_minutes", cfg.Database.ConnMaxLifetimeMinutes, defaultConnMaxLifetime),
		ConnMaxIdleTime: clampDBPoolDuration("database.conn_max_idle_time_minutes", cfg.Database.ConnMaxIdleTimeMinutes, defaultConnMaxIdleTime),
	}
}

func clampDBPoolDuration(key string, minutes int, fallback time.Duration) time.Duration {
	if minutes <= 0 || minutes > int(maxConfiguredConnAge/time.Minute) {
		slog.Warn("database connection pool duration clamped",
			"key", key,
			"before", minutes,
			"after", int(fallback/time.Minute),
		)
		return fallback
	}

	return time.Duration(minutes) * time.Minute
}

func applyDBPoolSettings(db *sql.DB, cfg *config.Config) {
	settings := clampDBPoolSettings(cfg)
	db.SetMaxOpenConns(settings.MaxOpenConns)
	db.SetMaxIdleConns(settings.MaxIdleConns)
	db.SetConnMaxLifetime(settings.ConnMaxLifetime)
	db.SetConnMaxIdleTime(settings.ConnMaxIdleTime)

	slog.Info("database connection pool configured",
		slog.Group("effective",
			slog.Int("max_open", settings.MaxOpenConns),
			slog.Int("max_idle", settings.MaxIdleConns),
			slog.Duration("max_lifetime", settings.ConnMaxLifetime),
			slog.Duration("max_idle_time", settings.ConnMaxIdleTime),
		),
	)
}
