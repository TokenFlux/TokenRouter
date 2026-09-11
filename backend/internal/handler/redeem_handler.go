// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package handler

import (
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type RedeemHandler = billinghttpapi.RedeemHandler

func NewRedeemHandler(redeemService *service.RedeemService) *RedeemHandler {
	return billinghttpapi.NewRedeemHandler(redeemService)
}

type RedeemRequest = billinghttpapi.RedeemRequest

type RedeemResponse = billinghttpapi.RedeemResponse
