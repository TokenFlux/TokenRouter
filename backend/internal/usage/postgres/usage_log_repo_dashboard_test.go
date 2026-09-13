package postgres

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRunDashboardQueriesSerializesNonPoolExecutor 验证事务等单连接执行器不会被并发复用。
func TestRunDashboardQueriesSerializesNonPoolExecutor(t *testing.T) {
	repo := &Store{}
	var running atomic.Int32
	var overlapped atomic.Bool
	query := func(context.Context) error {
		if running.Add(1) != 1 {
			overlapped.Store(true)
		}
		time.Sleep(time.Millisecond)
		running.Add(-1)
		return nil
	}

	require.NoError(t, repo.runDashboardQueries(context.Background(), query, query, query))
	require.False(t, overlapped.Load())
}

// TestRunDashboardQueriesParallelizesPoolExecutor 验证生产连接池仍会并行执行独立查询。
func TestRunDashboardQueriesParallelizesPoolExecutor(t *testing.T) {
	repo := &Store{db: new(sql.DB)}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan error, 1)
	query := func(context.Context) error {
		started <- struct{}{}
		<-release
		return nil
	}

	go func() {
		done <- repo.runDashboardQueries(context.Background(), query, query)
	}()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for range 2 {
		select {
		case <-started:
		case <-timer.C:
			close(release)
			<-done
			t.Fatal("连接池查询未并行启动")
		}
	}
	close(release)
	require.NoError(t, <-done)
}
