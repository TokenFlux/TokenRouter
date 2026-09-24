package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// provideGrokCredentialRecovery 共享应用的账号存储、运行阻断和 token 源，不创建新缓存。
func provideGrokCredentialRecovery(store *accountpostgres.AccountStore, tokens *account.GrokTokenSource, blocks *account.RuntimeBlockState) *account.GrokCredentialRecovery {
	return &account.GrokCredentialRecovery{
		Read:       store.GetByID,
		State:      grokCredentialStateWriter{store: store},
		Invalidate: tokens.InvalidateToken,
		Runtime:    blocks,
		Warn:       slog.Warn,
	}
}
func provideRequestCredentials(source *account.OpenAIExecutionCredentials, tokens *account.GrokTokenSource, recovery *account.GrokCredentialRecovery, blocks *account.RuntimeBlockState) *provider.RequestCredentials {
	return &provider.RequestCredentials{Source: source, HasGrokTokenSource: tokens != nil, Recovery: recovery, Runtime: blocks}
}
func provideRequestCredentialExecutor(runtime *provider.RequestCredentials) *gatewayhttp.RequestCredentialExecutor {
	return &gatewayhttp.RequestCredentialExecutor{Runtime: runtime}
}

// grokCredentialStateWriter 只补齐存储比较所需的既有原因值，不改变条件更新。
type grokCredentialStateWriter struct{ store *accountpostgres.AccountStore }

func (s grokCredentialStateWriter) SetGrokCredentialErrorIfMatch(ctx context.Context, id int64, snapshot account.CredentialMutationSnapshot, reason string) (bool, error) {
	return s.store.SetGrokCredentialErrorIfMatch(ctx, id, snapshot, reason, string(forward.GrokCredentialReasonProxyInvalid))
}
func (s grokCredentialStateWriter) SetGrokCredentialTempUnschedulableIfMatch(ctx context.Context, id int64, snapshot account.CredentialMutationSnapshot, until time.Time, reason string) (bool, error) {
	return s.store.SetGrokCredentialTempUnschedulableIfMatch(ctx, id, snapshot, until, reason)
}
