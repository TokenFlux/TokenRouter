//go:build unit

package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// updateSnapshotValue 仅提供重建元数据，不向核心引入完整账号或凭据。
type updateSnapshotValue struct{ id int64 }

func (v updateSnapshotValue) SnapshotMetadata() SnapshotMetadata {
	return SnapshotMetadata{ID: v.id}
}

type updateSnapshotCache struct {
	SnapshotCache
	values []SnapshotAccount
	err    error
}

func (c *updateSnapshotCache) SetAccount(_ context.Context, value SnapshotAccount) error {
	c.values = append(c.values, value)
	return c.err
}

// 保留旧快照包装的四项更新断言，直接验证唯一核心实现。
func TestSchedulerSnapshotService_UpdateAccountInCache(t *testing.T) {
	t.Run("calls cache.SetAccount", func(t *testing.T) {
		cache := &updateSnapshotCache{}
		core := NewSnapshotService(cache, nil, nil, nil, nil)
		require.NoError(t, core.UpdateAccountInCache(t.Context(), updateSnapshotValue{123}))
		require.Len(t, cache.values, 1)
		require.Equal(t, int64(123), cache.values[0].SnapshotMetadata().ID)
	})
	t.Run("returns nil when cache is nil", func(t *testing.T) {
		core := NewSnapshotService(nil, nil, nil, nil, nil)
		require.NoError(t, core.UpdateAccountInCache(t.Context(), updateSnapshotValue{1}))
	})
	t.Run("returns nil when account is nil", func(t *testing.T) {
		cache := &updateSnapshotCache{}
		core := NewSnapshotService(cache, nil, nil, nil, nil)
		require.NoError(t, core.UpdateAccountInCache(t.Context(), nil))
		require.Empty(t, cache.values)
	})
	t.Run("propagates cache error", func(t *testing.T) {
		expected := errors.New("cache error")
		core := NewSnapshotService(&updateSnapshotCache{err: expected}, nil, nil, nil, nil)
		require.ErrorIs(t, core.UpdateAccountInCache(t.Context(), updateSnapshotValue{1}), expected)
	})
}
