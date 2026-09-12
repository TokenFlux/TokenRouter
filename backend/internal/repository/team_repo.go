// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	sql "database/sql"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	postgres "github.com/TokenFlux/TokenRouter/internal/team/postgres"
)

func NewTeamRepository(db *sql.DB) service.TeamRepository {
	return postgres.NewTeamRepository(db, keypostgres.NewTeamKeys(db), billingpostgres.NewMemberUsageStore(db, nil))
}
