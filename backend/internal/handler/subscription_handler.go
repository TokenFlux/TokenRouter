// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package handler

import (
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type SubscriptionSummaryItem = billinghttpapi.SubscriptionSummaryItem

type SubscriptionProgressInfo = billinghttpapi.SubscriptionProgressInfo

type RevokeSubscriptionResponse = billinghttpapi.RevokeSubscriptionResponse

type SubscriptionHandler = billinghttpapi.SubscriptionHandler

func NewSubscriptionHandler(subscriptionService *service.SubscriptionService) *SubscriptionHandler {
	return billinghttpapi.NewSubscriptionHandler(subscriptionService)
}
