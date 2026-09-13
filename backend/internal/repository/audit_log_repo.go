// 旧仓储入口只转接 audit/postgres；生产 app 直接装配唯一存储。
package repository

import (
	"database/sql"

	auditpostgres "github.com/TokenFlux/TokenRouter/internal/audit/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func NewAuditLogRepository(db *sql.DB) service.AuditLogRepository {
	return auditpostgres.NewAuditLogRepository(db)
}
