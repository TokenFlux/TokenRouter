//go:build unit

// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// 旧行为断言只委托已迁账号能力。
func (s *TokenRefreshService) ensureOpenAIPrivacy(ctx context.Context, account *Account) {
	view := privacyRecord(account)
	s.privacyService().RefreshOpenAIPrivacy(ctx, view)
	if account != nil && view != nil {
		account.Extra = view.Extra
	}
}

type oauthRefreshStateUnavailableError = acctcore.RefreshStateUnavailableError

func (p *tokenRefreshProviderState) isTripped() bool { return p.core().IsTripped() }

type accountPermanentRefreshError = acctcore.AccountPermanentRefreshError
