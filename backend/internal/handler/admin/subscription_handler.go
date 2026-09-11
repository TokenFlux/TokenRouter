// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package admin

import (
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

type SubscriptionHandler = billinghttpapi.AdminSubscriptionHandler

func NewSubscriptionHandler(subscriptionService *service.SubscriptionService) *SubscriptionHandler {
	return billinghttpapi.NewAdminSubscriptionHandler(subscriptionService)
}

type AssignSubscriptionRequest = billinghttpapi.AssignSubscriptionRequest

type BulkAssignSubscriptionRequest = billinghttpapi.BulkAssignSubscriptionRequest

type AdjustSubscriptionRequest = billinghttpapi.AdjustSubscriptionRequest

type ResetSubscriptionQuotaRequest = billinghttpapi.ResetSubscriptionQuotaRequest

// getAdminIDFromContext 从上下文读取管理员 ID。
func getAdminIDFromContext(c *gin.Context) int64 {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		return 0
	}
	return subject.UserID
}
