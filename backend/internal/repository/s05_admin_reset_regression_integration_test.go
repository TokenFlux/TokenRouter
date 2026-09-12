//go:build integration

package repository

import (
	"bytes"
	"context"
	"fmt"
	adminhttp "github.com/TokenFlux/TokenRouter/internal/handler/admin"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestS05AdminKeyResetAndInvalidGroupAreAtomic 验证同一管理请求失败时不保留消费重置。
func TestS05AdminKeyResetAndInvalidGroupAreAtomic(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	users := NewUserRepository(client, integrationDB)
	keys := NewAPIKeyRepository(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "s05-reset-" + uuid.NewString()})
	now := time.Now().UTC().Truncate(time.Second)
	_, err := integrationDB.ExecContext(ctx, "UPDATE api_keys SET usage_5h=12,usage_1d=13,usage_7d=14,window_5h_start=$2,window_1d_start=$2,window_7d_start=$2 WHERE id=$1", key.ID, now)
	require.NoError(t, err)
	admin := service.NewAdminService(users, nil, nil, nil, keys, nil, nil, nil, nil, nil, nil, nil, client, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := adminhttp.NewAdminAPIKeyHandler(admin)
	router := gin.New()
	router.PUT("/admin/api-keys/:id", h.UpdateGroup)
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/admin/api-keys/%d", key.ID), bytes.NewBufferString(`{"group_id":-1,"reset_rate_limit_usage":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	stored, err := client.APIKey.Get(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, 12.0, stored.Usage5h, "非法分组失败不能先提交限额重置")
	require.Equal(t, 13.0, stored.Usage1d)
	require.Equal(t, 14.0, stored.Usage7d)
	require.NotNil(t, stored.Window5hStart)
	require.WithinDuration(t, now, *stored.Window5hStart, time.Millisecond)
}

// TestS05AdminKeyCombinedWriteRollsBackOnDatabaseFailure 验证真实数据库拒绝配置写入时，消费重置也回滚。
func TestS05AdminKeyCombinedWriteRollsBackOnDatabaseFailure(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	users := NewUserRepository(client, integrationDB)
	keys := NewAPIKeyRepository(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{})
	group := mustCreateGroup(t, client, &service.Group{Name: "s05-atomic-group-" + uuid.NewString()})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "s05-atomic-update-" + uuid.NewString()})
	_, err := integrationDB.ExecContext(ctx, "UPDATE api_keys SET usage_5h=12,usage_1d=13,usage_7d=14,window_5h_start=NOW(),window_1d_start=NOW(),window_7d_start=NOW() WHERE id=$1", key.ID)
	require.NoError(t, err)
	name := fmt.Sprintf("s05_reject_key_%d", key.ID)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 's05 reject key configuration'; END; $$", name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+name+" ON api_keys")
		_, _ = integrationDB.ExecContext(ctx, "DROP FUNCTION IF EXISTS "+name+"()")
	})
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE UPDATE OF group_id ON api_keys FOR EACH ROW WHEN (OLD.id=%d AND NEW.group_id IS DISTINCT FROM OLD.group_id) EXECUTE FUNCTION %s()", name, key.ID, name))
	require.NoError(t, err)
	groups, ok := NewGroupRepository(client, integrationDB).(service.AdminGroupRepository)
	require.True(t, ok)
	admin := service.NewAdminService(users, groups, nil, nil, keys, nil, nil, nil, nil, nil, nil, nil, client, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := adminhttp.NewAdminAPIKeyHandler(admin)
	router := gin.New()
	router.PUT("/admin/api-keys/:id", h.UpdateGroup)
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/admin/api-keys/%d", key.ID), bytes.NewBufferString(fmt.Sprintf(`{"group_id":%d,"reset_rate_limit_usage":true}`, group.ID)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	require.Equal(t, http.StatusInternalServerError, send().Code)
	stored, err := client.APIKey.Get(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, 12.0, stored.Usage5h)
	require.Equal(t, 13.0, stored.Usage1d)
	require.Equal(t, 14.0, stored.Usage7d)
	require.Nil(t, stored.GroupID)
	require.NotNil(t, stored.Window5hStart)
	_, err = integrationDB.ExecContext(ctx, "DROP TRIGGER "+name+" ON api_keys")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, send().Code)
	stored, err = client.APIKey.Get(ctx, key.ID)
	require.NoError(t, err)
	require.Zero(t, stored.Usage5h)
	require.Zero(t, stored.Usage1d)
	require.Zero(t, stored.Usage7d)
	require.Nil(t, stored.Window5hStart)
	require.Nil(t, stored.Window1dStart)
	require.Nil(t, stored.Window7dStart)
	require.Equal(t, group.ID, *stored.GroupID)
}
