// 平台账号内重试只使用技术输入和观测端口，不持有业务实体、数据库或 Gin。
package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mathrand "math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	googlewire "github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

type RetryInput struct {
	Ctx                                    context.Context
	Prefix                                 string
	AccountID                              int64
	AccountName                            string
	Native, OveragesEnabled, SingleAccount bool
	AccessToken, Action                    string
	Body                                   []byte
	RequestedModel                         string
	IsStickySession                        bool
	CreditsExhausted                       func() bool
	ModelLimited                           func(context.Context, string) bool
	ModelRemaining                         func(context.Context, string) time.Duration
}

func (p RetryInput) String() string { return fmt.Sprintf("antigravity retry account=%d", p.AccountID) }

type RetryObservation struct {
	AccountID                                             int64
	AccountName                                           string
	UpstreamStatusCode                                    int
	UpstreamRequestID, UpstreamURL, Kind, Message, Detail string
}
type RetryOptions struct {
	BaseURL              func() string
	Do                   func(*http.Request) (*http.Response, error)
	ApplyHeaders         func(*http.Request)
	LogBody              bool
	LogMaxBytes          int
	TruncateString       func(string, int) string
	TruncateForLog       func([]byte, int) string
	SafeURL              func(string) string
	Observe              func(RetryObservation)
	SetError             func(int, string, string)
	ReadErrorBody        func(*http.Response) []byte
	ApplyErrorPolicy     func(int, http.Header, []byte) (bool, int, error)
	HandleError          func(int, http.Header, []byte)
	SetModelLimits       func(string, int, time.Time, bool) bool
	ClearSticky          func()
	CreditsModel         func(string) string
	ClearCredits         func()
	CreditsFailure       func(string, *http.Response, error)
	Internal500Exhausted func()
	ResetInternal500     func()
}
type RetryAdapter struct{ Options RetryOptions }

// AntigravityRetryLoopResult 重试循环的结果
type AntigravityRetryLoopResult struct {
	Resp *http.Response
}

// ResolveAntigravityForwardBaseURL 解析转发用 base URL。
//
// 显式环境变量优先。未配置时，LoadCodeAssist 返回 paidTier 的付费账号使用
// daily 端点，其他账号继续使用生产端点，避免免费账号的 OAuth token 出现 401。
//
// 历史上这里改用 ForwardBaseURLs()（把 daily/sandbox 排到首位）并默认取首个地址，
// 导致网关把带生产 OAuth token 的请求发到 daily-cloudcode-pa.sandbox.googleapis.com，
// 上游拒绝 → 账号被 401「Invalid bearer token」/502 打入临时不可调度且无法恢复
// （见 #3611 / #2962）。后台「测试连接」用的是生产端点，所以「测试成功但网关 401」。
func ResolveAntigravityForwardBaseURL(mode string, paid bool) string {
	baseURLs := BaseURLs
	if len(baseURLs) == 0 {
		return ""
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if (mode == "daily" || mode == "sandbox") && len(baseURLs) > 1 {
		return baseURLs[1]
	}
	if mode == "" && paid && len(baseURLs) > 1 {
		return baseURLs[1]
	}
	return baseURLs[0]
}

// SmartRetryAction 智能重试的处理结果
type SmartRetryAction int

const (
	SmartRetryActionContinue      SmartRetryAction = iota // 继续默认重试逻辑
	SmartRetryActionBreakWithResp                         // 结束循环并返回 resp
	SmartRetryActionContinueURL                           // 继续 URL fallback 循环
)

// SmartRetryResult 智能重试的结果
type SmartRetryResult struct {
	Action      SmartRetryAction
	Resp        *http.Response
	Err         error
	SwitchError *AntigravityAccountSwitchError // 模型限流时返回账号切换信号
}

// HandleSmartRetry 处理 OAuth 账号的智能重试逻辑
// 将 429/503 限流处理逻辑抽取为独立函数，减少 AntigravityRetryLoop 的复杂度
func (s *RetryAdapter) HandleSmartRetry(p RetryInput, resp *http.Response, respBody []byte, baseURL string, urlIdx int, availableURLs []string) *SmartRetryResult {
	// "Resource has been exhausted" 是 URL 级别限流，切换 URL（仅 429）
	if resp.StatusCode == http.StatusTooManyRequests && IsURLLevelRateLimit(respBody) && urlIdx < len(availableURLs)-1 {
		logger.LegacyPrintf("service.antigravity_gateway", "%s URL fallback (429): %s -> %s", p.Prefix, baseURL, availableURLs[urlIdx+1])
		return &SmartRetryResult{Action: SmartRetryActionContinueURL}
	}

	category := Antigravity429Unknown
	if resp.StatusCode == http.StatusTooManyRequests {
		category = ClassifyAntigravity429(respBody)
	}

	// 判断是否触发智能重试
	shouldSmartRetry, shouldRateLimitModel, waitDuration, modelName, isModelCapacityExhausted := ShouldTriggerAntigravitySmartRetry(p.Native, respBody)

	// AI Credits 超量请求：
	// 仅在上游明确返回免费配额耗尽时才允许切换到 credits。
	if resp.StatusCode == http.StatusTooManyRequests &&
		category == Antigravity429QuotaExhausted &&
		p.OveragesEnabled &&
		!p.CreditsExhausted() {
		result := s.AttemptCreditsOveragesRetry(p, baseURL, modelName, waitDuration, resp.StatusCode, respBody)
		if result.Handled && result.Resp != nil {
			return &SmartRetryResult{
				Action: SmartRetryActionBreakWithResp,
				Resp:   result.Resp,
			}
		}
	}

	// 情况1: retryDelay >= 阈值，限流模型并切换账号
	if shouldRateLimitModel {
		// 单账号 503 退避重试模式：不设限流、不切换账号，改为原地等待+重试
		// 谷歌上游 503 (MODEL_CAPACITY_EXHAUSTED) 通常是暂时性的，等几秒就能恢复。
		// 多账号场景下切换账号是最优选择，但单账号场景下设限流毫无意义（只会导致双重等待）。
		if resp.StatusCode == http.StatusServiceUnavailable && p.SingleAccount {
			return s.HandleSingleAccountRetryInPlace(p, resp, respBody, baseURL, waitDuration, modelName)
		}

		rateLimitDuration := waitDuration
		if rateLimitDuration <= 0 {
			rateLimitDuration = AntigravityDefaultRateLimitDuration
		}
		logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d oauth_long_delay model=%s account=%d upstream_retry_delay=%v body=%s (model rate limit, switch account)",
			p.Prefix, resp.StatusCode, modelName, p.AccountID, rateLimitDuration, s.Options.TruncateForLog(respBody, 200))

		resetAt := time.Now().Add(rateLimitDuration)
		if !s.Options.SetModelLimits(modelName, resp.StatusCode, resetAt, false) {
			s.Options.HandleError(resp.StatusCode, resp.Header, respBody)
			logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d rate_limited account=%d (no model mapping)", p.Prefix, resp.StatusCode, p.AccountID)
		}
		s.Options.ClearSticky()

		// 返回账号切换信号，让上层切换账号重试
		return &SmartRetryResult{
			Action: SmartRetryActionBreakWithResp,
			SwitchError: &AntigravityAccountSwitchError{
				OriginalAccountID: p.AccountID,
				RateLimitedModel:  modelName,
				IsStickySession:   p.IsStickySession,
			},
		}
	}

	// 情况2: retryDelay < 阈值（或 MODEL_CAPACITY_EXHAUSTED），智能重试
	if shouldSmartRetry {
		var lastRetryResp *http.Response
		var lastRetryBody []byte

		// MODEL_CAPACITY_EXHAUSTED 使用独立的重试参数（60 次，固定 1s 间隔）
		maxAttempts := AntigravitySmartRetryMaxAttempts
		if isModelCapacityExhausted {
			maxAttempts = AntigravityModelCapacityRetryMaxAttempts
			waitDuration = AntigravityModelCapacityRetryWait

			// 全局去重：如果其他 goroutine 已在重试同一模型且尚在 cooldown 中，直接返回 503
			if modelName != "" {
				modelCapacityExhaustedMu.RLock()
				cooldownUntil, exists := modelCapacityExhaustedUntil[modelName]
				modelCapacityExhaustedMu.RUnlock()
				if exists && time.Now().Before(cooldownUntil) {
					log.Printf("%s status=%d model_capacity_exhausted_dedup model=%s account=%d cooldown_until=%v (skip retry)",
						p.Prefix, resp.StatusCode, modelName, p.AccountID, cooldownUntil.Format("15:04:05"))
					return &SmartRetryResult{
						Action: SmartRetryActionBreakWithResp,
						Resp: &http.Response{
							StatusCode: resp.StatusCode,
							Header:     resp.Header.Clone(),
							Body:       io.NopCloser(bytes.NewReader(respBody)),
						},
					}
				}
			}
		}

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			log.Printf("%s status=%d oauth_smart_retry attempt=%d/%d delay=%v model=%s account=%d",
				p.Prefix, resp.StatusCode, attempt, maxAttempts, waitDuration, modelName, p.AccountID)

			timer := time.NewTimer(waitDuration)
			select {
			case <-p.Ctx.Done():
				timer.Stop()
				log.Printf("%s status=context_canceled_during_smart_retry", p.Prefix)
				return &SmartRetryResult{Action: SmartRetryActionBreakWithResp, Err: p.Ctx.Err()}
			case <-timer.C:
			}

			// 智能重试：创建新请求
			retryReq, err := NewAPIRequestWithURL(p.Ctx, baseURL, p.Action, p.AccessToken, p.Body)
			if err != nil {
				logger.LegacyPrintf("service.antigravity_gateway", "%s status=smart_retry_request_build_failed error=%v", p.Prefix, err)
				s.Options.HandleError(resp.StatusCode, resp.Header, respBody)
				return &SmartRetryResult{
					Action: SmartRetryActionBreakWithResp,
					Resp: &http.Response{
						StatusCode: resp.StatusCode,
						Header:     resp.Header.Clone(),
						Body:       io.NopCloser(bytes.NewReader(respBody)),
					},
				}
			}
			s.Options.ApplyHeaders(retryReq)

			retryResp, retryErr := s.Options.Do(retryReq)
			if retryErr == nil && retryResp != nil && retryResp.StatusCode != http.StatusTooManyRequests && retryResp.StatusCode != http.StatusServiceUnavailable {
				log.Printf("%s status=%d smart_retry_success attempt=%d/%d", p.Prefix, retryResp.StatusCode, attempt, maxAttempts)
				// 重试成功，清除 MODEL_CAPACITY_EXHAUSTED cooldown
				if isModelCapacityExhausted && modelName != "" {
					modelCapacityExhaustedMu.Lock()
					delete(modelCapacityExhaustedUntil, modelName)
					modelCapacityExhaustedMu.Unlock()
				}
				return &SmartRetryResult{Action: SmartRetryActionBreakWithResp, Resp: retryResp}
			}

			// 网络错误时，继续重试
			if retryErr != nil || retryResp == nil {
				log.Printf("%s status=smart_retry_network_error attempt=%d/%d error=%v", p.Prefix, attempt, maxAttempts, retryErr)
				continue
			}

			// 重试失败，关闭之前的响应
			if lastRetryResp != nil {
				_ = lastRetryResp.Body.Close()
			}
			lastRetryResp = retryResp
			if retryResp != nil {
				lastRetryBody, _ = io.ReadAll(io.LimitReader(retryResp.Body, 8<<10))
				_ = retryResp.Body.Close()
			}

			// 解析新的重试信息，用于下次重试的等待时间（MODEL_CAPACITY_EXHAUSTED 使用固定循环，跳过）
			if !isModelCapacityExhausted && attempt < maxAttempts && lastRetryBody != nil {
				newShouldRetry, _, newWaitDuration, _, _ := ShouldTriggerAntigravitySmartRetry(p.Native, lastRetryBody)
				if newShouldRetry && newWaitDuration > 0 {
					waitDuration = newWaitDuration
				}
			}
		}

		// 所有重试都失败
		rateLimitDuration := waitDuration
		if rateLimitDuration <= 0 {
			rateLimitDuration = AntigravityDefaultRateLimitDuration
		}
		retryBody := lastRetryBody
		if retryBody == nil {
			retryBody = respBody
		}

		// MODEL_CAPACITY_EXHAUSTED：模型容量不足，切换账号无意义
		// 直接返回上游错误响应，不设置模型限流，不切换账号
		if isModelCapacityExhausted {
			// 设置 cooldown，让后续请求快速失败，避免重复重试
			if modelName != "" {
				modelCapacityExhaustedMu.Lock()
				modelCapacityExhaustedUntil[modelName] = time.Now().Add(AntigravityModelCapacityCooldown)
				modelCapacityExhaustedMu.Unlock()
			}
			log.Printf("%s status=%d smart_retry_exhausted_model_capacity attempts=%d model=%s account=%d body=%s (model capacity exhausted, not switching account)",
				p.Prefix, resp.StatusCode, maxAttempts, modelName, p.AccountID, s.Options.TruncateForLog(retryBody, 200))
			return &SmartRetryResult{
				Action: SmartRetryActionBreakWithResp,
				Resp: &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(retryBody)),
				},
			}
		}

		// 单账号 503 退避重试模式：智能重试耗尽后不设限流、不切换账号，
		// 直接返回 503 让 Handler 层的单账号退避循环做最终处理。
		if resp.StatusCode == http.StatusServiceUnavailable && p.SingleAccount {
			logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d smart_retry_exhausted_single_account attempts=%d model=%s account=%d body=%s (return 503 directly)",
				p.Prefix, resp.StatusCode, AntigravitySmartRetryMaxAttempts, modelName, p.AccountID, s.Options.TruncateForLog(retryBody, 200))
			return &SmartRetryResult{
				Action: SmartRetryActionBreakWithResp,
				Resp: &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(retryBody)),
				},
			}
		}

		log.Printf("%s status=%d smart_retry_exhausted attempts=%d model=%s account=%d upstream_retry_delay=%v body=%s (switch account)",
			p.Prefix, resp.StatusCode, maxAttempts, modelName, p.AccountID, rateLimitDuration, s.Options.TruncateForLog(retryBody, 200))

		resetAt := time.Now().Add(rateLimitDuration)
		s.Options.SetModelLimits(modelName, resp.StatusCode, resetAt, true)

		// 清除粘性会话绑定，避免下次请求仍命中限流账号
		s.Options.ClearSticky()

		// 返回账号切换信号，让上层切换账号重试
		return &SmartRetryResult{
			Action: SmartRetryActionBreakWithResp,
			SwitchError: &AntigravityAccountSwitchError{
				OriginalAccountID: p.AccountID,
				RateLimitedModel:  modelName,
				IsStickySession:   p.IsStickySession,
			},
		}
	}

	// 未触发智能重试，继续默认重试逻辑
	return &SmartRetryResult{Action: SmartRetryActionContinue}
}

// HandleSingleAccountRetryInPlace 单账号 503 退避重试的原地重试逻辑。
//
// 在多账号场景下，收到 503 + 长 retryDelay（≥ 7s）时会设置模型限流 + 切换账号；
// 但在单账号场景下，设限流毫无意义（因为切换回来的还是同一个账号，还要等限流过期）。
// 此方法改为在 Service 层原地等待 + 重试，避免双重等待问题：
//
//	旧流程：Service 设限流 → Handler 退避等待 → Service 等限流过期 → 再请求（总耗时 = 退避 + 限流）
//	新流程：Service 直接等 retryDelay → 重试 → 成功/再等 → 重试...（总耗时 ≈ 实际 retryDelay × 重试次数）
//
// 约束：
//   - 单次等待不超过 AntigravitySingleAccountSmartRetryMaxWait
//   - 总累计等待不超过 AntigravitySingleAccountSmartRetryTotalMaxWait
//   - 最多重试 AntigravitySingleAccountSmartRetryMaxAttempts 次
func (s *RetryAdapter) HandleSingleAccountRetryInPlace(
	p RetryInput,
	resp *http.Response,
	respBody []byte,
	baseURL string,
	waitDuration time.Duration,
	modelName string,
) *SmartRetryResult {
	// 限制单次等待时间
	if waitDuration > AntigravitySingleAccountSmartRetryMaxWait {
		waitDuration = AntigravitySingleAccountSmartRetryMaxWait
	}
	if waitDuration < AntigravitySmartRetryMinWait {
		waitDuration = AntigravitySmartRetryMinWait
	}

	logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d single_account_503_retry_in_place model=%s account=%d upstream_retry_delay=%v (retrying in-place instead of rate-limiting)",
		p.Prefix, resp.StatusCode, modelName, p.AccountID, waitDuration)

	var lastRetryResp *http.Response
	var lastRetryBody []byte
	totalWaited := time.Duration(0)

	for attempt := 1; attempt <= AntigravitySingleAccountSmartRetryMaxAttempts; attempt++ {
		// 检查累计等待是否超限
		if totalWaited+waitDuration > AntigravitySingleAccountSmartRetryTotalMaxWait {
			remaining := AntigravitySingleAccountSmartRetryTotalMaxWait - totalWaited
			if remaining <= 0 {
				logger.LegacyPrintf("service.antigravity_gateway", "%s single_account_503_retry: total_wait_exceeded total=%v max=%v, giving up",
					p.Prefix, totalWaited, AntigravitySingleAccountSmartRetryTotalMaxWait)
				break
			}
			waitDuration = remaining
		}

		logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d single_account_503_retry attempt=%d/%d delay=%v total_waited=%v model=%s account=%d",
			p.Prefix, resp.StatusCode, attempt, AntigravitySingleAccountSmartRetryMaxAttempts, waitDuration, totalWaited, modelName, p.AccountID)

		timer := time.NewTimer(waitDuration)
		select {
		case <-p.Ctx.Done():
			timer.Stop()
			logger.LegacyPrintf("service.antigravity_gateway", "%s status=context_canceled_during_single_account_retry", p.Prefix)
			return &SmartRetryResult{Action: SmartRetryActionBreakWithResp, Err: p.Ctx.Err()}
		case <-timer.C:
		}
		totalWaited += waitDuration

		// 创建新请求
		retryReq, err := NewAPIRequestWithURL(p.Ctx, baseURL, p.Action, p.AccessToken, p.Body)
		if err != nil {
			logger.LegacyPrintf("service.antigravity_gateway", "%s single_account_503_retry: request_build_failed error=%v", p.Prefix, err)
			break
		}
		s.Options.ApplyHeaders(retryReq)

		retryResp, retryErr := s.Options.Do(retryReq)
		if retryErr == nil && retryResp != nil && retryResp.StatusCode != http.StatusTooManyRequests && retryResp.StatusCode != http.StatusServiceUnavailable {
			logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d single_account_503_retry_success attempt=%d/%d total_waited=%v",
				p.Prefix, retryResp.StatusCode, attempt, AntigravitySingleAccountSmartRetryMaxAttempts, totalWaited)
			// 关闭之前的响应
			if lastRetryResp != nil {
				_ = lastRetryResp.Body.Close()
			}
			return &SmartRetryResult{Action: SmartRetryActionBreakWithResp, Resp: retryResp}
		}

		// 网络错误时继续重试
		if retryErr != nil || retryResp == nil {
			logger.LegacyPrintf("service.antigravity_gateway", "%s single_account_503_retry: network_error attempt=%d/%d error=%v",
				p.Prefix, attempt, AntigravitySingleAccountSmartRetryMaxAttempts, retryErr)
			continue
		}

		// 关闭之前的响应
		if lastRetryResp != nil {
			_ = lastRetryResp.Body.Close()
		}
		lastRetryResp = retryResp
		lastRetryBody, _ = io.ReadAll(io.LimitReader(retryResp.Body, 8<<10))
		_ = retryResp.Body.Close()

		// 解析新的重试信息，更新下次等待时间
		if attempt < AntigravitySingleAccountSmartRetryMaxAttempts && lastRetryBody != nil {
			_, _, newWaitDuration, _, _ := ShouldTriggerAntigravitySmartRetry(p.Native, lastRetryBody)
			if newWaitDuration > 0 {
				waitDuration = newWaitDuration
				if waitDuration > AntigravitySingleAccountSmartRetryMaxWait {
					waitDuration = AntigravitySingleAccountSmartRetryMaxWait
				}
				if waitDuration < AntigravitySmartRetryMinWait {
					waitDuration = AntigravitySmartRetryMinWait
				}
			}
		}
	}

	// 所有重试都失败，不设限流，直接返回 503
	// Handler 层的单账号退避循环会做最终处理
	retryBody := lastRetryBody
	if retryBody == nil {
		retryBody = respBody
	}
	logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d single_account_503_retry_exhausted attempts=%d total_waited=%v model=%s account=%d body=%s (return 503 directly)",
		p.Prefix, resp.StatusCode, AntigravitySingleAccountSmartRetryMaxAttempts, totalWaited, modelName, p.AccountID, s.Options.TruncateForLog(retryBody, 200))

	return &SmartRetryResult{
		Action: SmartRetryActionBreakWithResp,
		Resp: &http.Response{
			StatusCode: resp.StatusCode,
			Header:     resp.Header.Clone(),
			Body:       io.NopCloser(bytes.NewReader(retryBody)),
		},
	}
}

// AntigravityRetryLoop 执行带 URL fallback 的重试循环
func (s *RetryAdapter) AntigravityRetryLoop(p RetryInput) (*AntigravityRetryLoopResult, error) {
	// 预检查：模型限流 + overages 启用 + 积分未耗尽 → 直接注入 AI Credits
	overagesInjected := false
	if p.RequestedModel != "" && p.Native &&
		p.OveragesEnabled && !p.CreditsExhausted() &&
		p.ModelLimited(p.Ctx, p.RequestedModel) {
		if creditsBody := InjectEnabledCreditTypes(p.Body); creditsBody != nil {
			p.Body = creditsBody
			overagesInjected = true
			logger.LegacyPrintf("service.antigravity_gateway", "%s pre_check: model_rate_limited_credits_inject model=%s account=%d (injecting enabledCreditTypes)",
				p.Prefix, p.RequestedModel, p.AccountID)
		}
	}

	// 预检查：如果账号已限流，直接返回切换信号
	if p.RequestedModel != "" {
		if remaining := p.ModelRemaining(p.Ctx, p.RequestedModel); remaining > 0 {
			// 已注入积分的请求不再受普通模型限流预检查阻断。
			if overagesInjected {
				logger.LegacyPrintf("service.antigravity_gateway", "%s pre_check: credits_injected_ignore_rate_limit remaining=%v model=%s account=%d",
					p.Prefix, remaining.Truncate(time.Millisecond), p.RequestedModel, p.AccountID)
			} else if p.SingleAccount {
				// 单账号 503 退避重试模式：跳过限流预检查，直接发请求。
				// 首次请求设的限流是为了多账号调度器跳过该账号，在单账号模式下无意义。
				// 如果上游确实还不可用，HandleSmartRetry → HandleSingleAccountRetryInPlace
				// 会在 Service 层原地等待+重试，不需要在预检查这里等。
				logger.LegacyPrintf("service.antigravity_gateway", "%s pre_check: single_account_retry skipping rate_limit remaining=%v model=%s account=%d (will retry in-place if 503)",
					p.Prefix, remaining.Truncate(time.Millisecond), p.RequestedModel, p.AccountID)
			} else {
				logger.LegacyPrintf("service.antigravity_gateway", "%s pre_check: rate_limit_switch remaining=%v model=%s account=%d",
					p.Prefix, remaining.Truncate(time.Millisecond), p.RequestedModel, p.AccountID)
				return nil, &AntigravityAccountSwitchError{
					OriginalAccountID: p.AccountID,
					RateLimitedModel:  p.RequestedModel,
					IsStickySession:   p.IsStickySession,
				}
			}
		}
	}

	baseURL := s.Options.BaseURL()
	if baseURL == "" {
		return nil, errors.New("no antigravity forward base url configured")
	}
	availableURLs := []string{baseURL}

	var resp *http.Response
	var usedBaseURL string
	logBody := s.Options.LogBody
	maxBytes := 2048
	if s.Options.LogMaxBytes > 0 {
		maxBytes = s.Options.LogMaxBytes
	}
	getUpstreamDetail := func(body []byte) string {
		if !logBody {
			return ""
		}
		return s.Options.TruncateString(string(body), maxBytes)
	}

urlFallbackLoop:
	for urlIdx, baseURL := range availableURLs {
		usedBaseURL = baseURL
		allAttemptsInternal500 := true // 追踪本轮所有 attempt 是否全部命中 INTERNAL 500
		for attempt := 1; attempt <= AntigravityMaxRetries; attempt++ {
			select {
			case <-p.Ctx.Done():
				logger.LegacyPrintf("service.antigravity_gateway", "%s status=context_canceled error=%v", p.Prefix, p.Ctx.Err())
				return nil, p.Ctx.Err()
			default:
			}

			upstreamReq, err := NewAPIRequestWithURL(p.Ctx, baseURL, p.Action, p.AccessToken, p.Body)
			if err != nil {
				return nil, err
			}
			s.Options.ApplyHeaders(upstreamReq)

			resp, err = s.Options.Do(upstreamReq)
			if err == nil && resp == nil {
				err = errors.New("upstream returned nil response")
			}
			if err != nil {
				safeErr := logredact.SanitizeUpstreamQueries(err.Error())
				s.Options.Observe(RetryObservation{
					AccountID:          p.AccountID,
					AccountName:        p.AccountName,
					UpstreamStatusCode: 0,
					UpstreamURL:        s.Options.SafeURL(upstreamReq.URL.String()),
					Kind:               "request_error",
					Message:            safeErr,
				})
				if ShouldAntigravityFallbackToNextURL(err, 0) && urlIdx < len(availableURLs)-1 {
					logger.LegacyPrintf("service.antigravity_gateway", "%s URL fallback (connection error): %s -> %s", p.Prefix, baseURL, availableURLs[urlIdx+1])
					continue urlFallbackLoop
				}
				if attempt < AntigravityMaxRetries {
					logger.LegacyPrintf("service.antigravity_gateway", "%s status=request_failed retry=%d/%d error=%v", p.Prefix, attempt, AntigravityMaxRetries, err)
					if !SleepAntigravityBackoffWithContext(p.Ctx, attempt) {
						logger.LegacyPrintf("service.antigravity_gateway", "%s status=context_canceled_during_backoff", p.Prefix)
						return nil, p.Ctx.Err()
					}
					continue
				}
				logger.LegacyPrintf("service.antigravity_gateway", "%s status=request_failed retries_exhausted error=%v", p.Prefix, err)
				s.Options.SetError(0, safeErr, "")
				return nil, fmt.Errorf("upstream request failed after retries: %w", err)
			}

			// 统一处理错误响应
			if resp.StatusCode >= 400 {
				respBody := s.Options.ReadErrorBody(resp)
				_ = resp.Body.Close()

				if overagesInjected && ShouldMarkCreditsExhausted(resp, respBody, nil) {
					modelKey := s.Options.CreditsModel("")
					s.Options.CreditsFailure(modelKey, &http.Response{
						StatusCode: resp.StatusCode,
						Header:     resp.Header.Clone(),
						Body:       io.NopCloser(bytes.NewReader(respBody)),
					}, nil)
				}

				// ★ 统一入口：自定义错误码 + 临时不可调度
				if handled, outStatus, policyErr := s.Options.ApplyErrorPolicy(resp.StatusCode, resp.Header, respBody); handled {
					if policyErr != nil {
						return nil, policyErr
					}
					resp = &http.Response{
						StatusCode: outStatus,
						Header:     resp.Header.Clone(),
						Body:       io.NopCloser(bytes.NewReader(respBody)),
					}
					break urlFallbackLoop
				}

				// 429/503 限流处理：区分 URL 级别限流、智能重试和账户配额限流
				if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
					// 尝试智能重试处理（OAuth 账号专用）
					smartResult := s.HandleSmartRetry(p, resp, respBody, baseURL, urlIdx, availableURLs)
					switch smartResult.Action {
					case SmartRetryActionContinueURL:
						continue urlFallbackLoop
					case SmartRetryActionBreakWithResp:
						if smartResult.Err != nil {
							return nil, smartResult.Err
						}
						// 模型限流时返回切换账号信号
						if smartResult.SwitchError != nil {
							return nil, smartResult.SwitchError
						}
						resp = smartResult.Resp
						break urlFallbackLoop
					}
					// SmartRetryActionContinue: 继续默认重试逻辑

					// 账户/模型配额限流，重试 3 次（指数退避）- 默认逻辑（非 OAuth 账号或解析失败）
					if attempt < AntigravityMaxRetries {
						upstreamMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody))
						upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
						s.Options.Observe(RetryObservation{
							AccountID:          p.AccountID,
							AccountName:        p.AccountName,
							UpstreamStatusCode: resp.StatusCode,
							UpstreamRequestID:  resp.Header.Get("x-request-id"),
							UpstreamURL:        s.Options.SafeURL(upstreamReq.URL.String()),
							Kind:               "retry",
							Message:            upstreamMsg,
							Detail:             getUpstreamDetail(respBody),
						})
						logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d retry=%d/%d body=%s", p.Prefix, resp.StatusCode, attempt, AntigravityMaxRetries, s.Options.TruncateForLog(respBody, 200))
						if !SleepAntigravityBackoffWithContext(p.Ctx, attempt) {
							logger.LegacyPrintf("service.antigravity_gateway", "%s status=context_canceled_during_backoff", p.Prefix)
							return nil, p.Ctx.Err()
						}
						continue
					}

					// 重试用尽，标记账户限流
					s.Options.HandleError(resp.StatusCode, resp.Header, respBody)
					logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d rate_limited base_url=%s body=%s", p.Prefix, resp.StatusCode, baseURL, s.Options.TruncateForLog(respBody, 200))
					resp = &http.Response{
						StatusCode: resp.StatusCode,
						Header:     resp.Header.Clone(),
						Body:       io.NopCloser(bytes.NewReader(respBody)),
					}
					break urlFallbackLoop
				}

				// 其他可重试错误（500/502/504/529，不包括 429 和 503）
				if ShouldRetryAntigravityError(resp.StatusCode) {
					if attempt < AntigravityMaxRetries {
						upstreamMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody))
						upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
						s.Options.Observe(RetryObservation{
							AccountID:          p.AccountID,
							AccountName:        p.AccountName,
							UpstreamStatusCode: resp.StatusCode,
							UpstreamRequestID:  resp.Header.Get("x-request-id"),
							UpstreamURL:        s.Options.SafeURL(upstreamReq.URL.String()),
							Kind:               "retry",
							Message:            upstreamMsg,
							Detail:             getUpstreamDetail(respBody),
						})
						logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d retry=%d/%d body=%s", p.Prefix, resp.StatusCode, attempt, AntigravityMaxRetries, s.Options.TruncateForLog(respBody, 500))
						if !SleepAntigravityBackoffWithContext(p.Ctx, attempt) {
							logger.LegacyPrintf("service.antigravity_gateway", "%s status=context_canceled_during_backoff", p.Prefix)
							return nil, p.Ctx.Err()
						}
						// 追踪 INTERNAL 500：非匹配的 attempt 清除标记
						if !IsAntigravityInternalServerError(resp.StatusCode, respBody) {
							allAttemptsInternal500 = false
						}
						continue
					}
				}

				// INTERNAL 500 渐进惩罚：3 次重试全部命中特定 500 时递增计数器并惩罚
				if allAttemptsInternal500 && IsAntigravityInternalServerError(resp.StatusCode, respBody) {
					s.Options.Internal500Exhausted()
				}

				// 其他 4xx 错误或重试用尽，直接返回
				resp = &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(respBody)),
				}
				break urlFallbackLoop
			}

			// 成功响应（< 400）
			break urlFallbackLoop
		}
	}

	if resp != nil && resp.StatusCode < 400 && usedBaseURL != "" {
		DefaultURLAvailability.MarkSuccess(usedBaseURL)
	}

	// 成功响应时清零 INTERNAL 500 连续失败计数器（覆盖所有成功路径，含 smart retry）
	if resp != nil && resp.StatusCode < 400 {
		s.Options.ResetInternal500()
	}

	return &AntigravityRetryLoopResult{Resp: resp}, nil
}

// ShouldRetryAntigravityError 判断是否应该重试
func ShouldRetryAntigravityError(statusCode int) bool {
	switch statusCode {
	case 429, 500, 502, 503, 504, 529:
		return true
	default:
		return false
	}
}

// IsURLLevelRateLimit 判断是否为 URL 级别的限流（应切换 URL 重试）
// "Resource has been exhausted" 是 URL/节点级别限流，切换 URL 可能成功
// "exhausted your capacity on this model" 是账户/模型配额限流，切换 URL 无效
func IsURLLevelRateLimit(body []byte) bool {
	// 快速检查：包含 "Resource has been exhausted" 且不包含 "capacity on this model"
	bodyStr := string(body)
	return strings.Contains(bodyStr, "Resource has been exhausted") &&
		!strings.Contains(bodyStr, "capacity on this model")
}

// IsAntigravityConnectionError 判断是否为连接错误（网络超时、DNS 失败、连接拒绝）
func IsAntigravityConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// 检查超时错误
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// 检查连接错误（DNS 失败、连接拒绝）
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// ShouldAntigravityFallbackToNextURL 判断是否应切换到下一个 URL
// 仅连接错误和 HTTP 429 触发 URL 降级
func ShouldAntigravityFallbackToNextURL(err error, statusCode int) bool {
	if IsAntigravityConnectionError(err) {
		return true
	}
	return statusCode == http.StatusTooManyRequests
}

// SleepAntigravityBackoffWithContext 带 context 取消检查的退避等待
// 返回 true 表示正常完成等待，false 表示 context 已取消
func SleepAntigravityBackoffWithContext(ctx context.Context, attempt int) bool {
	delay := AntigravityRetryBaseDelay * time.Duration(1<<uint(attempt-1))
	if delay > AntigravityRetryMaxDelay {
		delay = AntigravityRetryMaxDelay
	}

	// +/- 20% jitter
	r := mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
	jitter := time.Duration(float64(delay) * 0.2 * (r.Float64()*2 - 1))
	sleepFor := delay + jitter
	if sleepFor < 0 {
		sleepFor = 0
	}

	timer := time.NewTimer(sleepFor)
	select {
	case <-ctx.Done():
		timer.Stop()
		return false
	case <-timer.C:
		return true
	}
}

// AntigravitySmartRetryInfo 智能重试所需的信息
type AntigravitySmartRetryInfo struct {
	RetryDelay               time.Duration // 重试延迟时间
	ModelName                string        // 限流的模型名称（如 "claude-sonnet-4-5"）
	IsModelCapacityExhausted bool          // 是否为模型容量不足（MODEL_CAPACITY_EXHAUSTED）
}

// ParseAntigravitySmartRetryInfo 解析 Google RPC RetryInfo 和 ErrorInfo 信息
// 返回解析结果，如果解析失败或不满足条件返回 nil
//
// 支持两种情况：
// 1. 429 RESOURCE_EXHAUSTED + RATE_LIMIT_EXCEEDED：
//   - error.status == "RESOURCE_EXHAUSTED"
//   - error.details[].reason == "RATE_LIMIT_EXCEEDED"
//
// 2. 503 UNAVAILABLE + MODEL_CAPACITY_EXHAUSTED：
//   - error.status == "UNAVAILABLE"
//   - error.details[].reason == "MODEL_CAPACITY_EXHAUSTED"
//
// 必须满足以下条件才会返回有效值：
// - error.details[] 中存在 @type == "type.googleapis.com/google.rpc.RetryInfo" 的元素
// - 该元素包含 retryDelay 字段，格式为 "数字s"（如 "0.201506475s"）
func ParseAntigravitySmartRetryInfo(body []byte) *AntigravitySmartRetryInfo {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}

	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		return nil
	}

	// 检查 status 是否符合条件
	// 情况1: 429 RESOURCE_EXHAUSTED (需要进一步检查 reason == RATE_LIMIT_EXCEEDED)
	// 情况2: 503 UNAVAILABLE (需要进一步检查 reason == MODEL_CAPACITY_EXHAUSTED)
	status, _ := errObj["status"].(string)
	isResourceExhausted := status == GoogleRPCStatusResourceExhausted
	isUnavailable := status == GoogleRPCStatusUnavailable

	if !isResourceExhausted && !isUnavailable {
		return nil
	}

	details, ok := errObj["details"].([]any)
	if !ok {
		return nil
	}

	var retryDelay time.Duration
	var modelName string
	var hasRateLimitExceeded bool      // 429 需要此 reason
	var hasModelCapacityExhausted bool // 503 需要此 reason

	for _, d := range details {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}

		atType, _ := dm["@type"].(string)

		// 从 ErrorInfo 提取模型名称和 reason
		if atType == GoogleRPCTypeErrorInfo {
			if meta, ok := dm["metadata"].(map[string]any); ok {
				if model, ok := meta["model"].(string); ok {
					modelName = NormalizeAntigravityModelName(model)
				}
			}
			// 检查 reason
			if reason, ok := dm["reason"].(string); ok {
				if reason == GoogleRPCReasonModelCapacityExhausted {
					hasModelCapacityExhausted = true
				}
				if reason == GoogleRPCReasonRateLimitExceeded {
					hasRateLimitExceeded = true
				}
			}
			continue
		}

		// 从 RetryInfo 提取重试延迟
		if atType == GoogleRPCTypeRetryInfo {
			delay, ok := dm["retryDelay"].(string)
			if !ok || delay == "" {
				continue
			}
			// 使用 time.ParseDuration 解析，支持所有 Go duration 格式
			// 例如: "0.5s", "10s", "4m50s", "1h30m", "200ms" 等
			dur, err := time.ParseDuration(delay)
			if err != nil {
				logger.LegacyPrintf("service.antigravity_gateway", "[Antigravity] failed to parse retryDelay: %s error=%v", delay, err)
				continue
			}
			retryDelay = dur
		}
	}

	// 验证条件
	// 情况1: RESOURCE_EXHAUSTED 需要有 RATE_LIMIT_EXCEEDED reason
	// 情况2: UNAVAILABLE 需要有 MODEL_CAPACITY_EXHAUSTED reason
	if isResourceExhausted && !hasRateLimitExceeded {
		return nil
	}
	if isUnavailable && !hasModelCapacityExhausted {
		return nil
	}

	// 必须有模型名才返回有效结果
	if modelName == "" {
		return nil
	}

	// 如果上游未提供 retryDelay，使用默认限流时间
	if retryDelay <= 0 {
		retryDelay = AntigravityDefaultRateLimitDuration
	}

	return &AntigravitySmartRetryInfo{
		RetryDelay:               retryDelay,
		ModelName:                modelName,
		IsModelCapacityExhausted: hasModelCapacityExhausted,
	}
}

// ShouldTriggerAntigravitySmartRetry 判断是否应该触发智能重试
// 返回：
//   - shouldRetry: 是否应该智能重试（retryDelay < AntigravityRateLimitThreshold，或 MODEL_CAPACITY_EXHAUSTED）
//   - shouldRateLimitModel: 是否应该限流模型并切换账号（仅 RATE_LIMIT_EXCEEDED 且 retryDelay >= 阈值）
//   - waitDuration: 等待时间
//   - modelName: 限流的模型名称
//   - isModelCapacityExhausted: 是否为模型容量不足（MODEL_CAPACITY_EXHAUSTED）
func ShouldTriggerAntigravitySmartRetry(native bool, respBody []byte) (shouldRetry bool, shouldRateLimitModel bool, waitDuration time.Duration, modelName string, isModelCapacityExhausted bool) {
	if !native {
		return false, false, 0, "", false
	}

	info := ParseAntigravitySmartRetryInfo(respBody)
	if info == nil {
		return false, false, 0, "", false
	}

	// MODEL_CAPACITY_EXHAUSTED（模型容量不足）：所有账号共享同一模型容量池
	// 切换账号无意义，使用固定 1s 间隔重试
	if info.IsModelCapacityExhausted {
		return true, false, AntigravityModelCapacityRetryWait, info.ModelName, true
	}

	// RATE_LIMIT_EXCEEDED（账号级限流）：
	// retryDelay >= 阈值：直接限流模型，不重试
	// 注意：如果上游未提供 retryDelay，ParseAntigravitySmartRetryInfo 已设置为默认 30s
	if info.RetryDelay >= AntigravityRateLimitThreshold {
		return false, true, info.RetryDelay, info.ModelName, false
	}

	// retryDelay < 阈值：智能重试
	waitDuration = info.RetryDelay
	if waitDuration < AntigravitySmartRetryMinWait {
		waitDuration = AntigravitySmartRetryMinWait
	}

	return true, false, waitDuration, info.ModelName, false
}
