package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestIsPostgresDeadlock(t *testing.T) {
	deadlockErr := &pq.Error{Code: "40P01"}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "直接死锁错误", err: deadlockErr, want: true},
		{name: "包装后的死锁错误", err: fmt.Errorf("wrapped: %w", deadlockErr), want: true},
		{name: "其他 PostgreSQL 错误", err: &pq.Error{Code: "23505"}, want: false},
		{name: "普通错误", err: errors.New("deadlock detected"), want: false},
		{name: "空错误", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsDeadlock(tt.err))
		})
	}
}

func TestRetryPostgresDeadlock_RetriesTwiceThenSucceeds(t *testing.T) {
	attempts := 0
	result, err := RetryDeadlock(context.Background(), "test_operation", 0, func() (int, error) {
		attempts++
		if attempts < postgresDeadlockMaxAttempts {
			return 0, &pq.Error{Code: "40P01"}
		}
		return 42, nil
	})

	require.NoError(t, err)
	require.Equal(t, 42, result)
	require.Equal(t, postgresDeadlockMaxAttempts, attempts)
}

func TestRetryPostgresDeadlock_StopsAfterMaximumAttempts(t *testing.T) {
	attempts := 0
	_, err := RetryDeadlock(context.Background(), "test_operation", 0, func() (int, error) {
		attempts++
		return 0, &pq.Error{Code: "40P01"}
	})

	require.Error(t, err)
	require.True(t, IsDeadlock(err))
	require.Equal(t, postgresDeadlockMaxAttempts, attempts)
}

func TestRetryPostgresDeadlock_DoesNotRetryOtherErrors(t *testing.T) {
	attempts := 0
	wantErr := errors.New("write failed")
	_, err := RetryDeadlock(context.Background(), "test_operation", 0, func() (int, error) {
		attempts++
		return 0, wantErr
	})

	require.ErrorIs(t, err, wantErr)
	require.Equal(t, 1, attempts)
}

func TestRetryPostgresDeadlock_StopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	_, err := RetryDeadlock(ctx, "test_operation", 0, func() (int, error) {
		attempts++
		cancel()
		return 0, &pq.Error{Code: "40P01"}
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, attempts)
}

// TestWaitDeadlockRetryCanceled 验证等待阶段继续传播取消。
func TestWaitDeadlockRetryCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, WaitDeadlockRetry(ctx, 0), context.Canceled)
}
