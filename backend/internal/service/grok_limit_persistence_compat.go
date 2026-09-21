package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 旧网关仅投影账号快照，状态预算及条件写规则由 account 唯一执行。
func persistGrokRateLimit(ctx context.Context, writer AccountRepository, value *Account, reset time.Time) {
	account.PersistGrokRateLimit(ctx, writer, AccountRecordView(value), reset, slog.Warn)
}

func clearGrokRateLimitAfterRecovery(ctx context.Context, writer AccountRepository, value *Account) {
	account.ClearGrokRateLimitAfterRecovery(ctx, writer, AccountRecordView(value), slog.Warn)
}
