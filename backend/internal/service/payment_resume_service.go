// 旧名称只委托支付核心；可见渠道装配在配置用例迁移后继续清理。
package service

import (
	payment "github.com/TokenFlux/TokenRouter/internal/payment"
)

const PaymentSourceHostedRedirect = payment.PaymentSourceHostedRedirect
const PaymentSourceWechatInAppResume = payment.PaymentSourceWechatInAppResume
const SettingPaymentVisibleMethodAlipaySource = payment.SettingPaymentVisibleMethodAlipaySource
const SettingPaymentVisibleMethodWxpaySource = payment.SettingPaymentVisibleMethodWxpaySource
const SettingPaymentVisibleMethodAlipayEnabled = payment.SettingPaymentVisibleMethodAlipayEnabled
const SettingPaymentVisibleMethodWxpayEnabled = payment.SettingPaymentVisibleMethodWxpayEnabled
const VisibleMethodSourceOfficialAlipay = payment.VisibleMethodSourceOfficialAlipay
const VisibleMethodSourceEasyPayAlipay = payment.VisibleMethodSourceEasyPayAlipay
const VisibleMethodSourceOfficialWechat = payment.VisibleMethodSourceOfficialWechat
const VisibleMethodSourceEasyPayWechat = payment.VisibleMethodSourceEasyPayWechat

type ResumeTokenClaims = payment.ResumeTokenClaims
type WeChatPaymentResumeClaims = payment.WeChatPaymentResumeClaims
type PaymentResumeService = payment.PaymentResumeService

func NewPaymentResumeService(signingKey []byte, verifyFallbacks ...[]byte) *PaymentResumeService {
	return payment.NewPaymentResumeService(signingKey, verifyFallbacks...)
}
func NormalizeVisibleMethod(method string) string { return payment.NormalizeVisibleMethod(method) }
func NormalizeVisibleMethods(methods []string) []string {
	return payment.NormalizeVisibleMethods(methods)
}
func NormalizePaymentSource(source string) string { return payment.NormalizePaymentSource(source) }
func NormalizeVisibleMethodSource(method, source string) string {
	return payment.NormalizeVisibleMethodSource(method, source)
}
func VisibleMethodProviderKeyForSource(method, source string) (string, bool) {
	return payment.VisibleMethodProviderKeyForSource(method, source)
}

func CanonicalizeReturnURL(raw string, srcHost string, srcURL string) (string, error) {
	return payment.CanonicalizeReturnURL(raw, srcHost, srcURL)
}

func newVisibleMethodLoadBalancer(inner payment.LoadBalancer, configService *PaymentConfigService) payment.LoadBalancer {
	return payment.NewVisibleMethodLoadBalancer(inner, configService.paymentCoreConfig())
}
