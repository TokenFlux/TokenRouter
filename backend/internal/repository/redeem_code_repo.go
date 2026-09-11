// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package repository

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

func NewRedeemCodeRepository(client *dbent.Client) service.RedeemCodeRepository {
	return billingpostgres.NewRedeemCodeRepository(client)
}
