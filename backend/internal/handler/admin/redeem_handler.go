// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package admin

import (
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	time "time"
)

type RedeemHandler = billinghttpapi.AdminRedeemHandler

func NewRedeemHandler(adminService service.AdminService, redeemService *service.RedeemService) *RedeemHandler {
	return billinghttpapi.NewAdminRedeemHandler(adminService, redeemService)
}

type GenerateRedeemCodesRequest = billinghttpapi.GenerateRedeemCodesRequest

type UpdateRedeemCodeRequest = billinghttpapi.UpdateRedeemCodeRequest

type CreateAndRedeemCodeRequest = billinghttpapi.CreateAndRedeemCodeRequest

func resolveRedeemCodeExpiresAt(expiresAt *int64, expiresInDays *int) (*time.Time, error) {
	return billinghttpapi.ResolveRedeemCodeExpiresAt(expiresAt, expiresInDays)
}
