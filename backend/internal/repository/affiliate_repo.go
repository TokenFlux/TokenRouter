// 旧仓储构造仅转接唯一推广实现，生产装配迁至 app 后由测试兼容入口保留到 S15/S16。
package repository

import (
	"database/sql"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	b "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	p "github.com/TokenFlux/TokenRouter/internal/promotion/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func NewAffiliateRepository(c *dbent.Client, _ *sql.DB) service.AffiliateRepository {
	return p.NewAffiliateRepository(c, func(tx *dbent.Tx) p.TransferBalance { return b.BalanceInTx(tx) })
}
