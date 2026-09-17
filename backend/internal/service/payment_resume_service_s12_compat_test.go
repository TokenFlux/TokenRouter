//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	payment "github.com/TokenFlux/TokenRouter/internal/payment"
)

const wechatPaymentResumeTokenType = payment.ResumeWechatPaymentResumeTokenType

func visibleMethodSourceSettingKey(method string) string {
	return payment.ResumeVisibleMethodSourceSettingKey(method)
}

func buildPaymentReturnURL(base string, orderID int64, outTradeNo string, resumeToken string) (string, error) {
	return payment.ResumeBuildPaymentReturnURL(base, orderID, outTradeNo, resumeToken)
}
