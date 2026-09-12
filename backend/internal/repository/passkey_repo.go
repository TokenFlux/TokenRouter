// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	sql "database/sql"
	postgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

func NewPasskeyRepository(db *sql.DB) service.PasskeyRepository {
	return postgres.NewPasskeyRepository(db)
}
