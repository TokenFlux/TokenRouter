//go:build unit

// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	context "context"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	require "github.com/stretchr/testify/require"
	strings "strings"
	testing "testing"
	time "time"
)

// apiKeyRepoStub 是 APIKeyRepository 接口的测试桩实现。
// 用于隔离测试 APIKeyService.Delete 方法，避免依赖真实数据库。
//
// 设计说明：
//   - apiKey/getByIDErr: 模拟 GetKeyAndOwnerID 返回的记录与错误
//   - deleteErr: 模拟 Delete 返回的错误
//   - deletedIDs: 记录被调用删除的 API Key ID，用于断言验证
type apiKeyRepoStub struct {
	apiKey                 *APIKey // 轻量查询返回的记录
	getByIDErr             error   // 轻量查询的错误返回值
	deleteErr              error   // 删除操作的错误返回值
	updateErr              error   // 更新操作的错误返回值
	deletedIDs             []int64 // 记录已删除的密钥编号列表
	updatedKeys            []APIKey
	allowListByUserID      bool
	listByUserIDKeys       []APIKey
	listByUserIDErr        error
	listByUserIDCalls      []int64
	listByUserIDParams     []pagination.PaginationParams
	listByUserIDFilters    []APIKeyListFilters
	allowListAllByUserID   bool
	listAllByUserIDKeys    []APIKey
	listAllByUserIDErr     error
	listAllByUserIDCalls   []int64
	listAllByUserIDFilters []APIKeyListFilters
	updateLastUsed         func(ctx context.Context, id int64, usedAt time.Time) error
	touchedIDs             []int64
	touchedUsedAts         []time.Time
}

func (s *apiKeyRepoStub) Create(ctx context.Context, key *APIKey) error {
	panic("unexpected Create call")
}

func (s *apiKeyRepoStub) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	if s.getByIDErr != nil {
		return nil, s.getByIDErr
	}
	if s.apiKey != nil {
		clone := *s.apiKey
		return &clone, nil
	}
	panic("unexpected GetByID call")
}

func (s *apiKeyRepoStub) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	if s.getByIDErr != nil {
		return "", 0, s.getByIDErr
	}
	if s.apiKey != nil {
		return s.apiKey.Key, s.apiKey.UserID, nil
	}
	return "", 0, ErrAPIKeyNotFound
}

func (s *apiKeyRepoStub) GetByKey(ctx context.Context, key string) (*APIKey, error) {
	panic("unexpected GetByKey call")
}

func (s *apiKeyRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*APIKey, error) {
	panic("unexpected GetByKeyForAuth call")
}

func (s *apiKeyRepoStub) Update(ctx context.Context, key *APIKey, _ APIKeyUpdateFields) error {
	if key != nil {
		s.updatedKeys = append(s.updatedKeys, *key)
	}
	return s.updateErr
}

// Delete 记录被删除的 API Key ID 并返回预设的错误。
// 通过 deletedIDs 可以验证删除操作是否被正确调用。
func (s *apiKeyRepoStub) Delete(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErr
}

// DeleteWithAudit 与 Delete 一样记录被删除的 ID,供 service 测试断言。
func (s *apiKeyRepoStub) DeleteWithAudit(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErr
}

func (s *apiKeyRepoStub) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	if !s.allowListByUserID {
		panic("unexpected ListByUserID call")
	}
	s.listByUserIDCalls = append(s.listByUserIDCalls, userID)
	s.listByUserIDParams = append(s.listByUserIDParams, params)
	s.listByUserIDFilters = append(s.listByUserIDFilters, filters)
	if s.listByUserIDErr != nil {
		return nil, nil, s.listByUserIDErr
	}
	keys := append([]APIKey(nil), s.listByUserIDKeys...)
	return keys, &pagination.PaginationResult{
		Total:    int64(len(keys)),
		Page:     params.Page,
		PageSize: params.PageSize,
		Pages:    1,
	}, nil
}

func (s *apiKeyRepoStub) ListAllByUserID(ctx context.Context, userID int64, filters APIKeyListFilters) ([]APIKey, error) {
	if !s.allowListAllByUserID {
		panic("unexpected ListAllByUserID call")
	}
	s.listAllByUserIDCalls = append(s.listAllByUserIDCalls, userID)
	s.listAllByUserIDFilters = append(s.listAllByUserIDFilters, filters)
	if s.listAllByUserIDErr != nil {
		return nil, s.listAllByUserIDErr
	}
	source := s.listByUserIDKeys
	if s.listAllByUserIDKeys != nil {
		source = s.listAllByUserIDKeys
	}
	return filterAPIKeyStubKeys(userID, source, filters), nil
}

func filterAPIKeyStubKeys(userID int64, keys []APIKey, filters APIKeyListFilters) []APIKey {
	result := make([]APIKey, 0, len(keys))
	search := strings.ToLower(filters.Search)
	for _, key := range keys {
		if key.UserID != userID {
			continue
		}
		if search != "" &&
			!strings.Contains(strings.ToLower(key.Name), search) &&
			!strings.Contains(strings.ToLower(key.Key), search) {
			continue
		}
		if filters.Status != "" && key.Status != filters.Status {
			continue
		}
		if filters.GroupID != nil {
			if *filters.GroupID == 0 {
				if key.GroupID != nil {
					continue
				}
			} else if key.GroupID == nil || *key.GroupID != *filters.GroupID {
				continue
			}
		}
		result = append(result, key)
	}
	return result
}

func (s *apiKeyRepoStub) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	panic("unexpected VerifyOwnership call")
}

func (s *apiKeyRepoStub) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	panic("unexpected CountByUserID call")
}

func (s *apiKeyRepoStub) ExistsByKey(ctx context.Context, key string) (bool, error) {
	panic("unexpected ExistsByKey call")
}

func (s *apiKeyRepoStub) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

func (s *apiKeyRepoStub) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error) {
	panic("unexpected SearchAPIKeys call")
}

func (s *apiKeyRepoStub) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected ClearGroupIDByGroupID call")
}

func (s *apiKeyRepoStub) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	panic("unexpected UpdateGroupIDByUserAndGroup call")
}

func (s *apiKeyRepoStub) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected CountByGroupID call")
}

func (s *apiKeyRepoStub) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	panic("unexpected ListKeysByUserID call")
}

func (s *apiKeyRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	panic("unexpected ListKeysByGroupID call")
}

func (s *apiKeyRepoStub) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	panic("unexpected IncrementQuotaUsed call")
}

func (s *apiKeyRepoStub) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	s.touchedIDs = append(s.touchedIDs, id)
	s.touchedUsedAts = append(s.touchedUsedAts, usedAt)
	if s.updateLastUsed != nil {
		return s.updateLastUsed(ctx, id, usedAt)
	}
	return nil
}

func (s *apiKeyRepoStub) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	panic("unexpected IncrementRateLimitUsage call")
}

func (s *apiKeyRepoStub) ResetRateLimitWindows(ctx context.Context, id int64) error {
	panic("unexpected ResetRateLimitWindows call")
}

func (s *apiKeyRepoStub) GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error) {
	panic("unexpected GetRateLimitData call")
}

// apiKeyCacheStub 是 APIKeyCache 接口的测试桩实现。
// 用于验证删除操作时缓存清理逻辑是否被正确调用。
//
// 设计说明：
//   - invalidated: 记录被清除缓存的用户 ID 列表
type apiKeyCacheStub struct {
	invalidated    []int64  // 记录调用 DeleteCreateAttemptCount 时传入的用户 ID
	deleteAuthKeys []string // 记录调用 DeleteAuthCache 时传入的缓存 key
}

// GetCreateAttemptCount 返回 0，表示用户未超过创建次数限制
func (s *apiKeyCacheStub) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

// IncrementCreateAttemptCount 空实现，本测试不验证此行为
func (s *apiKeyCacheStub) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

// DeleteCreateAttemptCount 记录被清除缓存的用户 ID。
// 删除 API Key 时会调用此方法清除用户的创建尝试计数缓存。
func (s *apiKeyCacheStub) DeleteCreateAttemptCount(ctx context.Context, userID int64) error {
	s.invalidated = append(s.invalidated, userID)
	return nil
}

// IncrementDailyUsage 空实现，本测试不验证此行为
func (s *apiKeyCacheStub) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return nil
}

// SetDailyUsageExpiry 空实现，本测试不验证此行为
func (s *apiKeyCacheStub) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return nil
}

func (s *apiKeyCacheStub) GetAuthCache(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) {
	return nil, nil
}

func (s *apiKeyCacheStub) SetAuthCache(ctx context.Context, key string, entry *APIKeyAuthCacheEntry, ttl time.Duration) error {
	return nil
}

func (s *apiKeyCacheStub) DeleteAuthCache(ctx context.Context, key string) error {
	s.deleteAuthKeys = append(s.deleteAuthKeys, key)
	return nil
}

func (s *apiKeyCacheStub) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return nil
}

func (s *apiKeyCacheStub) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	return nil
}

// TestApiKeyService_Delete_Success 测试所有者成功删除 API Key 的场景。
// 预期行为：
//   - GetKeyAndOwnerID 返回所有者 ID 为 7
//   - 调用者 userID 为 7（匹配）
//   - Delete 成功执行
//   - 缓存被正确清除（使用 ownerID）
//   - 返回 nil 错误
func TestApiKeyService_Delete_Success(t *testing.T) {
	repo := &apiKeyRepoStub{
		apiKey: &APIKey{ID: 42, UserID: 7, Key: "k"},
	}
	cache := &apiKeyCacheStub{}
	svc := &APIKeyService{apiKeyRepo: repo, cache: cache}
	svc.lastUsedTouchL1.Store(int64(42), time.Now())

	err := svc.Delete(context.Background(), 42, 7) // API Key ID=42, 调用者 userID=7
	require.NoError(t, err)
	require.Equal(t, []int64{42}, repo.deletedIDs)  // 验证正确的 API Key 被删除
	require.Equal(t, []int64{7}, cache.invalidated) // 验证所有者的缓存被清除
	require.Equal(t, []string{svc.KeyAuthCacheKey("k")}, cache.deleteAuthKeys)
	_, exists := svc.lastUsedTouchL1.Load(int64(42))
	require.False(t, exists, "delete should clear touch debounce cache")
}
