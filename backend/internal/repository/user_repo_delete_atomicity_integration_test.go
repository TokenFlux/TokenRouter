//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/internal/pkg/pagination"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// TestUserRepository_DeleteUser_AtomicWithAPIKeys 复现 AdminService.DeleteUser 的事务编排场景：
// 把"tombstone 并删 API Key"(apiKeyRepo.DeleteWithAudit) 与"删 User"(userRepo.Delete) 放进同一个外部事务时，
// userRepo.Delete 必须复用 context 中的事务，而不是用 base client 自起一个独立事务并提前提交。
//
// 用例用"回滚外层事务"来模拟 commit 失败 / 中止：
//   - 修复前：userRepo.Delete 用 base client 自起独立事务并 commit，回滚外层事务后用户仍被删除，
//     而 API Key 随外层事务回滚 → Case 1 断言失败，暴露原子性缺陷（即 issue #3021 的不可恢复状态）。
//   - 修复后：两者落在同一事务，回滚后用户与 API Key 一起恢复。
//
// 关键点：repo 必须用 base client 构造（NewUserRepository/NewAPIKeyRepository），并由本测试手动
// 开启外层事务，这与生产环境 wire 注入的方式一致；不能复用 APIKeyRepoSuite 的 testEntTx
// （那会让 repo 持有 tx client，走的是另一条 ErrTxStarted 复用路径，无法覆盖本场景）。
func TestUserRepository_DeleteUser_AtomicWithAPIKeys(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)

	userRepo := NewUserRepository(client, integrationDB)
	apiKeyRepo := NewAPIKeyRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{})
	key1 := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: fmt.Sprintf("sk-atomic-a-%d", user.ID)})
	key2 := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: fmt.Sprintf("sk-atomic-b-%d", user.ID)})

	t.Cleanup(func() {
		// 集成测试使用全局客户端，补充清理避免残留数据影响后续用例。
		_, _ = integrationDB.Exec(`DELETE FROM deleted_api_key_audits WHERE user_id = $1`, user.ID)
		_, _ = integrationDB.Exec(`DELETE FROM api_keys WHERE user_id = $1`, user.ID)
		_, _ = integrationDB.Exec(`DELETE FROM users WHERE id = $1`, user.ID)
	})

	listParams := pagination.PaginationParams{Page: 1, PageSize: 10}

	tx, err := client.Tx(ctx)
	require.NoError(t, err, "开启外层事务")
	opCtx := dbent.NewTxContext(ctx, tx)

	require.NoError(t, apiKeyRepo.DeleteWithAudit(opCtx, key1.ID))
	require.NoError(t, apiKeyRepo.DeleteWithAudit(opCtx, key2.ID))
	require.NoError(t, userRepo.Delete(opCtx, user.ID))

	require.NoError(t, tx.Rollback(), "回滚外层事务")

	gotUser, err := userRepo.GetByID(ctx, user.ID)
	require.NoError(t, err, "回滚后用户必须仍存在")
	require.Equal(t, user.ID, gotUser.ID)

	keys, _, err := apiKeyRepo.ListByUserID(ctx, user.ID, listParams, service.APIKeyListFilters{})
	require.NoError(t, err, "查询回滚后的密钥")
	require.Len(t, keys, 2, "回滚后密钥必须仍为可用状态")

	var auditCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM deleted_api_key_audits WHERE user_id = $1`, user.ID).Scan(&auditCount))
	require.Zero(t, auditCount, "回滚后不应留下审计记录")

	tx2, err := client.Tx(ctx)
	require.NoError(t, err, "再次开启外层事务")
	opCtx2 := dbent.NewTxContext(ctx, tx2)

	require.NoError(t, apiKeyRepo.DeleteWithAudit(opCtx2, key1.ID))
	require.NoError(t, apiKeyRepo.DeleteWithAudit(opCtx2, key2.ID))
	require.NoError(t, userRepo.Delete(opCtx2, user.ID))

	require.NoError(t, tx2.Commit(), "提交外层事务")

	_, err = userRepo.GetByID(ctx, user.ID)
	require.Error(t, err, "提交后用户应被软删除")

	keysAfter, _, err := apiKeyRepo.ListByUserID(ctx, user.ID, listParams, service.APIKeyListFilters{})
	require.NoError(t, err, "查询提交后的密钥")
	require.Empty(t, keysAfter, "提交后密钥应全部被软删除")

	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM deleted_api_key_audits WHERE user_id = $1`, user.ID).Scan(&auditCount))
	require.Zero(t, auditCount, "提交后也不得保留被删 Key 的凭据材料")
}
