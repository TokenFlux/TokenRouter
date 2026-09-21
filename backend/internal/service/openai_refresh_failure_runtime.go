package service

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// PrepareRefreshFailure 在存储操作前记录显式清理代次；发布时只记录该凭据身份。
func (s *OpenAIGatewayService) PrepareRefreshFailure(id int64) func(account.RefreshFailureNotice) {
	if s == nil {
		return func(account.RefreshFailureNotice) {}
	}
	mu := s.openAIAccountRuntimeBlockLock(id)
	mu.Lock()
	generation, _ := s.refreshFailureClearGeneration.Load(id)
	mu.Unlock()
	return func(notice account.RefreshFailureNotice) {
		if notice.AccountID != id || notice.Identity == "" || !isOpenAIAccount(&Account{Platform: notice.Platform, Type: notice.Type}) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		current, _ := s.refreshFailureClearGeneration.Load(id)
		if current != generation {
			return
		}
		now := time.Now()
		until := notice.Until
		if !until.After(now) {
			until = now.Add(openAIStopSchedulingBridgeCooldown)
		}
		s.refreshFailureBlocks.Block(id, notice.Identity, until, now)
	}
}
