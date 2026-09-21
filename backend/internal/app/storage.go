package app

import (
	"database/sql"
	"errors"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	idempotencypostgres "github.com/TokenFlux/TokenRouter/internal/idempotency/postgres"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	settingspostgres "github.com/TokenFlux/TokenRouter/internal/settings/postgres"
)

// provideSettingsStore 直接构造唯一设置实例，所有接口共享版本、通知与更新协调器。
// @project-doc docs/interfaces/configuration.md#runtime_settings
func provideSettingsStore(client *ent.Client) *settings.Store {
	return settings.New(settingspostgres.NewSettingRepository(client))
}

// provideIdempotencyRepository 使用应用已拥有的 SQL 连接，不创建额外连接池。
func provideIdempotencyRepository(db *sql.DB) idempotency.IdempotencyRepository {
	return idempotencypostgres.NewIdempotencyRepository(db)
}

// provideSQLDB 取得 Ent 已拥有的连接池，关闭仍由原资源拥有者负责。
func provideSQLDB(client *ent.Client) (*sql.DB, error) {
	if client == nil {
		return nil, errors.New("nil ent client")
	}
	driver, ok := client.Driver().(*entsql.Driver)
	if !ok {
		return nil, errors.New("ent driver does not expose *sql.DB")
	}
	return driver.DB(), nil
}
