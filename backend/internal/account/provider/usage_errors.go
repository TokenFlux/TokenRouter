package provider

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// buildAntigravityDegradedUsage 从 FetchQuota 错误构建降级 UsageInfo
func AntigravityDegradedUsage(err error, now time.Time) *accountcore.UsageInfo {
	errMsg := fmt.Sprintf("usage API error: %v", err)
	slog.Warn("antigravity usage fetch failed, returning degraded response", "error", err)

	info := &accountcore.UsageInfo{
		UpdatedAt: &now,
		Error:     errMsg,
	}

	// 从错误信息推断 error_code 和状态标记
	// 错误格式来自 antigravity/client.go: "fetchAvailableModels 失败 (HTTP %d): ..."
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "HTTP 401") ||
		strings.Contains(errStr, "UNAUTHENTICATED") ||
		strings.Contains(errStr, "invalid_grant"):
		info.ErrorCode = accountcore.ErrorCodeUnauthenticated
		info.NeedsReauth = true
	case strings.Contains(errStr, "HTTP 429"):
		info.ErrorCode = accountcore.ErrorCodeRateLimited
	default:
		info.ErrorCode = accountcore.ErrorCodeNetworkError
	}

	return info
}

// EnrichUsageWithAccountError 结合账号错误状态修正 UsageInfo
// 场景 1（成功路径）：FetchAvailableModels 正常返回，但账号已因 403 被标记为 error，
//
//	需要在正常 usage 数据上附加 forbidden/validation 信息。
//
// 场景 2（降级路径）：被封号的账号 OAuth token 失效，FetchAvailableModels 返回 401，
//
//	降级逻辑设置了 needs_reauth，但账号实际是 403 封号/需验证，需覆盖为正确状态。
func EnrichUsageWithAccountError(info *accountcore.UsageInfo, value *accountcore.Record) {
	if info == nil || value == nil || value.Status != accountcore.StatusError {
		return
	}
	msg := strings.ToLower(value.ErrorMessage)
	if !strings.Contains(msg, "403") && !strings.Contains(msg, "forbidden") &&
		!strings.Contains(msg, "violation") && !strings.Contains(msg, "validation") {
		return
	}
	fbType := antigravity.ClassifyForbiddenType(value.ErrorMessage)
	info.IsForbidden = true
	info.ForbiddenType = fbType
	info.ForbiddenReason = value.ErrorMessage
	info.NeedsVerify = fbType == accountcore.ForbiddenTypeValidation
	info.IsBanned = fbType == accountcore.ForbiddenTypeViolation
	info.ValidationURL = antigravity.ExtractValidationURL(value.ErrorMessage)
	info.ErrorCode = accountcore.ErrorCodeForbidden
	info.NeedsReauth = false
}
