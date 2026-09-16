//go:build unit

// 只供原 unit 契约测试保留旧私有入口，生产调用已接入所属模块。
package service

import (
	"context"
	"net/http"
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

const internal500PenaltyTier1Duration = acctcore.Internal500PenaltyTier1Duration
const internal500PenaltyTier2Duration = acctcore.Internal500PenaltyTier2Duration

func (s *AntigravityGatewayService) applyInternal500Penalty(
	ctx context.Context, prefix string, account *Account, count int64,
) {
	core := s.antigravityHealth()
	value := AccountRecordView(account)
	core.ApplyInternal500Penalty(ctx, prefix, value, count)
	if account != nil && value != nil {
		account.Extra = value.Extra
	}
}
func setModelRateLimitByModelName(ctx context.Context, repo AccountRepository, accountID int64, modelName, prefix string, statusCode int, resetAt time.Time, afterSmartRetry bool) bool {
	return acctcore.SetModelRateLimitByModelName(ctx, repo, accountID, modelName, prefix, statusCode, resetAt, afterSmartRetry, func(f string, args ...any) { logger.LegacyPrintf("service.antigravity_gateway", f, args...) })
}
func (s *AntigravityGatewayService) updateAccountModelRateLimitInCache(ctx context.Context, account *Account, modelKey string, resetAt time.Time) {
	core := s.antigravityHealth()
	value := AccountRecordView(account)
	core.UpdateAccountModelRateLimitInCache(ctx, value, modelKey, resetAt)
	if account != nil && value != nil {
		account.Extra = value.Extra
	}
}
func (f *AntigravityQuotaFetcher) buildUsageInfo(models *native.FetchAvailableModelsResponse, tier, normalized string, load *native.LoadCodeAssistResponse) *UsageInfo {
	return f.core.BuildUsageInfo(models, tier, normalized, load)
}
func normalizeTier(raw string) string { return acctcore.NormalizeAntigravityTier(raw) }

const smartRetryActionContinue = native.SmartRetryActionContinue
const smartRetryActionBreakWithResp = native.SmartRetryActionBreakWithResp
const smartRetryActionContinueURL = native.SmartRetryActionContinueURL

func (s *AntigravityGatewayService) handleSmartRetry(p antigravityRetryLoopParams, resp *http.Response, respBody []byte, baseURL string, urlIdx int, availableURLs []string) *smartRetryResult {
	adapter, input := s.antigravityRetryAdapter(p)
	return adapter.HandleSmartRetry(input, resp, respBody, baseURL, urlIdx, availableURLs)
}
func (s *AntigravityGatewayService) handleSingleAccountRetryInPlace(
	p antigravityRetryLoopParams,
	resp *http.Response,
	respBody []byte,
	baseURL string,
	waitDuration time.Duration,
	modelName string,
) *smartRetryResult {
	adapter, input := s.antigravityRetryAdapter(p)
	return adapter.HandleSingleAccountRetryInPlace(input, resp, respBody, baseURL, waitDuration, modelName)
}
func (s *AntigravityGatewayService) antigravityRetryLoop(p antigravityRetryLoopParams) (*antigravityRetryLoopResult, error) {
	adapter, input := s.antigravityRetryAdapter(p)
	return adapter.AntigravityRetryLoop(input)
}

const antigravity429Unknown = native.Antigravity429Unknown
const antigravity429RateLimited = native.Antigravity429RateLimited
const antigravity429QuotaExhausted = native.Antigravity429QuotaExhausted

func classifyAntigravity429(body []byte) antigravity429Category {
	return native.ClassifyAntigravity429(body)
}
func injectEnabledCreditTypes(body []byte) []byte { return native.InjectEnabledCreditTypes(body) }
func isAntigravityInternalServerError(statusCode int, body []byte) bool {
	return native.IsAntigravityInternalServerError(statusCode, body)
}

const antigravityMaxRetries = native.AntigravityMaxRetries
const antigravitySmartRetryMaxAttempts = native.AntigravitySmartRetryMaxAttempts
const antigravitySingleAccountSmartRetryMaxAttempts = native.AntigravitySingleAccountSmartRetryMaxAttempts
const antigravitySingleAccountSmartRetryMaxWait = native.AntigravitySingleAccountSmartRetryMaxWait
const antigravitySingleAccountSmartRetryTotalMaxWait = native.AntigravitySingleAccountSmartRetryTotalMaxWait

type smartRetryResult = native.SmartRetryResult
type antigravityRetryLoopResult = native.AntigravityRetryLoopResult
type antigravity429Category = native.Antigravity429Category

const antigravityForceTokenRefreshExtraKey = acctcore.AntigravityForceTokenRefreshExtraKey
const antigravityForceTokenRefreshReasonExtraKey = acctcore.AntigravityForceTokenRefreshReasonExtraKey
