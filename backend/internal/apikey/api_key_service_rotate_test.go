//go:build unit

// API Key 轮换的单元测试：所有权、托管 Key 拦截、CAS 冲突与缓存失效。

package apikey

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// apiKeyRotateCall 记录一次 RotateKey 的入参，用来确认 CAS 比的是旧 key。
type apiKeyRotateCall struct {
	id          int64
	expectedKey string
	newKey      string
}

// RotateKey 让 apiKeyRepoStub 满足 apiKeyCredentialRotator。
func (s *apiKeyRepoStub) RotateKey(ctx context.Context, id int64, expectedKey, newKey string) error {
	s.rotateCalls = append(s.rotateCalls, apiKeyRotateCall{id: id, expectedKey: expectedKey, newKey: newKey})
	return s.rotateErr
}

func newAPIKeyRotateTestService(repo *apiKeyRepoStub) (*APIKeyService, *apiKeyCacheStub) {
	cache := &apiKeyCacheStub{}
	return &APIKeyService{apiKeyRepo: repo, cache: cache, cfg: &Options{}}, cache
}

// 轮换成功：ID、名称、分组、额度、有效期全部保留，只换凭据值，
// 并同时失效旧凭据与可能存在的旧负缓存。
func TestAPIKeyService_Rotate_Success(t *testing.T) {
	groupID := int64(9)
	expiresAt := time.Now().Add(24 * time.Hour)
	repo := &apiKeyRepoStub{apiKey: &APIKey{
		ID: 42, UserID: 7, Key: "sk-old", Name: "prod",
		GroupID: &groupID, Quota: 12.5, ExpiresAt: &expiresAt,
	}}
	svc, cache := newAPIKeyRotateTestService(repo)

	got, err := svc.Rotate(context.Background(), 42, 7)
	require.NoError(t, err)
	require.Equal(t, int64(42), got.ID, "轮换必须保留 Key ID")
	require.NotEqual(t, "sk-old", got.Key, "凭据值必须变化")
	require.True(t, strings.HasPrefix(got.Key, "sk-"))
	require.Equal(t, "prod", got.Name)
	require.Equal(t, &groupID, got.GroupID)
	require.Equal(t, 12.5, got.Quota)
	require.Equal(t, &expiresAt, got.ExpiresAt)

	require.Len(t, repo.rotateCalls, 1)
	require.Equal(t, int64(42), repo.rotateCalls[0].id)
	require.Equal(t, "sk-old", repo.rotateCalls[0].expectedKey)
	require.Equal(t, got.Key, repo.rotateCalls[0].newKey)
	require.Equal(t, []string{svc.KeyAuthCacheKey("sk-old"), svc.KeyAuthCacheKey(got.Key)}, cache.deleteAuthKeys)
}

// 非所有者不能轮换，且不该产生任何写入或缓存失效。
func TestAPIKeyService_Rotate_NotOwner(t *testing.T) {
	repo := &apiKeyRepoStub{apiKey: &APIKey{ID: 42, UserID: 1, Key: "sk-old"}}
	svc, cache := newAPIKeyRotateTestService(repo)

	_, err := svc.Rotate(context.Background(), 42, 2)
	require.ErrorIs(t, err, ErrInsufficientPerms)
	require.Empty(t, repo.rotateCalls)
	require.Empty(t, cache.deleteAuthKeys)
}

// 服务端托管的隐藏 Key 按不存在处理，不向用户暴露它的存在性。
func TestAPIKeyService_Rotate_ManagedKeyHidden(t *testing.T) {
	managed := "creative_studio"
	repo := &apiKeyRepoStub{apiKey: &APIKey{ID: 42, UserID: 7, Key: "sk-old", ManagedBy: &managed}}
	svc, _ := newAPIKeyRotateTestService(repo)

	_, err := svc.Rotate(context.Background(), 42, 7)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Empty(t, repo.rotateCalls)
}

// 记录不存在时返回 NotFound。
func TestAPIKeyService_Rotate_NotFound(t *testing.T) {
	repo := &apiKeyRepoStub{getByIDErr: ErrAPIKeyNotFound}
	svc, _ := newAPIKeyRotateTestService(repo)

	_, err := svc.Rotate(context.Background(), 42, 7)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Empty(t, repo.rotateCalls)
}

// 并发轮换撞车：CAS 没命中，返回冲突且不产出第二个成功结果。
func TestAPIKeyService_Rotate_Conflict(t *testing.T) {
	repo := &apiKeyRepoStub{
		apiKey:    &APIKey{ID: 42, UserID: 7, Key: "sk-old"},
		rotateErr: ErrAPIKeyRotateConflict,
	}
	svc, cache := newAPIKeyRotateTestService(repo)

	_, err := svc.Rotate(context.Background(), 42, 7)
	require.ErrorIs(t, err, ErrAPIKeyRotateConflict)
	require.Len(t, repo.rotateCalls, 1, "CAS 尝试发生了，只是失败了")
	require.Empty(t, cache.deleteAuthKeys, "失败时不能失效缓存")
}
