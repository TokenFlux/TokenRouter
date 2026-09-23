//go:build integration

package identity_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestAuthCacheInvalidationTriggers_CoverSecurityMutationsOnly(t *testing.T) {
	integrationDB, integrationEntClient := identityDatabase(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	group := mustCreateGroup(t, integrationEntClient, &routing.Group{
		Name: fmt.Sprintf("auth-outbox-group-%d", suffix), RateMultiplier: 1, IsExclusive: true,
	})
	user := mustCreateUser(t, integrationEntClient, &identity.User{
		Email: fmt.Sprintf("auth-outbox-%d@example.com", suffix), Concurrency: 5,
	})
	groupID := group.ID
	keyValue := fmt.Sprintf("sk-auth-outbox-%d", suffix)
	apiKeyRepo := newKeyStoreFixture(integrationEntClient, integrationDB)
	key := &apikey.APIKey{UserID: user.ID, GroupID: &groupID, Key: keyValue, Name: "outbox", Status: billing.StatusActive}
	require.NoError(t, apiKeyRepo.Create(ctx, key))

	sum := sha256.Sum256([]byte(keyValue))
	cacheKey := hex.EncodeToString(sum[:])
	clear := func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey)
		require.NoError(t, err)
	}
	count := func() int {
		var value int
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey).Scan(&value))
		return value
	}
	clear()
	t.Cleanup(clear)
	t.Cleanup(func() {
		// 隔离共享集成数据库，避免影响断言全平台分组数量的测试套件。
		// 最后的清理会在此之后运行，并删除这些硬删除操作产生的失效事件。
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM user_allowed_groups WHERE user_id = $1 OR group_id = $2", user.ID, group.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
		require.NoError(t, err)
	})

	_, err := integrationDB.ExecContext(ctx, `
		UPDATE api_keys
		SET quota_used = quota_used + 1,
			usage_5h = usage_5h + 1,
			last_used_at = NOW()
		WHERE id = $1`, key.ID)
	require.NoError(t, err)
	require.Zero(t, count(), "usage-only key updates must not enqueue")

	_, err = integrationDB.ExecContext(ctx, "UPDATE api_keys SET status = 'disabled' WHERE id = $1", key.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count(), "key disable must enqueue")
	clear()
	_, err = integrationDB.ExecContext(ctx, "UPDATE api_keys SET status = 'active' WHERE id = $1", key.ID)
	require.NoError(t, err)
	clear()

	userRepo := postgres.NewUserStore(integrationEntClient, integrationDB)
	loadedUser, err := userRepo.GetByID(ctx, user.ID)
	require.NoError(t, err)
	_, err = userRepo.AdjustBalance(ctx, loadedUser.ID, 10)
	require.NoError(t, err)
	require.Zero(t, count(), "balance update with unchanged allowed groups must not enqueue")

	_, err = integrationDB.ExecContext(ctx, "UPDATE users SET status = 'disabled' WHERE id = $1", user.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count(), "user disable must enqueue all active keys")
	clear()
	_, err = integrationDB.ExecContext(ctx, "UPDATE users SET status = 'active' WHERE id = $1", user.ID)
	require.NoError(t, err)
	clear()

	_, err = integrationDB.ExecContext(ctx, "UPDATE groups SET name = name || '-cosmetic' WHERE id = $1", group.ID)
	require.NoError(t, err)
	require.Zero(t, count(), "cosmetic group update must not enqueue")
	_, err = integrationDB.ExecContext(ctx, "UPDATE groups SET allow_image_generation = NOT allow_image_generation WHERE id = $1", group.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count(), "image-generation permission changes must enqueue bound keys")
	clear()
	_, err = integrationDB.ExecContext(ctx, "UPDATE groups SET status = 'disabled' WHERE id = $1", group.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count(), "group disable must enqueue bound keys")
	clear()
	_, err = integrationDB.ExecContext(ctx, "UPDATE groups SET status = 'active' WHERE id = $1", group.ID)
	require.NoError(t, err)
	clear()

	_, err = integrationDB.ExecContext(ctx,
		"INSERT INTO user_allowed_groups (user_id, group_id) VALUES ($1, $2)", user.ID, group.ID)
	require.NoError(t, err)
	clear()
	_, err = integrationDB.ExecContext(ctx,
		"DELETE FROM user_allowed_groups WHERE user_id = $1 AND group_id = $2", user.ID, group.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count(), "exclusive-group revocation must enqueue")
	clear()

	require.NoError(t, apiKeyRepo.DeleteWithAudit(ctx, key.ID))
	require.Equal(t, 1, count(), "tombstone delete must hash OLD.key exactly once")
	var stored string
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT cache_key FROM auth_cache_invalidation_outbox WHERE cache_key = $1 LIMIT 1", cacheKey).Scan(&stored))
	require.Equal(t, cacheKey, stored)
	require.NotContains(t, stored, keyValue)
}
