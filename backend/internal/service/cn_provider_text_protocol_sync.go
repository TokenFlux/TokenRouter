package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
)

// SyncCNProviderTextProtocol 同步国产供应商的显式协议配置，不执行能力探测。
func (s *AccountTestService) SyncCNProviderTextProtocol(ctx context.Context, accountID int64) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil || account.Type != AccountTypeAPIKey || !account.IsCNProvider() {
		return
	}
	mode := openai_compat.TextRouteModeForceChatCompletions
	if account.UsesNativeCNResponses() {
		mode = openai_compat.TextRouteModeForceResponses
	}
	_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{openai_compat.ExtraKeyTextRouteMode: string(mode)})
}
