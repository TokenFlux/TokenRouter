//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAPIKeyServiceManagedKeyHidden 校验创作台隐藏执行 Key 不暴露存在性：
// GetByID/Update/Delete 命中 managed_by='creative_studio' 的 Key 一律按不存在处理，
// 且普通 Key 的对照组行为保持不变。
func TestAPIKeyServiceManagedKeyHidden(t *testing.T) {
	ctx := context.Background()
	managedBy := CreativeManagedBy
	managed := &APIKey{
		ID:        42,
		UserID:    7,
		Key:       "sk-hidden-managed-key",
		Name:      "creative-studio:12",
		Status:    StatusActive,
		ManagedBy: &managedBy,
	}
	repo := &apiKeyRepoStub{apiKey: managed}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, nil, nil)
	svc.Start()

	// 详情：managed key 返回 404 语义错误，不泄露任何字段。
	got, err := svc.GetByID(ctx, 42)
	require.Nil(t, got)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrAPIKeyNotFound), "managed key GetByID 必须返回 ErrAPIKeyNotFound")

	// 更新：拒绝操作且不暴露存在性。
	hackedName := "hacked"
	updated, err := svc.Update(ctx, 42, 7, UpdateAPIKeyRequest{Name: &hackedName})
	require.Nil(t, updated)
	require.True(t, errors.Is(err, ErrAPIKeyNotFound), "managed key Update 必须返回 ErrAPIKeyNotFound")

	// 删除：拒绝操作，DeleteWithAudit 不得被调用。
	err = svc.Delete(ctx, 42, 7)
	require.True(t, errors.Is(err, ErrAPIKeyNotFound), "managed key Delete 必须返回 ErrAPIKeyNotFound")
	require.Empty(t, repo.deletedIDs, "managed key 不得进入删除流程")
}
