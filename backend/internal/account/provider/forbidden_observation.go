package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
)

// ForbiddenObservation 复用平台解析，不作账号持久化或调度裁决。
func ForbiddenObservation(value *account.Record, message string, body []byte) account.ForbiddenObservation {
	kind := antigravity.ClassifyForbiddenType(string(body))
	url := ""
	if kind == account.ForbiddenTypeValidation {
		url = antigravity.ExtractValidationURL(string(body))
	}
	return account.ForbiddenObservation{Message: message, Body: body, HTML: upstream.IsHTMLResponse(body), Kind: kind, ValidationURL: url, ConcurrentRequestLimited: CNConcurrencyLimit403(value, message), ConcurrencyReason: "cn_concurrency_limit: " + kimi.ConcurrentRequestLimitMessage}
}

// CNConcurrencyLimit403 仅认可 Kimi 的精确限制消息，其他平台仍使用原错误处理。
func CNConcurrencyLimit403(value *account.Record, message string) bool {
	return value != nil && value.Platform == account.PlatformKimi && kimi.IsConcurrencyLimitMessage(message)
}
