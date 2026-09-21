package account

import (
	"context"
	"errors"
	"log"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAnthropicNegativeUsageCacheDoesNotCrossCredentialIdentity(t *testing.T) {
	oldErr := errors.New("old identity failed")
	cache := NewOAuthUsageCache()
	svc := NewOAuthUsageService(nil, cache, nil, OAuthUsageOptions{})
	cache.StoreAPI(int64(886), &OAuthAPIUsageCache{Identity: "old identity", Err: oldErr, Timestamp: time.Now()})
	a := &Record{ID: 886, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"access_token": "second"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.GetUsageForAccount(ctx, a, false)
	require.ErrorIs(t, err, context.Canceled)
}

// 已发起查询的旧身份不得在返回后把主动用量写到管理员的新身份。
type activePassiveIdentityRepo struct {
	sessionWindowSyncRepo
	current Record
}

func (r *activePassiveIdentityRepo) GetByID(context.Context, int64) (*Record, error) {
	v := r.current
	return &v, nil
}

func TestAnthropicActiveUsageDoesNotWriteNewCredentialIdentity(t *testing.T) {
	a := Record{ID: 887, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"access_token": "old"}}
	repo := &activePassiveIdentityRepo{current: a}
	repo.current.Credentials = map[string]any{"access_token": "administrator"}
	cache := NewOAuthUsageCache()
	stats := NewLocalUsageStatistics(usageBatchStatisticsFixture{}, cache, LocalUsageStatisticsOptions{Now: time.Now, Log: log.Printf})
	svc := NewOAuthUsageService(repo, cache, stats, OAuthUsageOptions{})
	response := &ClaudeUsageResponse{}
	response.FiveHour.Utilization = 31
	response.FiveHour.ResetsAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	cache.StoreAPI(a.ID, &OAuthAPIUsageCache{Identity: UsageCacheIdentity(&a), Response: response, Timestamp: time.Now()})
	_, err := svc.GetUsageForAccount(context.Background(), &a, false)
	require.NoError(t, err)
	require.Empty(t, repo.extraUpdates)
	require.Empty(t, repo.sessionWindowEnds)
}

// 与旧复现保持相同查询断言；替身补齐生产条件端口，真实 SQL 另行验证。
func (r *activePassiveIdentityRepo) UpdateUsageExtraIfUnchanged(ctx context.Context, v UsageObservationVersion, updates map[string]any) (bool, error) {
	if !MatchesCredentialVersion(&r.current, v.CredentialVersion) {
		return false, nil
	}
	return true, r.UpdateExtra(ctx, v.ID, updates)
}

func (r *activePassiveIdentityRepo) UpdateUsageSessionWindowEndIfUnchanged(ctx context.Context, v UsageObservationVersion, _ *time.Time, end time.Time) (bool, error) {
	if !MatchesCredentialVersion(&r.current, v.CredentialVersion) {
		return false, nil
	}
	return true, r.UpdateSessionWindowEnd(ctx, v.ID, end)
}
