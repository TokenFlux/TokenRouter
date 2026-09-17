// 旧仓储入口委托任务所属存储，S15/S16 清理。
package repository

import (
	"database/sql"

	native "github.com/TokenFlux/TokenRouter/internal/batchimage/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func NewBatchImageRepository(db *sql.DB) service.BatchImageRepository {
	return native.NewBatchImageRepository(db)
}
