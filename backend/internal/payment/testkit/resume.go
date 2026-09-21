package testkit

import (
	"os"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// Resume 组合原生密钥选择和令牌实现，保留旧配置密钥的验证回退。
func Resume(legacyKey []byte) *payment.PaymentResumeService {
	key, fallbacks := payment.ResolvePaymentResumeSigningKeys(os.Getenv("PAYMENT_RESUME_SIGNING_KEY"), legacyKey)
	return payment.NewPaymentResumeService(key, fallbacks...)
}
