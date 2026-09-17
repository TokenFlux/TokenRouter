package settings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/stretchr/testify/require"
)

// 各设置模块准备好后只有一次写入；失败不会执行任何应用。
func TestUpdatesAtomicCommitAndApplyFailure(t *testing.T) {
	for _, failWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "apply_failure", true: "write_failure"}[failWrite], func(t *testing.T) {
			repo := &writeStoreStub{}
			if failWrite {
				repo.err = errors.New("write failed")
			}
			update, err := New(repo).Updates().Begin(context.Background())
			require.NoError(t, err)
			defer update.Close()
			var applied []string
			err = update.Commit(PreparedChange{Module: "identity", Values: map[string]string{"a": "1"}, Apply: func(context.Context) error { applied = append(applied, "identity"); return errors.New("apply failed") }}, PreparedChange{Module: "payment", Values: map[string]string{"b": "2"}, Apply: func(context.Context) error { applied = append(applied, "payment"); return nil }})
			if failWrite {
				require.ErrorIs(t, err, repo.err)
				require.Empty(t, applied)
				require.Empty(t, repo.values)
				return
			}
			require.Equal(t, "SETTINGS_APPLY_FAILED", apperror.Reason(err))
			require.Equal(t, map[string]string{"persisted": "true", "modules": "identity"}, apperror.FromError(err).Metadata)
			require.Equal(t, []string{"identity", "payment"}, applied)
			require.Equal(t, map[string]string{"a": "1", "b": "2"}, repo.values)
			require.Error(t, update.Commit())
		})
	}
}

func TestUpdatesRejectDuplicateOwnershipBeforeWriting(t *testing.T) {
	repo := &writeStoreStub{}
	update, err := New(repo).Updates().Begin(context.Background())
	require.NoError(t, err)
	defer update.Close()
	err = update.Commit(PreparedChange{Module: "a", Values: map[string]string{"k": "1"}}, PreparedChange{Module: "b", Values: map[string]string{"k": "2"}})
	require.Error(t, err)
	require.Empty(t, repo.values)
}

// 停止同时取消拥有者与排队者，但等待未结束的任务时必须报告超时。
func TestUpdatesStopCancelsOwnersAndWaiters(t *testing.T) {
	updates := New(&writeStoreStub{}).Updates()
	first, err := updates.Begin(context.Background())
	require.NoError(t, err)
	waiter := make(chan error, 1)
	go func() {
		s, e := updates.Begin(context.Background())
		if s != nil {
			s.Close()
		}
		waiter <- e
	}()
	budget, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, updates.Stop(budget), context.DeadlineExceeded)
	require.ErrorIs(t, first.Context().Err(), context.Canceled)
	require.Error(t, <-waiter)
	first.Close()
	first.Close()
	require.NoError(t, updates.Stop(context.Background()))
	_, err = updates.Begin(context.Background())
	require.Equal(t, "SETTINGS_STOPPED", apperror.Reason(err))
}

func TestUpdatesWaitCancellationDoesNotReleaseOwner(t *testing.T) {
	updates := New(&writeStoreStub{}).Updates()
	first, err := updates.Begin(context.Background())
	require.NoError(t, err)
	defer first.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = updates.Begin(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, first.Commit(PreparedChange{Module: "site", Values: map[string]string{"name": "next"}}))
}
