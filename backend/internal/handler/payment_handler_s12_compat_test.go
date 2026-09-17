//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/service"

	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
)

func applyWeChatPaymentResumeClaims(req *CreateOrderRequest, claims *service.WeChatPaymentResumeClaims) error {
	return paymenthttp.UserApplyWeChatPaymentResumeClaims(req, claims)
}

func publicOrderStatusPaid(status string) bool { return paymenthttp.UserPublicOrderStatusPaid(status) }
