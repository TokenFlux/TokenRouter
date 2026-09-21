//go:build unit

package apikey_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/stretchr/testify/require"
)

// api_keys 的 quota_used / usage_5h|1d|7d 由计费热路径原子递增。
// 编辑 Key（改名、换分组……）若整行回写，并发累计的用量就会被旧快照覆盖。
// 这些用例锁死"只声明请求真正要改的列"。

type updateFieldsAPIKeyRepoStub struct {
	quotaBaseAPIKeyRepoStub
	key          *apikey.APIKey
	updateFields []apikey.APIKeyUpdateFields
}

// IncrementQuotaUsed 模拟计费热路径上的原子递增：只动 quota_used。
func (s *updateFieldsAPIKeyRepoStub) IncrementQuotaUsed(_ context.Context, _ int64, amount float64) (float64, error) {
	s.key.QuotaUsed += amount
	return s.key.QuotaUsed, nil
}

func (s *updateFieldsAPIKeyRepoStub) GetByID(context.Context, int64) (*apikey.APIKey, error) {
	clone := *s.key
	return &clone, nil
}

func (s *updateFieldsAPIKeyRepoStub) Update(_ context.Context, _ *apikey.APIKey, fields apikey.APIKeyUpdateFields) error {
	s.updateFields = append(s.updateFields, fields)
	return nil
}

func newUpdateFieldsAPIKeyService(key *apikey.APIKey) (*apikey.APIKeyService, *updateFieldsAPIKeyRepoStub) {
	repo := &updateFieldsAPIKeyRepoStub{key: key}
	return newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo}), repo
}

func TestAPIKeyUpdate_OnlyDeclaresRequestedColumns(t *testing.T) {
	name := "renamed"
	quota := 500.0
	rateLimit := 42.0
	whitelist := []string{"10.0.0.1"}
	fastModePolicy := apikey.APIKeyFastModePolicyForceOff
	fallbackToDefaultGroup := false
	modelMapping := map[string]string{"review": "gpt-5.6-luna"}

	tests := []struct {
		name string
		req  apikey.UpdateAPIKeyRequest
		want apikey.APIKeyUpdateFields
	}{
		{
			name: "model mapping only",
			req:  apikey.UpdateAPIKeyRequest{ModelMapping: &modelMapping},
			want: apikey.APIKeyUpdateFields{ModelMapping: true},
		},
		{
			name: "name only",
			req:  apikey.UpdateAPIKeyRequest{Name: &name},
			want: apikey.APIKeyUpdateFields{Name: true},
		},
		{
			name: "quota only",
			req:  apikey.UpdateAPIKeyRequest{Quota: &quota},
			want: apikey.APIKeyUpdateFields{Quota: true},
		},
		{
			name: "rate limit threshold only",
			req:  apikey.UpdateAPIKeyRequest{RateLimit5h: &rateLimit},
			want: apikey.APIKeyUpdateFields{RateLimits: true},
		},
		{
			name: "ip whitelist only",
			req:  apikey.UpdateAPIKeyRequest{IPWhitelist: &whitelist},
			want: apikey.APIKeyUpdateFields{IPRules: true},
		},
		{
			name: "fork fast mode policy only",
			req:  apikey.UpdateAPIKeyRequest{FastModePolicy: &fastModePolicy},
			want: apikey.APIKeyUpdateFields{FastModePolicy: true},
		},
		{
			name: "fork group fallback policy only",
			req:  apikey.UpdateAPIKeyRequest{FallbackToDefaultGroupWhenUnavailable: &fallbackToDefaultGroup},
			want: apikey.APIKeyUpdateFields{FallbackToDefaultGroupWhenUnavailable: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newUpdateFieldsAPIKeyService(&apikey.APIKey{
				ID:        1,
				UserID:    7,
				Key:       "sk-test",
				Name:      "before",
				Status:    billing.StatusActive,
				Quota:     100,
				QuotaUsed: 30,
				Usage5h:   12,
			})

			_, err := svc.Update(context.Background(), 1, 7, tt.req)
			require.NoError(t, err)
			require.Equal(t, []apikey.APIKeyUpdateFields{tt.want}, repo.updateFields)
		})
	}
}

func TestAPIKeyUpdateClearsModelMappingWithEmptyObject(t *testing.T) {
	emptyMapping := map[string]string{}
	svc, repo := newUpdateFieldsAPIKeyService(&apikey.APIKey{
		ID: 1, UserID: 7, Key: "sk-test", Status: billing.StatusActive,
		ModelMapping: map[string]string{"review": "gpt-5.6-luna"},
	})

	updated, err := svc.Update(context.Background(), 1, 7, apikey.UpdateAPIKeyRequest{ModelMapping: &emptyMapping})
	require.NoError(t, err)
	require.Empty(t, updated.ModelMapping)
	require.Equal(t, []apikey.APIKeyUpdateFields{{ModelMapping: true}}, repo.updateFields)
}

// 显式重置仍需声明对应的列，避免收窄写入列时把功能改坏。
func TestAPIKeyUpdate_DeclaresUsageColumnsOnExplicitReset(t *testing.T) {
	reset := true
	svc, repo := newUpdateFieldsAPIKeyService(&apikey.APIKey{
		ID: 1, UserID: 7, Key: "sk-test", Status: billing.StatusActive, Quota: 100, QuotaUsed: 30, Usage5h: 12,
	})

	_, err := svc.Update(context.Background(), 1, 7, apikey.UpdateAPIKeyRequest{
		ResetQuota:          &reset,
		ResetRateLimitUsage: &reset,
	})
	require.NoError(t, err)
	require.Equal(t, []apikey.APIKeyUpdateFields{{QuotaUsed: true, RateLimitUsage: true}}, repo.updateFields)
}

// 配额扩容会顺带把 quota_exhausted 复活为 active，此时必须声明 status。
func TestAPIKeyUpdate_DeclaresStatusWhenReactivated(t *testing.T) {
	quota := 500.0
	svc, repo := newUpdateFieldsAPIKeyService(&apikey.APIKey{
		ID: 1, UserID: 7, Key: "sk-test", Status: apikey.StatusAPIKeyQuotaExhausted, Quota: 100, QuotaUsed: 100,
	})

	_, err := svc.Update(context.Background(), 1, 7, apikey.UpdateAPIKeyRequest{Quota: &quota})
	require.NoError(t, err)
	require.Equal(t, []apikey.APIKeyUpdateFields{{Quota: true, Status: true}}, repo.updateFields)
}

// 计费热路径把 Key 标记为配额耗尽时只写 status，
// 否则会把刚原子递增的 quota_used 按快照覆盖掉。
func TestUpdateQuotaUsed_ExhaustedMarkOnlyDeclaresStatus(t *testing.T) {
	repo := &updateFieldsAPIKeyRepoStub{key: &apikey.APIKey{
		ID: 1, UserID: 7, Key: "sk-test", Status: billing.StatusActive, Quota: 10, QuotaUsed: 10,
	}}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})

	require.NoError(t, svc.UpdateQuotaUsed(context.Background(), 1, 5))
	require.Equal(t, []apikey.APIKeyUpdateFields{{Status: true}}, repo.updateFields)
}
