// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	sql "database/sql"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// NewChannelRepository 仅返回 routing 的唯一 SQL 实现。
func NewChannelRepository(db *sql.DB) service.ChannelRepository {
	return routingpostgres.NewChannelStore(db)
}
