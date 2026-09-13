// 旧存储构造器仅委托唯一 Ops Store。
package repository

import (
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/postgres"
)

func NewOpsRepository(db *sql.DB) ops.OpsRepository { return postgres.NewOpsRepository(db) }
