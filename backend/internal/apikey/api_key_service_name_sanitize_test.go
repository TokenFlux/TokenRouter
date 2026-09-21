//go:build unit

package apikey_test

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type apiKeyNameSanitizeRepoStub struct {
	apiKey  *apikey.APIKey
	created []*apikey.APIKey
	updated []*apikey.APIKey
}

func (s *apiKeyNameSanitizeRepoStub) Create(ctx context.Context, key *apikey.APIKey) error {
	clone := *key
	if clone.ID == 0 {
		clone.ID = int64(len(s.created) + 1)
	}
	s.created = append(s.created, &clone)
	*key = clone
	return nil
}

func (s *apiKeyNameSanitizeRepoStub) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	if s.apiKey == nil || s.apiKey.ID != id {
		return nil, apikey.ErrAPIKeyNotFound
	}
	clone := *s.apiKey
	return &clone, nil
}

func (s *apiKeyNameSanitizeRepoStub) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	panic("unexpected GetKeyAndOwnerID call")
}

func (s *apiKeyNameSanitizeRepoStub) GetByKey(ctx context.Context, key string) (*apikey.APIKey, error) {
	panic("unexpected GetByKey call")
}

func (s *apiKeyNameSanitizeRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*apikey.APIKey, error) {
	panic("unexpected GetByKeyForAuth call")
}

func (s *apiKeyNameSanitizeRepoStub) Update(ctx context.Context, key *apikey.APIKey, _ apikey.APIKeyUpdateFields) error {
	clone := *key
	s.updated = append(s.updated, &clone)
	s.apiKey = &clone
	return nil
}

func (s *apiKeyNameSanitizeRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *apiKeyNameSanitizeRepoStub) DeleteWithAudit(ctx context.Context, id int64) error {
	panic("unexpected DeleteWithAudit call")
}

func (s *apiKeyNameSanitizeRepoStub) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserID call")
}

func (s *apiKeyNameSanitizeRepoStub) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	panic("unexpected VerifyOwnership call")
}

func (s *apiKeyNameSanitizeRepoStub) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	panic("unexpected CountByUserID call")
}

func (s *apiKeyNameSanitizeRepoStub) ExistsByKey(ctx context.Context, key string) (bool, error) {
	return false, nil
}

func (s *apiKeyNameSanitizeRepoStub) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

func (s *apiKeyNameSanitizeRepoStub) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]apikey.APIKey, error) {
	panic("unexpected SearchAPIKeys call")
}

func (s *apiKeyNameSanitizeRepoStub) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected ClearGroupIDByGroupID call")
}

func (s *apiKeyNameSanitizeRepoStub) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	panic("unexpected UpdateGroupIDByUserAndGroup call")
}

func (s *apiKeyNameSanitizeRepoStub) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected CountByGroupID call")
}

func (s *apiKeyNameSanitizeRepoStub) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	panic("unexpected ListKeysByUserID call")
}

func (s *apiKeyNameSanitizeRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	panic("unexpected ListKeysByGroupID call")
}

func (s *apiKeyNameSanitizeRepoStub) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	panic("unexpected IncrementQuotaUsed call")
}

func (s *apiKeyNameSanitizeRepoStub) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	panic("unexpected UpdateLastUsed call")
}

func (s *apiKeyNameSanitizeRepoStub) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	panic("unexpected IncrementRateLimitUsage call")
}

func (s *apiKeyNameSanitizeRepoStub) ResetRateLimitWindows(ctx context.Context, id int64) error {
	panic("unexpected ResetRateLimitWindows call")
}

func (s *apiKeyNameSanitizeRepoStub) GetRateLimitData(ctx context.Context, id int64) (*apikey.APIKeyRateLimitData, error) {
	panic("unexpected GetRateLimitData call")
}

func TestAPIKeyService_Create_EscapesNameBeforePersist(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{}
	svc := newAPIKeyTestService(apiKeyTestDependencies{
		apiKeyRepo: repo,
		userRepo:   &userRepoStub{user: &identity.User{ID: 7, Status: billing.StatusActive, Role: identity.RoleUser}},
	})
	customKey := "sk_valid_xss_key_1"

	created, err := svc.Create(context.Background(), 7, apikey.CreateAPIKeyRequest{
		Name:      `<img src=x onerror=alert(1)>`,
		CustomKey: &customKey,
	})

	require.NoError(t, err)
	require.Equal(t, "&lt;img src=x onerror=alert(1)&gt;", created.Name)
	require.Len(t, repo.created, 1)
	require.Equal(t, "&lt;img src=x onerror=alert(1)&gt;", repo.created[0].Name)
}

func TestAPIKeyService_Create_DefaultsGroupFallbackEnabled(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{}
	svc := newAPIKeyTestService(apiKeyTestDependencies{
		apiKeyRepo: repo,
		userRepo:   &userRepoStub{user: &identity.User{ID: 7, Status: billing.StatusActive, Role: identity.RoleUser}},
	})
	customKey := "sk_valid_default_fallback"

	created, err := svc.Create(context.Background(), 7, apikey.CreateAPIKeyRequest{
		Name:      "default fallback",
		CustomKey: &customKey,
	})

	require.NoError(t, err)
	require.True(t, created.FallbackToDefaultGroupWhenUnavailable)
	require.Len(t, repo.created, 1)
	require.True(t, repo.created[0].FallbackToDefaultGroupWhenUnavailable)
	require.Equal(t, apikey.APIKeyFastModePolicyFollowRequest, created.FastModePolicy)
}

func TestAPIKeyService_CreateRejectsInvalidFastModePolicy(t *testing.T) {
	svc := newAPIKeyTestService(apiKeyTestDependencies{})

	_, err := svc.Create(context.Background(), 7, apikey.CreateAPIKeyRequest{FastModePolicy: "invalid"})
	require.ErrorIs(t, err, apikey.ErrInvalidAPIKeyFastModePolicy)
}

func TestAPIKeyService_Create_AllowsDisablingGroupFallback(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{}
	svc := newAPIKeyTestService(apiKeyTestDependencies{
		apiKeyRepo: repo,
		userRepo:   &userRepoStub{user: &identity.User{ID: 7, Status: billing.StatusActive, Role: identity.RoleUser}},
	})
	customKey := "sk_valid_disabled_fallback"
	fallback := false

	created, err := svc.Create(context.Background(), 7, apikey.CreateAPIKeyRequest{
		Name:                                  "disabled fallback",
		CustomKey:                             &customKey,
		FallbackToDefaultGroupWhenUnavailable: &fallback,
	})

	require.NoError(t, err)
	require.False(t, created.FallbackToDefaultGroupWhenUnavailable)
	require.Len(t, repo.created, 1)
	require.False(t, repo.created[0].FallbackToDefaultGroupWhenUnavailable)
}

func TestAPIKeyService_Update_EscapesNameBeforePersist(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{
		apiKey: &apikey.APIKey{ID: 11, UserID: 7, Key: "sk_existing_key_01", Name: "old", Status: billing.StatusActive},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})
	name := `<script>alert("x")</script>`

	updated, err := svc.Update(context.Background(), 11, 7, apikey.UpdateAPIKeyRequest{Name: &name})

	require.NoError(t, err)
	require.Equal(t, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;", updated.Name)
	require.Len(t, repo.updated, 1)
	require.Equal(t, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;", repo.updated[0].Name)
}

func TestAPIKeyService_UpdatePersistsFastModePolicy(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{
		apiKey: &apikey.APIKey{ID: 11, UserID: 7, Key: "sk_existing_key_02", Name: "old", Status: billing.StatusActive, FastModePolicy: apikey.APIKeyFastModePolicyFollowRequest},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})
	policy := apikey.APIKeyFastModePolicyForceOff

	updated, err := svc.Update(context.Background(), 11, 7, apikey.UpdateAPIKeyRequest{FastModePolicy: &policy})
	require.NoError(t, err)
	require.Equal(t, apikey.APIKeyFastModePolicyForceOff, updated.FastModePolicy)
	require.Equal(t, apikey.APIKeyFastModePolicyForceOff, repo.updated[0].FastModePolicy)
}

// 部分更新未携带 IP 限制字段时必须保留原有配置。
func TestAPIKeyService_UpdatePreservesOmittedIPRestrictions(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{
		apiKey: &apikey.APIKey{
			ID:          11,
			UserID:      7,
			Key:         "sk_existing_ip_key_01",
			Status:      billing.StatusActive,
			IPWhitelist: []string{"192.0.2.10"},
			IPBlacklist: []string{"198.51.100.0/24"},
		},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})

	updated, err := svc.Update(context.Background(), 11, 7, apikey.UpdateAPIKeyRequest{})

	require.NoError(t, err)
	require.Equal(t, []string{"192.0.2.10"}, updated.IPWhitelist)
	require.Equal(t, []string{"198.51.100.0/24"}, updated.IPBlacklist)
}

// 显式空数组只清空对应的 IP 限制，未携带的另一字段保持不变。
func TestAPIKeyService_UpdateClearsExplicitEmptyIPRestriction(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{
		apiKey: &apikey.APIKey{
			ID:          11,
			UserID:      7,
			Key:         "sk_existing_ip_key_02",
			Status:      billing.StatusActive,
			IPWhitelist: []string{"192.0.2.10"},
			IPBlacklist: []string{"198.51.100.0/24"},
		},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})
	emptyWhitelist := []string{}

	updated, err := svc.Update(context.Background(), 11, 7, apikey.UpdateAPIKeyRequest{IPWhitelist: &emptyWhitelist})

	require.NoError(t, err)
	require.Empty(t, updated.IPWhitelist)
	require.Equal(t, []string{"198.51.100.0/24"}, updated.IPBlacklist)
}

// 显式更新 IP 限制时仍必须校验模式格式。
func TestAPIKeyService_UpdateRejectsInvalidIPRestriction(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{
		apiKey: &apikey.APIKey{ID: 11, UserID: 7, Key: "sk_existing_ip_key_03", Status: billing.StatusActive},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})
	invalidBlacklist := []string{"not-an-ip"}

	_, err := svc.Update(context.Background(), 11, 7, apikey.UpdateAPIKeyRequest{IPBlacklist: &invalidBlacklist})

	require.ErrorIs(t, err, apikey.ErrInvalidIPPattern)
	require.Empty(t, repo.updated)
}
