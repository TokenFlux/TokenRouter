package selection

import (
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func isOpenAIAccount(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && (account.Record.Platform == capability.PlatformOpenAI || account.Record.Platform == capability.PlatformGrok)
}

func (s *Compatible) isOpenAIAccountRuntimeBlocked(account *gatewayprovider.ExecutionAccount) bool {
	if s == nil || !isOpenAIAccount(account) {
		return false
	}
	return s.runtimeBlockState().Blocked(account.Record.ID, func() string { return accountcore.RefreshCredentialIdentity(gatewayprovider.ExecutionRecord(account)) })
}

func openAIAccountModelTransientModel(canonicalModel string) string {
	return accountcore.NormalizeTransientModel(canonicalModel)
}

func (s *Compatible) clearOpenAIAccountModelTransientState(accountID int64, model string) {
	state := s.getOpenAIAccountModelTransientState()
	if state == nil {
		return
	}
	state.RecordSuccess(accountID, model)
}

func (s *Compatible) isOpenAIAccountModelRuntimeBlocked(account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
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

func (s *Compatible) isOpenAIAccountRequestRuntimeBlocked(account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	return s != nil && (s.isOpenAIAccountRuntimeBlocked(account) || s.isOpenAIAccountModelRuntimeBlocked(account, requestedModel))
}
