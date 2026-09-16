package service

import (
	"context"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func grokSpendingLimitResetAt(account *Account, now time.Time) time.Time {
	return accountcore.GrokSpendingLimitResetAt(AccountRecordView(account), now)
}

// clearGrokNeedsReauthExtra 在刷新或重新认证成功后清除软性重新认证标记。
// 此操作尽力执行，失败时也不会中断请求链路。
func clearGrokNeedsReauthExtra(ctx context.Context, repo AccountRepository, accountID int64) {
	if repo == nil || accountID <= 0 {
		return
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	_ = repo.UpdateExtra(stateCtx, accountID, map[string]any{
		"grok_needs_reauth":        false,
		"grok_needs_reauth_reason": "",
		"grok_needs_reauth_at":     "",
	})
}

func accountGrokNeedsReauth(account *Account) bool {
	return accountcore.GrokNeedsReauth(AccountRecordView(account))
}
