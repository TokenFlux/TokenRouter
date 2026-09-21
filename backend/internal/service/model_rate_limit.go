package service

import (
	"context"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

const (
	modelRateLimitsKey                 = "model_rate_limits"
	antigravityGeminiModelRateLimitKey = "antigravity:gemini"
	openAIImageGenerationRateLimitKey  = accountcore.OpenAIImageGenerationRateLimitKey
	openAICodexSparkRateLimitReason    = accountcore.CodexSparkRateLimitReason
)

// isRateLimitActiveForKey 检查指定 key 的限流是否生效
func (a *Account) isRateLimitActiveForKey(key string) bool {
	return AccountRecordView(a).ModelRateLimitActive(key)
}

// getRateLimitRemainingForKey 获取指定 key 的限流剩余时间，0 表示未限流或已过期
func (a *Account) getRateLimitRemainingForKey(key string) time.Duration {
	return AccountRecordView(a).ModelRateLimitRemaining(key)
}

func (a *Account) isModelRateLimitedWithContext(ctx context.Context, requestedModel string) bool {
	for _, key := range a.modelRateLimitKeysForRequest(ctx, requestedModel) {
		if a.isRateLimitActiveForKey(key) {
			return true
		}
	}
	return false
}

// GetModelRateLimitRemainingTime 获取模型限流剩余时间
// 返回 0 表示未限流或已过期
func (a *Account) GetModelRateLimitRemainingTime(requestedModel string) time.Duration {
	return a.GetModelRateLimitRemainingTimeWithContext(context.Background(), requestedModel)
}

func (a *Account) GetModelRateLimitRemainingTimeWithContext(ctx context.Context, requestedModel string) time.Duration {
	remaining := time.Duration(0)
	for _, key := range a.modelRateLimitKeysForRequest(ctx, requestedModel) {
		if keyRemaining := a.getRateLimitRemainingForKey(key); keyRemaining > remaining {
			remaining = keyRemaining
		}
	}
	return remaining
}

func (a *Account) modelRateLimitKeysForRequest(ctx context.Context, requestedModel string) []string {
	return accountModelPolicy(a).LimitKeys(ctx, requestedModel)
}

// 旧请求入口只读取本次 thinking，模型规则由原生适配器执行。
func resolveFinalAntigravityModelKey(ctx context.Context, value *Account, model string) string {
	return accountprovider.FinalAntigravityModel(AccountRecordView(value), model, modelHealthThinking(ctx))
}

// 旧限流入口共用原生模型与 Gemini 家族 key。
func antigravityModelRateLimitKeys(model string) []string {
	return accountprovider.AntigravityModelLimitKeys(model)
}
