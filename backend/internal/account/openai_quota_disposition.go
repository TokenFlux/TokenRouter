package account

import (
	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"time"
)

// OpenAI429Disposition 区分明确耗尽窗口和兼容重置观测，不决定请求重试。
type OpenAI429Disposition uint8

const (
	OpenAI429Transient OpenAI429Disposition = iota
	OpenAI429Quota5h
	OpenAI429Quota7d
	OpenAI429QuotaReset
)

// OpenAIExhaustedWindow 保留 7d 优先、缺少重置仍视为耗尽的原语义。
func OpenAIExhaustedWindow(snapshot *openaiprotocol.OpenAICodexUsageSnapshot, clock func() time.Time) (OpenAI429Disposition, *time.Time) {
	if snapshot == nil {
		return OpenAI429Transient, nil
	}
	normalized := snapshot.Normalize()
	if normalized == nil {
		return OpenAI429Transient, nil
	}
	if normalized.Used7dPercent != nil && *normalized.Used7dPercent >= 100 {
		if normalized.Reset7dSeconds != nil {
			reset := clock().Add(time.Duration(*normalized.Reset7dSeconds) * time.Second)
			return OpenAI429Quota7d, &reset
		}
		return OpenAI429Quota7d, nil
	}
	if normalized.Used5hPercent != nil && *normalized.Used5hPercent >= 100 {
		if normalized.Reset5hSeconds != nil {
			reset := clock().Add(time.Duration(*normalized.Reset5hSeconds) * time.Second)
			return OpenAI429Quota5h, &reset
		}
		return OpenAI429Quota5h, nil
	}
	return OpenAI429Transient, nil
}
