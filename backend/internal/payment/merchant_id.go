// 商户订单号生成保留原时间格式、字符集和随机选择。
package payment

import (
	"math/rand/v2"
	"time"
)

const OrderIDPrefix = "sub2_"

// GenerateOutTradeNo creates a unique external order ID for payment providers.
// Format: sub2_20250409aB3kX9mQ (prefix + date + 8-char random)
func GenerateOutTradeNo() string {
	date := time.Now().Format("20060102")
	rnd := GenerateMerchantRandomString(8)
	return OrderIDPrefix + date + rnd
}
func GenerateMerchantRandomString(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}
