package testkit

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// RequestCredentials 只组合测试提供的原生端口，不复制刷新、互斥或故障分类实现。
func RequestCredentials(store provider.ExecutionAccountStore, source *account.OpenAIExecutionCredentials, tokens *account.GrokTokenSource, blocks *account.RuntimeBlockState) *provider.RequestCredentials {
	if blocks == nil {
		blocks = account.NewRuntimeBlockState(time.Now)
	}
	if source == nil {
		source = &account.OpenAIExecutionCredentials{}
	}
	recovery := &account.GrokCredentialRecovery{Runtime: blocks, Warn: slog.Warn}
	if store != nil {
		source.Parent = func(ctx context.Context, id int64) (*account.Record, error) {
			value, err := store.GetByID(ctx, id)
			return provider.ExecutionRecord(value), err
		}
		recovery.Read = source.Parent
		recovery.State, _ = store.(account.GrokCredentialStateWriter)
	}
	if tokens != nil {
		source.Grok = tokens.GetAccessToken
		recovery.Invalidate = tokens.InvalidateToken
	}
	return &provider.RequestCredentials{Source: source, HasGrokTokenSource: tokens != nil, Recovery: recovery, Runtime: blocks}
}
