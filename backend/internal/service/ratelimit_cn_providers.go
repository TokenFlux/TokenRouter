package service

import (
	"context"
	"net/http"
	"strings"
)

// 国产供应商（kimi/zhipu/deepseek）的响应式冷却辅助。
//
// 与 openai/anthropic 不同：
//   - 余额不足是「可恢复」状态（充值/检测恢复后自动重新调度），不能走 handleAuthError
//     永久置 status=error。这里改为 SetTempUnschedulable，由独立用量监控在同一
//     身份的余额恢复后 ClearTempUnschedulable。
//   - Coding Plan 滚动窗口耗尽（429）的冷却终点应是真实的窗口重置时间（已由
//     用量监控写入统一快照），而非默认的秒级兜底。

// kimiConcurrentRequestLimitMessage 是 Kimi 账号并发限制的精确上游文案。
const kimiConcurrentRequestLimitMessage = "You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."

// cnConcurrencyLimitReasonPrefix 标记 Kimi 并发限制导致的临时停调，
// 供恢复任务与其它账号状态来源区分。
const cnConcurrencyLimitReasonPrefix = "cn_concurrency_limit"

// isCNProviderConcurrencyLimit403 只识别 Kimi 返回的精确并发限制文案，
// 避免把其它权限错误或其它国产平台的相似文案误判为可恢复状态。
func isCNProviderConcurrencyLimit403(account *Account, upstreamMsg string) bool {
	return account != nil && account.Platform == PlatformKimi &&
		strings.TrimSpace(upstreamMsg) == kimiConcurrentRequestLimitMessage
}

func (s *RateLimitService) handleCNProviderConcurrencyLimit403(
	ctx context.Context,
	account *Account,
) {
	s.HealthCore().ApplyCNConcurrencyLimit(ctx, AccountRecordView(account), cnConcurrencyLimitReasonPrefix+": "+kimiConcurrentRequestLimitMessage)
}

// cnProviderResponseIndicatesInsufficientBalance 通过响应体文案识别余额不足
// （智谱 payg 无独立余额端点，仅能靠响应文案识别）。
func cnProviderResponseIndicatesInsufficientBalance(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	s := strings.ToLower(string(body))
	return strings.Contains(s, "余额不足") ||
		strings.Contains(s, "insufficient balance") ||
		strings.Contains(s, "insufficient_credit") ||
		strings.Contains(s, "balance is not enough") ||
		strings.Contains(s, "no enough balance")
}

func (s *RateLimitService) handleCNProviderInsufficientBalance(
	ctx context.Context,
	account *Account,
	upstreamMsg string,
) {
	s.HealthCore().ApplyCNInsufficientBalance(ctx, AccountRecordView(account), upstreamMsg)
}

// applyCNProviderReactive429 处理国产供应商的 429 响应。
// 返回 true 表示已处理（调用方应 return），false 表示未命中、继续走默认 429 逻辑。
func (s *RateLimitService) applyCNProviderReactive429(
	ctx context.Context,
	account *Account,
	headers http.Header,
	responseBody []byte,
) bool {
	if !account.IsCNProvider() {
		return false
	}
	// 1) 余额不足文案：可恢复临时停调（含智谱 payg 这类无余额端点的场景）。
	if cnProviderResponseIndicatesInsufficientBalance(responseBody) {
		s.handleCNProviderInsufficientBalance(ctx, account, extractUpstreamErrorMessage(responseBody))
		return true
	}
	return s.HealthCore().ApplyCNQuotaSnapshotCooldown(ctx, AccountRecordView(account))
}
