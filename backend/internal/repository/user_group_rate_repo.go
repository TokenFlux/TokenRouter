// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package repository

import (
	sql "database/sql"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

func NewUserGroupRateRepository(sqlDB *sql.DB) service.UserGroupRateRepository {
	return billingpostgres.NewUserGroupRateRepository(sqlDB)
}
