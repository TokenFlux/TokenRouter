package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// 旧网关仅投影账号快照，状态预算及条件写规则由 account 唯一执行。
func persistGrokRateLimit(ctx context.Context, writer gatewayprovider.ExecutionAccountStore, value *gatewayprovider.ExecutionAccount, reset time.Time) {
	account.PersistGrokRateLimit(ctx, writer, gatewayprovider.ExecutionRecord(value), reset, slog.Warn)
}

func clearGrokRateLimitAfterRecovery(ctx context.Context, writer gatewayprovider.ExecutionAccountStore, value *gatewayprovider.ExecutionAccount) {
	account.ClearGrokRateLimitAfterRecovery(ctx, writer, gatewayprovider.ExecutionRecord(value), slog.Warn)
}
