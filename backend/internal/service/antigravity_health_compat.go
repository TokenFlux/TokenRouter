// 旧健康入口仅投影账号与发布端口，规则和写入顺序由 account 唯一实现。
package service

import (
	"context"
	"log/slog"
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

func (s *AntigravityGatewayService) antigravityHealth() *acctcore.AntigravityHealth {
	core := &acctcore.AntigravityHealth{Store: s.accountRepo, Counter: s.internal500Cache, ModelKeys: antigravityModelRateLimitKeys, Error: slog.Error, Warn: slog.Warn, Info: slog.Info, Logf: func(f string, args ...any) { logger.LegacyPrintf("service.antigravity_gateway", f, args...) }}
	if s.schedulerSnapshot != nil {
		core.Publish = func(ctx context.Context, value *acctcore.Record) error {
			return s.schedulerSnapshot.UpdateAccountInCache(ctx, AccountFromRecord(value))
		}
	}
	return core
}

func (s *AntigravityGatewayService) handleInternal500RetryExhausted(
	ctx context.Context, prefix string, account *Account,
) {
	core := s.antigravityHealth()
	value := AccountRecordView(account)
	core.HandleInternal500RetryExhausted(ctx, prefix, value)
	if account != nil && value != nil {
		account.Extra = value.Extra
	}
}
func (s *AntigravityGatewayService) resetInternal500Counter(
	ctx context.Context, prefix string, accountID int64,
) {
	core := s.antigravityHealth()
	core.ResetInternal500Counter(ctx, prefix, accountID)
}

const creditsExhaustedKey = acctcore.CreditsExhaustedKey

func (s *AntigravityGatewayService) setCreditsExhausted(ctx context.Context, account *Account) {
	core := s.antigravityHealth()
	value := AccountRecordView(account)
	core.SetCreditsExhausted(ctx, value)
	if account != nil && value != nil {
		account.Extra = value.Extra
	}
}
func (s *AntigravityGatewayService) clearCreditsExhausted(ctx context.Context, account *Account) {
	core := s.antigravityHealth()
	value := AccountRecordView(account)
	core.ClearCreditsExhausted(ctx, value)
	if account != nil && value != nil {
		account.Extra = value.Extra
	}
}

func (s *AntigravityGatewayService) setAntigravityModelRateLimits(ctx context.Context, repo AccountRepository, account *Account, modelName, prefix string, statusCode int, resetAt time.Time, afterSmartRetry bool) bool {
	core := s.antigravityHealth()
	value := AccountRecordView(account)
	result := core.SetAntigravityModelRateLimits(ctx, repo, value, modelName, prefix, statusCode, resetAt, afterSmartRetry)
	if account != nil && value != nil {
		account.Extra = value.Extra
	}
	return result
}
