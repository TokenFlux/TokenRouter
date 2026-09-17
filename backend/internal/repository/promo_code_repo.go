// 旧名称只委托同一推广存储；S15/S16 清理。
package repository

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	p "github.com/TokenFlux/TokenRouter/internal/promotion/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func NewPromoCodeRepository(client *dbent.Client) service.PromoCodeRepository {
	return p.NewPromoCodeRepository(client)
}
