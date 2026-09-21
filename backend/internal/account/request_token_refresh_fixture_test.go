//go:build unit

package account

import (
	"context"
	"log/slog"
	"reflect"
	"time"
)

// 夹具只组合生产实现，缓存等待、资格、刷新与 CAS 算法不在测试中复制。
func newOpenAIRefreshSourceFixture(repo *openAIAccountRepoStub, cache *openAITokenCacheStub, authorization *openAIOAuthServiceStub) *OpenAITokenSource {
	source := &OpenAITokenSource{Repository: repo, Cache: cache, Metrics: &OpenAITokenMetricsStore{}, Policy: OpenAIProviderRefreshPolicy(), Debug: slog.Debug, Warn: slog.Warn}
	if authorization != nil {
		api := NewOAuthRefreshAPI(repo, cache, RefreshOptions{Platform: AccountRefreshPlatformPolicy()})
		executor := &OpenAITokenRefresher{Authorization: authorization}
		source.Refresh = func(ctx context.Context, value *Record, window time.Duration) (*OAuthRefreshResult, error) {
			return api.RefreshIfNeeded(ctx, value, executor, window)
		}
	}
	return source
}

func newClaudeRefreshSourceFixture(repo *claudeAccountRepoStub, cache *claudeTokenCacheStub, authorization *claudeOAuthServiceStub) *ClaudeTokenSource {
	source := &ClaudeTokenSource{Options: ClaudeTokenOptions{Repository: repo, Cache: cache, Policy: ClaudeProviderRefreshPolicy(), Debug: slog.Debug, Warn: slog.Warn}}
	if authorization != nil {
		api := NewOAuthRefreshAPI(repo, cache, RefreshOptions{Platform: AccountRefreshPlatformPolicy()})
		executor := claudeExchangeFixture{authorization}
		source.Options.Refresh = func(ctx context.Context, value *Record, window time.Duration) (*OAuthRefreshResult, error) {
			return api.RefreshIfNeeded(ctx, value, executor, window)
		}
	}
	return source
}

// Claude 执行器只注入测试交换结果，资格与合并调用所属模块的生产函数。
type claudeExchangeFixture struct{ exchange *claudeOAuthServiceStub }

func (claudeExchangeFixture) CanRefresh(value *Record) bool { return CanRefreshClaude(value) }
func (claudeExchangeFixture) NeedsRefresh(value *Record, window time.Duration) bool {
	return NeedsRefreshClaude(value, window)
}
func (claudeExchangeFixture) CacheKey(value *Record) string { return ClaudeTokenCacheKey(value) }
func (e claudeExchangeFixture) Refresh(ctx context.Context, value *Record) (map[string]any, error) {
	return RefreshClaudeCredentials(ctx, value, e.exchange.RefreshAccountToken)
}

// 存储替身保留原写失败注入，并实现真实协调器要求的条件写契约。
func (r *openAIAccountRepoStub) UpdateOAuthCredentialsIfUnchanged(ctx context.Context, version CredentialVersion, credentials map[string]any) (bool, error) {
	if r.account == nil || !reflect.DeepEqual(FailureVersion(r.account).CredentialVersion, version) {
		return false, nil
	}
	next := CloneRecord(r.account)
	next.Credentials = CloneValues(credentials)
	err := r.Update(ctx, next)
	return err == nil, err
}
func (r *claudeAccountRepoStub) UpdateOAuthCredentialsIfUnchanged(ctx context.Context, version CredentialVersion, credentials map[string]any) (bool, error) {
	if r.account == nil || !reflect.DeepEqual(FailureVersion(r.account).CredentialVersion, version) {
		return false, nil
	}
	next := CloneRecord(r.account)
	next.Credentials = CloneValues(credentials)
	err := r.Update(ctx, next)
	return err == nil, err
}
