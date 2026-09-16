//go:build unit

// 生产消费者已迁出；原 unit 断言通过唯一实现的兼容入口验证。
package service

import (
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func sanitizeGrokResponsesTools(body []byte) ([]byte, error) {
	return grokBodyCodec().SanitizeGrokResponsesTools(body)
}

func grokRateLimitResetAt(snapshot *nativegrok.QuotaSnapshot, now time.Time) (time.Time, bool) {
	return accountcore.GrokRateLimitResetAt(snapshot, now)
}
