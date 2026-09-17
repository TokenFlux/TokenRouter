//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/payment"

	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	"github.com/gin-gonic/gin"
)

const maxWebhookBodySize = paymenthttp.WebhookMaxWebhookBodySize

const webhookLogTruncateLen = paymenthttp.WebhookWebhookLogTruncateLen

func shouldAcknowledgeWebhookProviderLookupError(err error) bool {
	return paymenthttp.WebhookShouldAcknowledgeWebhookProviderLookupError(err)
}

func extractOutTradeNo(rawBody, providerKey string) string {
	return paymenthttp.WebhookExtractOutTradeNo(rawBody, providerKey)
}

func extractStripeOutTradeNo(rawBody string) string {
	return paymenthttp.WebhookExtractStripeOutTradeNo(rawBody)
}

func verifyNotificationWithProviders(ctx context.Context, providers []payment.Provider, rawBody string, headers map[string]string) (string, *payment.PaymentNotification, error) {
	return paymenthttp.WebhookVerifyNotificationWithProviders(ctx, providers, rawBody, headers)
}

type wxpaySuccessResponse = paymenthttp.WebhookWxpaySuccessResponse

func writeSuccessResponse(c *gin.Context, providerKey string) {
	paymenthttp.WebhookWriteSuccessResponse(c, providerKey)
}
