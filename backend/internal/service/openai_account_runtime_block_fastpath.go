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

// 旧状态类型引用网关唯一的请求级预算。

func isOpenAIOAuthAccount(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && account.View().IsOpenAIOAuthLike()
}

func isGrokOAuthAccount(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && account.Record.Platform == capability.PlatformGrok && account.Record.Type == capability.AccountTypeOAuth
}

func isOpenAIAccount(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && (account.Record.Platform == capability.PlatformOpenAI || account.Record.Platform == capability.PlatformGrok)
}

// ShouldRetryOpenAIOAuth429 向账号健康观测提供同账号重试判断，延迟持久化账号
// cooldown until the gateway's same-account retry window is exhausted.
func (s *OpenAIGatewayService) ShouldRetryOpenAIOAuth429(value *gatewayprovider.ExecutionAccount, headers http.Header, body []byte) bool {
	if s == nil {
		return false
	}
	return accountprovider.CanRetryOpenAI429(s.runtimeBlockState(), gatewayprovider.ExecutionRecord(value), headers, body)
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

func (s *OpenAIGatewayService) ShouldStopOpenAIOAuth429Failover(account *gatewayprovider.ExecutionAccount, statusCode int, failedSwitches int, state *failover.OAuth429State) bool {
	return failover.StopOAuth429(failover.OAuth429Account{OpenAI: isOpenAIOAuthAccount(account), Grok: isGrokOAuthAccount(account)}, statusCode, failedSwitches, state)
}
