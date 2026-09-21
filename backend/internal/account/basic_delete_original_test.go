//go:build unit

package account_test

import (
	"context"
	"errors"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// accountRepoStub 是 AccountRepository 接口的测试桩实现。
// 用于隔离测试 AccountService.Delete 方法，避免依赖真实数据库。
//
// 设计说明：
//   - exists: 模拟 ExistsByID 返回的存在性结果
//   - existsErr: 模拟 ExistsByID 返回的错误
//   - deleteErr: 模拟 Delete 返回的错误
//   - deletedIDs: 记录被调用删除的账号 ID，用于断言验证
type accountRepoStub struct {
	accountcore.BasicAccountStore
	exists     bool    // ExistsByID 的返回值
	existsErr  error   // ExistsByID 的错误返回值
	deleteErr  error   // Delete 的错误返回值
	deletedIDs []int64 // 记录已删除的账号 ID 列表
}

// TestAccountService_Delete_NotFound 测试删除不存在的账号时返回正确的错误。
// 预期行为：
//   - ExistsByID 返回 false（账号不存在）
//   - 返回 ErrAccountNotFound 错误
//   - Delete 方法不被调用（deletedIDs 为空）
func TestAccountService_Delete_NotFound(t *testing.T) {
	repo := &accountRepoStub{exists: false}
	svc := accountcore.NewBasicAccounts(repo, nil, nil)

	err := svc.Delete(context.Background(), 55)
	require.ErrorIs(t, err, accountcore.ErrAccountNotFound)
	require.Empty(t, repo.deletedIDs)
}

// TestAccountService_Delete_CheckError 测试存在性检查失败时的错误处理。
// 预期行为：
//   - ExistsByID 返回数据库错误
//   - 返回包含 "check account" 的错误信息
//   - Delete 方法不被调用
func TestAccountService_Delete_CheckError(t *testing.T) {
	repo := &accountRepoStub{existsErr: errors.New("db down")}
	svc := accountcore.NewBasicAccounts(repo, nil, nil)

	err := svc.Delete(context.Background(), 55)
	require.Error(t, err)
	require.ErrorContains(t, err, "check account")
	require.Empty(t, repo.deletedIDs)
}

// TestAccountService_Delete_DeleteError 测试删除操作失败时的错误处理。
// 预期行为：
//   - ExistsByID 返回 true（账号存在）
//   - Delete 被调用但返回错误
//   - 返回包含 "delete account" 的错误信息
//   - deletedIDs 记录了尝试删除的 ID
func TestAccountService_Delete_DeleteError(t *testing.T) {
	repo := &accountRepoStub{
		exists:    true,
		deleteErr: errors.New("delete failed"),
	}
	svc := accountcore.NewBasicAccounts(repo, nil, nil)

	err := svc.Delete(context.Background(), 55)
	require.Error(t, err)
	require.ErrorContains(t, err, "delete account")
	require.Equal(t, []int64{55}, repo.deletedIDs)
}

// TestAccountService_Delete_Success 测试删除操作成功的场景。
// 预期行为：
//   - ExistsByID 返回 true（账号存在）
//   - Delete 成功执行
//   - 返回 nil 错误
//   - deletedIDs 记录了被删除的 ID
func TestAccountService_Delete_Success(t *testing.T) {
	repo := &accountRepoStub{exists: true}
	svc := accountcore.NewBasicAccounts(repo, nil, nil)

	err := svc.Delete(context.Background(), 55)
	require.NoError(t, err)
	require.Equal(t, []int64{55}, repo.deletedIDs)
}

// ExistsByID 保留原删除前存在性检查替身。
func (s *accountRepoStub) ExistsByID(context.Context, int64) (bool, error) {
	return s.exists, s.existsErr
}

// Delete 记录原删除调用顺序及结果。
func (s *accountRepoStub) Delete(_ context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErr
}
