package app

import (
	"context"
	"log"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// 配置和日志在装配时固定，是否冷却由 account 判断。
func provideRetryCooldown(store *accountpostgres.AccountStore) *account.RetryCooldown {
	var source account.RetryCooldownStore
	if store != nil {
		source = store
	}
	return account.NewRetryCooldown(source, account.RetryCooldownOptions{
		Logf: log.Printf,
		LookupError: func(id int64, err error) {
			logging.LegacyPrintf("service.gateway", "查询重试耗尽账号失败: account=%d error=%v", id, err)
		},
	})
}
func messageRetryCooldown(command *account.RetryCooldown) func(context.Context, int64, *forward.UpstreamFailoverError) {
	return func(ctx context.Context, id int64, failure *forward.UpstreamFailoverError) {
		input := account.RetryCooldownInput{AccountID: id}
		if failure != nil {
			input.Status = failure.StatusCode
			input.Retryable = failure.RetryableOnSameAccount
			input.RequestScopedTransient = failure.RequestScopedTransient
		}
		command.Apply(ctx, input)
	}
}

// 隔离端口只投影当前 Key，保持原一小时 owner TTL。
func messageSessionIsolation(cache session.GatewayCache) func(context.Context, *apikey.APIKey, int64, string, string) error {
	return func(ctx context.Context, key *apikey.APIKey, userID int64, source, hash string) error {
		if key == nil {
			return nil
		}
		groupID := int64(0)
		if key.GroupID != nil {
			groupID = *key.GroupID
		}
		return session.EnsureIsolation(ctx, cache, session.IsolationInput{UserID: userID, GroupID: groupID, Source: source, Hash: hash, TTL: time.Hour, Enabled: key.Group != nil && key.Group.SessionIsolationEnabled})
	}
}

// 摘要端口沿用同一内存存储，HTTP context 不参与内存键或过期时间计算。
func messageDigestFind(store *session.DigestSessionStore) func(context.Context, int64, string, string) (string, int64, string, bool) {
	return func(_ context.Context, id int64, prefix, chain string) (string, int64, string, bool) {
		return store.Find(id, prefix, chain)
	}
}
func messageDigestSave(store *session.DigestSessionStore) func(context.Context, int64, string, string, string, int64, string) error {
	return func(_ context.Context, id int64, prefix, chain, uuid string, accountID int64, old string) error {
		store.Save(id, prefix, chain, uuid, accountID, old)
		return nil
	}
}
