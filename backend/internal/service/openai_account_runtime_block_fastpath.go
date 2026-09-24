package service

import (
	"net/http"

	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

const (
	openAIAccountStateUpdateTimeout = 5 * time.Second
)

// 旧状态类型引用网关唯一的请求级预算。

const (
	openAIOAuth429Transient  = accountcore.OpenAI429Transient
	openAIOAuth429Quota5h    = accountcore.OpenAI429Quota5h
	openAIOAuth429Quota7d    = accountcore.OpenAI429Quota7d
	openAIOAuth429QuotaReset = accountcore.OpenAI429QuotaReset
)

func isOpenAIOAuthAccount(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && account.View().IsOpenAIOAuthLike()
}

func isGrokOAuthAccount(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && account.Record.Platform == capability.PlatformGrok && account.Record.Type == capability.AccountTypeOAuth
}

func isOpenAIAccount(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && (account.Record.Platform == capability.PlatformOpenAI || account.Record.Platform == capability.PlatformGrok)
}

func (s *OpenAIGatewayService) shouldRetryOpenAIOAuth429OnSameAccount(account *gatewayprovider.ExecutionAccount, statusCode int, shouldDisable bool) bool {
	return s.shouldRetryOpenAIOAuth429OnSameAccountWithResponse(account, statusCode, shouldDisable, nil, nil)
}

func (s *OpenAIGatewayService) shouldRetryOpenAIOAuth429OnSameAccountWithResponse(account *gatewayprovider.ExecutionAccount, statusCode int, shouldDisable bool, headers http.Header, responseBody []byte) bool {
	if shouldDisable || statusCode != http.StatusTooManyRequests || !isOpenAIOAuthAccount(account) || account.View().IsShadow() {
		return false
	}
	disposition, _ := classifyOpenAIOAuth429(headers, responseBody)
	if disposition != openAIOAuth429Transient {
		return false
	}
	// markOpenAIOAuth429RateLimited parks the account once the window expires.
	// Do not accidentally create a fresh window after that transition.
	if s.isOpenAIAccountRuntimeBlocked(account) {
		return false
	}
	return s.openAIOAuth429RetryWindowActive(account)
}

// ShouldRetryOpenAIOAuth429 向账号健康观测提供同账号重试判断，延迟持久化账号
// cooldown until the gateway's same-account retry window is exhausted.
func (s *OpenAIGatewayService) ShouldRetryOpenAIOAuth429(value *gatewayprovider.ExecutionAccount, headers http.Header, body []byte) bool {
	if s == nil {
		return false
	}
	return accountprovider.CanRetryOpenAI429(s.runtimeBlockState(), gatewayprovider.ExecutionRecord(value), headers, body)
}

func (s *OpenAIGatewayService) openAIOAuth429RetryWindowActive(account *gatewayprovider.ExecutionAccount) bool {
	if s == nil || !isOpenAIOAuthAccount(account) || account.View().IsShadow() {
		return false
	}
	return s.runtimeBlockState().RetryWindowActive(account.Record.ID)
}

func (s *OpenAIGatewayService) BlockAccountScheduling(account *gatewayprovider.ExecutionAccount, until time.Time, reason string) {
	if s == nil || !isOpenAIAccount(account) {
		return
	}
	s.runtimeBlockState().Block(account.Record.ID, until, reason)
}

func (s *OpenAIGatewayService) ClearAccountSchedulingBlock(id int64) {
	if s != nil {
		s.runtimeBlockState().ClearAccountSchedulingBlock(id)
	}
}

// ManagedRecoveryFence 复用原代次，不为手动恢复安装另一套运行状态。
func (s *OpenAIGatewayService) ManagedRecoveryFence(id int64) uint64 {
	if s == nil {
		return 0
	}
	return s.runtimeBlockState().ManagedRecoveryFence(id)
}

func (s *OpenAIGatewayService) ClearAccountSchedulingBlockIfFence(id int64, expected uint64) bool {
	return s != nil && s.runtimeBlockState().ClearAccountSchedulingBlockIfFence(id, expected)
}

func (s *OpenAIGatewayService) isOpenAIAccountRuntimeBlocked(account *gatewayprovider.ExecutionAccount) bool {
	if s == nil || !isOpenAIAccount(account) {
		return false
	}
	return s.runtimeBlockState().Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(gatewayprovider.ExecutionRecord(account)) })
}

func (s *OpenAIGatewayService) getOpenAIAccountModelTransientState() *accountcore.ModelTransientState {
	if s == nil {
		return nil
	}
	s.openaiModelTransientOnce.Do(func() {
		if s.openaiModelTransient == nil {
			s.openaiModelTransient = accountcore.NewModelTransientState(0)
		}
	})
	return s.openaiModelTransient
}

func openAIAccountModelTransientModel(canonicalModel string) string {
	return accountcore.NormalizeTransientModel(canonicalModel)
}

func (s *OpenAIGatewayService) recordOpenAIAccountModelTransientFailure(account *gatewayprovider.ExecutionAccount, canonicalModel string, now time.Time) accountcore.ModelTransientDecision {
	if s == nil || account == nil {
		return accountcore.ModelTransientDecision{}
	}
	state := s.getOpenAIAccountModelTransientState()
	if state == nil {
		return accountcore.ModelTransientDecision{}
	}
	return state.RecordFailure(account.Record.ID, openAIAccountModelTransientModel(canonicalModel), now)
}

func (s *OpenAIGatewayService) isOpenAIAccountModelRuntimeBlocked(account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	if s == nil || account == nil {
		return false
	}
	state := s.getOpenAIAccountModelTransientState()
	if state == nil {
		return false
	}
	canonicalModel := gatewayprovider.ExecutionModelPolicy(account).CanonicalSchedulingModel(requestedModel)
	return state.IsBlocked(account.Record.ID, openAIAccountModelTransientModel(canonicalModel), time.Now())
}

func (s *OpenAIGatewayService) isOpenAIAccountRequestRuntimeBlocked(account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	return s != nil && (s.isOpenAIAccountRuntimeBlocked(account) || s.isOpenAIAccountModelRuntimeBlocked(account, requestedModel))
}

func (s *OpenAIGatewayService) ShouldStopOpenAIOAuth429Failover(account *gatewayprovider.ExecutionAccount, statusCode int, failedSwitches int, state *failover.OAuth429State) bool {
	return failover.StopOAuth429(failover.OAuth429Account{OpenAI: isOpenAIOAuthAccount(account), Grok: isGrokOAuthAccount(account)}, statusCode, failedSwitches, state)
}

// 旧重试策略只读取统一窗口观测，仍由调用方决定是否继续。
func classifyOpenAIOAuth429(headers http.Header, body []byte) (accountcore.OpenAI429Disposition, *time.Time) {
	return accountprovider.ClassifyOpenAI429(headers, body)
}
