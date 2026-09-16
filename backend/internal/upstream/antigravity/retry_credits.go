// 平台账号内重试只使用技术输入和观测端口，不持有业务实体、数据库或 Gin。
package antigravity

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

type Antigravity429Category string

const (
	Antigravity429Unknown        Antigravity429Category = "unknown"
	Antigravity429RateLimited    Antigravity429Category = "rate_limited"
	Antigravity429QuotaExhausted Antigravity429Category = "quota_exhausted"
)

var (
	antigravityQuotaExhaustedKeywords = []string{"quota_exhausted", "quota exhausted"}

	creditsExhaustedKeywords = []string{
		"google_one_ai",
		"insufficient credit",
		"insufficient credits",
		"not enough credit",
		"not enough credits",
		"credit exhausted",
		"credits exhausted",
		"credit balance",
		"minimumcreditamountforusage",
		"minimum credit amount for usage",
		"minimum credit",
		"resource has been exhausted",
	}
)

// ClassifyAntigravity429 将 Antigravity 的 429 响应归类为配额耗尽、限流或未知。
func ClassifyAntigravity429(body []byte) Antigravity429Category {
	if len(body) == 0 {
		return Antigravity429Unknown
	}
	lowerBody := strings.ToLower(string(body))
	for _, keyword := range antigravityQuotaExhaustedKeywords {
		if strings.Contains(lowerBody, keyword) {
			return Antigravity429QuotaExhausted
		}
	}
	if info := ParseAntigravitySmartRetryInfo(body); info != nil && !info.IsModelCapacityExhausted {
		return Antigravity429RateLimited
	}
	return Antigravity429Unknown
}

// InjectEnabledCreditTypes 在已序列化的 v1internal JSON body 中注入 AI Credits 类型。
func InjectEnabledCreditTypes(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	payload["enabledCreditTypes"] = []string{"GOOGLE_ONE_AI"}
	result, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return result
}

// ShouldMarkCreditsExhausted 判断一次 credits 请求失败是否应标记为 credits 耗尽。
func ShouldMarkCreditsExhausted(resp *http.Response, respBody []byte, reqErr error) bool {
	if reqErr != nil || resp == nil {
		return false
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout {
		return false
	}
	// 注意：不再检查 IsURLLevelRateLimit。此函数仅在积分重试失败后调用，
	// 如果注入 enabledCreditTypes 后仍返回 "Resource has been exhausted"，
	// 说明积分也已耗尽，应该标记。clearCreditsExhausted 会在后续成功时自动清除。
	if info := ParseAntigravitySmartRetryInfo(respBody); info != nil {
		return false
	}
	bodyLower := strings.ToLower(string(respBody))
	for _, keyword := range creditsExhaustedKeywords {
		if strings.Contains(bodyLower, keyword) {
			return true
		}
	}
	return false
}

type CreditsOveragesRetryResult struct {
	Handled bool
	Resp    *http.Response
}

// AttemptCreditsOveragesRetry 在确认免费配额耗尽后，尝试注入 AI Credits 继续请求。
func (s *RetryAdapter) AttemptCreditsOveragesRetry(
	p RetryInput,
	baseURL string,
	modelName string,
	waitDuration time.Duration,
	originalStatusCode int,
	respBody []byte,
) *CreditsOveragesRetryResult {
	creditsBody := InjectEnabledCreditTypes(p.Body)
	if creditsBody == nil {
		return &CreditsOveragesRetryResult{Handled: false}
	}
	modelKey := s.Options.CreditsModel(modelName)
	logger.LegacyPrintf("service.antigravity_gateway", "%s status=429 credit_overages_retry model=%s account=%d (injecting enabledCreditTypes)",
		p.Prefix, modelKey, p.AccountID)

	creditsReq, err := NewAPIRequestWithURL(p.Ctx, baseURL, p.Action, p.AccessToken, creditsBody)
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "%s credit_overages_failed model=%s account=%d build_request_err=%v",
			p.Prefix, modelKey, p.AccountID, err)
		return &CreditsOveragesRetryResult{Handled: true}
	}
	s.Options.ApplyHeaders(creditsReq)

	creditsResp, err := s.Options.Do(creditsReq)
	if err == nil && creditsResp != nil && creditsResp.StatusCode < 400 {
		s.Options.ClearCredits()
		logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d credit_overages_success model=%s account=%d",
			p.Prefix, creditsResp.StatusCode, modelKey, p.AccountID)
		return &CreditsOveragesRetryResult{Handled: true, Resp: creditsResp}
	}

	s.Options.CreditsFailure(modelKey, creditsResp, err)
	return &CreditsOveragesRetryResult{Handled: true}
}
