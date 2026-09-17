// 旧仓储入口委托任务所属存储，S15/S16 清理。
package repository

import (
	"database/sql"

	native "github.com/TokenFlux/TokenRouter/internal/creative/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func NewCreativeRunOutboxRepository(db *sql.DB) service.CreativeRunOutboxRepository {
	return native.NewCreativeRunOutboxRepository(db)
}
