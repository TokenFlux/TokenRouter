// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package admin

import (
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type SubscriptionHandler = billinghttpapi.AdminSubscriptionHandler

func NewSubscriptionHandler(subscriptionService *service.SubscriptionService) *SubscriptionHandler {
	return billinghttpapi.NewAdminSubscriptionHandler(subscriptionService)
}

type AssignSubscriptionRequest = billinghttpapi.AssignSubscriptionRequest

type BulkAssignSubscriptionRequest = billinghttpapi.BulkAssignSubscriptionRequest

type AdjustSubscriptionRequest = billinghttpapi.AdjustSubscriptionRequest

type ResetSubscriptionQuotaRequest = billinghttpapi.ResetSubscriptionQuotaRequest
